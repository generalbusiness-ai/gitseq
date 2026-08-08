package gitstore

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var attachmentName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type Store struct {
	Repo string
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
	argv := append([]string{"--git-dir", s.Repo}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(os.Environ(), env...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(output))
	}
	return bytes.TrimSpace(output), nil
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
	now := fmt.Sprintf("%d +0000", time.Now().Unix())
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
		"-c", "user.signingKey=" + signingKey,
		"commit-tree", "-S", tree,
	}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	output, err := s.run(ctx, []byte(message), env, args...)
	return string(output), err
}

func (s Store) UpdateRef(ctx context.Context, ref, newOID, oldOID string) error {
	_, err := s.run(ctx, nil, nil, "update-ref", ref, newOID, oldOID)
	return err
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

func (s Store) CommitMessage(ctx context.Context, oid string) (string, error) {
	output, err := s.run(ctx, nil, nil, "show", "-s", "--format=%B", oid)
	return string(output) + "\n", err
}

func (s Store) CommitTree(ctx context.Context, oid string) (string, error) {
	output, err := s.run(ctx, nil, nil, "show", "-s", "--format=%T", oid)
	return string(output), err
}

func (s Store) ReadFile(ctx context.Context, commit, path string) ([]byte, error) {
	if commit == "" || path == "" || strings.ContainsAny(path, "\x00\r\n:") {
		return nil, errors.New("invalid commit or tree path")
	}
	argv := []string{"--git-dir", s.Repo, "show", commit + ":" + path}
	cmd := exec.CommandContext(ctx, "git", argv...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git show file: %w: %s", err, bytes.TrimSpace(output))
	}
	return output, nil
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

// ValidatePayloadTree checks the kernel's only payload semantics: one event
// blob, optional flat attachments, and a total inline byte ceiling.
func (s Store) ValidatePayloadTree(ctx context.Context, tree string, ceiling uint64) error {
	argv := []string{"--git-dir", s.Repo, "ls-tree", "-lrz", "--full-tree", tree}
	output, err := exec.CommandContext(ctx, "git", argv...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git ls-tree: %w: %s", err, bytes.TrimSpace(output))
	}
	seenEvent := false
	var total uint64
	for _, record := range bytes.Split(output, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		parts := bytes.SplitN(record, []byte{'\t'}, 2)
		if len(parts) != 2 {
			return errors.New("malformed payload tree entry")
		}
		metadata := strings.Fields(string(parts[0]))
		path := string(parts[1])
		if len(metadata) != 4 || metadata[1] != "blob" {
			return fmt.Errorf("payload path %q is not a blob", path)
		}
		size, err := strconv.ParseUint(metadata[3], 10, 64)
		if err != nil || total > ceiling || size > ceiling-total {
			return errors.New("payload exceeds genesis ceiling")
		}
		total += size
		switch {
		case path == "event" && !seenEvent:
			seenEvent = true
		case strings.HasPrefix(path, "attachments/") && attachmentName.MatchString(strings.TrimPrefix(path, "attachments/")):
		default:
			return fmt.Errorf("invalid payload path %q", path)
		}
	}
	if !seenEvent {
		return errors.New("payload tree has no event blob")
	}
	return nil
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
	_, err = s.run(ctx, nil, nil,
		"-c", "gpg.format=ssh",
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
	fields := strings.Split(publicKey, " ")
	if len(fields) != 2 || fields[0] != "ssh-ed25519" || fields[1] == "" {
		return errors.New("sequencer public key must be canonical ssh-ed25519")
	}
	raw, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil || base64.StdEncoding.EncodeToString(raw) != fields[1] {
		return errors.New("sequencer public key has non-canonical base64")
	}
	algorithm, rest, ok := sshWireString(raw)
	if !ok || string(algorithm) != fields[0] {
		return errors.New("sequencer public key algorithm mismatch")
	}
	key, rest, ok := sshWireString(rest)
	if !ok || len(key) != 32 || len(rest) != 0 {
		return errors.New("sequencer public key has invalid ed25519 body")
	}
	return nil
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
