package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileCheckpointPointerRejectsOversizedAndNoncanonicalJSON(t *testing.T) {
	t.Parallel()
	valid, err := json.Marshal(checkpointPointer{
		Schema: checkpointPointerSchema,
		Commit: strings.Repeat("a", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	oversized, err := json.Marshal(checkpointPointer{
		Schema: checkpointPointerSchema,
		Commit: strings.Repeat("a", maxCheckpointPointerBytes),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(oversized) <= maxCheckpointPointerBytes {
		t.Fatalf("oversized fixture is only %d bytes", len(oversized))
	}

	tests := []struct {
		name    string
		content []byte
		wantErr bool
	}{
		{name: "canonical", content: valid},
		{name: "oversized canonical JSON", content: oversized, wantErr: true},
		{name: "trailing newline", content: append(bytes.Clone(valid), '\n'), wantErr: true},
		{name: "unknown field", content: []byte(`{"schema":"gitseq-checkpoint-pointer@1","commit":"` + strings.Repeat("a", 40) + `","extra":true}`), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "checkpoint.json")
			if err := os.WriteFile(path, test.content, 0o600); err != nil {
				t.Fatal(err)
			}
			commit, err := (fileCheckpointPointer{path: path}).Load()
			if (err != nil) != test.wantErr {
				t.Fatalf("Load() commit=%q err=%v, wantErr=%v", commit, err, test.wantErr)
			}
		})
	}
}
