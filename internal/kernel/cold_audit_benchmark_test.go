package kernel

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/observe"
)

// Run with -benchtime=1x. Setup writes ordinary, signed loose Git objects
// directly so generating the evidence does not hide the audit behind hours of
// subprocess launches. Only Verify is timed.
func BenchmarkColdAuditAtDepth50000(b *testing.B) {
	if b.N != 1 {
		b.Skip("run with -benchtime=1x")
	}
	b.StopTimer()
	store, genesis := prepareColdAuditBenchmark(b, 50_000)
	observer := &auditObserver{}
	store.Observer = observer
	b.ResetTimer()
	b.StartTimer()
	verification, err := Verify(context.Background(), store, genesis)
	b.StopTimer()
	if err != nil {
		b.Fatal(err)
	}
	if verification.Depth != 50_000 || verification.Events != 50_000 {
		b.Fatalf("verification = %+v", verification)
	}
	gitProcesses := 0
	for _, measurement := range observer.measurements {
		if measurement.Operation == observe.OperationGit {
			gitProcesses++
		}
	}
	b.ReportMetric(float64(gitProcesses), "git-processes")
	b.ReportMetric(float64(gitProcesses)/50_001, "git-processes/record")
}

func prepareColdAuditBenchmark(t testing.TB, events int) (gitstore.Store, string) {
	t.Helper()
	ctx := context.Background()
	store, err := gitstore.InitBare(ctx, filepath.Join(t.TempDir(), "audit.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	sequencerPublic, sequencerPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, actorPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, publicWire := benchmarkSSHPublicKey(sequencerPublic)
	emptyTree, err := store.EmptyTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	payloadTree, err := store.WritePayloadTree(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := GenesisDescriptor{Version: 0, ObjectFormat: "sha1", PayloadCeiling: 1 << 20, SequencerPublicKey: publicKey}
	message, err := genesisMessage(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	genesis := writeBenchmarkCommit(t, store.Repo, emptyTree, "", message, 1_700_000_000, sequencerPrivate, publicWire)
	head := genesis
	for index := 0; index < events; index++ {
		signed, err := intent.Sign(intent.Intent{
			Version: intent.Version, Target: "git:sha1:" + genesis, Schema: "benchmark.v0",
			EnvelopeVersion: 0, PayloadTree: "git:sha1:" + payloadTree,
			IdempotencyNS: "cold-audit", IdempotencyKey: fmt.Sprintf("event-%d", index),
		}, actorPrivate)
		if err != nil {
			t.Fatal(err)
		}
		head = writeBenchmarkCommit(t, store.Repo, payloadTree, head, intent.Envelope(signed, nil), int64(1_700_000_001+index), sequencerPrivate, publicWire)
	}
	if err := store.UpdateRef(ctx, Ref(genesis), head, strings.Repeat("0", sha1.Size*2)); err != nil {
		t.Fatal(err)
	}
	return store, genesis
}

func writeBenchmarkCommit(t testing.TB, repository, tree, parent, message string, timestamp int64, private ed25519.PrivateKey, publicWire []byte) string {
	t.Helper()
	var headers strings.Builder
	fmt.Fprintf(&headers, "tree %s\n", tree)
	if parent != "" {
		fmt.Fprintf(&headers, "parent %s\n", parent)
	}
	fmt.Fprintf(&headers, "author benchmark <actor@gitseq.invalid> %d +0000\n", timestamp)
	fmt.Fprintf(&headers, "committer benchmark <sequencer@gitseq.invalid> %d +0000\n", timestamp)
	unsigned := []byte(headers.String() + "\n" + message)
	armored := pem.EncodeToMemory(&pem.Block{Type: "SSH SIGNATURE", Bytes: benchmarkSSHSIG(unsigned, private, publicWire)})
	folded := strings.ReplaceAll(strings.TrimSuffix(string(armored), "\n"), "\n", "\n ")
	content := []byte(headers.String() + "gpgsig " + folded + "\n\n" + message)
	object := append([]byte(fmt.Sprintf("commit %d\x00", len(content))), content...)
	digest := sha1.Sum(object)
	oid := hex.EncodeToString(digest[:])
	path := filepath.Join(repository, "objects", oid[:2], oid[2:])
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(object); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, compressed.Bytes(), 0o444); err != nil {
		t.Fatal(err)
	}
	return oid
}

func benchmarkSSHPublicKey(public ed25519.PublicKey) (string, []byte) {
	wire := benchmarkSSHString(nil, []byte("ssh-ed25519"))
	wire = benchmarkSSHString(wire, public)
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(wire), wire
}

func benchmarkSSHSIG(message []byte, private ed25519.PrivateKey, publicWire []byte) []byte {
	digest := sha256.Sum256(message)
	signed := append([]byte(nil), "SSHSIG"...)
	signed = benchmarkSSHString(signed, []byte("git"))
	signed = benchmarkSSHString(signed, nil)
	signed = benchmarkSSHString(signed, []byte("sha256"))
	signed = benchmarkSSHString(signed, digest[:])
	rawSignature := ed25519.Sign(private, signed)
	signature := benchmarkSSHString(nil, []byte("ssh-ed25519"))
	signature = benchmarkSSHString(signature, rawSignature)
	encoded := append([]byte(nil), "SSHSIG"...)
	encoded = benchmarkUint32(encoded, 1)
	encoded = benchmarkSSHString(encoded, publicWire)
	encoded = benchmarkSSHString(encoded, []byte("git"))
	encoded = benchmarkSSHString(encoded, nil)
	encoded = benchmarkSSHString(encoded, []byte("sha256"))
	return benchmarkSSHString(encoded, signature)
}

func benchmarkSSHString(target, value []byte) []byte {
	target = benchmarkUint32(target, uint32(len(value)))
	return append(target, value...)
}

func benchmarkUint32(target []byte, value uint32) []byte {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	return append(target, encoded[:]...)
}
