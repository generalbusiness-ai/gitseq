package custody

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
)

type domain struct {
	store      gitstore.Store
	scratch    gitstore.Store
	key        string
	publicKey  string
	genesis    string
	target     string
	privateKey ed25519.PrivateKey
}

func newDomain(t *testing.T, root, name string) domain {
	t.Helper()
	ctx := context.Background()
	store, err := gitstore.InitBare(ctx, filepath.Join(root, name+".git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	scratch, err := gitstore.InitBare(ctx, filepath.Join(root, name+"-scratch.git"), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(root, name+"-sequencer")
	publicKey, err := gitstore.GenerateSSHKey(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	genesis, err := kernel.Create(ctx, store, kernel.GenesisDescriptor{Version: 0, ObjectFormat: "sha1", PayloadCeiling: 1 << 20, SequencerPublicKey: publicKey}, key)
	if err != nil {
		t.Fatal(err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return domain{store: store, scratch: scratch, key: key, publicKey: publicKey, genesis: genesis, target: "git:sha1:" + genesis, privateKey: privateKey}
}

func submit(t *testing.T, domain domain, key string, body Body, restsOn []string) (kernel.Result, Record) {
	t.Helper()
	ctx := context.Background()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := domain.scratch.WritePayloadTree(ctx, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version, Target: domain.target, Schema: "custody.v0", PayloadTree: "git:sha1:" + tree,
		RestsOn: restsOn, IdempotencyNS: "custody-test", IdempotencyKey: key,
	}, domain.privateKey)
	if err != nil {
		t.Fatal(err)
	}
	result, err := kernel.Submit(ctx, domain.store, kernel.Request{Signed: signed, Payload: payload}, kernel.Options{SigningKey: domain.key})
	if err != nil {
		t.Fatal(err)
	}
	storedPayload, err := domain.store.ReadFile(ctx, result.Commit, "event")
	if err != nil {
		t.Fatal(err)
	}
	var storedBody Body
	if err := json.Unmarshal(storedPayload, &storedBody); err != nil {
		t.Fatal(err)
	}
	return result, Record{ID: eventID(domain.genesis, result.Commit), Log: domain.target, RestsOn: restsOn, Body: storedBody}
}

func eventID(genesis, commit string) string {
	return "git:sha1:" + genesis + "#git:sha1:" + commit
}

func TestThreeStepSagaAcrossSecurityDomains(t *testing.T) {
	root := t.TempDir()
	domainA := newDomain(t, root, "a")
	domainB := newDomain(t, root, "b")
	domainC := "git:sha1:unrelated-domain-c"
	asset := "asset:spike:one"

	_, offer := submit(t, domainA, "offer-b", Body{Type: Offer, Asset: asset, From: domainA.target, To: domainB.target}, nil)
	_, losingOffer := submit(t, domainA, "offer-c", Body{Type: Offer, Asset: asset, From: domainA.target, To: domainC}, nil)
	_, acceptance := submit(t, domainB, "accept", Body{Type: Accept, Asset: asset, From: domainA.target, To: domainB.target}, []string{offer.ID})
	_, settlement := submit(t, domainA, "settle", Body{Type: Settle, Asset: asset, From: domainA.target, To: domainB.target}, []string{acceptance.ID})

	recordsFromA := []Record{offer, losingOffer, settlement, acceptance}
	recordsFromB := []Record{acceptance, offer, losingOffer, settlement}
	state, err := Fold(asset, domainA.target, recordsFromA)
	if err != nil {
		t.Fatal(err)
	}
	stateFromB, err := Fold(asset, domainA.target, recordsFromB)
	if err != nil {
		t.Fatal(err)
	}
	if state.Owner != domainB.target {
		t.Fatalf("owner = %s, want %s", state.Owner, domainB.target)
	}
	if state.Status != Resolved || stateFromB.Status != Resolved {
		t.Fatalf("completed saga should resolve: A=%#v B=%#v", state, stateFromB)
	}
	if stateFromB.Owner != state.Owner {
		t.Fatalf("folds diverged by observation order: A=%s B=%s", state.Owner, stateFromB.Owner)
	}
	statuses := make(map[string]string)
	for _, decision := range state.Decisions {
		statuses[decision.ID] = decision.Status
	}
	for _, decision := range stateFromB.Decisions {
		if statuses[decision.ID] != decision.Status {
			t.Fatalf("folds disagree on %s: A=%s B=%s", decision.ID, statuses[decision.ID], decision.Status)
		}
	}
	if statuses[offer.ID] != Settled || statuses[acceptance.ID] != Settled || statuses[settlement.ID] != Settled {
		t.Fatalf("completed saga not settled: %#v", statuses)
	}
	if statuses[losingOffer.ID] != Ineffective {
		t.Fatalf("losing offer disappeared or gained effect: %#v", statuses)
	}
	for _, candidate := range []domain{domainA, domainB} {
		verification, err := kernel.Verify(context.Background(), candidate.store, candidate.genesis)
		if err != nil || verification.Events == 0 {
			t.Fatalf("domain verification = %#v, %v", verification, err)
		}
	}
}

func TestMultipleCompletedSettlementsBecomeDisputed(t *testing.T) {
	asset, a, b := "asset", "a", "b"
	records := []Record{
		{ID: "offer-1", Log: a, Body: Body{Type: Offer, Asset: asset, From: a, To: b}},
		{ID: "accept-1", Log: b, RestsOn: []string{"offer-1"}, Body: Body{Type: Accept, Asset: asset, From: a, To: b}},
		{ID: "settle-1", Log: a, RestsOn: []string{"accept-1"}, Body: Body{Type: Settle, Asset: asset, From: a, To: b}},
		{ID: "offer-2", Log: a, Body: Body{Type: Offer, Asset: asset, From: a, To: b}},
		{ID: "accept-2", Log: b, RestsOn: []string{"offer-2"}, Body: Body{Type: Accept, Asset: asset, From: a, To: b}},
		{ID: "settle-2", Log: a, RestsOn: []string{"accept-2"}, Body: Body{Type: Settle, Asset: asset, From: a, To: b}},
	}
	state, err := Fold(asset, a, records)
	if err != nil {
		t.Fatalf("ambiguity must be a projection, not an error: %v", err)
	}
	if state.Status != Disputed || state.Owner != a {
		t.Fatalf("competing settlements = %#v, want disputed at last resolved owner", state)
	}
	if len(state.Decisions) != len(records) {
		t.Fatalf("fold omitted decisions: got %d want %d", len(state.Decisions), len(records))
	}
	for _, decision := range state.Decisions {
		if decision.Status != Disputed {
			t.Fatalf("event %s = %s, want disputed", decision.ID, decision.Status)
		}
	}
}

// decisionsByID indexes a folded state so a test can name the event it means
// rather than counting positions.
func decisionsByID(t *testing.T, state State) map[string]Decision {
	t.Helper()
	byID := make(map[string]Decision, len(state.Decisions))
	for _, decision := range state.Decisions {
		byID[decision.ID] = decision
	}
	return byID
}

// saga builds the three records of one complete custody saga. basis is the
// settlement that gave this offeror custody; empty claims the initial owner's.
func saga(suffix, asset, from, to string, basis ...string) []Record {
	offer, accept, settle := "offer-"+suffix, "accept-"+suffix, "settle-"+suffix
	var restsOn []string
	if len(basis) == 1 && basis[0] != "" {
		restsOn = []string{basis[0]}
	}
	return []Record{
		{ID: offer, Log: from, RestsOn: restsOn, Body: Body{Type: Offer, Asset: asset, From: from, To: to}},
		{ID: accept, Log: to, RestsOn: []string{offer}, Body: Body{Type: Accept, Asset: asset, From: from, To: to}},
		{ID: settle, Log: from, RestsOn: []string{accept}, Body: Body{Type: Settle, Asset: asset, From: from, To: to}},
	}
}

func TestNonOwnerSagaDoesNotDisputeTheOwnersTransfer(t *testing.T) {
	asset, a, b, c, d := "asset", "a", "b", "c", "d"
	records := append(saga("owner", asset, a, b), saga("stranger", asset, c, d)...)

	state, err := Fold(asset, a, records)
	if err != nil {
		t.Fatalf("fold: %v", err)
	}
	if state.Status != Resolved {
		t.Fatalf("status = %s, want resolved: a saga from a non-owner is not a competitor", state.Status)
	}
	if state.Owner != b {
		t.Fatalf("owner = %s, want %s: the owner-authorized transfer must still take effect", state.Owner, b)
	}
	byID := decisionsByID(t, state)
	for _, id := range []string{"offer-owner", "accept-owner", "settle-owner"} {
		if byID[id].Status != Settled {
			t.Fatalf("%s = %s, want settled", id, byID[id].Status)
		}
	}
	if got := byID["settle-stranger"]; got.Status != Ineffective || got.Reason != "offeror did not hold custody" {
		t.Fatalf("settle-stranger = %#v, want ineffective for want of custody", got)
	}
}

func TestCompetingOwnerAuthorizedSettlementsStayDisputed(t *testing.T) {
	asset, a, b, c, x, y := "asset", "a", "b", "c", "x", "y"
	records := append(saga("one", asset, a, b), saga("two", asset, a, c)...)
	records = append(records, saga("stranger", asset, x, y)...)

	state, err := Fold(asset, a, records)
	if err != nil {
		t.Fatalf("ambiguity must be a projection, not an error: %v", err)
	}
	if state.Status != Disputed || state.Owner != a {
		t.Fatalf("state = %#v, want disputed at the last resolved owner", state)
	}
	byID := decisionsByID(t, state)
	for _, id := range []string{"offer-one", "accept-one", "settle-one", "offer-two", "accept-two", "settle-two"} {
		if byID[id].Status != Disputed {
			t.Fatalf("%s = %s, want disputed: both were authorized by the owner", id, byID[id].Status)
		}
	}
	if got := byID["settle-stranger"]; got.Status == Disputed {
		t.Fatalf("settle-stranger = %#v, want not disputed: it was never the owner's to offer", got)
	}
	if len(state.Decisions) != len(records) {
		t.Fatalf("fold omitted decisions: got %d want %d", len(state.Decisions), len(records))
	}
}

func TestSequentialTransfersEachTakeEffect(t *testing.T) {
	asset, a, b, c := "asset", "a", "b", "c"
	records := append(saga("first", asset, a, b), saga("second", asset, b, c, "settle-first")...)

	state, err := Fold(asset, a, records)
	if err != nil {
		t.Fatalf("fold: %v", err)
	}
	if state.Status != Resolved {
		t.Fatalf("status = %s, want resolved: sequential transfers do not compete", state.Status)
	}
	if state.Owner != c {
		t.Fatalf("owner = %s, want %s: custody must follow the whole chain", state.Owner, c)
	}
	byID := decisionsByID(t, state)
	for id, decision := range byID {
		if decision.Status != Settled {
			t.Fatalf("%s = %s, want settled", id, decision.Status)
		}
	}
}

func TestCustodyIsNotGrantedRetroactively(t *testing.T) {
	asset, a, b, c := "asset", "a", "b", "c"
	// b settles the asset onward before it ever holds it, then a transfers to b.
	records := append(saga("early", asset, b, c), saga("actual", asset, a, b)...)

	state, err := Fold(asset, a, records)
	if err != nil {
		t.Fatalf("fold: %v", err)
	}
	if state.Owner != b {
		t.Fatalf("owner = %s, want %s: a settlement signed before custody must not fire on acquisition", state.Owner, b)
	}
	byID := decisionsByID(t, state)
	if got := byID["settle-early"]; got.Status != Ineffective || got.Reason != "offeror did not hold custody" {
		t.Fatalf("settle-early = %#v, want ineffective for want of custody at the time it was folded", got)
	}
}

func TestOfferNamingSeveralBasesIsRefused(t *testing.T) {
	asset, a, b, c := "asset", "a", "b", "c"
	records := append(saga("first", asset, a, b), saga("second", asset, b, c, "settle-first")...)
	// An offer that names two grants names none in particular. Taking the
	// first would let an offeror bury a second claim behind a valid one.
	ambiguous := saga("greedy", asset, b, "d", "settle-first")
	ambiguous[0].RestsOn = []string{"settle-first", "settle-second"}
	records = append(records, ambiguous...)

	state, err := Fold(asset, a, records)
	if err != nil {
		t.Fatalf("fold: %v", err)
	}
	byID := decisionsByID(t, state)
	if got := byID["settle-greedy"]; got.Status != Ineffective || got.Reason != "offer does not name one custody basis" {
		t.Fatalf("settle-greedy = %#v, want refused for naming several bases", got)
	}
	if state.Status != Resolved || state.Owner != c {
		t.Fatalf("state = %#v, want the honest chain to resolve at %s", state, c)
	}
}

func TestOfferNamingAnUnknownBasisIsRefused(t *testing.T) {
	asset, a, b, c := "asset", "a", "b", "c"
	records := append(saga("first", asset, a, b), saga("phantom", asset, b, c, "settle-never-happened")...)

	state, err := Fold(asset, a, records)
	if err != nil {
		t.Fatalf("fold: %v", err)
	}
	byID := decisionsByID(t, state)
	// b really does hold the asset here, so "did not hold custody" would be a
	// false account of why this saga fails. It fails because the grant it
	// claims is not in the log at all.
	if got := byID["settle-phantom"]; got.Status != Ineffective || got.Reason != "offer names an unknown custody basis" {
		t.Fatalf("settle-phantom = %#v, want refused for naming a basis this log does not carry", got)
	}
	if state.Status != Resolved || state.Owner != b {
		t.Fatalf("state = %#v, want the honest transfer to resolve at %s", state, b)
	}
}

func TestBasisMustHaveGrantedCustodyToTheOfferor(t *testing.T) {
	asset, a, b, c, d := "asset", "a", "b", "c", "d"
	records := append(saga("first", asset, a, b), saga("second", asset, b, c, "settle-first")...)
	// d names a real settlement, but that settlement granted custody to b.
	records = append(records, saga("borrowed", asset, d, "e", "settle-first")...)

	state, err := Fold(asset, a, records)
	if err != nil {
		t.Fatalf("fold: %v", err)
	}
	byID := decisionsByID(t, state)
	if got := byID["settle-borrowed"]; got.Status != Ineffective || got.Reason != "offeror did not hold custody" {
		t.Fatalf("settle-borrowed = %#v, want ineffective: the grant it names was not its own", got)
	}
	if state.Status != Resolved || state.Owner != c {
		t.Fatalf("state = %#v, want the real chain to resolve at %s", state, c)
	}
}

func TestReacquiredCustodyCanBeTransferredOnward(t *testing.T) {
	asset, a, b, c := "asset", "a", "b", "c"
	records := append(saga("out", asset, a, b), saga("back", asset, b, a, "settle-out")...)
	records = append(records, saga("onward", asset, a, c, "settle-back")...)

	state, err := Fold(asset, a, records)
	if err != nil {
		t.Fatalf("fold: %v", err)
	}
	if state.Status != Resolved || state.Owner != c {
		t.Fatalf("state = %#v, want %s to hold after a to b to a to c", state, c)
	}
	byID := decisionsByID(t, state)
	// The reacquisition must be spent by name. a's second transfer rests on
	// settle-back, not on the initial grant it already used for settle-out.
	for _, id := range []string{"settle-out", "settle-back", "settle-onward"} {
		if byID[id].Status != Settled {
			t.Fatalf("%s = %s, want settled", id, byID[id].Status)
		}
	}
}

// foldEveryOrder folds records in 2n deterministic orders -- every rotation and
// every reversed rotation -- and returns the projection, failing if any order
// disagrees with any other. Ordering authority is exactly what this package
// gave up, so a disagreement here is the defect it was built to remove.
func foldEveryOrder(t *testing.T, asset, initialOwner string, records []Record) State {
	t.Helper()
	var want State
	for offset := 0; offset < len(records); offset++ {
		rotated := append(append([]Record{}, records[offset:]...), records[:offset]...)
		reversed := make([]Record, 0, len(rotated))
		for index := len(rotated) - 1; index >= 0; index-- {
			reversed = append(reversed, rotated[index])
		}
		for _, order := range [][]Record{rotated, reversed} {
			state, err := Fold(asset, initialOwner, order)
			if err != nil {
				t.Fatalf("fold at offset %d: %v", offset, err)
			}
			normalized := State{Asset: state.Asset, Owner: state.Owner, Status: state.Status, Decisions: decisionsInIDOrder(state)}
			if offset == 0 && len(want.Decisions) == 0 {
				want = normalized
				continue
			}
			if !reflect.DeepEqual(normalized, want) {
				t.Fatalf("order at offset %d projected %#v, want %#v", offset, normalized, want)
			}
		}
	}
	return want
}

func decisionsInIDOrder(state State) []Decision {
	ordered := append([]Decision{}, state.Decisions...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	return ordered
}

func TestChainProjectsTheSameInEveryOrder(t *testing.T) {
	asset, a, b, c := "asset", "a", "b", "c"
	records := append(saga("first", asset, a, b), saga("second", asset, b, c, "settle-first")...)

	state := foldEveryOrder(t, asset, a, records)
	if state.Status != Resolved || state.Owner != c {
		t.Fatalf("state = %#v, want a to b to c to resolve at %s in every order", state, c)
	}
}

func TestLateCompetingTransferDisputesInEveryOrder(t *testing.T) {
	asset, a, b, c, d := "asset", "a", "b", "c", "d"
	records := append(saga("first", asset, a, b), saga("second", asset, b, c, "settle-first")...)
	// a to d names no basis, so it claims the same initial grant a to b claims.
	// Two settlements spending one basis are disputed however late it arrives.
	records = append(records, saga("late", asset, a, d)...)

	state := foldEveryOrder(t, asset, a, records)
	if state.Status != Disputed || state.Owner != a {
		t.Fatalf("state = %#v, want disputed at %s: two transfers spend the initial grant", state, a)
	}
	byID := make(map[string]Decision, len(state.Decisions))
	for _, decision := range state.Decisions {
		byID[decision.ID] = decision
	}
	for _, id := range []string{"settle-first", "settle-late"} {
		if byID[id].Status != Disputed {
			t.Fatalf("%s = %s, want disputed", id, byID[id].Status)
		}
	}
}

func TestBasisNamingSomethingOtherThanASettlementIsRefused(t *testing.T) {
	asset, a, b, c := "asset", "a", "b", "c"
	// b names the offer it accepted rather than the settlement that completed
	// it. An offer is not a grant of custody, so this must fail closed for the
	// same reason a basis absent from the log does.
	records := append(saga("first", asset, a, b), saga("premature", asset, b, c, "offer-first")...)

	state, err := Fold(asset, a, records)
	if err != nil {
		t.Fatalf("fold: %v", err)
	}
	byID := decisionsByID(t, state)
	if got := byID["settle-premature"]; got.Status != Ineffective || got.Reason != "offer names an unknown custody basis" {
		t.Fatalf("settle-premature = %#v, want refused: only a settlement grants custody", got)
	}
	if state.Status != Resolved || state.Owner != b {
		t.Fatalf("state = %#v, want the honest transfer to resolve at %s", state, b)
	}
}
