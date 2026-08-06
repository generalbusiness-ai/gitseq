package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"gitseq/spike/internal/gitstore"
	"gitseq/spike/internal/kernel"
)

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: gitseq-spike <create|submit|verify>")
	}
	ctx := context.Background()
	switch os.Args[1] {
	case "create":
		set := flag.NewFlagSet("create", flag.ExitOnError)
		repo := set.String("repo", "", "bare repository path")
		format := set.String("object-format", "sha1", "sha1 or sha256")
		key := set.String("key", "", "sequencer SSH private key path")
		_ = set.Parse(os.Args[2:])
		if *repo == "" || *key == "" {
			fatalf("create requires --repo and --key")
		}
		store, err := gitstore.InitBare(ctx, *repo, *format)
		check(err)
		publicKey, err := readOrCreateKey(ctx, *key)
		check(err)
		genesis, err := kernel.Create(ctx, store, kernel.GenesisDescriptor{Version: 0, ObjectFormat: *format, PayloadCeiling: 1 << 20, SequencerPublicKey: publicKey}, *key)
		check(err)
		writeJSON(map[string]string{"genesis": genesis, "ref": kernel.Ref(genesis)})
	case "submit":
		set := flag.NewFlagSet("submit", flag.ExitOnError)
		repo := set.String("repo", "", "bare repository path")
		key := set.String("key", "", "sequencer SSH private key path")
		requestPath := set.String("request", "", "JSON request path")
		failpoint := set.String("failpoint", "", "named process-exit failpoint")
		_ = set.Parse(os.Args[2:])
		if *repo == "" || *key == "" || *requestPath == "" {
			fatalf("submit requires --repo, --key, and --request")
		}
		content, err := os.ReadFile(*requestPath)
		check(err)
		var request kernel.Request
		check(json.Unmarshal(content, &request))
		result, err := kernel.Submit(ctx, gitstore.Store{Repo: *repo}, request, kernel.Options{SigningKey: *key, Failpoint: kernel.ExitFailpoint(*failpoint)})
		check(err)
		writeJSON(result)
	case "verify":
		set := flag.NewFlagSet("verify", flag.ExitOnError)
		repo := set.String("repo", "", "bare repository path")
		genesis := set.String("genesis", "", "raw genesis object id")
		_ = set.Parse(os.Args[2:])
		if *repo == "" || *genesis == "" {
			fatalf("verify requires --repo and --genesis")
		}
		result, err := kernel.Verify(ctx, gitstore.Store{Repo: *repo}, *genesis)
		check(err)
		writeJSON(result)
	default:
		fatalf("unknown command %q", os.Args[1])
	}
}

func readOrCreateKey(ctx context.Context, path string) (string, error) {
	if _, err := os.Stat(path); err == nil {
		content, err := os.ReadFile(path + ".pub")
		if err != nil {
			return "", err
		}
		var kind, body string
		if _, err := fmt.Sscan(string(content), &kind, &body); err != nil {
			return "", err
		}
		return kind + " " + body, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	return gitstore.GenerateSSHKey(ctx, path)
}

func writeJSON(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	check(encoder.Encode(value))
}

func check(err error) {
	if err != nil {
		fatalf("%v", err)
	}
}

func fatalf(format string, values ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", values...)
	os.Exit(1)
}
