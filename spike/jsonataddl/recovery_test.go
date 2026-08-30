package jsonataddl

// Crash-recovery sweep. A recording VFS wrapped around the OS VFS captures
// every durable mutation the projection writer makes (WAL and main-database
// writes, truncations, and file deletions, in order). Because the on-disk
// state only changes at those mutations, replaying the first k mutations
// onto empty files reproduces the exact bytes a process kill would leave at
// every possible instant — including the inconvenient ones between two WAL
// frame writes of a single commit, one write short of a commit frame, and in
// the middle of a checkpoint backfill — plus a torn variant that applies only
// half of the next write. Each image is then opened cold, and recovery must
// yield exactly the clean prefix projection at whatever frontier it reports.
//
// The model is process death: completed writes persist, in order. Lost or
// reordered writes that an OS crash or power failure could produce under
// synchronous=NORMAL are outside this driver seam; see RECOVERY.md.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"database/sql"

	"github.com/generalbusiness-ai/gitseq/host"
	sqlitedriver "github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/vfs"
)

type mutationOp struct {
	file   string // "db", "wal", or "other"
	kind   string // "write", "truncate", "delete"
	offset int64
	size   int64
	data   []byte
}

type crashMarker struct {
	label string
	ops   int // mutation ops recorded before this marker
}

type crashRecorder struct {
	mu      sync.Mutex
	ops     []mutationOp
	markers []crashMarker
}

func (r *crashRecorder) record(op mutationOp) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ops = append(r.ops, op)
}

func (r *crashRecorder) marker(label string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markers = append(r.markers, crashMarker{label: label, ops: len(r.ops)})
}

func (r *crashRecorder) snapshot() ([]mutationOp, []crashMarker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]mutationOp(nil), r.ops...), append([]crashMarker(nil), r.markers...)
}

type crashVFS struct {
	os       vfs.VFSFilename
	recorder *crashRecorder
}

func (v crashVFS) Open(name string, flags vfs.OpenFlag) (vfs.File, vfs.OpenFlag, error) {
	return v.os.Open(name, flags)
}

func (v crashVFS) OpenFilename(name *vfs.Filename, flags vfs.OpenFlag) (vfs.File, vfs.OpenFlag, error) {
	file, out, err := v.os.OpenFilename(name, flags)
	if err != nil {
		return file, out, err
	}
	if flags&(vfs.OPEN_DELETEONCLOSE|vfs.OPEN_TEMP_DB|vfs.OPEN_TRANSIENT_DB|vfs.OPEN_TEMP_JOURNAL|vfs.OPEN_SUBJOURNAL) != 0 {
		// Ephemeral files (statement subjournals, temp databases) are gone
		// after any crash and recovery never reads them; they are not part of
		// the durable state this recorder replays. A main or super journal
		// would matter, but WAL mode must never open one: leave those
		// classified "other" so the analysis fails loudly if one appears.
		return file, out, nil
	}
	kind := "other"
	switch {
	case flags&vfs.OPEN_MAIN_DB != 0:
		kind = "db"
	case flags&vfs.OPEN_WAL != 0:
		kind = "wal"
	case flags&vfs.OPEN_MAIN_JOURNAL != 0:
		// SQLite initializes a brand-new database in rollback-journal mode
		// before the journal_mode=wal switch takes hold, so the creation
		// window really does write a main journal and raw database pages.
		kind = "journal"
	}
	wrapped := &crashFile{File: file, kind: kind, recorder: v.recorder}
	if _, ok := file.(vfs.FileSharedMemory); ok {
		return crashFileShm{wrapped}, out, nil
	}
	return wrapped, out, nil
}

func (v crashVFS) Delete(name string, syncDir bool) error {
	switch {
	case strings.HasSuffix(name, "-wal"):
		v.recorder.record(mutationOp{file: "wal", kind: "delete"})
	case strings.HasSuffix(name, "-journal"):
		v.recorder.record(mutationOp{file: "journal", kind: "delete"})
	case strings.HasSuffix(name, "-shm"):
		// The WAL index is volatile shared memory; its deletion is not a
		// durable mutation and cold recovery never reads it.
	default:
		v.recorder.record(mutationOp{file: "other", kind: "delete"})
	}
	return v.os.Delete(name, syncDir)
}

func (v crashVFS) Access(name string, flags vfs.AccessFlag) (bool, error) {
	return v.os.Access(name, flags)
}

func (v crashVFS) FullPathname(name string) (string, error) {
	return v.os.FullPathname(name)
}

type crashFile struct {
	vfs.File
	kind     string
	recorder *crashRecorder
}

func (f *crashFile) WriteAt(p []byte, off int64) (int, error) {
	f.recorder.record(mutationOp{file: f.kind, kind: "write", offset: off, size: int64(len(p)), data: append([]byte(nil), p...)})
	return f.File.WriteAt(p, off)
}

func (f *crashFile) Truncate(size int64) error {
	f.recorder.record(mutationOp{file: f.kind, kind: "truncate", size: size})
	return f.File.Truncate(size)
}

func (f *crashFile) Unwrap() vfs.File { return f.File }

func (f *crashFile) DeviceCharacteristics() vfs.DeviceCharacteristic {
	// Batch-atomic writes would let SQLite skip durable steps this recorder
	// does not model; withhold the capability from the wrapped connection.
	return f.File.DeviceCharacteristics() &^ vfs.IOCAP_BATCH_ATOMIC
}

type crashFileShm struct {
	*crashFile
}

func (f crashFileShm) SharedMemory() vfs.SharedMemory {
	return f.crashFile.File.(vfs.FileSharedMemory).SharedMemory()
}

// materializeImage replays the first upto mutations (plus, when torn, half of
// the next write) onto empty files, reproducing the on-disk bytes at one
// crash instant.
func materializeImage(t *testing.T, dir string, ops []mutationOp, upto int, torn bool) string {
	t.Helper()
	paths := map[string]string{
		"db":      filepath.Join(dir, "crash.sqlite"),
		"wal":     filepath.Join(dir, "crash.sqlite-wal"),
		"journal": filepath.Join(dir, "crash.sqlite-journal"),
	}
	handles := map[string]*os.File{}
	handle := func(file string) *os.File {
		if open, exists := handles[file]; exists {
			return open
		}
		open, err := os.OpenFile(paths[file], os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		handles[file] = open
		return open
	}
	apply := func(op mutationOp, partial bool) {
		switch op.kind {
		case "write":
			data := op.data
			if partial {
				data = data[:len(data)/2]
			}
			if _, err := handle(op.file).WriteAt(data, op.offset); err != nil {
				t.Fatal(err)
			}
		case "truncate":
			if err := handle(op.file).Truncate(op.size); err != nil {
				t.Fatal(err)
			}
		case "delete":
			if open, exists := handles[op.file]; exists {
				open.Close()
				delete(handles, op.file)
			}
			if err := os.Remove(paths[op.file]); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
	}
	for index := 0; index < upto; index++ {
		apply(ops[index], false)
	}
	if torn {
		apply(ops[upto], true)
	}
	for _, open := range handles {
		if err := open.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return paths["db"]
}

type imageFrontier struct {
	present  bool
	position int
	complete bool
	gapEvent string
}

// inspectImage opens a crash image cold — exactly as a restarted process
// would — checks integrity, reads the recovered frontier, and produces a
// logical dump of every table except the frontier record itself. health is
// "ok", or a description of why the image is unreadable; an unreadable image
// is only admissible inside the pre-initialization creation window, where
// the disposable projection's discard-and-rebuild rule covers it.
func inspectImage(t *testing.T, ctx context.Context, path string) (imageFrontier, string, string) {
	t.Helper()
	database, err := sqlitedriver.Open(sqliteDSN(path, false), nil)
	if err != nil {
		return imageFrontier{}, "", fmt.Sprintf("open: %v", err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	var integrity string
	if err := database.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return imageFrontier{}, "", fmt.Sprintf("integrity check did not run: %v", err)
	}
	if integrity != "ok" {
		return imageFrontier{}, "", fmt.Sprintf("integrity: %s", integrity)
	}
	var frontier imageFrontier
	var complete int
	var interpretedEvent, gapReason string
	err = database.QueryRowContext(ctx, `SELECT interpreted_event, interpreted_position, complete, gap_event, gap_reason FROM gitseq_frontier WHERE singleton = 1`).
		Scan(&interpretedEvent, &frontier.position, &complete, &frontier.gapEvent, &gapReason)
	switch {
	case err == nil:
		frontier.present, frontier.complete = true, complete == 1
	case strings.Contains(err.Error(), "no such table"):
		// Recovery rolled back to before the single initialization commit:
		// an unambiguous discard-and-rebuild signal.
	default:
		t.Fatal(err)
	}
	return frontier, logicalDump(t, ctx, database), "ok"
}

func logicalDump(t *testing.T, ctx context.Context, database *sql.DB) string {
	t.Helper()
	names, err := database.QueryContext(ctx, `SELECT name, type FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' AND type IN ('table', 'view') ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer names.Close()
	var dump strings.Builder
	var tables []string
	for names.Next() {
		var name, kind string
		if err := names.Scan(&name, &kind); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&dump, "%s %s\n", kind, name)
		if kind == "table" && name != "gitseq_frontier" {
			tables = append(tables, name)
		}
	}
	if err := names.Err(); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		rows, err := database.QueryContext(ctx, `SELECT * FROM `+quoteIdentifier(table))
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var rendered []string
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			for index := range values {
				values[index] = jsonValue(values[index])
			}
			rendered = append(rendered, fmt.Sprintf("%#v", values))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		sort.Strings(rendered)
		fmt.Fprintf(&dump, "rows %s\n", table)
		for _, row := range rendered {
			fmt.Fprintf(&dump, "  %s\n", row)
		}
	}
	return dump.String()
}

// buildReferenceDumps builds one clean projection per interpreted prefix and
// captures its logical dump. These are the only states any crash image is
// allowed to recover to.
func buildReferenceDumps(t *testing.T, ctx context.Context, profile *Profile, log host.Log) []string {
	t.Helper()
	dumps := make([]string, log.Depth+1)
	for position := 0; position <= log.Depth; position++ {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("ref-%d.sqlite", position))
		reference := buildProjection(t, ctx, profile, prefixLog(log, position), path)
		reference.Close()
		database, err := sqlitedriver.Open(sqliteDSN(path, false), nil)
		if err != nil {
			t.Fatal(err)
		}
		dumps[position] = logicalDump(t, ctx, database)
		database.Close()
	}
	return dumps
}

type sweepAnalysis struct {
	initCommit   int         // op index of the initialization commit-frame write
	eventCommits map[int]int // position -> op index of its commit-frame write
	lastWal      int         // op index of the final WAL write in the run
	dbWrites     int
	walResets    int
	spannedEvent int // an event whose commit flushed two or more WAL frames
}

func lastWalWriteBefore(ops []mutationOp, boundary int) int {
	for index := boundary - 1; index >= 0; index-- {
		if ops[index].file == "wal" && ops[index].kind == "write" {
			return index
		}
	}
	return -1
}

func analyzeSweep(t *testing.T, ops []mutationOp, markers []crashMarker) sweepAnalysis {
	t.Helper()
	analysis := sweepAnalysis{eventCommits: map[int]int{}, spannedEvent: -1, initCommit: -1}
	previous := 0
	for _, marker := range markers {
		commit := lastWalWriteBefore(ops, marker.ops)
		if marker.label != "checkpoint" && commit < previous {
			t.Fatalf("marker %q has no WAL commit write in its span", marker.label)
		}
		if marker.label == "init" {
			analysis.initCommit = commit
		}
		if position, found := strings.CutPrefix(marker.label, "event-"); found {
			number, err := strconv.Atoi(position)
			if err != nil {
				t.Fatal(err)
			}
			analysis.eventCommits[number] = commit
			span := 0
			for index := previous; index <= commit; index++ {
				if ops[index].file == "wal" && ops[index].kind == "write" {
					span++
				}
			}
			if span >= 2 && analysis.spannedEvent < 0 {
				analysis.spannedEvent = number
			}
		}
		previous = marker.ops
	}
	analysis.lastWal = lastWalWriteBefore(ops, len(ops))
	for _, op := range ops {
		if op.file == "other" {
			t.Fatalf("mutation on an unmodeled file kind: %+v", op)
		}
		if op.file == "db" && op.kind == "write" {
			analysis.dbWrites++
		}
		if op.file == "wal" && (op.kind == "truncate" || op.kind == "delete") {
			analysis.walResets++
		}
	}
	return analysis
}

func (a sweepAnalysis) expectedPosition(boundary int) int {
	expected := 0
	for _, commit := range a.eventCommits {
		if commit < boundary {
			expected++
		}
	}
	return expected
}

// crashBuild replays the log through a writer whose every durable mutation is
// recorded, marking the initialization commit and each event commit, and
// forcing a WAL checkpoint mid-replay after checkpointAfter (0 disables).
func crashBuild(t *testing.T, ctx context.Context, profile *Profile, log host.Log, vfsName string, checkpointAfter int) (*Projection, error, []mutationOp, []crashMarker) {
	t.Helper()
	recorder := &crashRecorder{}
	vfs.Register(vfsName, crashVFS{os: vfs.Find("os").(vfs.VFSFilename), recorder: recorder})
	t.Cleanup(func() { vfs.Unregister(vfsName) })
	options := newBuildOptions()
	options.writerVFS = vfsName
	options.afterInitialize = func(context.Context, *sql.DB) error {
		recorder.marker("init")
		return nil
	}
	options.afterEvent = func(ctx context.Context, writer *sql.DB, position int) error {
		recorder.marker(fmt.Sprintf("event-%d", position))
		if position == checkpointAfter {
			var busy, frames, checkpointed int
			if err := writer.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &frames, &checkpointed); err != nil {
				return err
			}
			if busy != 0 {
				return fmt.Errorf("mid-replay checkpoint was busy")
			}
			recorder.marker("checkpoint")
		}
		return nil
	}
	projection, err := build(ctx, profile, log, filepath.Join(t.TempDir(), "crash-build.sqlite"), options)
	ops, markers := recorder.snapshot()
	return projection, err, ops, markers
}

func sweepImages(t *testing.T, ctx context.Context, ops []mutationOp, analysis sweepAnalysis, refs []string, expectGap func(boundary int) string, expectComplete func(boundary int) bool) {
	t.Helper()
	verified, discarded := 0, 0
	imageRoot := t.TempDir()
	verify := func(boundary int, torn bool) {
		dir := filepath.Join(imageRoot, fmt.Sprintf("image-%d-%v", boundary, torn))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(dir)
		path := materializeImage(t, dir, ops, boundary, torn)
		frontier, dump, health := inspectImage(t, ctx, path)
		if health != "ok" {
			// A crash inside the pre-initialization creation window can leave
			// a file SQLite refuses (for example a torn first database page
			// beside a not-yet-valid rollback journal). That is loudly
			// detectable, never a plausible projection, and the disposable
			// projection's rule for it is discard and rebuild. Anywhere past
			// the initialization commit it would be a real defect.
			if boundary > analysis.initCommit {
				t.Fatalf("crash image before op %d (torn=%v): unreadable after the initialization commit at op %d: %s", boundary, torn, analysis.initCommit, health)
			}
			discarded++
			return
		}
		if !frontier.present {
			if boundary > analysis.initCommit {
				t.Fatalf("crash image before op %d (torn=%v): frontier missing after the initialization commit at op %d", boundary, torn, analysis.initCommit)
			}
			if dump != "" {
				t.Fatalf("crash image before op %d (torn=%v): no frontier, yet a partial schema survived recovery:\n%s", boundary, torn, dump)
			}
			verified++
			return
		}
		expected := analysis.expectedPosition(boundary)
		if frontier.position != expected {
			t.Fatalf("crash image before op %d (torn=%v): recovered frontier position %d, want %d", boundary, torn, frontier.position, expected)
		}
		if want := expectComplete(boundary); frontier.complete != want {
			t.Fatalf("crash image before op %d (torn=%v): complete = %v, want %v", boundary, torn, frontier.complete, want)
		}
		if want := expectGap(boundary); frontier.gapEvent != want {
			t.Fatalf("crash image before op %d (torn=%v): gap event %q, want %q", boundary, torn, frontier.gapEvent, want)
		}
		if dump != refs[frontier.position] {
			t.Fatalf("crash image before op %d (torn=%v): recovered state does not match the clean prefix projection at its recovered frontier %d\n--- recovered ---\n%s--- clean prefix %d ---\n%s",
				boundary, torn, frontier.position, dump, frontier.position, refs[frontier.position])
		}
		verified++
	}
	for boundary := 0; boundary <= len(ops); boundary++ {
		verify(boundary, false)
		if boundary < len(ops) && ops[boundary].kind == "write" {
			verify(boundary, true)
		}
	}
	t.Logf("verified %d crash images over %d durable mutations (%d creation-window images unreadable and discarded; init commit at %d, event commits %v, %d main-db page writes, %d WAL resets)",
		verified, len(ops), discarded, analysis.initCommit, analysis.eventCommits, analysis.dbWrites, analysis.walResets)
}

// TestCrashSweepRecoversAtomicFrontier kills the projection build at every
// durable write boundary (and mid-write) across replay, mid-replay
// checkpoint, completion, and close-time checkpoint, and requires every
// recovered image to be exactly one clean prefix projection whose frontier
// names that prefix.
func TestCrashSweepRecoversAtomicFrontier(t *testing.T) {
	ctx := context.Background()
	profile, log := inventoryLog(t, ctx, historyActs...)
	projection, err, ops, markers := crashBuild(t, ctx, profile, log, "jsonataddl-crash-sweep", 5)
	if err != nil {
		t.Fatal(err)
	}
	projection.Close()

	analysis := analyzeSweep(t, ops, markers)
	if len(analysis.eventCommits) != log.Depth {
		t.Fatalf("recorded %d event commits, want %d", len(analysis.eventCommits), log.Depth)
	}
	// The inconvenient interruption points must actually exist in this run;
	// a sweep over convenient points only would prove nothing.
	if analysis.spannedEvent < 0 {
		t.Fatal("no event commit flushed two or more WAL frames; the sweep cannot hit a mid-commit boundary")
	}
	if analysis.dbWrites == 0 {
		t.Fatal("no checkpoint backfill writes were recorded; the sweep cannot hit a mid-checkpoint boundary")
	}
	if analysis.walResets == 0 {
		t.Fatal("no WAL reset was recorded; the sweep cannot hit a crash during WAL truncation")
	}

	refs := buildReferenceDumps(t, ctx, profile, log)
	sweepImages(t, ctx, ops, analysis, refs,
		func(int) string { return "" },
		func(boundary int) bool { return analysis.lastWal < boundary })
}

// TestCrashSweepRecordsGapAtomically drives the same sweep through a replay
// whose final event fails its fold (duplicate reservation id): the failed
// transaction must leave no trace at any crash instant, and the gap metadata
// becomes durable only at its own commit — before that, a crash yields the
// clean prefix with the gap merely undiscovered, which a suffix replay would
// re-derive.
func TestCrashSweepRecordsGapAtomically(t *testing.T) {
	ctx := context.Background()
	acts := []testAct{
		{schema: "stock_received", payload: map[string]any{"id": "s1", "sku": "ink", "qty": 10}},
		{schema: "reservation_requested", payload: map[string]any{"id": "r1", "sku": "ink", "qty": 2}},
		{schema: "reservation_requested", payload: map[string]any{"id": "r1", "sku": "ink", "qty": 2}},
	}
	profile, log := inventoryLog(t, ctx, acts...)
	projection, err, ops, markers := crashBuild(t, ctx, profile, log, "jsonataddl-crash-gap", 0)
	if projection == nil {
		t.Fatal("gap build did not return the readable prefix")
	}
	projection.Close()
	var gap *GapError
	if !errors.As(err, &gap) || gap.Position != log.Depth {
		t.Fatalf("Build error = %v, want final-position GapError", err)
	}

	analysis := analyzeSweep(t, ops, markers)
	if len(analysis.eventCommits) != log.Depth-1 {
		t.Fatalf("recorded %d event commits, want %d", len(analysis.eventCommits), log.Depth-1)
	}

	refs := buildReferenceDumps(t, ctx, profile, prefixLog(log, log.Depth-1))
	sweepImages(t, ctx, ops, analysis, refs,
		func(boundary int) string {
			if analysis.lastWal < boundary {
				return log.Records[log.Depth-1].ID
			}
			return ""
		},
		func(int) bool { return false })
}
