package gitstore

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/observe"
)

var attachmentName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type Store struct {
	Repo     string
	Observer observe.Observer
}

// CommitMetadata is the identity-bearing portion of a commit needed by kernel
// history scans and checkpoint recovery. It deliberately omits author and
// signature headers: callers verify the commit signature separately before
// trusting the parent, message, tree, or sequencer-signed committer time.
type CommitMetadata struct {
	OID       string
	Tree      string
	Parents   []string
	Timestamp int64
	Message   string
}

func InitBare(ctx context.Context, path, objectFormat string) (Store, error) {
	if objectFormat != "sha1" && objectFormat != "sha256" {
		return Store{}, fmt.Errorf("unsupported object format %q", objectFormat)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Store{}, err
	}
	cmd := exec.CommandContext(ctx, "git", "init", "--bare", "--object-format="+objectFormat, path)
	if output, err := cmd.CombinedOutput(); err != nil {
		return Store{}, fmt.Errorf("git init: %w: %s", err, output)
	}
	return Store{Repo: path}, nil
}

func (s Store) run(ctx context.Context, input []byte, env []string, args ...string) ([]byte, error) {
	return s.runWithEnvironment(ctx, input, append(os.Environ(), env...), args...)
}

func (s Store) runHermetic(ctx context.Context, input []byte, env []string, args ...string) ([]byte, error) {
	return s.runWithEnvironment(ctx, input, hermeticGitEnvironment(env), args...)
}

func (s Store) runWithEnvironment(ctx context.Context, input []byte, environment []string, args ...string) ([]byte, error) {
	observer := s.Observer
	if observer == nil {
		observer = observe.FromContext(ctx)
	}
	done := observe.Begin(ctx, observer, observe.OperationGit, observe.GitPath(args))
	argv := storeGitArguments(s.Repo, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = environment
	output, err := cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(output))
		if done != nil {
			done(err)
		}
		return nil, err
	}
	if done != nil {
		done(nil)
	}
	return bytes.TrimSpace(output), nil
}

// storeGitArguments keeps Git replacement refs outside the immutable object
// boundary. Every command that reads or verifies objects must resolve the OID
// that the sequence actually names, never a repository-local substitute.
func storeGitArguments(repo string, args ...string) []string {
	argv := make([]string, 0, len(args)+3)
	argv = append(argv, "--no-replace-objects", "--git-dir", repo)
	return append(argv, args...)
}

// hermeticGitEnvironment removes ambient configuration injection while
// retaining the repository-local configuration that declares its object
// format. Signing and verification pin their executable settings explicitly.
func hermeticGitEnvironment(extra []string) []string {
	environment := make([]string, 0, len(os.Environ())+len(extra)+3)
	for _, variable := range append(os.Environ(), extra...) {
		name, _, _ := strings.Cut(variable, "=")
		if name == "GIT_CONFIG" || strings.HasPrefix(name, "GIT_CONFIG_") {
			continue
		}
		environment = append(environment, variable)
	}
	return append(environment,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
	)
}

func trustedSSHKeygen() (string, error) {
	program, err := exec.LookPath("ssh-keygen")
	if err != nil {
		return "", fmt.Errorf("find ssh-keygen: %w", err)
	}
	program, err = filepath.Abs(program)
	if err != nil {
		return "", fmt.Errorf("resolve ssh-keygen: %w", err)
	}
	return program, nil
}

func (s Store) ObjectFormat(ctx context.Context) (string, error) {
	output, err := s.run(ctx, nil, nil, "rev-parse", "--show-object-format")
	return string(output), err
}

func (s Store) TypedOID(ctx context.Context, oid string) (string, error) {
	format, err := s.ObjectFormat(ctx)
	if err != nil {
		return "", err
	}
	return "git:" + format + ":" + oid, nil
}

func ParseTypedOID(value string) (format, oid string, err error) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 || parts[0] != "git" || (parts[1] != "sha1" && parts[1] != "sha256") || parts[2] == "" {
		return "", "", fmt.Errorf("invalid typed git object id %q", value)
	}
	return parts[1], parts[2], nil
}

func (s Store) EmptyTree(ctx context.Context) (string, error) {
	output, err := s.run(ctx, nil, nil, "mktree")
	return string(output), err
}

func (s Store) WriteBlob(ctx context.Context, content []byte) (string, error) {
	output, err := s.run(ctx, content, nil, "hash-object", "-w", "--stdin")
	return string(output), err
}

// WriteSingleFileTree writes one ordinary blob-backed Git tree. It is used by
// local, content-addressed metadata such as verified checkpoints; the caller
// still decides whether and where the resulting object is referenced.
func (s Store) WriteSingleFileTree(ctx context.Context, name string, content []byte) (string, error) {
	if !attachmentName.MatchString(name) {
		return "", fmt.Errorf("invalid tree entry name %q", name)
	}
	oid, err := s.WriteBlob(ctx, content)
	if err != nil {
		return "", err
	}
	entry := fmt.Sprintf("100644 blob %s\t%s\n", oid, name)
	output, err := s.run(ctx, []byte(entry), nil, "mktree")
	return string(output), err
}

// HashPayloadTree reproduces WritePayloadTree's object identity without
// writing objects or invoking Git. Checkpoint recovery uses it to prove that
// cached payload bytes still match the payload tree in the actor-signed
// intent; the checkpoint signer cannot substitute application content.
func HashPayloadTree(objectFormat string, event []byte, attachments map[string][]byte) (string, error) {
	eventOID, err := hashObject(objectFormat, "blob", event)
	if err != nil {
		return "", err
	}
	root := []treeEntry{{mode: "100644", name: "event", oid: eventOID}}
	if len(attachments) > 0 {
		names := make([]string, 0, len(attachments))
		for name := range attachments {
			if !attachmentName.MatchString(name) {
				return "", fmt.Errorf("invalid attachment name %q", name)
			}
			names = append(names, name)
		}
		sort.Strings(names)
		entries := make([]treeEntry, 0, len(names))
		for _, name := range names {
			oid, err := hashObject(objectFormat, "blob", attachments[name])
			if err != nil {
				return "", err
			}
			entries = append(entries, treeEntry{mode: "100644", name: name, oid: oid})
		}
		attachmentsOID, err := hashTree(objectFormat, entries)
		if err != nil {
			return "", err
		}
		root = append(root, treeEntry{mode: "40000", name: "attachments", oid: attachmentsOID, directory: true})
	}
	return hashTree(objectFormat, root)
}

type treeEntry struct {
	mode      string
	name      string
	oid       string
	directory bool
}

func hashTree(objectFormat string, entries []treeEntry) (string, error) {
	sort.Slice(entries, func(i, j int) bool {
		left, right := entries[i].name, entries[j].name
		if entries[i].directory {
			left += "/"
		}
		if entries[j].directory {
			right += "/"
		}
		return left < right
	})
	var content bytes.Buffer
	for _, entry := range entries {
		rawOID, err := hex.DecodeString(entry.oid)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&content, "%s %s", entry.mode, entry.name)
		content.WriteByte(0)
		content.Write(rawOID)
	}
	return hashObject(objectFormat, "tree", content.Bytes())
}

func hashObject(objectFormat, kind string, content []byte) (string, error) {
	var digest hash.Hash
	switch objectFormat {
	case "sha1":
		digest = sha1.New()
	case "sha256":
		digest = sha256.New()
	default:
		return "", fmt.Errorf("unsupported object format %q", objectFormat)
	}
	fmt.Fprintf(digest, "%s %d", kind, len(content))
	digest.Write([]byte{0})
	digest.Write(content)
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func (s Store) WritePayloadTree(ctx context.Context, event []byte, attachments map[string][]byte) (string, error) {
	eventOID, err := s.WriteBlob(ctx, event)
	if err != nil {
		return "", err
	}
	root := fmt.Sprintf("100644 blob %s\tevent\n", eventOID)
	if len(attachments) > 0 {
		names := make([]string, 0, len(attachments))
		for name := range attachments {
			if !attachmentName.MatchString(name) {
				return "", fmt.Errorf("invalid attachment name %q", name)
			}
			names = append(names, name)
		}
		sort.Strings(names)
		var nested strings.Builder
		for _, name := range names {
			oid, err := s.WriteBlob(ctx, attachments[name])
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&nested, "100644 blob %s\t%s\n", oid, name)
		}
		attachmentsTree, err := s.run(ctx, []byte(nested.String()), nil, "mktree")
		if err != nil {
			return "", err
		}
		root += fmt.Sprintf("040000 tree %s\tattachments\n", attachmentsTree)
	}
	output, err := s.run(ctx, []byte(root), nil, "mktree")
	return string(output), err
}

type CommitIdentity struct {
	AuthorName     string
	AuthorEmail    string
	CommitterName  string
	CommitterEmail string
}

func (s Store) SignedCommit(ctx context.Context, tree, parent, message, signingKey string, identity CommitIdentity) (string, error) {
	commit, _, err := s.SignedCommitWithTimestamp(ctx, tree, parent, message, signingKey, identity)
	return commit, err
}

// SignedCommitWithTimestamp returns the Unix commit time written into the
// signed object. A newly projected event can then use the same durable time as
// a later historical scan without reading the commit back from Git.
func (s Store) SignedCommitWithTimestamp(ctx context.Context, tree, parent, message, signingKey string, identity CommitIdentity) (string, int64, error) {
	sshKeygen, err := trustedSSHKeygen()
	if err != nil {
		return "", 0, err
	}
	timestamp := time.Now().Unix()
	now := fmt.Sprintf("%d +0000", timestamp)
	env := []string{
		"GIT_AUTHOR_NAME=" + identity.AuthorName,
		"GIT_AUTHOR_EMAIL=" + identity.AuthorEmail,
		"GIT_AUTHOR_DATE=" + now,
		"GIT_COMMITTER_NAME=" + identity.CommitterName,
		"GIT_COMMITTER_EMAIL=" + identity.CommitterEmail,
		"GIT_COMMITTER_DATE=" + now,
	}
	args := []string{
		"-c", "gpg.format=ssh",
		"-c", "gpg.ssh.program=" + sshKeygen,
		"-c", "user.signingKey=" + signingKey,
		"commit-tree", "-S", tree,
	}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	output, err := s.runHermetic(ctx, []byte(message), env, args...)
	return string(output), timestamp, err
}

func (s Store) UpdateRef(ctx context.Context, ref, newOID, oldOID string) error {
	_, err := s.run(ctx, nil, nil, "update-ref", ref, newOID, oldOID)
	return err
}

// DeleteRef removes a ref only while it still names the expected object. The
// expected value is what makes deletion safe under contention: a ref that has
// moved on belongs to somebody else, and removing it by name alone would take
// their record away. Deleting without one is refused rather than supported,
// because every caller here holds a value it can name.
func (s Store) DeleteRef(ctx context.Context, ref, oldOID string) error {
	if oldOID == "" {
		return errors.New("refusing to delete a ref without an expected old value")
	}
	_, err := s.run(ctx, nil, nil, "update-ref", "-d", ref, oldOID)
	return err
}

// RefValue reads the object a ref names, and reports absence separately from
// failure. A ref that does not exist is an ordinary answer callers act on —
// nobody holds this yet — and folding it into an error would make an empty
// repository indistinguishable from a broken one.
func (s Store) RefValue(ctx context.Context, ref string) (string, bool, error) {
	if ref == "" || strings.ContainsAny(ref, " \t\x00\r\n") {
		return "", false, fmt.Errorf("invalid ref name %q", ref)
	}
	observer := s.Observer
	if observer == nil {
		observer = observe.FromContext(ctx)
	}
	args := []string{"rev-parse", "--verify", "--quiet", ref}
	done := observe.Begin(ctx, observer, observe.OperationGit, observe.GitPath(args))
	cmd := exec.CommandContext(ctx, "git", storeGitArguments(s.Repo, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	value := strings.TrimSpace(stdout.String())
	if err != nil {
		// --quiet turns an unresolvable ref into a silent exit 1. That is the
		// ref being absent. A broken store still exits 128 and still speaks.
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && value == "" && stderr.Len() == 0 {
			if done != nil {
				done(nil)
			}
			return "", false, nil
		}
		err = fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(stderr.Bytes()))
		if done != nil {
			done(err)
		}
		return "", false, err
	}
	if done != nil {
		done(nil)
	}
	if value == "" {
		return "", false, nil
	}
	return value, true, nil
}

// BlobLimit reads a blob after checking its type and size in one Git call, so
// a ref pointing at a hostile or oversized object cannot force an unbounded
// allocation before anything has been validated.
func (s Store) BlobLimit(ctx context.Context, oid string, limit int64) ([]byte, error) {
	if oid == "" || limit < 0 || strings.ContainsAny(oid, " \t\x00\r\n:") {
		return nil, errors.New("invalid object id or byte limit")
	}
	output, err := s.run(ctx, []byte(oid+"\n"), nil, "cat-file", "--batch-check=%(objecttype) %(objectsize)")
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(output))
	if len(fields) != 2 || fields[0] != "blob" {
		return nil, fmt.Errorf("object %s is not a readable blob: %s", oid, output)
	}
	size, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse blob size: %w", err)
	}
	if size > limit {
		return nil, fmt.Errorf("blob size %d exceeds limit %d", size, limit)
	}
	return s.run(ctx, nil, nil, "cat-file", "blob", oid)
}

func (s Store) Head(ctx context.Context, ref string) (string, error) {
	output, err := s.run(ctx, nil, nil, "rev-parse", "--verify", ref)
	return string(output), err
}

func (s Store) RevList(ctx context.Context, ref string) ([]string, error) {
	output, err := s.run(ctx, nil, nil, "rev-list", "--first-parent", "--reverse", ref)
	if err != nil {
		return nil, err
	}
	if len(output) == 0 {
		return nil, nil
	}
	return strings.Fields(string(output)), nil
}

// RevListMetadata returns the first-parent history oldest first, including the
// tree and complete message for each commit, from one local Git enumeration.
// Git commit messages cannot contain NUL, so -z plus NUL field separators do
// not depend on intent-level character restrictions.
func (s Store) RevListMetadata(ctx context.Context, ref string) ([]CommitMetadata, error) {
	var metadata []CommitMetadata
	err := s.WalkRevListMetadata(ctx, ref, func(commit CommitMetadata) error {
		metadata = append(metadata, commit)
		return nil
	})
	return metadata, err
}

// WalkRevListMetadata streams first-parent commit metadata oldest first. It
// retains at most one untrusted commit message at a time, so a long history
// cannot turn per-envelope bounds into an aggregate message allocation.
func (s Store) WalkRevListMetadata(ctx context.Context, ref string, visit func(CommitMetadata) error) error {
	return s.walkRevListMetadata(ctx, ref, visit)
}

// WalkRevListMetadataAfter streams the exclusive first-parent suffix from base
// through head, oldest first. The visitor must still validate ancestry.
func (s Store) WalkRevListMetadataAfter(ctx context.Context, base, head string, visit func(CommitMetadata) error) error {
	return s.walkRevListMetadata(ctx, base+".."+head, visit)
}

func (s Store) walkRevListMetadata(ctx context.Context, revision string, visit func(CommitMetadata) error) error {
	args := storeGitArguments(s.Repo, "log", "-z", "--first-parent", "--reverse", "--format=%H%x00%T%x00%P%x00%ct%x00%B", revision)
	cmd := exec.CommandContext(ctx, "git", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	reader := bufio.NewReader(stdout)
	for {
		commit, done, readErr := readCommitMetadata(reader)
		if readErr != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return readErr
		}
		if done {
			break
		}
		if err := visit(commit); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return err
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("git %s: %w: %s", strings.Join(args[2:], " "), err, bytes.TrimSpace(stderr.Bytes()))
	}
	return nil
}

func readCommitMetadata(reader *bufio.Reader) (CommitMetadata, bool, error) {
	fields := make([][]byte, 5)
	for index := range fields {
		field, err := reader.ReadBytes(0)
		if err == io.EOF && index == 0 && len(field) == 0 {
			return CommitMetadata{}, true, nil
		}
		if err != nil || len(field) == 0 || field[len(field)-1] != 0 {
			return CommitMetadata{}, false, errors.New("malformed git log metadata framing")
		}
		fields[index] = field[:len(field)-1]
	}
	if len(fields[0]) == 0 || len(fields[1]) == 0 {
		return CommitMetadata{}, false, errors.New("malformed git log metadata identity")
	}
	timestamp, err := strconv.ParseInt(string(fields[3]), 10, 64)
	if err != nil {
		return CommitMetadata{}, false, fmt.Errorf("malformed git log metadata timestamp: %w", err)
	}
	return CommitMetadata{
		OID: string(fields[0]), Tree: string(fields[1]),
		Parents: strings.Fields(string(fields[2])), Timestamp: timestamp, Message: string(fields[4]),
	}, false, nil
}

// RevListAfter returns the first-parent commits strictly after base and
// reachable from head, oldest first. Callers must still verify that the first
// returned commit names base as its sole parent: git's range syntax alone does
// not prove that base is an ancestor of head.
func (s Store) RevListAfter(ctx context.Context, base, head string) ([]string, error) {
	output, err := s.run(ctx, nil, nil, "rev-list", "--first-parent", "--reverse", base+".."+head)
	if err != nil {
		return nil, err
	}
	if len(output) == 0 {
		return nil, nil
	}
	return strings.Fields(string(output)), nil
}

func (s Store) CommitMessage(ctx context.Context, oid string) (string, error) {
	output, err := s.run(ctx, nil, nil, "show", "-s", "--format=%B", oid)
	return string(output) + "\n", err
}

// CommitMessageWithTimestamp reads the envelope and signed committer time for
// one commit. Scans no longer call it: they take both from streamed metadata
// instead. It remains the reference for the message normalization that
// enumeration has to reproduce, because reading through Store.run trims the
// command's outer whitespace and that trimming is part of the established
// admission semantics. Tests measure the streamed path against this one.
func (s Store) CommitMessageWithTimestamp(ctx context.Context, oid string) (string, int64, error) {
	output, err := s.run(ctx, nil, nil, "show", "-s", "--format=%ct%x00%B", oid)
	if err != nil {
		return "", 0, err
	}
	parts := bytes.SplitN(output, []byte{0}, 2)
	if len(parts) != 2 {
		return "", 0, errors.New("commit metadata is missing its timestamp separator")
	}
	timestamp, err := strconv.ParseInt(string(parts[0]), 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("parse commit timestamp: %w", err)
	}
	return string(parts[1]) + "\n", timestamp, nil
}

func (s Store) ReadFile(ctx context.Context, commit, path string) ([]byte, error) {
	if commit == "" || path == "" || strings.ContainsAny(path, "\x00\r\n:") {
		return nil, errors.New("invalid commit or tree path")
	}
	argv := storeGitArguments(s.Repo, "show", commit+":"+path)
	cmd := exec.CommandContext(ctx, "git", argv...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git show file: %w: %s", err, bytes.TrimSpace(output))
	}
	return output, nil
}

// ReadFileLimit checks the blob size before reading it, so a corrupt metadata
// ref cannot force an unbounded allocation before its contents are verified.
func (s Store) ReadFileLimit(ctx context.Context, commit, path string, limit int64) ([]byte, error) {
	if commit == "" || path == "" || limit < 0 || strings.ContainsAny(path, "\x00\r\n:") {
		return nil, errors.New("invalid commit, tree path, or byte limit")
	}
	output, err := s.run(ctx, nil, nil, "cat-file", "-s", commit+":"+path)
	if err != nil {
		return nil, err
	}
	size, err := strconv.ParseInt(string(output), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse blob size: %w", err)
	}
	if size > limit {
		return nil, fmt.Errorf("blob size %d exceeds limit %d", size, limit)
	}
	return s.ReadFile(ctx, commit, path)
}

func (s Store) ListFiles(ctx context.Context, commit, directory string) ([]string, error) {
	if commit == "" || strings.ContainsAny(directory, "\x00\r\n:") {
		return nil, errors.New("invalid commit or tree path")
	}
	args := []string{"ls-tree", "-r", "--name-only", commit}
	if directory != "" {
		args = append(args, "--", directory)
	}
	output, err := s.run(ctx, nil, nil, args...)
	if err != nil {
		return nil, err
	}
	if len(output) == 0 {
		return nil, nil
	}
	return strings.Fields(string(output)), nil
}

func (s Store) CommitParents(ctx context.Context, oid string) ([]string, error) {
	output, err := s.run(ctx, nil, nil, "show", "-s", "--format=%P", oid)
	if err != nil {
		return nil, err
	}
	if len(output) == 0 {
		return nil, nil
	}
	return strings.Fields(string(output)), nil
}

func (s Store) VerifySSHCommit(ctx context.Context, oid, principal, publicKey string) error {
	if err := ValidateSSHPublicKey(publicKey); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "gitseq-allowed-signers-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "allowed_signers")
	line := principal + " " + strings.TrimSpace(publicKey) + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		return err
	}
	sshKeygen, err := trustedSSHKeygen()
	if err != nil {
		return err
	}
	_, err = s.runHermetic(ctx, nil, nil,
		"-c", "gpg.format=ssh",
		"-c", "gpg.ssh.program="+sshKeygen,
		"-c", "gpg.ssh.allowedSignersFile="+path,
		"verify-commit", oid,
	)
	return err
}

// ValidateSSHPublicKey accepts the one canonical key form gitseq generates and
// pins in genesis. Keeping comments, options, principals, and line breaks out
// of this value is security-critical: it is embedded after the principal in an
// OpenSSH allowed-signers line.
func ValidateSSHPublicKey(publicKey string) error {
	_, _, err := parseSSHEd25519PublicKey(publicKey)
	return err
}

func sshWireString(data []byte) (value, rest []byte, ok bool) {
	if len(data) < 4 {
		return nil, nil, false
	}
	size := uint64(binary.BigEndian.Uint32(data[:4]))
	if size > uint64(len(data)-4) {
		return nil, nil, false
	}
	end := 4 + int(size)
	return data[4:end], data[end:], true
}

func GenerateSSHKey(ctx context.Context, path string) (publicKey string, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ssh-keygen: %w: %s", err, output)
	}
	content, err := os.ReadFile(path + ".pub")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(content))
	if len(fields) < 2 {
		return "", errors.New("malformed ssh public key")
	}
	publicKey = fields[0] + " " + fields[1]
	if err := ValidateSSHPublicKey(publicKey); err != nil {
		return "", err
	}
	return publicKey, nil
}
