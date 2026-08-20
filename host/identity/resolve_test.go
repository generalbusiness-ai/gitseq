package identity_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/gitseq/host/identity"
)

// These tests build a verified log by hand and resolve it. Building it here is
// what lets the time rules be stated exactly: a sequencer stamps whole
// seconds, so a repository built inside one test would put every record in the
// same second and an expiry test would prove nothing. Everything used is
// public, and identity_test.go proves the same rules through a real
// repository, so the two together cover both the encoding and the judgement.

const testGenesis = "5d2622748872b7e2dec3fe5c59e4be73a35e0bc8"

type logBuilder struct {
	t       *testing.T
	records []host.Record
}

func testKey(t testing.TB) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return public, private
}

// fingerprint is this test's stand-in for the kernel's actor fingerprint. Only
// its consistency matters to resolution: the same key must name the same actor
// wherever it appears.
func fingerprint(key ed25519.PublicKey) string { return hex.EncodeToString(key) }

// newLog starts a log whose first record is the binding, which is what makes
// its signer the initializing key.
func newLog(t *testing.T, initializer ed25519.PublicKey) *logBuilder {
	builder := &logBuilder{t: t}
	builder.add(initializer, "gitseq/app-binding@0", json.RawMessage(`{}`), 1000)
	return builder
}

func (b *logBuilder) add(key ed25519.PublicKey, schema string, body any, at int64) string {
	b.t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		b.t.Fatal(err)
	}
	id := fmt.Sprintf("git:sha1:%s#git:sha1:%040x", testGenesis, len(b.records)+1)
	b.records = append(b.records, host.Record{
		ID: id, Actor: fingerprint(key), ActorKey: key,
		Schema: schema, Payload: payload, Timestamp: at,
	})
	return id
}

// raw appends a payload exactly as given, for the shapes a well-formed encoder
// would never produce.
func (b *logBuilder) raw(key ed25519.PublicKey, schema string, payload []byte, at int64) string {
	b.t.Helper()
	id := fmt.Sprintf("git:sha1:%s#git:sha1:%040x", testGenesis, len(b.records)+1)
	b.records = append(b.records, host.Record{
		ID: id, Actor: fingerprint(key), ActorKey: key,
		Schema: schema, Payload: payload, Timestamp: at,
	})
	return id
}

func (b *logBuilder) resolve() *identity.Resolution {
	return identity.Resolve(host.Log{Genesis: testGenesis, Records: b.records})
}

func (b *logBuilder) declareWitness(key ed25519.PublicKey, witness ed25519.PublicKey, schemes []string, at int64) string {
	return b.add(key, identity.WitnessSchema, identity.WitnessDeclaration{
		Genesis: testGenesis, Key: hex.EncodeToString(witness), Schemes: schemes,
	}, at)
}

func alice() identity.Identity {
	return identity.Identity{Scheme: identity.GitHubScheme, Subject: "4242", Handle: "alice"}
}

// The rung the whole extraction exists for: a browser-minted key nobody has
// ever seen becomes a persistent identity because the deployment says a
// provider vouched for it, and the saying is a record.
func TestWitnessedAnchorGivesASessionKeyAPersistentIdentity(t *testing.T) {
	initializer, _ := testKey(t)
	witness, _ := testKey(t)
	session, _ := testKey(t)

	log := newLog(t, initializer)
	log.declareWitness(initializer, witness, []string{identity.GitHubScheme}, 1000)
	subject := alice()
	anchored := log.add(witness, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(session), Identity: &subject, Scope: "play",
	}, 2000)

	resolved := log.resolve().Lookup(fingerprint(session), 2000)
	if !resolved.Anchored {
		t.Fatal("witnessed session key resolved as unanchored")
	}
	if resolved.Identity != alice() {
		t.Errorf("identity = %+v, want %+v", resolved.Identity, alice())
	}
	if resolved.Vouching != identity.Witnessed {
		t.Errorf("vouching = %v, want witnessed", resolved.Vouching)
	}
	if resolved.Verification != identity.InLog {
		t.Errorf("verification = %v, want in-log", resolved.Verification)
	}
	if resolved.Scope != "play" || resolved.Record != anchored {
		t.Errorf("scope/record = %q/%q, want %q/%q", resolved.Scope, resolved.Record, "play", anchored)
	}
}

// A key nobody vouched for is a complete answer, not a failure. The whole
// adoption story rests on this being ordinary.
func TestUnvouchedKeyResolvesUnanchored(t *testing.T) {
	initializer, _ := testKey(t)
	stranger, _ := testKey(t)
	log := newLog(t, initializer)

	if resolved := log.resolve().Lookup(fingerprint(stranger), 2000); resolved.Anchored {
		t.Fatalf("stranger resolved as anchored: %+v", resolved)
	}
}

// A delegation carries the endorser's identity and can be no stronger than the
// endorsement the endorser holds. An agent credential minted under a witnessed
// anchor is witnessed, whoever signed the delegation: signing a delegation is
// not a vouching rung and promotes nothing.
func TestDelegationInheritsIdentityAndNeverOutranksItsBasis(t *testing.T) {
	initializer, _ := testKey(t)
	witness, _ := testKey(t)
	person, _ := testKey(t)
	agent, _ := testKey(t)

	log := newLog(t, initializer)
	log.declareWitness(initializer, witness, []string{identity.GitHubScheme}, 1000)
	subject := alice()
	log.add(witness, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(person), Identity: &subject,
	}, 2000)
	log.add(person, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(agent), Scope: "move",
	}, 3000)

	resolved := log.resolve().Lookup(fingerprint(agent), 3000)
	if !resolved.Anchored {
		t.Fatal("agent credential resolved as unanchored")
	}
	if resolved.Identity != alice() {
		t.Errorf("identity = %+v, want the endorser's %+v", resolved.Identity, alice())
	}
	if resolved.Vouching != identity.Witnessed {
		t.Errorf("vouching = %v, want witnessed: a delegation cannot outrank the anchor that minted it", resolved.Vouching)
	}
	if resolved.Scope != "move" {
		t.Errorf("scope = %q, want %q", resolved.Scope, "move")
	}
}

// The verification axis reduces on the same rule, independently of vouching.
func TestDelegationInheritsTheWeakerVerification(t *testing.T) {
	initializer, _ := testKey(t)
	witness, _ := testKey(t)
	person, _ := testKey(t)
	agent, _ := testKey(t)

	log := newLog(t, initializer)
	log.declareWitness(initializer, witness, []string{"forge"}, 1000)
	subject := identity.Identity{Scheme: "forge", Subject: "9"}
	log.add(witness, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(person), Identity: &subject,
		Verification: "live-lookup",
	}, 2000)
	log.add(person, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(agent),
	}, 3000)

	resolution := log.resolve()
	if got := resolution.Lookup(fingerprint(person), 2000).Verification; got != identity.LiveLookup {
		t.Errorf("witnessed verification = %v, want live-lookup", got)
	}
	// The delegation's own signature is in the log, but what it rests on is
	// not, so the answer is the weaker one.
	if got := resolution.Lookup(fingerprint(agent), 3000).Verification; got != identity.LiveLookup {
		t.Errorf("delegated verification = %v, want live-lookup", got)
	}
}

// Nobody can hand on an identity they do not hold, in either of the two ways
// they might try: by endorsing from an unanchored key, or by writing an
// identity into a delegation.
func TestAnEndorserWithNoAnchorMintsNothing(t *testing.T) {
	initializer, _ := testKey(t)
	witness, _ := testKey(t)
	stranger, _ := testKey(t)
	target, _ := testKey(t)

	log := newLog(t, initializer)
	log.declareWitness(initializer, witness, []string{identity.GitHubScheme}, 1000)
	log.add(stranger, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(target),
	}, 2000)

	if resolved := log.resolve().Lookup(fingerprint(target), 2000); resolved.Anchored {
		t.Fatalf("an unanchored endorser minted an identity: %+v", resolved)
	}
}

func TestOnlyTheWitnessMayNameAnIdentity(t *testing.T) {
	initializer, _ := testKey(t)
	witness, _ := testKey(t)
	person, _ := testKey(t)
	target, _ := testKey(t)

	log := newLog(t, initializer)
	log.declareWitness(initializer, witness, []string{identity.GitHubScheme}, 1000)
	subject := alice()
	log.add(witness, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(person), Identity: &subject,
	}, 2000)
	// An anchored person tries to name somebody else as bob rather than pass
	// on their own identity.
	bob := identity.Identity{Scheme: identity.GitHubScheme, Subject: "9999", Handle: "bob"}
	log.add(person, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(target), Identity: &bob,
	}, 3000)

	if resolved := log.resolve().Lookup(fingerprint(target), 3000); resolved.Anchored {
		t.Fatalf("a non-witness named an identity and it took force: %+v", resolved)
	}
}

// The witness key's standing comes from the log, and only from the key that
// initialized the repository.
func TestOnlyTheInitializingKeyDeclaresTheWitness(t *testing.T) {
	initializer, _ := testKey(t)
	impostor, _ := testKey(t)
	witness, _ := testKey(t)
	session, _ := testKey(t)

	log := newLog(t, initializer)
	log.declareWitness(impostor, witness, []string{identity.GitHubScheme}, 1000)
	subject := alice()
	log.add(witness, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(session), Identity: &subject,
	}, 2000)

	resolution := log.resolve()
	if _, ok := resolution.Witness(); ok {
		t.Error("a witness declared by the wrong key took force")
	}
	if resolved := resolution.Lookup(fingerprint(session), 2000); resolved.Anchored {
		t.Fatalf("an undeclared witness anchored a key: %+v", resolved)
	}
}

// A witness is declared for named schemes and cannot reach outside them, so
// adding a provider is a visible act rather than a silent widening.
func TestWitnessCannotMintOutsideItsDeclaredSchemes(t *testing.T) {
	initializer, _ := testKey(t)
	witness, _ := testKey(t)
	session, _ := testKey(t)

	log := newLog(t, initializer)
	log.declareWitness(initializer, witness, []string{identity.GitHubScheme}, 1000)
	elsewhere := identity.Identity{Scheme: "nostr", Subject: "npub1"}
	log.add(witness, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(session), Identity: &elsewhere,
	}, 2000)

	if resolved := log.resolve().Lookup(fingerprint(session), 2000); resolved.Anchored {
		t.Fatalf("witness minted an identity in a scheme it was not declared for: %+v", resolved)
	}
}

// Rotation is one more record, and it does not reach back: what the previous
// key signed keeps the force it had where it stands.
func TestWitnessRotationReplacesWithoutUnmakingWhatCameBefore(t *testing.T) {
	initializer, _ := testKey(t)
	first, _ := testKey(t)
	second, _ := testKey(t)
	early, _ := testKey(t)
	late, _ := testKey(t)

	log := newLog(t, initializer)
	log.declareWitness(initializer, first, []string{identity.GitHubScheme}, 1000)
	subject := alice()
	log.add(first, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(early), Identity: &subject,
	}, 2000)
	log.declareWitness(initializer, second, []string{identity.GitHubScheme}, 3000)
	log.add(second, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(late), Identity: &subject,
	}, 4000)
	// The retired key tries to keep witnessing after it was replaced.
	stale, _ := testKey(t)
	log.add(first, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(stale), Identity: &subject,
	}, 5000)

	resolution := log.resolve()
	declared, ok := resolution.Witness()
	if !ok || declared.Key != hex.EncodeToString(second) {
		t.Fatalf("witness in force = %+v, want the second key", declared)
	}
	if !resolution.Lookup(fingerprint(early), 2000).Anchored {
		t.Error("an anchor the previous witness signed lost its force")
	}
	if !resolution.Lookup(fingerprint(late), 4000).Anchored {
		t.Error("the current witness could not anchor")
	}
	if resolution.Lookup(fingerprint(stale), 5000).Anchored {
		t.Error("a replaced witness key kept witnessing")
	}
}

// An endorsement says nothing about the records that came before it, and
// nothing after it expires. Both edges are judged against the log's own signed
// time.
func TestAnchorHoldsOnlyBetweenItsPositionAndItsExpiry(t *testing.T) {
	initializer, _ := testKey(t)
	witness, _ := testKey(t)
	session, _ := testKey(t)

	log := newLog(t, initializer)
	log.declareWitness(initializer, witness, []string{identity.GitHubScheme}, 1000)
	subject := alice()
	log.add(witness, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(session), Identity: &subject, NotAfter: 5000,
	}, 2000)

	resolution := log.resolve()
	for _, at := range []int64{1999, 5001} {
		if resolution.Lookup(fingerprint(session), at).Anchored {
			t.Errorf("anchored at %d, outside the endorsement's window", at)
		}
	}
	for _, at := range []int64{2000, 5000} {
		if !resolution.Lookup(fingerprint(session), at).Anchored {
			t.Errorf("unanchored at %d, inside the endorsement's window", at)
		}
	}
}

// A revoked key is provable from the log alone, and the withdrawal does not
// reach back: what was folded before it keeps the identity it was folded with.
func TestRevocationEndsAnAnchorAtItsOwnPositionAndNoEarlier(t *testing.T) {
	initializer, _ := testKey(t)
	witness, _ := testKey(t)
	session, _ := testKey(t)

	log := newLog(t, initializer)
	log.declareWitness(initializer, witness, []string{identity.GitHubScheme}, 1000)
	subject := alice()
	anchored := log.add(witness, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(session), Identity: &subject,
	}, 2000)
	log.add(witness, identity.RevokeSchema, identity.Revocation{
		Genesis: testGenesis, Anchor: anchored,
	}, 4000)

	resolution := log.resolve()
	if !resolution.Lookup(fingerprint(session), 3000).Anchored {
		t.Error("a withdrawal reached back and unmade a record already folded")
	}
	if resolution.Lookup(fingerprint(session), 4000).Anchored {
		t.Error("an anchor survived its own withdrawal")
	}
}

func TestOnlyTheEndorserWithdrawsAnEndorsement(t *testing.T) {
	initializer, _ := testKey(t)
	witness, _ := testKey(t)
	session, _ := testKey(t)
	meddler, _ := testKey(t)

	log := newLog(t, initializer)
	log.declareWitness(initializer, witness, []string{identity.GitHubScheme}, 1000)
	subject := alice()
	anchored := log.add(witness, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(session), Identity: &subject,
	}, 2000)
	log.add(meddler, identity.RevokeSchema, identity.Revocation{
		Genesis: testGenesis, Anchor: anchored,
	}, 3000)

	if !log.resolve().Lookup(fingerprint(session), 4000).Anchored {
		t.Fatal("somebody else's revocation withdrew an endorsement")
	}
}

// Withdrawing an anchor withdraws what that anchor minted. Anything else would
// leave the agent keys a revocation was called to stop.
func TestWithdrawingAnAnchorWithdrawsWhatItMinted(t *testing.T) {
	initializer, _ := testKey(t)
	witness, _ := testKey(t)
	person, _ := testKey(t)
	agent, _ := testKey(t)

	log := newLog(t, initializer)
	log.declareWitness(initializer, witness, []string{identity.GitHubScheme}, 1000)
	subject := alice()
	anchored := log.add(witness, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(person), Identity: &subject,
	}, 2000)
	log.add(person, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(agent),
	}, 3000)
	log.add(witness, identity.RevokeSchema, identity.Revocation{
		Genesis: testGenesis, Anchor: anchored,
	}, 4000)

	resolution := log.resolve()
	if !resolution.Lookup(fingerprint(agent), 3500).Anchored {
		t.Error("the agent credential was not in force before the withdrawal")
	}
	if resolution.Lookup(fingerprint(agent), 5000).Anchored {
		t.Error("an agent credential outlived the anchor that minted it")
	}
}

// A delegation cannot outlive its basis by naming a later expiry, which is the
// same rule reached through expiry rather than withdrawal.
func TestDelegationCannotOutliveTheAnchorItRestsOn(t *testing.T) {
	initializer, _ := testKey(t)
	witness, _ := testKey(t)
	person, _ := testKey(t)
	agent, _ := testKey(t)

	log := newLog(t, initializer)
	log.declareWitness(initializer, witness, []string{identity.GitHubScheme}, 1000)
	subject := alice()
	log.add(witness, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(person), Identity: &subject, NotAfter: 4000,
	}, 2000)
	log.add(person, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(agent), NotAfter: 9000,
	}, 3000)

	if log.resolve().Lookup(fingerprint(agent), 5000).Anchored {
		t.Fatal("a delegation named a later expiry than its basis and got it")
	}
}

// An endorsement is good for the repository it names, and for no other. This
// is what stops one deployment's anchors from being replayed into another log.
func TestAnchorFromAnotherRepositoryHasNoForce(t *testing.T) {
	initializer, _ := testKey(t)
	witness, _ := testKey(t)
	session, _ := testKey(t)

	log := newLog(t, initializer)
	log.declareWitness(initializer, witness, []string{identity.GitHubScheme}, 1000)
	subject := alice()
	log.add(witness, identity.AnchorSchema, identity.Anchor{
		Genesis: "0000000000000000000000000000000000000000",
		Subject: fingerprint(session), Identity: &subject,
	}, 2000)

	if resolved := log.resolve().Lookup(fingerprint(session), 2000); resolved.Anchored {
		t.Fatalf("an endorsement for another repository took force here: %+v", resolved)
	}
}

// A witness declaration naming another repository has no standing either, so
// the same replay is closed at both records.
func TestWitnessDeclarationFromAnotherRepositoryHasNoForce(t *testing.T) {
	initializer, _ := testKey(t)
	witness, _ := testKey(t)

	log := newLog(t, initializer)
	log.add(initializer, identity.WitnessSchema, identity.WitnessDeclaration{
		Genesis: "0000000000000000000000000000000000000000",
		Key:     hex.EncodeToString(witness), Schemes: []string{identity.GitHubScheme},
	}, 1000)

	if _, ok := log.resolve().Witness(); ok {
		t.Fatal("a witness declared for another repository took force here")
	}
}

// Every malformed shape is passed over rather than raised, so nobody able to
// append can make a repository's identities unreadable by recording one. The
// good anchor after each of them still resolves.
func TestMalformedRecordsArePassedOverAndLeaveTheAnswerStanding(t *testing.T) {
	initializer, _ := testKey(t)
	witness, _ := testKey(t)
	session, _ := testKey(t)

	subject := alice()
	canonical, err := json.Marshal(identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(session), Identity: &subject,
	})
	if err != nil {
		t.Fatal(err)
	}
	malformed := [][]byte{
		nil,
		[]byte(`{`),
		[]byte(`{"genesis":"` + testGenesis + `","subject":"x","surprise":1}`),
		[]byte(`{"genesis":"` + testGenesis + `","subject":""}`),
		[]byte(`{"genesis":"` + testGenesis + `","subject":"x","verification":"vibes"}`),
		[]byte(`{"genesis":"` + testGenesis + `","subject":"x","not_after":-1}`),
		// Canonical bytes with whitespace added: one meaning must have one
		// encoding, or two builds can disagree about what a log says.
		append(append([]byte{}, canonical...), ' '),
		// Two values in one payload.
		append(append([]byte{}, canonical...), canonical...),
		// A key spelled in another case, and a key given twice. A JSON
		// decoder resolves both without complaining, which is exactly why
		// re-encoding and comparing bytes is the rule: each of these would
		// otherwise be a second way to write one record.
		[]byte(`{"Genesis":"` + testGenesis + `","subject":"x"}`),
		[]byte(`{"genesis":"` + testGenesis + `","subject":"x","subject":"y"}`),
	}

	log := newLog(t, initializer)
	log.declareWitness(initializer, witness, []string{identity.GitHubScheme}, 1000)
	for _, payload := range malformed {
		log.raw(witness, identity.AnchorSchema, payload, 2000)
	}
	if resolved := log.resolve().Lookup(fingerprint(session), 2000); resolved.Anchored {
		t.Errorf("a malformed anchor took force: %+v", resolved)
	}

	log.raw(witness, identity.AnchorSchema, canonical, 3000)
	if !log.resolve().Lookup(fingerprint(session), 3000).Anchored {
		t.Error("a good anchor after malformed ones did not resolve")
	}
}

// A witness declaration that cannot select a key, or that could witness for
// nothing, has no standing.
func TestMalformedWitnessDeclarationsHaveNoForce(t *testing.T) {
	initializer, _ := testKey(t)
	witness, _ := testKey(t)
	good := hex.EncodeToString(witness)

	cases := map[string]identity.WitnessDeclaration{
		"no key":         {Genesis: testGenesis, Schemes: []string{identity.GitHubScheme}},
		"key not hex":    {Genesis: testGenesis, Key: "zz", Schemes: []string{identity.GitHubScheme}},
		"key wrong size": {Genesis: testGenesis, Key: "abcd", Schemes: []string{identity.GitHubScheme}},
		"no scheme":      {Genesis: testGenesis, Key: good, Schemes: nil},
		"scheme twice":   {Genesis: testGenesis, Key: good, Schemes: []string{"github", "github"}},
		"scheme shouty":  {Genesis: testGenesis, Key: good, Schemes: []string{"GitHub"}},
	}
	for name, declaration := range cases {
		t.Run(name, func(t *testing.T) {
			log := newLog(t, initializer)
			log.add(initializer, identity.WitnessSchema, declaration, 1000)
			if _, ok := log.resolve().Witness(); ok {
				t.Fatalf("%s took force", name)
			}
		})
	}
}

// Re-anchoring is one more record: the most recent endorsement in force
// answers, which is how a wider scope or a later expiry is granted.
func TestTheMostRecentEndorsementInForceAnswers(t *testing.T) {
	initializer, _ := testKey(t)
	witness, _ := testKey(t)
	session, _ := testKey(t)

	log := newLog(t, initializer)
	log.declareWitness(initializer, witness, []string{identity.GitHubScheme}, 1000)
	subject := alice()
	log.add(witness, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(session), Identity: &subject, Scope: "watch",
	}, 2000)
	log.add(witness, identity.AnchorSchema, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(session), Identity: &subject, Scope: "play",
	}, 3000)

	resolution := log.resolve()
	if got := resolution.Lookup(fingerprint(session), 3000).Scope; got != "play" {
		t.Errorf("scope at 3000 = %q, want the newer %q", got, "play")
	}
	// The older endorsement still answers where the newer one does not stand.
	if got := resolution.Lookup(fingerprint(session), 2500).Scope; got != "watch" {
		t.Errorf("scope at 2500 = %q, want the older %q", got, "watch")
	}
}

// An empty log answers rather than panics: a repository with nothing in it has
// no identities, which is a fact and not a failure.
func TestEmptyLogResolvesToNothing(t *testing.T) {
	resolution := identity.Resolve(host.Log{Genesis: testGenesis})
	if _, ok := resolution.Witness(); ok {
		t.Error("an empty log declared a witness")
	}
	if resolution.Lookup("anybody", 1).Anchored {
		t.Error("an empty log anchored somebody")
	}
}

func TestAxisNamesReadAsTheyAreWritten(t *testing.T) {
	cases := []struct{ got, want string }{
		{identity.Witnessed.String(), "witnessed"},
		{identity.VouchingUnknown.String(), "unvouched"},
		{identity.InLog.String(), "in-log"},
		{identity.LiveLookup.String(), "live-lookup"},
		{identity.VerificationUnknown.String(), "unverified"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("axis name %q, want %q", c.got, c.want)
		}
	}
}
