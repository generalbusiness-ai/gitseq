package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentLocalSavesUseUniqueTemporaryFiles(t *testing.T) {
	t.Parallel()
	workspace := residentWorkspace(t)

	// Occupy the two fixed names used by the old implementation. Unique
	// per-write temporary files must make these irrelevant.
	for _, path := range []string{
		filepath.Join(workspace.MetaDir, "config.json.tmp"),
		filepath.Join(workspace.MetaDir, residentFile+".tmp"),
	} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	const writers = 32
	start := make(chan struct{})
	errors := make(chan error, writers*2)
	var group sync.WaitGroup
	for i := 0; i < writers; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			<-start
			if err := workspace.save(); err != nil {
				errors <- fmt.Errorf("save %d: %w", i, err)
			}
			if _, err := workspace.PublishResident(fmt.Sprintf("http://127.0.0.1:%d", 7800+i)); err != nil {
				errors <- fmt.Errorf("publish %d: %w", i, err)
			}
		}(i)
	}
	close(start)
	group.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}

	if _, err := Open(context.Background(), workspace.Repo); err != nil {
		t.Fatalf("concurrent saves left an invalid config: %v", err)
	}
	if advertisement := workspace.ResidentAdvertisement(); advertisement.State != AdvertisementPublished || advertisement.URL == "" {
		t.Fatalf("concurrent publishes left no valid resident: %+v", advertisement)
	}
	for _, pattern := range []string{".config.json.tmp-*", ".resident.json.tmp-*"} {
		matches, err := filepath.Glob(filepath.Join(workspace.MetaDir, pattern))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			t.Fatalf("temporary files remain after concurrent saves: %v", matches)
		}
	}
}
