package gitstore

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/observe"
)

// AuditBatch reads and hashes objects through one long-lived cat-file process.
// A cold audit uses it instead of starting Git once for every commit and tree.
type AuditBatch struct {
	ctx          context.Context
	objectFormat string
	objectBytes  int
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       *bufio.Reader
	stderr       bytes.Buffer
	done         func(error)
	closed       bool
}

// AuditedCommit contains the metadata and exact bytes covered by an SSH
// signature. The object hash has already been recomputed before this value is
// returned.
type AuditedCommit struct {
	CommitMetadata
	SignedContent []byte
	Signature     []byte
}

// OpenAuditBatch starts the single object reader used by a cold audit.
func (s Store) OpenAuditBatch(ctx context.Context, objectFormat string) (*AuditBatch, error) {
	objectBytes := 0
	switch objectFormat {
	case "sha1":
		objectBytes = 20
	case "sha256":
		objectBytes = 32
	default:
		return nil, fmt.Errorf("unsupported object format %q", objectFormat)
	}
	observer := s.Observer
	if observer == nil {
		observer = observe.FromContext(ctx)
	}
	done := observe.Begin(ctx, observer, observe.OperationGit, observe.PathScan)
	cmd := exec.CommandContext(ctx, "git", "--git-dir", s.Repo, "cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		if done != nil {
			done(err)
		}
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		if done != nil {
			done(err)
		}
		return nil, err
	}
	batch := &AuditBatch{
		ctx: ctx, objectFormat: objectFormat, objectBytes: objectBytes,
		cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), done: done,
	}
	cmd.Stderr = &batch.stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		if done != nil {
			done(err)
		}
		return nil, err
	}
	return batch, nil
}

// Close finishes the batch process and reports any Git-side failure.
func (b *AuditBatch) Close() error {
	if b == nil || b.closed {
		return nil
	}
	b.closed = true
	_ = b.stdin.Close()
	err := b.cmd.Wait()
	if err != nil {
		if b.ctx.Err() != nil {
			err = b.ctx.Err()
		} else {
			err = fmt.Errorf("git cat-file --batch: %w: %s", err, bytes.TrimSpace(b.stderr.Bytes()))
		}
	}
	if b.done != nil {
		b.done(err)
	}
	return err
}

func (b *AuditBatch) readObject(oid, wantType string, maxBytes uint64) ([]byte, error) {
	if b == nil || b.closed {
		return nil, errors.New("audit object reader is closed")
	}
	if !validObjectID(oid, b.objectBytes) {
		return nil, fmt.Errorf("invalid %s object id %q", b.objectFormat, oid)
	}
	if _, err := io.WriteString(b.stdin, oid+"\n"); err != nil {
		return nil, err
	}
	header, err := b.stdout.ReadString('\n')
	if err != nil {
		b.poison()
		return nil, fmt.Errorf("read git cat-file header: %w", err)
	}
	fields := strings.Fields(strings.TrimSuffix(header, "\n"))
	if len(fields) != 3 || fields[0] != oid || fields[1] != wantType {
		b.poison()
		return nil, fmt.Errorf("git cat-file returned malformed %s header %q", wantType, strings.TrimSpace(header))
	}
	size, err := strconv.ParseUint(fields[2], 10, 64)
	if err != nil {
		b.poison()
		return nil, fmt.Errorf("git cat-file returned invalid object size: %w", err)
	}
	if size > maxBytes || size > uint64(maxInt()) {
		b.poison()
		return nil, fmt.Errorf("%s object size %d exceeds limit %d", wantType, size, maxBytes)
	}
	content := make([]byte, int(size))
	if _, err := io.ReadFull(b.stdout, content); err != nil {
		b.poison()
		return nil, fmt.Errorf("read git %s object: %w", wantType, err)
	}
	terminator, err := b.stdout.ReadByte()
	if err != nil || terminator != '\n' {
		b.poison()
		return nil, errors.New("malformed git cat-file object framing")
	}
	actual, err := hashObject(b.objectFormat, wantType, content)
	if err != nil {
		return nil, err
	}
	if actual != oid {
		return nil, fmt.Errorf("%s object hash differs from requested id", wantType)
	}
	return content, nil
}

func (b *AuditBatch) poison() {
	if b != nil && b.cmd != nil && b.cmd.Process != nil {
		_ = b.cmd.Process.Kill()
	}
}

func maxInt() int {
	return int(^uint(0) >> 1)
}

func validObjectID(oid string, objectBytes int) bool {
	if len(oid) != objectBytes*2 {
		return false
	}
	for _, character := range oid {
		if character < '0' || (character > '9' && character < 'a') || character > 'f' {
			return false
		}
	}
	return true
}

// ReadCommit reads, hashes, and parses one signed commit object.
func (b *AuditBatch) ReadCommit(oid string) (AuditedCommit, error) {
	content, err := b.readObject(oid, "commit", ^uint64(0))
	if err != nil {
		return AuditedCommit{}, err
	}
	return parseAuditedCommit(oid, content)
}

func parseAuditedCommit(oid string, content []byte) (AuditedCommit, error) {
	separator := bytes.Index(content, []byte("\n\n"))
	if separator < 0 {
		return AuditedCommit{}, errors.New("commit has no message separator")
	}
	headers := content[:separator+1]
	message := string(content[separator+2:])
	lines := bytes.Split(headers, []byte{'\n'})
	unsigned := make([]byte, 0, len(content))
	var (
		tree      string
		parents   []string
		timestamp int64
		signature []byte
		seenSig   bool
		seenTime  bool
		continued bool
	)
	for index := 0; index < len(lines)-1; {
		line := lines[index]
		if len(line) > 0 && line[0] == ' ' {
			if !continued {
				return AuditedCommit{}, errors.New("malformed commit header continuation")
			}
			unsigned = append(unsigned, line...)
			unsigned = append(unsigned, '\n')
			index++
			continue
		}
		name, value, ok := bytes.Cut(line, []byte{' '})
		if !ok || len(name) == 0 || len(value) == 0 {
			return AuditedCommit{}, errors.New("malformed commit header")
		}
		if string(name) == "gpgsig" || string(name) == "gpgsig-sha256" {
			if seenSig {
				return AuditedCommit{}, errors.New("commit has multiple signatures")
			}
			seenSig = true
			var armor bytes.Buffer
			armor.Write(value)
			armor.WriteByte('\n')
			index++
			for index < len(lines)-1 && len(lines[index]) > 0 && lines[index][0] == ' ' {
				armor.Write(lines[index][1:])
				armor.WriteByte('\n')
				index++
			}
			signature = armor.Bytes()
			continued = false
			continue
		}
		unsigned = append(unsigned, line...)
		unsigned = append(unsigned, '\n')
		continued = true
		switch string(name) {
		case "tree":
			if tree != "" {
				return AuditedCommit{}, errors.New("commit has multiple trees")
			}
			tree = string(value)
		case "parent":
			parents = append(parents, string(value))
		case "committer":
			if seenTime {
				return AuditedCommit{}, errors.New("commit has multiple committers")
			}
			fields := bytes.Fields(value)
			if len(fields) < 2 {
				return AuditedCommit{}, errors.New("malformed commit committer")
			}
			parsed, err := strconv.ParseInt(string(fields[len(fields)-2]), 10, 64)
			if err != nil {
				return AuditedCommit{}, fmt.Errorf("malformed commit timestamp: %w", err)
			}
			timestamp = parsed
			seenTime = true
		}
		index++
	}
	if tree == "" || !seenTime || !seenSig {
		return AuditedCommit{}, errors.New("commit is missing required signed metadata")
	}
	unsigned = append(unsigned, '\n')
	unsigned = append(unsigned, content[separator+2:]...)
	return AuditedCommit{
		CommitMetadata: CommitMetadata{OID: oid, Tree: tree, Parents: parents, Timestamp: timestamp, Message: message},
		SignedContent:  unsigned, Signature: signature,
	}, nil
}

// VerifySSHSignature verifies the OpenSSH ssh-ed25519 signature embedded in a
// commit against the exact commit bytes and the current sequencer key.
func VerifySSHSignature(commit AuditedCommit, publicKey string) error {
	expectedWire, expectedKey, err := parseSSHEd25519PublicKey(publicKey)
	if err != nil {
		return err
	}
	block, rest := pem.Decode(commit.Signature)
	if block == nil || block.Type != "SSH SIGNATURE" || len(rest) != 0 || len(block.Headers) != 0 {
		return errors.New("invalid SSH signature armor")
	}
	data := block.Bytes
	if !bytes.HasPrefix(data, []byte("SSHSIG")) {
		return errors.New("invalid SSH signature magic")
	}
	data = data[len("SSHSIG"):]
	if len(data) < 4 || binary.BigEndian.Uint32(data[:4]) != 1 {
		return errors.New("unsupported SSH signature version")
	}
	data = data[4:]
	embeddedKey, data, ok := sshWireString(data)
	if !ok || !bytes.Equal(embeddedKey, expectedWire) {
		return errors.New("SSH signature key differs from sequencer key")
	}
	namespace, data, ok := sshWireString(data)
	if !ok || string(namespace) != "git" {
		return errors.New("SSH signature namespace is not git")
	}
	reserved, data, ok := sshWireString(data)
	if !ok || len(reserved) != 0 {
		return errors.New("SSH signature reserved field is not empty")
	}
	hashAlgorithm, data, ok := sshWireString(data)
	if !ok {
		return errors.New("SSH signature has no hash algorithm")
	}
	signature, data, ok := sshWireString(data)
	if !ok || len(data) != 0 {
		return errors.New("malformed SSH signature body")
	}
	signatureAlgorithm, signature, ok := sshWireString(signature)
	if !ok || string(signatureAlgorithm) != "ssh-ed25519" {
		return errors.New("SSH signature algorithm is not ssh-ed25519")
	}
	rawSignature, signature, ok := sshWireString(signature)
	if !ok || len(signature) != 0 || len(rawSignature) != ed25519.SignatureSize {
		return errors.New("invalid ed25519 signature body")
	}
	var digest []byte
	switch string(hashAlgorithm) {
	case "sha256":
		sum := sha256.Sum256(commit.SignedContent)
		digest = sum[:]
	case "sha512":
		sum := sha512.Sum512(commit.SignedContent)
		digest = sum[:]
	default:
		return fmt.Errorf("unsupported SSH signature hash %q", hashAlgorithm)
	}
	signed := make([]byte, 0, 64+len(digest))
	signed = append(signed, "SSHSIG"...)
	signed = appendSSHString(signed, namespace)
	signed = appendSSHString(signed, reserved)
	signed = appendSSHString(signed, hashAlgorithm)
	signed = appendSSHString(signed, digest)
	if !ed25519.Verify(expectedKey, signed, rawSignature) {
		return errors.New("invalid sequencer SSH signature")
	}
	return nil
}

func appendUint32(target []byte, value uint32) []byte {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	return append(target, encoded[:]...)
}

func appendSSHString(target, value []byte) []byte {
	target = appendUint32(target, uint32(len(value)))
	return append(target, value...)
}

func parseSSHEd25519PublicKey(publicKey string) ([]byte, ed25519.PublicKey, error) {
	fields := strings.Split(publicKey, " ")
	if len(fields) != 2 || fields[0] != "ssh-ed25519" {
		return nil, nil, errors.New("sequencer public key must be canonical ssh-ed25519")
	}
	raw, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil || base64.StdEncoding.EncodeToString(raw) != fields[1] {
		return nil, nil, errors.New("sequencer public key has non-canonical base64")
	}
	algorithm, rest, ok := sshWireString(raw)
	if !ok || string(algorithm) != fields[0] {
		return nil, nil, errors.New("sequencer public key algorithm mismatch")
	}
	key, rest, ok := sshWireString(rest)
	if !ok || len(key) != ed25519.PublicKeySize || len(rest) != 0 {
		return nil, nil, errors.New("sequencer public key has invalid ed25519 body")
	}
	return raw, ed25519.PublicKey(key), nil
}

// PayloadTree reads and hashes the signed payload tree, verifies its exact
// shape and aggregate bound, and optionally returns its blob contents.
func (b *AuditBatch) PayloadTree(tree string, ceiling uint64, load bool) ([]byte, map[string][]byte, error) {
	root, err := b.readTree(tree)
	if err != nil {
		return nil, nil, err
	}
	var eventOID, attachmentsOID string
	for _, entry := range root {
		switch {
		case entry.name == "event" && isBlobMode(entry.mode) && eventOID == "":
			eventOID = entry.oid
		case entry.name == "attachments" && entry.mode == "40000" && attachmentsOID == "":
			attachmentsOID = entry.oid
		default:
			return nil, nil, fmt.Errorf("invalid payload path %q", entry.name)
		}
	}
	if eventOID == "" {
		return nil, nil, errors.New("payload tree has no event blob")
	}
	event, err := b.readPayloadBlob(eventOID, ceiling)
	if err != nil {
		return nil, nil, err
	}
	total := uint64(len(event))
	var attachments map[string][]byte
	if attachmentsOID != "" {
		entries, err := b.readTree(attachmentsOID)
		if err != nil {
			return nil, nil, err
		}
		if load && len(entries) > 0 {
			attachments = make(map[string][]byte, len(entries))
		}
		for _, entry := range entries {
			if !isBlobMode(entry.mode) || !attachmentName.MatchString(entry.name) {
				return nil, nil, fmt.Errorf("invalid payload path %q", "attachments/"+entry.name)
			}
			remaining := ceiling - total
			content, err := b.readPayloadBlob(entry.oid, remaining)
			if err != nil {
				return nil, nil, err
			}
			total += uint64(len(content))
			if load {
				attachments[entry.name] = content
			}
		}
	}
	if !load {
		return nil, nil, nil
	}
	return event, attachments, nil
}

func (b *AuditBatch) readPayloadBlob(oid string, remaining uint64) ([]byte, error) {
	content, err := b.readObject(oid, "blob", remaining)
	if err != nil && strings.Contains(err.Error(), "exceeds limit") {
		return nil, errors.New("payload exceeds genesis ceiling")
	}
	return content, err
}

func isBlobMode(mode string) bool {
	switch mode {
	case "100644", "100755", "120000":
		return true
	default:
		return false
	}
}

// TreeIsEmpty hashes and checks the tree named by a key-rotation commit.
func (b *AuditBatch) TreeIsEmpty(tree string) (bool, error) {
	content, err := b.readObject(tree, "tree", ^uint64(0))
	return len(content) == 0, err
}

type auditedTreeEntry struct {
	mode string
	name string
	oid  string
}

func (b *AuditBatch) readTree(oid string) ([]auditedTreeEntry, error) {
	content, err := b.readObject(oid, "tree", ^uint64(0))
	if err != nil {
		return nil, err
	}
	entries := make([]auditedTreeEntry, 0, bytes.Count(content, []byte{0}))
	for len(content) > 0 {
		space := bytes.IndexByte(content, ' ')
		nul := bytes.IndexByte(content, 0)
		if space <= 0 || nul <= space+1 || len(content) < nul+1+b.objectBytes {
			return nil, errors.New("malformed git tree object")
		}
		mode := string(content[:space])
		name := string(content[space+1 : nul])
		rawOID := content[nul+1 : nul+1+b.objectBytes]
		entries = append(entries, auditedTreeEntry{mode: mode, name: name, oid: fmt.Sprintf("%x", rawOID)})
		content = content[nul+1+b.objectBytes:]
	}
	return entries, nil
}
