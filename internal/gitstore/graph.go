package gitstore

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
)

// GraphCommit is one row of the repo-underneath railway: an ordinary git
// commit with its parents, decorations, and any Rests-On trailers that
// bridge it to workroom events.
type GraphCommit struct {
	Hash    string   `json:"hash"`
	Parents []string `json:"parents"`
	Refs    []string `json:"refs,omitempty"`
	Subject string   `json:"subject"`
	Author  string   `json:"author"`
	Time    int64    `json:"time"`
	Body    string   `json:"body,omitempty"`
	RestsOn []string `json:"rests_on,omitempty"`
}

// Graph lists recent commits across branches and tags in topological order,
// newest first. It deliberately excludes refs/seq/* — the sequenced log is
// the agreed-sequence pane, not a railway lane.
// graphFields is the number of NUL-separated fields each commit contributes.
// The count is the whole of the framing: git refuses a NUL byte in a commit
// log message and in an ident, so no field a committer controls can produce
// one, and a fixed stride over a flat NUL-split stream cannot be desynchronised
// by anything a commit can carry.
//
// The earlier framing used %x1f between fields and %x1e between records. Both
// are ordinary bytes that git stores verbatim, and four of these fields are
// committer-controlled: subject, author name, body, and the trailer value. A
// body containing those separators forged a whole row - a hash, subject, author
// and far-future timestamp of the committer's choosing, ordered wherever they
// wanted it. Anyone who could push a branch could write history into this view.
const graphFields = 8

func (s Store) Graph(ctx context.Context, limit int) ([]GraphCommit, error) {
	if limit <= 0 {
		limit = 60
	}
	format := "%H%x00%P%x00%D%x00%s%x00%an%x00%at%x00%(trailers:key=Rests-On,valueonly=true)%x00%b"
	output, err := s.run(ctx, nil, nil,
		"log", "-z", "--topo-order", "-n", strconv.Itoa(limit),
		"--branches", "--tags", "--format="+format)
	if err != nil {
		// An unborn repository (no commits yet) renders an empty railway.
		if strings.Contains(err.Error(), "does not have any commits") || strings.Contains(err.Error(), "bad revision") {
			return nil, nil
		}
		return nil, err
	}
	commits, err := parseGraphStream(output)
	if err != nil {
		return nil, err
	}
	if err := s.refuseMalformedCommits(ctx, commits); err != nil {
		return nil, err
	}
	return commits, nil
}

// refuseMalformedCommits rejects rows whose stored object is not the immutable
// object the railway named.
//
// The framing above is necessary and not sufficient. `git commit` refuses a NUL
// in a log message, so no honestly-created commit carries the separator - but
// `git hash-object --literally` writes one anyway, receive-pack accepts it, and
// `git fsck --strict` calls it nulInCommit. For such an object `git log -z`
// still emits a full, valid-looking set of fields with the body silently
// truncated at the NUL. The porcelain guarantee covers objects git wrote, not
// objects git merely stores.
//
// This reads through AuditBatch rather than a reader of its own. An earlier
// revision hand-rolled the cat-file batch protocol here and got progressively
// closer to it over two rounds of review while never reaching it: first a byte
// scan that returned success for an object it never saw, then a framing parser
// that accepted any payload paired with any requested hash. AuditBatch already
// validates the id, type, size, bounds and terminator, recomputes the object
// hash so the bytes are provably the object named, and poisons the reader when
// the stream desynchronises. Duplicating a security protocol inside its own
// package inherits none of that.
func (s Store) refuseMalformedCommits(ctx context.Context, commits []GraphCommit) error {
	if len(commits) == 0 {
		return nil
	}
	format, err := s.ObjectFormat(ctx)
	if err != nil {
		return err
	}
	batch, err := s.OpenAuditBatch(ctx, format)
	if err != nil {
		return err
	}
	return verifyCommitObjects(batch, commits)
}

// verifyCommitObjects reads each commit and closes the batch, returning the
// first refusal. Separated from opening the batch so a test can supply one
// whose stream already holds an unread response: Graph's own flow asks for
// exactly what it reads, so the surplus case cannot arise there, and a
// propagation nothing can exercise is a claim rather than a check.
func verifyCommitObjects(batch *AuditBatch, commits []GraphCommit) error {
	// Close is not deferred-and-discarded: it is where a surplus response after
	// the last requested object is detected, so throwing its error away would
	// leave that case unrefused no matter what the loop below checks.
	closed := false
	defer func() {
		if !closed {
			_ = batch.Close()
		}
	}()
	for _, commit := range commits {
		content, err := batch.readObject(commit.Hash, "commit", maxGraphCommitBytes)
		if err != nil {
			return fmt.Errorf("commit %s: %w", commit.Hash, err)
		}
		if bytes.IndexByte(content, 0) >= 0 {
			return fmt.Errorf("commit %s carries a NUL byte: git fsck reports these as nulInCommit, and rendering it would silently truncate its metadata", commit.Hash)
		}
	}
	closed = true
	return batch.Close()
}

// maxGraphCommitBytes bounds one commit object read for the railway. A commit
// message is prose; anything past this is not a row worth drawing, and the
// bound keeps a hostile object from choosing this process's allocation.
const maxGraphCommitBytes = 1 << 20

// parseGraphStream turns the NUL-framed stream into rows. It is separate from
// the command so the refusal paths can be exercised directly: they are the
// branches that used to fail silently, and a test that has to stage a
// malformed repository to reach them would not reach them at all.
func parseGraphStream(output []byte) ([]GraphCommit, error) {
	if len(output) == 0 {
		return nil, nil
	}

	fields := strings.Split(string(output), "\x00")
	// -z terminates the final record too, leaving one empty tail token. Only
	// that one is dropped: an empty field in the middle is meaningful, since a
	// commit with no decorations or no trailers has them legitimately.
	if fields[len(fields)-1] == "" {
		fields = fields[:len(fields)-1]
	}
	if len(fields)%graphFields != 0 {
		// Fail closed. The previous parser skipped a short record silently, so
		// a truncated or malformed commit vanished from the railway rather than
		// announcing itself - a view that quietly omits history is worse than
		// one that refuses to render.
		return nil, fmt.Errorf("git log returned %d fields, not a multiple of %d: the commit stream is malformed", len(fields), graphFields)
	}

	commits := make([]GraphCommit, 0, len(fields)/graphFields)
	for start := 0; start < len(fields); start += graphFields {
		record := fields[start : start+graphFields]
		commit := GraphCommit{Hash: record[0], Subject: record[3], Author: record[4]}
		if record[1] != "" {
			commit.Parents = strings.Fields(record[1])
		}
		for _, ref := range strings.Split(record[2], ", ") {
			ref = strings.TrimSpace(strings.TrimPrefix(ref, "HEAD -> "))
			if ref != "" && ref != "HEAD" && !strings.HasPrefix(ref, "refs/seq/") {
				commit.Refs = append(commit.Refs, strings.TrimPrefix(strings.TrimPrefix(ref, "tag: "), "refs/heads/"))
			}
		}
		seconds, err := strconv.ParseInt(record[5], 10, 64)
		if err != nil {
			// Also fail closed. A zero time silently sorted a commit to the
			// beginning of the railway, which a hostile timestamp could aim.
			return nil, fmt.Errorf("commit %q carries an unparsable timestamp %q", record[0], record[5])
		}
		commit.Time = seconds
		for _, trailer := range strings.Split(record[6], "\n") {
			if trailer = strings.TrimSpace(trailer); trailer != "" {
				commit.RestsOn = append(commit.RestsOn, trailer)
			}
		}
		commit.Body = strings.TrimSpace(record[7])
		commits = append(commits, commit)
	}
	return commits, nil
}
