package apphost

// These tests run the contended party as a real second process, because the
// condition under test is cross-process: goroutines in one process share the
// runtime, the descriptors, and the scheduler, which is precisely what a
// second process does not. The overlap is sequenced by a witness inside the
// lock path, not by timing: the child prints "contended" only after a
// non-blocking attempt at the configuration lock — made inside lockMetaFile,
// on the very handle the blocking acquisition then uses — was refused
// because another process held it. The parent releases only after reading
// that line, so the parent's transaction provably still held the lock, with
// its write unstored, when the child's attempt was refused; and the child's
// transaction provably began after the parent's store, because its fresh
// read must contain what the parent stored. Remove either production lock
// and the witness never runs: the parent then never stores, the child's read
// fails its check, and the test fails on the missing line.

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestMain re-executes this test binary as the second process of the
// cross-process tests when the child environment variables are set, and runs
// the tests normally otherwise.
func TestMain(m *testing.M) {
	if dir := os.Getenv("GITSEQ_CONFIG_CHILD_DIR"); dir != "" {
		os.Exit(runConfigChild(os.Getenv("GITSEQ_CONFIG_CHILD_MODE"), dir))
	}
	os.Exit(m.Run())
}

// runConfigChild is the second process. It installs the lock witness before
// its contended call, so "contended" is printed from inside lockMetaFile, and
// only once a non-blocking attempt at the lock — exclusive for the updating
// child, shared for the reading child, matching the blocking acquisition it
// precedes — was refused by another process's hold. A free lock at that
// attempt is a protocol failure, not a pass: it would mean the parent was not
// holding its transaction open. Exit status carries the verdict; failures are
// described on stderr.
func runConfigChild(mode, dir string) int {
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	lockAttemptWitness = func(file *os.File) error {
		refused, err := tryLockFile(file, mode == "update")
		if err != nil {
			return fmt.Errorf("probe the configuration lock: %w", err)
		}
		if !refused {
			return errors.New("the configuration lock was free at the child's attempt: no other process was holding it")
		}
		fmt.Println("contended")
		return nil
	}
	switch mode {
	case "update":
		if _, err := UpdateConfig(dir, baseTestConfig(), func(config *Config) (bool, error) {
			if _, held := config.Actors["parent"]; !held {
				return false, errors.New("the fresh read is missing the parent process's stored write")
			}
			config.Actors["child"] = Actor{Name: "child", Fingerprint: sha1ID('c'), KeyFile: "actors/child.key"}
			return true, nil
		}); err != nil {
			return fail(err)
		}
	case "load":
		config, err := LoadConfig(dir)
		if err != nil {
			return fail(err)
		}
		if _, held := config.Actors["parent"]; !held {
			return fail(errors.New("the read is missing the parent process's stored write"))
		}
	default:
		return fail(fmt.Errorf("unknown child mode %q", mode))
	}
	return 0
}

// holdTransactionOpen starts an update that stores the "parent" actor and
// keeps its transaction open — lock held, state read, nothing stored — until
// the release file appears. It reports on held when the lock is held, and the
// transaction's result on the returned channel.
func holdTransactionOpen(dir, release string, held chan<- struct{}) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := UpdateConfig(dir, baseTestConfig(), func(config *Config) (bool, error) {
			close(held)
			deadline := time.Now().Add(30 * time.Second)
			for {
				if _, err := os.Stat(release); err == nil {
					break
				}
				if time.Now().After(deadline) {
					return false, errors.New("no release marker appeared; the child's refused lock attempt was never witnessed")
				}
				time.Sleep(2 * time.Millisecond)
			}
			if config.Actors == nil {
				config.Actors = make(map[string]Actor, 1)
			}
			config.Actors["parent"] = Actor{Name: "parent", Fingerprint: sha1ID('b'), KeyFile: "actors/parent.key"}
			return true, nil
		})
		done <- err
	}()
	return done
}

// startConfigChild launches the second process in the given mode and returns
// it once its "contended" line reports that its lock attempt was refused
// while another process held the lock. Any other first line — including the
// end of the stream when the contended call never reached the lock — fails
// the test here.
func startConfigChild(t *testing.T, mode, dir string) *exec.Cmd {
	t.Helper()
	child := exec.Command(os.Args[0], "-test.run=^$")
	child.Env = append(os.Environ(), "GITSEQ_CONFIG_CHILD_DIR="+dir, "GITSEQ_CONFIG_CHILD_MODE="+mode)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "contended\n" {
		t.Fatalf("child did not witness the contended lock: %q, %v", line, err)
	}
	return child
}

// An update by a second process, begun while this process's transaction held
// the lock with its write still unstored, must wait for that transaction and
// then read the state it stored: neither process's write may be lost.
func TestUpdateConfigSerializesAgainstAnotherProcess(t *testing.T) {
	dir := t.TempDir()
	if err := SaveConfig(dir, baseTestConfig()); err != nil {
		t.Fatal(err)
	}
	release := filepath.Join(t.TempDir(), "release")
	held := make(chan struct{})
	done := holdTransactionOpen(dir, release, held)
	<-held
	child := startConfigChild(t, "update", dir)
	// The child's exclusive attempt was refused while this process's
	// transaction held the lock with nothing stored. Only now may the
	// transaction complete.
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("the child process's update failed: %v", err)
	}
	final, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"parent", "child"} {
		if _, held := final.Actors[name]; !held {
			t.Fatalf("the stored file lost %q: %v", name, final.Actors)
		}
	}
}

// A read by a second process, attempted while this process's transaction held
// the lock with its write still unstored, must be refused then and observe
// the stored result once granted — reader and writer coordinate through the
// shared and exclusive sides of one lock, rather than relying on rename
// atomicity toward a reader mid-read.
func TestLoadConfigCoordinatesWithAWriterInAnotherProcess(t *testing.T) {
	dir := t.TempDir()
	if err := SaveConfig(dir, baseTestConfig()); err != nil {
		t.Fatal(err)
	}
	release := filepath.Join(t.TempDir(), "release")
	held := make(chan struct{})
	done := holdTransactionOpen(dir, release, held)
	<-held
	child := startConfigChild(t, "load", dir)
	// The child's shared attempt was refused while this process's transaction
	// held the exclusive lock with nothing stored. Only now may the
	// transaction complete.
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("the child process's read failed or read stale state: %v", err)
	}
}
