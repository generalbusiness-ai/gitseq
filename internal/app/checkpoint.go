package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/generalbusiness-ai/gitseq/internal/kernel"
)

const (
	checkpointPointerSchema   = "gitseq-checkpoint-pointer@1"
	maxCheckpointPointerBytes = 4 << 10
	checkpointEnvironment     = "GITSEQ_CHECKPOINT"
)

type checkpointPointer struct {
	Schema string `json:"schema"`
	Commit string `json:"commit"`
}

type fileCheckpointPointer struct{ path string }

func (p fileCheckpointPointer) Load() (string, error) {
	file, err := os.Open(p.path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("checkpoint pointer is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCheckpointPointerBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxCheckpointPointerBytes {
		return "", errors.New("checkpoint pointer is too large")
	}
	var pointer checkpointPointer
	if err := json.Unmarshal(data, &pointer); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(pointer)
	if err != nil || !bytes.Equal(canonical, data) || pointer.Schema != checkpointPointerSchema || pointer.Commit == "" {
		return "", errors.New("checkpoint pointer is not canonical")
	}
	return pointer.Commit, nil
}

func (p fileCheckpointPointer) Store(commit string) error {
	if current, err := p.Load(); err == nil && current == commit {
		return nil
	}
	data, err := json.Marshal(checkpointPointer{Schema: checkpointPointerSchema, Commit: commit})
	if err != nil {
		return err
	}
	directory := filepath.Dir(p.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".checkpoint-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, p.path)
}

func validateGenesis(format, genesis string) error {
	want := 40
	if format == "sha256" {
		want = 64
	} else if format != "sha1" {
		return errors.New("unsupported object format")
	}
	if len(genesis) != want {
		return errors.New("invalid genesis object id")
	}
	if _, err := hex.DecodeString(genesis); err != nil {
		return errors.New("invalid genesis object id")
	}
	return nil
}

func (w *Workspace) checkpointPointerPath() string {
	return filepath.Join(w.MetaDir, "checkpoints", w.Config.Genesis+".json")
}

func (w *Workspace) checkpointOptions() kernel.CheckpointOptions {
	if os.Getenv(checkpointEnvironment) == "off" {
		return kernel.CheckpointOptions{}
	}
	return kernel.CheckpointOptions{
		Profile: w.foldProfile(), SigningKey: w.Config.SequencerKey,
		Pointer: fileCheckpointPointer{path: w.checkpointPointerPath()},
	}
}

// InvalidateCheckpoint clears both local selectors. The next normal read
// performs a cold audit and, when sequencer custody is available, publishes a
// fresh signed checkpoint. Set GITSEQ_CHECKPOINT=off to keep the shortcut off.
func (w *Workspace) InvalidateCheckpoint(ctx context.Context) error {
	pointerErr := os.Remove(w.checkpointPointerPath())
	if pointerErr != nil && !errors.Is(pointerErr, os.ErrNotExist) {
		return pointerErr
	}
	ref := kernel.CheckpointRef(w.Config.Genesis)
	old, err := w.Store.Head(ctx, ref)
	if err != nil {
		return nil
	}
	return w.Store.UpdateRef(ctx, ref, w.Config.Genesis, old)
}
