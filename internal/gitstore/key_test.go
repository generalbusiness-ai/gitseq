package gitstore

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSSHPublicKeyRejectsAllowedSignersInjection(t *testing.T) {
	ctx := context.Background()
	first, err := GenerateSSHKey(ctx, filepath.Join(t.TempDir(), "first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateSSHKey(ctx, filepath.Join(t.TempDir(), "second"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSSHPublicKey(first); err != nil {
		t.Fatalf("generated key rejected: %v", err)
	}
	_, body, _ := strings.Cut(first, " ")
	otherAlgorithm := testSSHPublicKey("ssh-rsa", make([]byte, 32))
	invalid := map[string]string{
		"second allowed signer":   first + "\nsequencer " + second,
		"leading space":           " " + first,
		"trailing space":          first + " ",
		"double space":            strings.Replace(first, " ", "  ", 1),
		"tab separator":           strings.Replace(first, " ", "\t", 1),
		"comment":                 first + " generated",
		"allowed signers option":  "cert-authority " + first,
		"unsupported type":        otherAlgorithm,
		"invalid wire body":       "ssh-ed25519 AAAA",
		"noncanonical padding":    first + "=",
		"embedded base64 newline": "ssh-ed25519 " + body[:12] + "\n" + body[12:],
		"embedded base64 return":  "ssh-ed25519 " + body[:12] + "\r" + body[12:],
	}
	for name, value := range invalid {
		t.Run(name, func(t *testing.T) {
			if err := ValidateSSHPublicKey(value); err == nil {
				t.Fatalf("accepted %q", value)
			}
		})
	}
}

func TestVerifySSHCommitValidatesPublicKeyBeforeFormatting(t *testing.T) {
	ctx := context.Background()
	store, err := InitBare(ctx, filepath.Join(t.TempDir(), "repo.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "sequencer")
	publicKey, err := GenerateSSHKey(ctx, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := store.EmptyTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := store.SignedCommit(ctx, tree, "", "test\n", keyPath, CommitIdentity{
		AuthorName: "test", AuthorEmail: "test@example.invalid",
		CommitterName: "test", CommitterEmail: "test@example.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.VerifySSHCommit(ctx, commit, "sequencer", publicKey); err != nil {
		t.Fatalf("valid key did not verify commit: %v", err)
	}
	// VerifySSHCommit trims the key when constructing allowed_signers. Without
	// its explicit validation, this non-canonical key becomes valid and the
	// verification succeeds, so this assertion holds that boundary in place.
	if err := store.VerifySSHCommit(ctx, commit, "sequencer", publicKey+" "); err == nil {
		t.Fatal("VerifySSHCommit accepted a non-canonical public key")
	}
}

func TestSigningAndVerificationQuarantineExecutableGitConfig(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := InitBare(ctx, filepath.Join(root, "repo.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(root, "sequencer")
	publicKey, err := GenerateSSHKey(ctx, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "sentinel-ran")
	sentinel := filepath.Join(root, "sentinel")
	script := "#!/bin/sh\nprintf ran > '" + marker + "'\nexit 1\n"
	if err := os.WriteFile(sentinel, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.run(ctx, nil, nil, "config", "gpg.ssh.program", sentinel); err != nil {
		t.Fatal(err)
	}
	globalConfig := filepath.Join(root, "hostile-global-config")
	if err := os.WriteFile(globalConfig, []byte("[invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tree, err := store.EmptyTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "gpg.ssh.program")
	t.Setenv("GIT_CONFIG_VALUE_0", sentinel)
	commit, err := store.SignedCommit(ctx, tree, "", "test\n", keyPath, CommitIdentity{
		AuthorName: "test", AuthorEmail: "test@example.invalid",
		CommitterName: "test", CommitterEmail: "test@example.invalid",
	})
	if err != nil {
		t.Fatalf("hostile config changed signing: %v", err)
	}
	if err := store.VerifySSHCommit(ctx, commit, "sequencer", publicKey); err != nil {
		t.Fatalf("hostile config changed verification: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("hostile Git configuration invoked sentinel: %v", err)
	}
}

func TestSignedCommitTimestampRoundTripsWithMessage(t *testing.T) {
	ctx := context.Background()
	store, err := InitBare(ctx, filepath.Join(t.TempDir(), "repo.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "sequencer")
	if _, err := GenerateSSHKey(ctx, keyPath); err != nil {
		t.Fatal(err)
	}
	tree, err := store.EmptyTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	commit, written, err := store.SignedCommitWithTimestamp(ctx, tree, "", "event envelope\n", keyPath, CommitIdentity{
		AuthorName: "test", AuthorEmail: "test@example.invalid",
		CommitterName: "test", CommitterEmail: "test@example.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	message, loaded, err := store.CommitMessageWithTimestamp(ctx, commit)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != written || loaded <= 0 {
		t.Fatalf("timestamp loaded = %d, written = %d", loaded, written)
	}
	if message != "event envelope\n" {
		t.Fatalf("message = %q", message)
	}
}

func TestGenerateSSHKeyValidatesKeygenOutput(t *testing.T) {
	body := strings.TrimPrefix(testSSHPublicKey("ssh-rsa", make([]byte, 32)), "ssh-rsa ")
	binDir := t.TempDir()
	script := "#!/bin/sh\n" +
		"while [ \"$#\" -gt 0 ]; do\n" +
		"  if [ \"$1\" = -f ]; then shift; key_path=$1; fi\n" +
		"  shift\n" +
		"done\n" +
		"printf '%s\\n' 'ssh-rsa " + body + "' > \"$key_path.pub\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "ssh-keygen"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	if _, err := GenerateSSHKey(context.Background(), filepath.Join(t.TempDir(), "generated")); err == nil {
		t.Fatal("GenerateSSHKey accepted non-ed25519 ssh-keygen output")
	}
}

func testSSHPublicKey(algorithm string, key []byte) string {
	raw := make([]byte, 0, 8+len(algorithm)+len(key))
	raw = appendSSHWireString(raw, []byte(algorithm))
	raw = appendSSHWireString(raw, key)
	return algorithm + " " + base64.StdEncoding.EncodeToString(raw)
}

func appendSSHWireString(dst, value []byte) []byte {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value)))
	dst = append(dst, size[:]...)
	return append(dst, value...)
}
