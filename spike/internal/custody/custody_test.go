package custody

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"path/filepath"
	"testing"

	"gitseq/spike/internal/gitstore"
	"gitseq/spike/internal/intent"
	"gitseq/spike/internal/kernel"
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
	if statuses[offer.ID] != "settled" || statuses[acceptance.ID] != "settled" || statuses[settlement.ID] != "settled" {
		t.Fatalf("completed saga not settled: %#v", statuses)
	}
	if statuses[losingOffer.ID] != "ineffective" {
		t.Fatalf("losing offer disappeared or gained effect: %#v", statuses)
	}
	for _, candidate := range []domain{domainA, domainB} {
		verification, err := kernel.Verify(context.Background(), candidate.store, candidate.genesis)
		if err != nil || verification.Events == 0 {
			t.Fatalf("domain verification = %#v, %v", verification, err)
		}
	}
}

func TestMultipleCompletedSettlementsRequirePolicy(t *testing.T) {
	asset, a, b := "asset", "a", "b"
	records := []Record{
		{ID: "offer-1", Log: a, Body: Body{Type: Offer, Asset: asset, From: a, To: b}},
		{ID: "accept-1", Log: b, RestsOn: []string{"offer-1"}, Body: Body{Type: Accept, Asset: asset, From: a, To: b}},
		{ID: "settle-1", Log: a, RestsOn: []string{"accept-1"}, Body: Body{Type: Settle, Asset: asset, From: a, To: b}},
		{ID: "offer-2", Log: a, Body: Body{Type: Offer, Asset: asset, From: a, To: b}},
		{ID: "accept-2", Log: b, RestsOn: []string{"offer-2"}, Body: Body{Type: Accept, Asset: asset, From: a, To: b}},
		{ID: "settle-2", Log: a, RestsOn: []string{"accept-2"}, Body: Body{Type: Settle, Asset: asset, From: a, To: b}},
	}
	if _, err := Fold(asset, a, records); err == nil {
		t.Fatal("competing completed settlements need an explicit application policy")
	}
}
