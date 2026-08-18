package gitstore

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditBatchVerifiesSignedCommitsAndPayloads(t *testing.T) {
	ctx := context.Background()
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			store, err := InitBare(ctx, filepath.Join(t.TempDir(), "repo.git"), format)
			if err != nil {
				t.Fatal(err)
			}
			keyPath := filepath.Join(t.TempDir(), "sequencer")
			publicKey, err := GenerateSSHKey(ctx, keyPath)
			if err != nil {
				t.Fatal(err)
			}
			tree, err := store.WritePayloadTree(ctx, []byte("event payload"), map[string][]byte{
				"a.json": []byte(`{"first":true}`),
				"z.txt":  []byte("last"),
			})
			if err != nil {
				t.Fatal(err)
			}
			commit, timestamp, err := store.SignedCommitWithTimestamp(ctx, tree, "", "signed event\n\nbody\n", keyPath, CommitIdentity{
				AuthorName: "test", AuthorEmail: "test@example.invalid",
				CommitterName: "test", CommitterEmail: "test@example.invalid",
			})
			if err != nil {
				t.Fatal(err)
			}
			batch, err := store.OpenAuditBatch(ctx, format)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := batch.Close(); err != nil {
					t.Errorf("close audit batch: %v", err)
				}
			}()
			audited, err := batch.ReadCommit(commit)
			if err != nil {
				t.Fatal(err)
			}
			if audited.Tree != tree || audited.Timestamp != timestamp || audited.Message != "signed event\n\nbody\n" || len(audited.Parents) != 0 {
				t.Fatalf("audited metadata = %+v", audited.CommitMetadata)
			}
			if err := VerifySSHSignature(audited, publicKey); err != nil {
				t.Fatalf("in-process signature verification: %v", err)
			}
			if err := store.VerifySSHCommit(ctx, commit, "sequencer", publicKey); err != nil {
				t.Fatalf("git signature verification: %v", err)
			}
			payload, attachments, err := batch.PayloadTree(tree, 128, true)
			if err != nil {
				t.Fatal(err)
			}
			if string(payload) != "event payload" || string(attachments["a.json"]) != `{"first":true}` || string(attachments["z.txt"]) != "last" {
				t.Fatalf("payload = %q attachments = %#v", payload, attachments)
			}
		})
	}
}

func TestVerifySSHSignatureRejectsChangedInputs(t *testing.T) {
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
	otherKey, err := GenerateSSHKey(ctx, filepath.Join(t.TempDir(), "other"))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := store.EmptyTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := store.SignedCommit(ctx, tree, "", "signed\n", keyPath, CommitIdentity{
		AuthorName: "test", AuthorEmail: "test@example.invalid",
		CommitterName: "test", CommitterEmail: "test@example.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := store.OpenAuditBatch(ctx, "sha1")
	if err != nil {
		t.Fatal(err)
	}
	audited, err := batch.ReadCommit(commit)
	if err != nil {
		t.Fatal(err)
	}
	if err := batch.Close(); err != nil {
		t.Fatal(err)
	}
	changedContent := audited
	changedContent.SignedContent = append([]byte(nil), audited.SignedContent...)
	changedContent.SignedContent[len(changedContent.SignedContent)-1] ^= 1
	changedSignature := audited
	changedSignature.Signature = append([]byte(nil), audited.Signature...)
	changedSignature.Signature[len(changedSignature.Signature)/2] ^= 1
	changedNamespace := audited
	block, _ := pem.Decode(audited.Signature)
	block.Bytes = append([]byte(nil), block.Bytes...)
	_, afterKey, ok := sshWireString(block.Bytes[len("SSHSIG")+4:])
	if !ok {
		t.Fatal("test signature has no embedded key")
	}
	namespace, _, ok := sshWireString(afterKey)
	if !ok || string(namespace) != "git" {
		t.Fatal("test signature has no git namespace")
	}
	copy(namespace, "bad")
	changedNamespace.Signature = pem.EncodeToMemory(block)
	for name, test := range map[string]struct {
		commit AuditedCommit
		key    string
	}{
		"wrong key":         {commit: audited, key: otherKey},
		"changed content":   {commit: changedContent, key: publicKey},
		"changed signature": {commit: changedSignature, key: publicKey},
		"wrong namespace":   {commit: changedNamespace, key: publicKey},
	} {
		t.Run(name, func(t *testing.T) {
			if err := VerifySSHSignature(test.commit, test.key); err == nil {
				t.Fatal("changed signature input was accepted")
			}
		})
	}
}

func TestAuditBatchRecomputesObjectHash(t *testing.T) {
	ctx := context.Background()
	store, err := InitBare(ctx, filepath.Join(t.TempDir(), "repo.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	oid, err := store.WriteBlob(ctx, []byte("good"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Repo, "objects", oid[:2], oid[2:])
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	var replacement bytes.Buffer
	writer := zlib.NewWriter(&replacement)
	if _, err := writer.Write([]byte("blob 3\x00bad")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, replacement.Bytes(), 0o444); err != nil {
		t.Fatal(err)
	}
	batch, err := store.OpenAuditBatch(ctx, "sha1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = batch.Close() }()
	if _, err := batch.readObject(oid, "blob", 4); err == nil {
		t.Fatal("content stored under the wrong object id was accepted")
	}
}

func TestAuditBatchEnforcesPayloadShapeAndCeiling(t *testing.T) {
	ctx := context.Background()
	store, err := InitBare(ctx, filepath.Join(t.TempDir(), "repo.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	valid, err := store.WritePayloadTree(ctx, []byte("12345"), map[string][]byte{"a": []byte("6789")})
	if err != nil {
		t.Fatal(err)
	}
	invalid, err := store.WriteSingleFileTree(ctx, "wrong", []byte("12345"))
	if err != nil {
		t.Fatal(err)
	}
	batch, err := store.OpenAuditBatch(ctx, "sha1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = batch.Close() }()
	if _, _, err := batch.PayloadTree(valid, 9, false); err != nil {
		t.Fatalf("exact ceiling rejected: %v", err)
	}
	if _, _, err := batch.PayloadTree(valid, 8, false); err == nil || !strings.Contains(err.Error(), "payload exceeds genesis ceiling") {
		t.Fatalf("over-ceiling payload error = %v", err)
	}
	// The oversize response intentionally terminates this reader. Use a fresh
	// process to prove that malformed shape is rejected independently.
	_ = batch.Close()
	batch, err = store.OpenAuditBatch(ctx, "sha1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := batch.PayloadTree(invalid, 9, false); err == nil || !strings.Contains(err.Error(), "invalid payload path") {
		t.Fatalf("invalid tree error = %v", err)
	}
}
