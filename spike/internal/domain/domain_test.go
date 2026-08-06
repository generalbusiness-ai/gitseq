package domain

import (
	"context"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitseq/spike/internal/gitstore"
	"gitseq/spike/internal/kernel"
)

type credential struct {
	user     string
	password string
}

func TestRepositoryIsTheHTTPReadBoundaryEvenForKnownOID(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	credentials := map[string]credential{
		"/domain-a.git": {user: "alice", password: "a-secret"},
		"/domain-b.git": {user: "bob", password: "b-secret"},
	}
	genesis := make(map[string]string)
	for path := range credentials {
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/"), ".git")
		store, err := gitstore.InitBare(ctx, filepath.Join(root, name+".git"), "sha1")
		if err != nil {
			t.Fatal(err)
		}
		keyPath := filepath.Join(root, name+"-key")
		publicKey, err := gitstore.GenerateSSHKey(ctx, keyPath)
		if err != nil {
			t.Fatal(err)
		}
		genesis[path], err = kernel.Create(ctx, store, kernel.GenesisDescriptor{
			Version: 0, ObjectFormat: "sha1", PayloadCeiling: 1 << 20, SequencerPublicKey: publicKey,
		}, keyPath)
		if err != nil {
			t.Fatal(err)
		}
	}

	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	backend := &cgi.Handler{
		Path: gitPath, Args: []string{"http-backend"}, Dir: root,
		Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var expected credential
		var found bool
		for prefix, candidate := range credentials {
			if strings.HasPrefix(request.URL.Path, prefix+"/") {
				expected, found = candidate, true
				break
			}
		}
		user, password, authenticated := request.BasicAuth()
		if !found || !authenticated || user != expected.user || password != expected.password {
			response.Header().Set("WWW-Authenticate", `Basic realm="gitseq-domain"`)
			http.Error(response, "not authorized for repository", http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(response, request)
	}))
	defer server.Close()

	local := filepath.Join(root, "client")
	if output, err := exec.CommandContext(ctx, "git", "init", local).CombinedOutput(); err != nil {
		t.Fatalf("git init client: %v: %s", err, output)
	}
	fetch := func(user, password, repository, oid string) error {
		url := "http://" + user + ":" + password + "@" + strings.TrimPrefix(server.URL, "http://") + repository
		cmd := exec.CommandContext(ctx, "git", "-c", "credential.helper=", "fetch", "--no-tags", url, oid)
		cmd.Dir = local
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		_, err := cmd.CombinedOutput()
		return err
	}

	// Knowing B's durable object ID is not authority to fetch B through A's
	// credentials. The probe asks for the raw hash, not an advertised ref.
	if err := fetch("alice", "a-secret", "/domain-b.git", genesis["/domain-b.git"]); err == nil {
		t.Fatal("A credentials fetched B's known object ID")
	}
	if err := fetch("bob", "b-secret", "/domain-b.git", genesis["/domain-b.git"]); err != nil {
		t.Fatalf("B credentials could not fetch B's known object ID: %v", err)
	}
	if err := fetch("alice", "a-secret", "/domain-a.git", genesis["/domain-a.git"]); err != nil {
		t.Fatalf("A credentials could not fetch A: %v", err)
	}
}
