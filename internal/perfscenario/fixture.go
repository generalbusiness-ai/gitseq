// Package perfscenario prepares and materializes the synthetic repositories
// consumed by the opt-in performance lane.
package perfscenario

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/perflane"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

const manifestSchema = "gitseq.performance-fixture-manifest.v1"

// FixturePlan is the logical, reproducible input to materialization. The Git
// object IDs themselves include generated signing keys and commit timestamps,
// so comparisons reuse one exact prepared repository and record its separate
// ExactDigest rather than claiming independent regeneration is byte-identical.
type FixturePlan struct {
	GeneratorVersion string
	Seed             uint64
	Depth            int
	Shape            string
	PayloadBuckets   []int
	CheckpointDepths []int
	ActorCount       int
}

// Manifest binds a deterministic logical workload to the exact signed Git
// materialization reused by every compared sample.
type Manifest struct {
	Schema           string            `json:"schema"`
	GeneratorVersion string            `json:"generator_version"`
	Seed             uint64            `json:"seed"`
	Shape            string            `json:"shape"`
	Depth            int               `json:"depth"`
	Repository       string            `json:"repository"`
	Genesis          string            `json:"genesis"`
	SeedEvent        string            `json:"seed_event"`
	Actor            string            `json:"actor"`
	ActorCount       int               `json:"actor_count"`
	Heads            map[string]string `json:"heads"`
	Checkpoints      map[string]string `json:"checkpoints"`
	LogicalDigest    string            `json:"logical_digest"`
	ExactDigest      string            `json:"exact_digest"`
}

// Sample selects one exact sequence head and an optional older checkpoint.
// A negative tail removes the checkpoint ref and exercises honest cold
// fallback. Tail is otherwise required to have been prepared in the manifest.
type Sample struct {
	Depth int
	Tail  int
}

// Prepare creates one maximum-depth signed chain. It never overwrites an
// existing fixture: retained evidence must not silently change under a cache
// key that already has readers.
func Prepare(ctx context.Context, directory string, plan FixturePlan) (Manifest, error) {
	if plan.Depth < 1 {
		return Manifest{}, errors.New("fixture depth must be positive")
	}
	if plan.GeneratorVersion == "" || plan.Shape == "" {
		return Manifest{}, errors.New("fixture generator version and shape are required")
	}
	if len(plan.PayloadBuckets) == 0 {
		return Manifest{}, errors.New("fixture payload buckets are required")
	}
	if plan.ActorCount == 0 {
		plan.ActorCount = 1
	}
	if plan.ActorCount < 1 {
		return Manifest{}, errors.New("fixture actor count must be positive")
	}
	minimumDepth := 1 + 2*(plan.ActorCount-1)
	if plan.Depth < minimumDepth {
		return Manifest{}, fmt.Errorf("fixture depth %d cannot contain %d actors; need at least %d records", plan.Depth, plan.ActorCount, minimumDepth)
	}
	if _, err := os.Stat(directory); err == nil {
		return Manifest{}, fmt.Errorf("fixture directory already exists: %s", directory)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return Manifest{}, err
	}
	if output, err := command(ctx, directory, "git", "init", "-q", "."); err != nil {
		return Manifest{}, fmt.Errorf("git init: %w: %s", err, output)
	}
	workspace, seed, err := app.Init(ctx, directory, "operator", 1<<20)
	if err != nil {
		return Manifest{}, err
	}
	seedHead, err := workspace.Store.Head(ctx, kernel.Ref(workspace.Config.Genesis))
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{
		Schema: manifestSchema, GeneratorVersion: plan.GeneratorVersion, Seed: plan.Seed,
		Shape: plan.Shape, Depth: plan.Depth, Repository: directory,
		Genesis: workspace.Config.Genesis, SeedEvent: seed.ID, Actor: "operator", ActorCount: plan.ActorCount,
		Heads: map[string]string{"1": seedHead}, Checkpoints: make(map[string]string),
	}
	logical := sha256.New()
	fmt.Fprintf(logical, "%s\n%d\n%s\n%d\n", plan.GeneratorVersion, plan.Seed, plan.Shape, plan.ActorCount)
	prng := perflane.NewPRNG(plan.Seed)
	depth := 1
	for index := 1; index < plan.ActorCount; index++ {
		_, records, addErr := workspace.AddActor(ctx, "operator", fmt.Sprintf("actor-%03d", index+1), "agent")
		if addErr != nil {
			return Manifest{}, fmt.Errorf("add fixture actor %d: %w", index+1, addErr)
		}
		for recordIndex, record := range records {
			depth++
			manifest.Heads[strconv.Itoa(depth)] = eventCommit(record.ID)
			fmt.Fprintf(logical, "%d\x00actor\x00%d\x00%d\n", depth, index+1, recordIndex)
		}
	}
	lastRequest, lastPromise, lastReport, lastArtifact := "", "", "", ""
	writer, err := newFixtureWriter(workspace, plan.Depth-depth)
	if err != nil {
		return Manifest{}, err
	}
	bulkBase := manifest.Heads[strconv.Itoa(depth)]
	for depth++; depth <= plan.Depth; depth++ {
		bucket := plan.PayloadBuckets[int(prng.Uint64n(uint64(len(plan.PayloadBuckets))))]
		act := generatedAct(plan.Shape, depth, bucket, workspace.Config.Actors["operator"].Fingerprint, seed.ID, lastRequest, lastPromise, lastReport, lastArtifact)
		commit, event, err := writer.append(ctx, manifest.Heads[strconv.Itoa(depth-1)], act.value)
		if err != nil {
			return Manifest{}, fmt.Errorf("generate depth %d (%s): %w", depth, act.label, err)
		}
		fmt.Fprintf(logical, "%d\x00%s\x00%d\x00%s\n", depth, act.label, bucket, act.logical)
		manifest.Heads[strconv.Itoa(depth)] = commit
		switch act.label {
		case "request":
			lastRequest = event
		case "promise":
			lastPromise = event
		case "report":
			lastReport = event
		case "artifact":
			lastArtifact = event
		}
	}
	if err := writer.close(ctx); err != nil {
		return Manifest{}, err
	}
	finalHead := manifest.Heads[strconv.Itoa(plan.Depth)]
	if finalHead != bulkBase {
		if err := workspace.Store.UpdateRef(ctx, kernel.Ref(workspace.Config.Genesis), finalHead, bulkBase); err != nil {
			return Manifest{}, fmt.Errorf("publish generated fixture head: %w", err)
		}
	}
	currentHead := finalHead
	for _, checkpointDepth := range plan.CheckpointDepths {
		checkpointHead := manifest.Heads[strconv.Itoa(checkpointDepth)]
		if checkpointHead == "" {
			return Manifest{}, fmt.Errorf("fixture has no head for checkpoint depth %d", checkpointDepth)
		}
		if err := workspace.Store.UpdateRef(ctx, kernel.Ref(workspace.Config.Genesis), checkpointHead, currentHead); err != nil {
			return Manifest{}, fmt.Errorf("select checkpoint depth %d: %w", checkpointDepth, err)
		}
		currentHead = checkpointHead
		reader, openErr := app.Open(ctx, directory)
		if openErr != nil {
			return Manifest{}, openErr
		}
		// Each retained checkpoint is an independent fixture input. Do not let
		// an earlier, shallower fixture checkpoint turn preparation of a deep
		// checkpoint into an enormous verified tail scan. A cold audit keeps
		// preparation bounded while preserving the ordinary checkpoint writer
		// and every verification invariant in the checkpoint it produces.
		if invalidateErr := reader.InvalidateCheckpoint(ctx); invalidateErr != nil {
			return Manifest{}, fmt.Errorf("clear selector before checkpoint depth %d: %w", checkpointDepth, invalidateErr)
		}
		if _, snapshotErr := reader.Snapshot(ctx); snapshotErr != nil {
			return Manifest{}, fmt.Errorf("verify checkpoint depth %d: %w", checkpointDepth, snapshotErr)
		}
		checkpoint, checkpointErr := workspace.Store.Head(ctx, kernel.CheckpointRef(workspace.Config.Genesis))
		if checkpointErr != nil {
			return Manifest{}, fmt.Errorf("checkpoint at depth %d was not written: %w", checkpointDepth, checkpointErr)
		}
		manifest.Checkpoints[strconv.Itoa(checkpointDepth)] = checkpoint
	}
	if currentHead != finalHead {
		if err := workspace.Store.UpdateRef(ctx, kernel.Ref(workspace.Config.Genesis), finalHead, currentHead); err != nil {
			return Manifest{}, fmt.Errorf("restore generated fixture head: %w", err)
		}
	}
	// The writer constructs every object from canonical signed inputs. Check
	// the final signature with Git as an independent format boundary; each
	// measured cold run then performs the ordinary full-chain verification.
	if err := workspace.Store.VerifySSHCommit(ctx, finalHead, "sequencer", publicKeyText(writer.sequencer)); err != nil {
		return Manifest{}, fmt.Errorf("verify generated fixture head: %w", err)
	}
	manifest.LogicalDigest = hex.EncodeToString(logical.Sum(nil))
	manifest.ExactDigest, err = exactObjectDigest(ctx, directory)
	if err != nil {
		return Manifest{}, err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(filepath.Join(directory, "performance-fixture.json"), encoded, 0o644); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func eventCommit(event string) string {
	if index := strings.LastIndexByte(event, ':'); index >= 0 {
		return event[index+1:]
	}
	return ""
}

type generated struct {
	label   string
	logical string
	value   app.Act
}

func generatedAct(shape string, depth, payloadBytes int, actor, seed, request, promise, report, artifact string) generated {
	text := strings.Repeat("x", payloadBytes)
	key := fmt.Sprintf("perf-%s-%d", shape, depth)
	assertion := generated{label: "assert", logical: text, value: app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: text,
		RestsOn: []string{seed}, IdempotencyKey: key,
	}}
	switch shape {
	case "open_request_heavy":
		return generated{label: "request", logical: text, value: app.Act{
			Verb: app.VerbState, Kind: workroom.KindRequest, Text: text,
			Body:    map[string]string{"to": actor, "conditions": "synthetic condition"},
			RestsOn: []string{seed}, IdempotencyKey: key,
		}}
	case "terminal_history_heavy":
		switch depth % 4 {
		case 1:
			if report != "" {
				return generated{label: "ratify", logical: report, value: app.Act{
					Verb: app.VerbRatify, Target: report, IdempotencyKey: key,
				}}
			}
		case 2:
			return generated{label: "request", logical: text, value: app.Act{
				Verb: app.VerbState, Kind: workroom.KindRequest, Text: text,
				Body:    map[string]string{"to": actor, "conditions": "synthetic condition"},
				RestsOn: []string{seed}, IdempotencyKey: key,
			}}
		case 3:
			if request != "" {
				return generated{label: "promise", logical: request, value: app.Act{
					Verb: app.VerbState, Kind: workroom.KindPromise, Text: "synthetic promise",
					RestsOn: []string{request}, IdempotencyKey: key,
				}}
			}
		case 0:
			if promise != "" {
				return generated{label: "report", logical: promise, value: app.Act{
					Verb: app.VerbState, Kind: workroom.KindReport, Text: "synthetic report",
					RestsOn: []string{promise}, IdempotencyKey: key,
				}}
			}
		}
	case "artifact_staleness_heavy":
		if depth%2 == 0 || artifact == "" {
			return generated{label: "artifact", logical: text, value: app.Act{
				Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "synthetic artifact",
				Body:    map[string]string{"path": "performance/fixture", "commit": fmt.Sprintf("%040x", depth)},
				RestsOn: []string{seed}, IdempotencyKey: key,
			}}
		}
		return generated{label: "supersede", logical: artifact, value: app.Act{
			Verb: app.VerbSupersede, Target: artifact, Text: "synthetic succession",
			RestsOn: []string{seed}, IdempotencyKey: key,
		}}
	case "assert_heavy", "linear":
		return assertion
	}
	return assertion
}

// LoadManifest reads and validates the immutable fixture record.
func LoadManifest(directory string) (Manifest, error) {
	content, err := os.ReadFile(filepath.Join(directory, "performance-fixture.json"))
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	if manifest.Schema != manifestSchema || manifest.Repository == "" || manifest.Genesis == "" || manifest.ExactDigest == "" || manifest.ActorCount < 1 {
		return Manifest{}, errors.New("invalid performance fixture manifest")
	}
	return manifest, nil
}

// Materialize creates isolated writable refs/config over the source fixture's
// immutable object store. The returned cleanup is safe to call more than once.
func Materialize(ctx context.Context, fixture, destination string, sample Sample) (Manifest, func() error, error) {
	manifest, err := LoadManifest(fixture)
	if err != nil {
		return Manifest{}, nil, err
	}
	head := manifest.Heads[strconv.Itoa(sample.Depth)]
	if head == "" {
		return Manifest{}, nil, fmt.Errorf("fixture has no head at depth %d", sample.Depth)
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return Manifest{}, nil, err
	}
	cleanup := func() error { return os.RemoveAll(destination) }
	fail := func(cause error) (Manifest, func() error, error) {
		_ = cleanup()
		return Manifest{}, nil, cause
	}
	if output, commandErr := command(ctx, destination, "git", "init", "-q", "."); commandErr != nil {
		return fail(fmt.Errorf("git init sample: %w: %s", commandErr, output))
	}
	sourceGit, err := gitDir(ctx, fixture)
	if err != nil {
		return fail(err)
	}
	destinationGit, err := gitDir(ctx, destination)
	if err != nil {
		return fail(err)
	}
	alternates := filepath.Join(destinationGit, "objects", "info", "alternates")
	if err := os.MkdirAll(filepath.Dir(alternates), 0o755); err != nil {
		return fail(err)
	}
	objects, err := filepath.Abs(filepath.Join(sourceGit, "objects"))
	if err != nil {
		return fail(err)
	}
	if err := os.WriteFile(alternates, []byte(objects+"\n"), 0o644); err != nil {
		return fail(err)
	}
	if err := copyTree(filepath.Join(sourceGit, "gitseq"), filepath.Join(destinationGit, "gitseq")); err != nil {
		return fail(err)
	}
	if err := rewriteConfig(filepath.Join(destinationGit, "gitseq")); err != nil {
		return fail(err)
	}
	if output, commandErr := command(ctx, destination, "git", "update-ref", string(kernel.Ref(manifest.Genesis)), head); commandErr != nil {
		return fail(fmt.Errorf("set sequence head: %w: %s", commandErr, output))
	}
	checkpointRef := string(kernel.CheckpointRef(manifest.Genesis))
	checkpointPointer := filepath.Join(destinationGit, "gitseq", "checkpoints", manifest.Genesis+".json")
	if sample.Tail >= 0 {
		checkpointDepth := sample.Depth - sample.Tail
		checkpoint := manifest.Checkpoints[strconv.Itoa(checkpointDepth)]
		if checkpoint == "" {
			return fail(fmt.Errorf("fixture has no checkpoint for depth %d tail %d", sample.Depth, sample.Tail))
		}
		if output, commandErr := command(ctx, destination, "git", "update-ref", checkpointRef, checkpoint); commandErr != nil {
			return fail(fmt.Errorf("set checkpoint: %w: %s", commandErr, output))
		}
		pointer, encodeErr := json.Marshal(map[string]string{"schema": "gitseq-checkpoint-pointer@1", "commit": checkpoint})
		if encodeErr != nil {
			return fail(encodeErr)
		}
		if mkdirErr := os.MkdirAll(filepath.Dir(checkpointPointer), 0o700); mkdirErr != nil {
			return fail(fmt.Errorf("create checkpoint pointer directory: %w", mkdirErr))
		}
		if writeErr := os.WriteFile(checkpointPointer, pointer, 0o600); writeErr != nil {
			return fail(fmt.Errorf("set checkpoint pointer: %w", writeErr))
		}
	} else if output, commandErr := command(ctx, destination, "git", "update-ref", "-d", checkpointRef); commandErr != nil {
		return fail(fmt.Errorf("remove checkpoint: %w: %s", commandErr, output))
	} else if removeErr := os.Remove(checkpointPointer); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return fail(fmt.Errorf("remove checkpoint pointer: %w", removeErr))
	}
	return manifest, cleanup, nil
}

func rewriteConfig(meta string) error {
	path := filepath.Join(meta, "config.json")
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var config app.Config
	if err := json.Unmarshal(content, &config); err != nil {
		return err
	}
	config.SequencerKey = filepath.Join(meta, filepath.Base(config.SequencerKey))
	// A sample may select a head older than the source fixture's last verified
	// position. That source-local rollback witness must not cross into the new
	// writable sample; its first ordinary audit establishes its own frontier.
	config.VerifiedFrontier = nil
	for name, actor := range config.Actors {
		actor.KeyFile = filepath.Join(meta, "actors", filepath.Base(actor.KeyFile))
		config.Actors[name] = actor
	}
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o600)
}

func exactObjectDigest(ctx context.Context, repository string) (string, error) {
	output, err := command(ctx, repository, "git", "cat-file", "--batch-all-objects", "--batch-check=%(objectname)")
	if err != nil {
		return "", fmt.Errorf("enumerate fixture objects: %w: %s", err, output)
	}
	lines := strings.Fields(output)
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n") + "\n"))
	return hex.EncodeToString(sum[:]), nil
}

func gitDir(ctx context.Context, repository string) (string, error) {
	output, err := command(ctx, repository, "git", "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", fmt.Errorf("resolve git dir: %w: %s", err, output)
	}
	return strings.TrimSpace(output), nil
}

func command(ctx context.Context, directory, name string, arguments ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, arguments...)
	cmd.Dir = directory
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		info, err := entry.Info()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

// ParseObjectList is kept small and exported only through tests in this
// package; accepting an io.Reader makes malformed/incomplete evidence easy to
// exercise without starting Git.
func parseObjectList(reader io.Reader) ([]string, error) {
	var values []string
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		value := strings.TrimSpace(scanner.Text())
		if value != "" {
			values = append(values, value)
		}
	}
	return values, scanner.Err()
}
