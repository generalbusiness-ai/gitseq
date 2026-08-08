package gitstore

import (
	"context"
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
	invalid := map[string]string{
		"second allowed signer":  first + "\nsequencer " + second,
		"leading space":          " " + first,
		"trailing space":         first + " ",
		"double space":           strings.Replace(first, " ", "  ", 1),
		"tab separator":          strings.Replace(first, " ", "\t", 1),
		"comment":                first + " generated",
		"allowed signers option": "cert-authority " + first,
		"unsupported type":       "ssh-rsa " + body,
		"invalid wire body":      "ssh-ed25519 AAAA",
		"noncanonical base64":    first + "=",
	}
	for name, value := range invalid {
		t.Run(name, func(t *testing.T) {
			if err := ValidateSSHPublicKey(value); err == nil {
				t.Fatalf("accepted %q", value)
			}
		})
	}
}
