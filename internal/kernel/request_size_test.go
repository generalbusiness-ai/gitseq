package kernel

import (
	"crypto/ed25519"
	"fmt"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/intent"
)

// The genesis ceiling is a hard bound and stays one. What changes is what an
// author is told when they meet it. Each case here fits exactly, then is
// refused one byte under, and the refusal is compared with a message this test
// builds from its own measurements: the ceiling, the total, the split across
// envelope, payload and attachments, the largest attachment by name, and the
// remedy.

func (f fixtureState) sizedRequest(t testing.TB, private ed25519.PrivateKey, key string, payload []byte, attachments map[string][]byte) Request {
	t.Helper()
	tree, err := f.scratch.WritePayloadTree(f.ctx, payload, attachments)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version,
		Target:  "git:" + f.format + ":" + f.genesis,
		Schema:  "spike.event.v0", PayloadTree: "git:" + f.format + ":" + tree,
		IdempotencyNS: "test", IdempotencyKey: key,
	}, private)
	if err != nil {
		t.Fatal(err)
	}
	return Request{Signed: signed, Payload: payload, Attachments: attachments}
}

// sizedRequestUnchecked signs a request whose attachments are never written to
// a payload tree, so a name no tree would accept can still be measured. It is
// how the untrusted-name case reaches the kernel's accounting the way a raw
// submission body reaches it.
func (f fixtureState) sizedRequestUnchecked(t testing.TB, private ed25519.PrivateKey, key string, payload []byte, attachments map[string][]byte) Request {
	t.Helper()
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version,
		Target:  "git:" + f.format + ":" + f.genesis,
		Schema:  "spike.event.v0", PayloadTree: "git:" + f.format + ":" + strings.Repeat("0", 40),
		IdempotencyNS: "test", IdempotencyKey: key,
	}, private)
	if err != nil {
		t.Fatal(err)
	}
	return Request{Signed: signed, Payload: payload, Attachments: attachments}
}

func TestCeilingRefusalNamesTheMeasuredSplitAtEveryBoundary(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	cases := []struct {
		name        string
		payload     []byte
		attachments map[string][]byte
		split       string
	}{
		{
			name:  "envelope alone",
			split: "payload 0, no attachments",
		},
		{
			name:    "payload over the remainder",
			payload: make([]byte, 4096),
			split:   "payload 4096, no attachments",
		},
		{
			name:        "one attachment over the remainder",
			payload:     []byte("{}"),
			attachments: map[string][]byte{"evidence.json": make([]byte, 8192)},
			split:       `payload 2, attachments 8192 in 1 file, largest "evidence.json" at 8192 bytes`,
		},
		{
			// No single attachment is near the ceiling here. The aggregate is
			// what refuses, which is the shape the measured logs actually
			// carry, and the two equal largest settle by name so one request
			// always names the same file.
			name:        "several attachments in aggregate",
			payload:     []byte("aggr"),
			attachments: map[string][]byte{"a.log": make([]byte, 3000), "b.log": make([]byte, 3000), "c.log": make([]byte, 2000)},
			split:       `payload 4, attachments 8000 in 3 files, largest "a.log" at 3000 bytes`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			request := f.sizedRequest(t, private, "boundary-"+testCase.name, testCase.payload, testCase.attachments)
			decoded, err := intent.Verify(request.Signed)
			if err != nil {
				t.Fatal(err)
			}
			envelope := len(intent.Envelope(request.Signed, decoded.RestsOn))
			total := envelope + len(testCase.payload)
			for _, content := range testCase.attachments {
				total += len(content)
			}
			if err := ValidateRequestSize(request, uint64(total)); err != nil {
				t.Fatalf("exactly the ceiling was refused: %v", err)
			}
			err = ValidateRequestSize(request, uint64(total-1))
			if err == nil {
				t.Fatal("one byte over the ceiling was admitted")
			}
			want := fmt.Sprintf("event exceeds genesis ceiling: %d bytes against a ceiling of %d (envelope %d, %s); shrink the evidence, split it across several events, or cite a repository path instead of attaching bytes",
				total, total-1, envelope, testCase.split)
			if err.Error() != want {
				t.Fatalf("refusal =\n  %s\nwant\n  %s", err.Error(), want)
			}
		})
	}
}

// Submit is where the ceiling is enforced, so the diagnostic has to survive
// the whole path an author actually takes, and the refusal still has to leave
// the sequence exactly where it was.
func TestSubmitRefusesOversizedAttachmentsWithTheSameDiagnostic(t *testing.T) {
	t.Parallel()
	f := newFixtureWithCeiling(t, "sha1", 4<<10)
	private := actor(t)
	before, err := f.store.Head(f.ctx, Ref(f.genesis))
	if err != nil {
		t.Fatal(err)
	}
	request := f.sizedRequest(t, private, "submit-oversized", []byte("report"),
		map[string][]byte{"observations.json": make([]byte, 6000), "notes.md": make([]byte, 500)})
	_, err = Submit(f.ctx, f.store, request, Options{SigningKey: f.signingKey})
	if err == nil {
		t.Fatal("Submit admitted an event over the ceiling")
	}
	for _, want := range []string{
		"event exceeds genesis ceiling: ",
		"against a ceiling of 4096",
		`largest "observations.json" at 6000 bytes`,
		"attachments 6500 in 2 files",
		"cite a repository path instead of attaching bytes",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Submit refusal %q does not carry %q", err.Error(), want)
		}
	}
	after, err := f.store.Head(f.ctx, Ref(f.genesis))
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("refused submission moved the sequence: before=%s after=%s", before, after)
	}
}

// Attachments live in a map, and Go randomises map order, so a diagnostic that
// simply kept the first of several equal-largest attachments would name a
// different file on different runs of the same request. Repeating one
// measurement is how that is caught: the message has to be one message.
func TestCeilingRefusalNamesTheSameLargestAttachmentEveryTime(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	attachments := map[string][]byte{}
	for _, name := range []string{"a.log", "b.log", "c.log", "d.log", "e.log", "f.log"} {
		attachments[name] = make([]byte, 3000)
	}
	request := f.sizedRequest(t, actor(t), "stable-largest", []byte("tie"), attachments)
	seen := map[string]int{}
	for attempt := 0; attempt < 64; attempt++ {
		err := ValidateRequestSize(request, 1)
		if err == nil {
			t.Fatal("an oversized request was admitted")
		}
		seen[err.Error()]++
	}
	if len(seen) != 1 {
		t.Fatalf("the same request produced %d different refusals: %v", len(seen), seen)
	}
	for message := range seen {
		if !strings.Contains(message, `largest "a.log" at 3000 bytes`) {
			t.Fatalf("refusal %q does not name the alphabetically first of the equal largest", message)
		}
	}
}

// The pre-signing measurement is only worth having if it is the same
// measurement. The actor key and the signature are fixed widths, so a
// placeholder signature must produce the identical envelope, and therefore the
// identical total and split, as the signature that eventually lands. Each case
// is measured both ways at the exact ceiling and one byte under it, so an
// agreement is proved on the refusal text as well as on the verdict.
func TestUnsignedMeasurementEqualsTheSignedMeasurement(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	private := actor(t)
	public := private.Public().(ed25519.PublicKey)
	cases := []struct {
		name        string
		payload     []byte
		attachments map[string][]byte
		rests       []string
	}{
		{name: "envelope alone"},
		{name: "payload only", payload: make([]byte, 4096)},
		{name: "one attachment", payload: []byte("{}"), attachments: map[string][]byte{"evidence.json": make([]byte, 8192)}},
		{
			name: "several attachments and causal references", payload: []byte("aggr"),
			attachments: map[string][]byte{"a.log": make([]byte, 3000), "b.log": make([]byte, 3000), "c.log": make([]byte, 2000)},
			// Causal references are carried into the envelope verbatim, so
			// they are part of what has to agree. Nothing resolves them here.
			rests: []string{
				"git:sha1:" + strings.Repeat("a", 40) + "#git:sha1:" + strings.Repeat("b", 40),
				"git:sha1:" + strings.Repeat("a", 40) + "#git:sha1:" + strings.Repeat("c", 40),
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			tree, err := f.scratch.WritePayloadTree(f.ctx, testCase.payload, testCase.attachments)
			if err != nil {
				t.Fatal(err)
			}
			value := intent.Intent{
				Version: intent.Version, Target: "git:" + f.format + ":" + f.genesis,
				Schema: "spike.event.v0", PayloadTree: "git:" + f.format + ":" + tree,
				RestsOn: testCase.rests, IdempotencyNS: "test", IdempotencyKey: "unsigned-" + testCase.name,
			}
			encodedIntent, err := intent.Encode(value)
			if err != nil {
				t.Fatal(err)
			}
			signed, err := intent.SignEncoded(encodedIntent, private)
			if err != nil {
				t.Fatal(err)
			}
			request := Request{Signed: signed, Payload: testCase.payload, Attachments: testCase.attachments}
			decoded, err := intent.Verify(request.Signed)
			if err != nil {
				t.Fatal(err)
			}

			signedSize := measureRequest(intent.Envelope(request.Signed, decoded.RestsOn), request)
			placeholder := Request{
				Signed:  intent.Signed{Intent: encodedIntent, ActorKey: public, Signature: make([]byte, ed25519.SignatureSize)},
				Payload: testCase.payload, Attachments: testCase.attachments,
			}
			unsignedSize := measureRequest(intent.Envelope(placeholder.Signed, testCase.rests), placeholder)
			if unsignedSize != signedSize {
				t.Fatalf("unsigned measurement %+v differs from signed measurement %+v", unsignedSize, signedSize)
			}

			exact := signedSize.total()
			for _, ceiling := range []uint64{exact, exact - 1} {
				fromSigned := ValidateRequestSize(request, ceiling)
				fromUnsigned := ValidateUnsignedRequestSize(encodedIntent, public, testCase.rests, testCase.payload, testCase.attachments, ceiling)
				if (fromSigned == nil) != (fromUnsigned == nil) {
					t.Fatalf("at ceiling %d the signed verdict was %v and the unsigned verdict was %v", ceiling, fromSigned, fromUnsigned)
				}
				if fromSigned != nil && fromSigned.Error() != fromUnsigned.Error() {
					t.Fatalf("at ceiling %d the refusals differ:\n  signed:   %s\n  unsigned: %s", ceiling, fromSigned, fromUnsigned)
				}
			}
		})
	}
}

// A wrong-length actor key would measure a shorter envelope than the act will
// carry, so the measurement refuses to guess.
func TestUnsignedMeasurementRefusesAnActorKeyOfTheWrongLength(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	encodedIntent, err := intent.Encode(intent.Intent{
		Version: intent.Version, Target: "git:" + f.format + ":" + f.genesis,
		Schema: "spike.event.v0", PayloadTree: "git:" + f.format + ":" + strings.Repeat("0", 40),
		IdempotencyNS: "test", IdempotencyKey: "short-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = ValidateUnsignedRequestSize(encodedIntent, make([]byte, ed25519.PublicKeySize-1), nil, nil, nil, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "invalid actor public key length") {
		t.Fatalf("short actor key error = %v", err)
	}
}

// stepwiseCeiling is the accounting this file replaced, kept verbatim as an
// oracle. It charged the envelope, then the payload, then each attachment,
// refusing as soon as a running total would pass. The new measurement sums
// first and judges once, which is only a safe change if the two refuse for
// exactly the same requests.
func stepwiseCeiling(envelope, payload uint64, attachments []uint64, ceiling uint64) bool {
	eventSize := envelope
	if eventSize > ceiling || payload > ceiling-eventSize {
		return true
	}
	eventSize += payload
	for _, size := range attachments {
		if eventSize > ceiling || size > ceiling-eventSize {
			return true
		}
		eventSize += size
	}
	return eventSize > ceiling
}

func TestSummedAccountingRefusesExactlyWhatTheStepwiseAccountingRefused(t *testing.T) {
	t.Parallel()
	maximum := ^uint64(0)
	cases := []struct {
		name        string
		envelope    uint64
		payload     uint64
		attachments []uint64
		ceiling     uint64
	}{
		{name: "envelope alone at the ceiling", envelope: 400, ceiling: 400},
		{name: "envelope alone one over", envelope: 401, ceiling: 400},
		{name: "payload alone at the ceiling", envelope: 400, payload: 100, ceiling: 500},
		{name: "payload alone one over", envelope: 400, payload: 101, ceiling: 500},
		{name: "one attachment at the ceiling", envelope: 400, payload: 100, attachments: []uint64{500}, ceiling: 1000},
		{name: "one attachment one over", envelope: 400, payload: 100, attachments: []uint64{501}, ceiling: 1000},
		{name: "aggregate at the ceiling", envelope: 400, payload: 100, attachments: []uint64{200, 200, 100}, ceiling: 1000},
		{name: "aggregate one over", envelope: 400, payload: 100, attachments: []uint64{200, 200, 101}, ceiling: 1000},
		{name: "empty everything under any ceiling", ceiling: 1},
		{name: "zero ceiling admits an empty request", ceiling: 0},
		{name: "zero ceiling refuses one byte", envelope: 1, ceiling: 0},
		{name: "the ceiling is the maximum", envelope: 400, payload: 100, attachments: []uint64{maximum - 501}, ceiling: maximum},
		{name: "a sum that would wrap", envelope: maximum, payload: maximum, attachments: []uint64{maximum}, ceiling: maximum},
		{name: "two attachments that would wrap", envelope: 1, attachments: []uint64{maximum, maximum}, ceiling: maximum},
		// The one shape where the remaining budget alone cannot tell: the
		// whole ceiling is still unspent, so a saturated total compares equal
		// to it rather than above it, and only the recorded saturation says
		// the true total is larger.
		{name: "a saturating sum against an unspent maximum ceiling", attachments: []uint64{maximum, maximum}, ceiling: maximum},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			size := requestSize{envelope: testCase.envelope, payload: testCase.payload,
				count: len(testCase.attachments), largestName: "oracle.bin"}
			for _, content := range testCase.attachments {
				var wrapped bool
				size.attachments, wrapped = addSize(size.attachments, content)
				size.saturated = size.saturated || wrapped
			}
			refused := size.judge(testCase.ceiling) != nil
			if want := stepwiseCeiling(testCase.envelope, testCase.payload, testCase.attachments, testCase.ceiling); refused != want {
				t.Fatalf("summed accounting refused = %v, stepwise accounting refused = %v", refused, want)
			}
		})
	}
}

func TestAddSizeSaturatesInsteadOfWrapping(t *testing.T) {
	t.Parallel()
	maximum := ^uint64(0)
	cases := []struct {
		total, size, want uint64
		wrapped           bool
	}{
		{total: 0, size: 0, want: 0},
		{total: 1, size: 2, want: 3},
		{total: maximum - 1, size: 1, want: maximum},
		{total: maximum, size: 1, want: maximum, wrapped: true},
		{total: maximum, size: maximum, want: maximum, wrapped: true},
		{total: maximum/2 + 1, size: maximum/2 + 1, want: maximum, wrapped: true},
	}
	for _, testCase := range cases {
		got, wrapped := addSize(testCase.total, testCase.size)
		if got != testCase.want || wrapped != testCase.wrapped {
			t.Fatalf("addSize(%d, %d) = %d, %v; want %d, %v", testCase.total, testCase.size, got, wrapped, testCase.want, testCase.wrapped)
		}
	}
}

// The kernel measures a request before the payload tree writer validates
// attachment names, so a raw submission body can carry a key of any length.
// The diagnostic must not echo it back whole.
func TestRefusalBoundsAnOverlongAttachmentName(t *testing.T) {
	t.Parallel()
	f := newFixtureWithCeiling(t, "sha1", 1<<10)
	overlong := strings.Repeat("n", 4<<20)
	request := f.sizedRequestUnchecked(t, actor(t), "overlong-name", []byte("{}"),
		map[string][]byte{overlong: make([]byte, 2<<10)})
	decoded, err := intent.Verify(request.Signed)
	if err != nil {
		t.Fatal(err)
	}
	err = validateRequestSize(request, decoded, 1<<10)
	if err == nil {
		t.Fatal("an oversized request with an overlong attachment name was admitted")
	}
	if length := len(err.Error()); length > 1024 {
		t.Fatalf("refusal is %d bytes long; an untrusted name reached it whole", length)
	}
	if !strings.Contains(err.Error(), `largest "`+strings.Repeat("n", maxAttachmentNameBytes)+`" at 2048 bytes`) {
		t.Fatalf("refusal does not carry the bounded name: %s", err.Error())
	}
}
