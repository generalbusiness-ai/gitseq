package perfscenario

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/ed25519"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// fixtureWriter creates synthetic objects without exercising the production
// submission path. It preserves the same signed wire format, Prepare checks
// the final signature with Git, and each measured sample uses the ordinary
// reader to verify the selected chain. Avoiding per-record process startup is
// what makes the 500k contract tier practical to prepare.
type fixtureWriter struct {
	objects      string
	format       string
	genesis      string
	payloadLimit uint64
	actor        ed25519.PrivateKey
	sequencer    ed25519.PrivateKey
	timestamp    int64
	gitDir       string
	pack         *os.File
	packHash     hash.Hash
	packWriter   io.Writer
}

func newFixtureWriter(workspace *app.Workspace, commits int) (*fixtureWriter, error) {
	if commits < 0 || uint64(commits) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("fixture pack commit count %d is out of range", commits)
	}
	_, actor, err := workspace.Actor("operator")
	if err != nil {
		return nil, err
	}
	sequencer, err := readOpenSSHEd25519(workspace.Config.SequencerKey)
	if err != nil {
		return nil, fmt.Errorf("read fixture sequencer key: %w", err)
	}
	w := &fixtureWriter{
		objects: filepath.Join(workspace.CommonDir, "objects"), format: workspace.Config.ObjectFormat,
		genesis: workspace.Config.Genesis, payloadLimit: workspace.Config.PayloadCeiling,
		actor: actor, sequencer: sequencer, timestamp: time.Now().Unix(), gitDir: workspace.CommonDir,
	}
	if commits == 0 {
		return w, nil
	}
	packDirectory := filepath.Join(w.objects, "pack")
	if err := os.MkdirAll(packDirectory, 0o755); err != nil {
		return nil, err
	}
	w.pack, err = os.CreateTemp(packDirectory, "fixture-pack-*.pack")
	if err != nil {
		return nil, err
	}
	w.packHash, err = objectHash(w.format)
	if err != nil {
		_ = w.pack.Close()
		return nil, err
	}
	w.packWriter = io.MultiWriter(w.pack, w.packHash)
	if _, err := w.packWriter.Write([]byte{'P', 'A', 'C', 'K', 0, 0, 0, 2, byte(commits >> 24), byte(commits >> 16), byte(commits >> 8), byte(commits)}); err != nil {
		_ = w.pack.Close()
		return nil, err
	}
	return w, nil
}

func (w *fixtureWriter) append(_ context.Context, parent string, act app.Act) (commit, event string, err error) {
	if act.IdempotencyKey == "" {
		return "", "", errors.New("synthetic fixture act has no idempotency key")
	}
	if len(act.Attachments) != 0 {
		return "", "", errors.New("synthetic fixture writer does not accept attachments")
	}
	schema, payload, rests, err := fixturePayload(act)
	if err != nil {
		return "", "", err
	}
	encoded, err := workroom.Encode(payload)
	if err != nil {
		return "", "", err
	}
	payloadOID, err := w.writeObject("blob", encoded)
	if err != nil {
		return "", "", err
	}
	treeContent, err := singleFileTree(payloadOID, w.format)
	if err != nil {
		return "", "", err
	}
	treeOID, err := w.writeObject("tree", treeContent)
	if err != nil {
		return "", "", err
	}
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version, Target: "git:" + w.format + ":" + w.genesis,
		Schema: schema, PayloadTree: "git:" + w.format + ":" + treeOID, RestsOn: rests,
		IdempotencyNS: "workroom/v0", IdempotencyKey: act.IdempotencyKey,
	}, w.actor)
	if err != nil {
		return "", "", err
	}
	message := intent.Envelope(signed, rests)
	if uint64(len(message)+len(encoded)) > w.payloadLimit {
		return "", "", errors.New("synthetic fixture event exceeds genesis ceiling")
	}
	fingerprint := intent.ActorFingerprint(signed.ActorKey)
	unsigned := []byte(fmt.Sprintf("tree %s\nparent %s\nauthor actor %s <%s@actor.gitseq.invalid> %d +0000\ncommitter gitseq sequencer <sequencer@gitseq.invalid> %d +0000\n\n%s",
		treeOID, parent, fingerprint[:16], fingerprint[:16], w.timestamp, w.timestamp, message))
	armored := sshSignature(unsigned, w.sequencer)
	separator := bytes.Index(unsigned, []byte("\n\n"))
	if separator < 0 {
		return "", "", errors.New("synthetic commit has no header separator")
	}
	var signedCommit bytes.Buffer
	signedCommit.Write(unsigned[:separator])
	signedCommit.WriteString("\ngpgsig ")
	signedCommit.WriteString(strings.ReplaceAll(strings.TrimSuffix(string(armored), "\n"), "\n", "\n "))
	signedCommit.Write(unsigned[separator:])
	commit, err = w.writeCommit(signedCommit.Bytes())
	if err != nil {
		return "", "", err
	}
	event = "git:" + w.format + ":" + w.genesis + "#git:" + w.format + ":" + commit
	return commit, event, nil
}

func (w *fixtureWriter) writeCommit(content []byte) (string, error) {
	digest, err := objectHash(w.format)
	if err != nil {
		return "", err
	}
	header := []byte(fmt.Sprintf("commit %d\x00", len(content)))
	digest.Write(header)
	digest.Write(content)
	oid := hex.EncodeToString(digest.Sum(nil))
	if w.pack == nil {
		return w.writeObject("commit", content)
	}
	size := len(content)
	first := byte(1<<4) | byte(size&15)
	size >>= 4
	if size > 0 {
		first |= 0x80
	}
	if _, err := w.packWriter.Write([]byte{first}); err != nil {
		return "", err
	}
	for size > 0 {
		value := byte(size & 0x7f)
		size >>= 7
		if size > 0 {
			value |= 0x80
		}
		if _, err := w.packWriter.Write([]byte{value}); err != nil {
			return "", err
		}
	}
	compressed := zlib.NewWriter(w.packWriter)
	if _, err := compressed.Write(content); err != nil {
		_ = compressed.Close()
		return "", err
	}
	if err := compressed.Close(); err != nil {
		return "", err
	}
	return oid, nil
}

func (w *fixtureWriter) close(ctx context.Context) error {
	if w.pack == nil {
		return nil
	}
	checksum := w.packHash.Sum(nil)
	if _, err := w.pack.Write(checksum); err != nil {
		_ = w.pack.Close()
		return err
	}
	temporary := w.pack.Name()
	if err := w.pack.Close(); err != nil {
		return err
	}
	target := filepath.Join(filepath.Dir(temporary), "pack-"+hex.EncodeToString(checksum)+".pack")
	if err := os.Rename(temporary, target); err != nil {
		return err
	}
	if output, err := command(ctx, filepath.Dir(w.gitDir), "git", "--git-dir", w.gitDir, "index-pack", target); err != nil {
		return fmt.Errorf("index fixture pack: %w: %s", err, output)
	}
	w.pack = nil
	return nil
}

func objectHash(format string) (hash.Hash, error) {
	switch format {
	case "sha1":
		return sha1.New(), nil
	case "sha256":
		return sha256.New(), nil
	default:
		return nil, fmt.Errorf("unsupported fixture object format %q", format)
	}
}

func fixturePayload(act app.Act) (string, any, []string, error) {
	rests := append([]string(nil), act.RestsOn...)
	switch act.Verb {
	case app.VerbState:
		return workroom.SchemaState, workroom.State{Kind: act.Kind, Text: act.Text, Body: act.Body}, rests, nil
	case app.VerbRatify:
		return workroom.SchemaRatify, workroom.Ratify{Target: act.Target}, []string{act.Target}, nil
	case app.VerbSupersede:
		return workroom.SchemaSupersede, workroom.Supersede{Target: act.Target, Text: act.Text}, append([]string{act.Target}, rests...), nil
	default:
		return "", nil, nil, fmt.Errorf("unsupported synthetic fixture verb %q", act.Verb)
	}
}

func (w *fixtureWriter) writeObject(kind string, content []byte) (string, error) {
	digest, err := objectHash(w.format)
	if err != nil {
		return "", err
	}
	header := []byte(fmt.Sprintf("%s %d\x00", kind, len(content)))
	digest.Write(header)
	digest.Write(content)
	oid := hex.EncodeToString(digest.Sum(nil))
	directory := filepath.Join(w.objects, oid[:2])
	target := filepath.Join(directory, oid[2:])
	if _, err := os.Stat(target); err == nil {
		return oid, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	temporary, err := os.CreateTemp(directory, "fixture-object-*")
	if err != nil {
		return "", err
	}
	temporaryName := temporary.Name()
	cleanup := func() { _ = os.Remove(temporaryName) }
	writer := zlib.NewWriter(temporary)
	if _, err = writer.Write(header); err == nil {
		_, err = writer.Write(content)
	}
	if closeErr := writer.Close(); err == nil {
		err = closeErr
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		cleanup()
		return "", err
	}
	if err := os.Chmod(temporaryName, 0o444); err != nil {
		cleanup()
		return "", err
	}
	if err := os.Rename(temporaryName, target); err != nil {
		cleanup()
		if _, statErr := os.Stat(target); statErr == nil {
			return oid, nil
		}
		return "", err
	}
	return oid, nil
}

func singleFileTree(oid, objectFormat string) ([]byte, error) {
	raw, err := hex.DecodeString(oid)
	if err != nil {
		return nil, err
	}
	want := sha1.Size
	if objectFormat == "sha256" {
		want = sha256.Size
	}
	if len(raw) != want {
		return nil, fmt.Errorf("object id has %d bytes, want %d", len(raw), want)
	}
	return append([]byte("100644 event\x00"), raw...), nil
}

func sshSignature(message []byte, private ed25519.PrivateKey) []byte {
	public := private.Public().(ed25519.PublicKey)
	key := appendSSHString(appendSSHString(nil, []byte("ssh-ed25519")), public)
	hashed := sha512Sum(message)
	toSign := []byte("SSHSIG")
	toSign = appendSSHString(toSign, []byte("git"))
	toSign = appendSSHString(toSign, nil)
	toSign = appendSSHString(toSign, []byte("sha512"))
	toSign = appendSSHString(toSign, hashed)
	rawSignature := ed25519.Sign(private, toSign)
	signature := appendSSHString(appendSSHString(nil, []byte("ssh-ed25519")), rawSignature)
	envelope := append([]byte("SSHSIG"), 0, 0, 0, 1)
	envelope = appendSSHString(envelope, key)
	envelope = appendSSHString(envelope, []byte("git"))
	envelope = appendSSHString(envelope, nil)
	envelope = appendSSHString(envelope, []byte("sha512"))
	envelope = appendSSHString(envelope, signature)
	return pem.EncodeToMemory(&pem.Block{Type: "SSH SIGNATURE", Bytes: envelope})
}

func sha512Sum(message []byte) []byte {
	sum := sha512.Sum512(message)
	return sum[:]
}

func appendSSHString(destination, value []byte) []byte {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value)))
	destination = append(destination, size[:]...)
	return append(destination, value...)
}

func readOpenSSHEd25519(path string) (ed25519.PrivateKey, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(content)
	if block == nil || block.Type != "OPENSSH PRIVATE KEY" {
		return nil, errors.New("not an OpenSSH private key")
	}
	data := block.Bytes
	const magic = "openssh-key-v1\x00"
	if !bytes.HasPrefix(data, []byte(magic)) {
		return nil, errors.New("invalid OpenSSH private key magic")
	}
	data = data[len(magic):]
	var cipherName, kdfName, kdfOptions, public, privateBlock []byte
	for _, destination := range []*[]byte{&cipherName, &kdfName, &kdfOptions} {
		*destination, data, err = takeSSHString(data)
		if err != nil {
			return nil, err
		}
	}
	if string(cipherName) != "none" || string(kdfName) != "none" || len(kdfOptions) != 0 || len(data) < 4 || binary.BigEndian.Uint32(data[:4]) != 1 {
		return nil, errors.New("fixture sequencer key must be one unencrypted key")
	}
	data = data[4:]
	public, data, err = takeSSHString(data)
	if err != nil {
		return nil, err
	}
	privateBlock, data, err = takeSSHString(data)
	if err != nil || len(data) != 0 || len(privateBlock) < 8 {
		return nil, errors.New("malformed OpenSSH private key")
	}
	if !bytes.Equal(privateBlock[:4], privateBlock[4:8]) {
		return nil, errors.New("OpenSSH private key check integers differ")
	}
	privateBlock = privateBlock[8:]
	keyType, rest, err := takeSSHString(privateBlock)
	if err != nil || string(keyType) != "ssh-ed25519" {
		return nil, errors.New("fixture sequencer key is not Ed25519")
	}
	publicKey, rest, err := takeSSHString(rest)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("malformed Ed25519 public key")
	}
	privateKey, rest, err := takeSSHString(rest)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("malformed Ed25519 private key")
	}
	_, rest, err = takeSSHString(rest)
	if err != nil {
		return nil, errors.New("malformed OpenSSH private key comment")
	}
	for index, value := range rest {
		if value != byte(index+1) {
			return nil, errors.New("invalid OpenSSH private key padding")
		}
	}
	wantPublic := appendSSHString(appendSSHString(nil, []byte("ssh-ed25519")), publicKey)
	if !bytes.Equal(public, wantPublic) || !bytes.Equal(privateKey[32:], publicKey) {
		return nil, errors.New("OpenSSH public and private key material differ")
	}
	return append(ed25519.PrivateKey(nil), privateKey...), nil
}

func takeSSHString(data []byte) ([]byte, []byte, error) {
	if len(data) < 4 {
		return nil, nil, errors.New("truncated SSH string")
	}
	size := uint64(binary.BigEndian.Uint32(data[:4]))
	if size > uint64(len(data)-4) {
		return nil, nil, errors.New("oversized SSH string")
	}
	end := 4 + int(size)
	return data[4:end], data[end:], nil
}

func publicKeyText(private ed25519.PrivateKey) string {
	public := private.Public().(ed25519.PublicKey)
	wire := appendSSHString(appendSSHString(nil, []byte("ssh-ed25519")), public)
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(wire)
}
