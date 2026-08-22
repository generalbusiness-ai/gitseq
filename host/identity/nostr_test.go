package identity_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/gitseq/host/identity"
)

func nostrKey(t testing.TB) *btcec.PrivateKey {
	t.Helper()
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func signNostrAnchor(t testing.TB, key *btcec.PrivateKey, anchor identity.Anchor) identity.Anchor {
	t.Helper()
	message, err := identity.NostrDelegation(anchor)
	if err != nil {
		t.Fatal(err)
	}
	proof := signNostrEvent(t, key, message)
	anchor.Nostr = &proof
	return anchor
}

func signNostrAnchorAt(t testing.TB, key *btcec.PrivateKey, anchor identity.Anchor, createdAt int64) identity.Anchor {
	t.Helper()
	message, err := identity.NostrDelegation(anchor)
	if err != nil {
		t.Fatal(err)
	}
	proof := signNostrEventAt(t, key, message, createdAt)
	anchor.Nostr = &proof
	return anchor
}

func signNostrEvent(t testing.TB, key *btcec.PrivateKey, content string) identity.NostrProof {
	t.Helper()
	return signNostrEventAt(t, key, content, 1_700_000_000)
}

func signNostrEventAt(t testing.TB, key *btcec.PrivateKey, content string, createdAt int64) identity.NostrProof {
	t.Helper()
	proof := identity.NostrProof{
		PubKey:    hex.EncodeToString(schnorr.SerializePubKey(key.PubKey())),
		CreatedAt: createdAt,
		Kind:      identity.NostrProofKind,
		Tags:      [][]string{},
		Content:   content,
	}
	var serialized bytes.Buffer
	encoder := json.NewEncoder(&serialized)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode([]any{0, proof.PubKey, proof.CreatedAt, proof.Kind, proof.Tags, proof.Content}); err != nil {
		t.Fatal(err)
	}
	id := sha256.Sum256(bytes.TrimSuffix(serialized.Bytes(), []byte{'\n'}))
	proof.ID = hex.EncodeToString(id[:])
	signature, err := schnorr.Sign(key, id[:])
	if err != nil {
		t.Fatal(err)
	}
	proof.Sig = hex.EncodeToString(signature.Serialize())
	return proof
}

func TestNostrProofUsesNIP01BrowserSerialization(t *testing.T) {
	proof := signNostrEvent(t, nostrKey(t), "gitseq&nip07")
	serialized := `[0,"` + proof.PubKey + `",1700000000,20000,[],"gitseq&nip07"]`
	want := sha256.Sum256([]byte(serialized))
	if proof.ID != hex.EncodeToString(want[:]) {
		t.Fatalf("event id = %s, want NIP-01 JSON.stringify digest %x", proof.ID, want)
	}
}

func signNostrWithdrawal(t testing.TB, key *btcec.PrivateKey, revocation identity.Revocation) identity.NostrProof {
	t.Helper()
	message, err := identity.NostrWithdrawal(revocation)
	if err != nil {
		t.Fatal(err)
	}
	return signNostrEvent(t, key, message)
}

func TestNostrDelegationBindsEveryAuthorityField(t *testing.T) {
	anchor := identity.Anchor{
		Genesis:  testGenesis,
		Subject:  strings.Repeat("a", 64),
		Scope:    "play & recover",
		NotAfter: 1234,
	}
	got, err := identity.NostrDelegation(anchor)
	if err != nil {
		t.Fatal(err)
	}
	want := "nostr:delegation:" + anchor.Subject + ":genesis=" + testGenesis + "&not_after=1234&scope=play+%26+recover"
	if got != want {
		t.Fatalf("delegation = %q, want %q", got, want)
	}

	for name, mutate := range map[string]func(*identity.Anchor){
		"repository": func(a *identity.Anchor) { a.Genesis = strings.Repeat("0", 40) },
		"subject":    func(a *identity.Anchor) { a.Subject = strings.Repeat("b", 64) },
		"scope":      func(a *identity.Anchor) { a.Scope = "watch" },
		"expiry":     func(a *identity.Anchor) { a.NotAfter++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := anchor
			mutate(&changed)
			message, err := identity.NostrDelegation(changed)
			if err != nil {
				t.Fatal(err)
			}
			if message == got {
				t.Fatalf("mutating %s did not change the signed message", name)
			}
		})
	}
}

func TestSelfSignedNostrAnchorResolvesAndDisplaysBothAxes(t *testing.T) {
	initializer, _ := testKey(t)
	session, _ := testKey(t)
	root := nostrKey(t)

	log := newLog(t, initializer)
	anchor := signNostrAnchor(t, root, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(session), Scope: "play",
	})
	record := log.add(session, identity.AnchorSchema, anchor, 2000)

	resolved := log.resolve().LookupAt(record)
	if !resolved.Anchored {
		t.Fatal("valid Nostr proof resolved as unanchored")
	}
	wantIdentity := identity.Identity{
		Scheme:  identity.NostrScheme,
		Subject: hex.EncodeToString(schnorr.SerializePubKey(root.PubKey())),
	}
	if resolved.Identity != wantIdentity {
		t.Errorf("identity = %+v, want %+v", resolved.Identity, wantIdentity)
	}
	if resolved.Vouching != identity.SelfSigned || resolved.Verification != identity.InLog {
		t.Errorf("axes = %v/%v, want self-signed/in-log", resolved.Vouching, resolved.Verification)
	}
	if resolved.Scope != "play" || resolved.Record != record {
		t.Errorf("scope/record = %q/%q, want play/%q", resolved.Scope, resolved.Record, record)
	}
	wantDisplay := identity.NostrScheme + ":" + wantIdentity.Subject + " (self-signed; in-log)"
	if got := resolved.Display(fingerprint(session)); got != wantDisplay {
		t.Errorf("display = %q, want %q", got, wantDisplay)
	}
	if got := (identity.Resolved{}).Display(fingerprint(session)); got != fingerprint(session)+" (unanchored)" {
		t.Errorf("unanchored display = %q", got)
	}
}

func TestNostrEventTimeDoesNotGovernGitseqResolution(t *testing.T) {
	initializer, _ := testKey(t)
	session, _ := testKey(t)
	root := nostrKey(t)
	anchor := identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(session), Scope: "play", NotAfter: 2500,
	}
	content, err := identity.NostrDelegation(anchor)
	if err != nil {
		t.Fatal(err)
	}
	proof := signNostrEventAt(t, root, content, 9_000_000_000)
	anchor.Nostr = &proof

	log := newLog(t, initializer)
	log.add(session, identity.AnchorSchema, anchor, 2000)
	beforeExpiry := log.act(session, 2400)
	if !log.resolve().LookupAt(beforeExpiry).Anchored {
		t.Fatal("future Nostr event time prevented the Gitseq anchor taking force")
	}
	afterExpiry := log.act(session, 2501)
	if log.resolve().LookupAt(afterExpiry).Anchored {
		t.Fatal("Nostr event time overrode the Gitseq NotAfter boundary")
	}
}

func TestOnlyTheNostrRootWithdrawsItsAnchor(t *testing.T) {
	initializer, _ := testKey(t)
	session, _ := testKey(t)
	submitter, _ := testKey(t)
	root := nostrKey(t)

	log := newLog(t, initializer)
	anchor := signNostrAnchor(t, root, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(session), Scope: "play",
	})
	anchored := log.add(session, identity.AnchorSchema, anchor, 2000)
	revocation := identity.Revocation{Genesis: testGenesis, Anchor: anchored}
	wrong := nostrKey(t)
	wrongProof := signNostrWithdrawal(t, wrong, revocation)
	revocation.Nostr = &wrongProof
	log.add(submitter, identity.RevokeSchema, revocation, 3000)
	afterWrongRoot := log.act(session, 3500)
	if !log.resolve().LookupAt(afterWrongRoot).Anchored {
		t.Fatal("another Nostr root withdrew the anchor")
	}

	proof := signNostrWithdrawal(t, root, identity.Revocation{Genesis: testGenesis, Anchor: anchored})
	revocation.Nostr = &proof
	log.add(submitter, identity.RevokeSchema, revocation, 4000)
	afterRightRoot := log.act(session, 4000)
	if log.resolve().LookupAt(afterRightRoot).Anchored {
		t.Fatal("the Nostr root could not withdraw its anchor through another Gitseq submitter")
	}
}

func TestNostrRootWithdrawalRetiresTheGrantAcrossReplays(t *testing.T) {
	initializer, _ := testKey(t)
	session, _ := testKey(t)
	submitter, _ := testKey(t)
	root := nostrKey(t)

	log := newLog(t, initializer)
	anchor := signNostrAnchor(t, root, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(session), Scope: "play",
	})
	first := log.add(session, identity.AnchorSchema, anchor, 2000)
	// The same subject can copy the identical grant into another Gitseq record.
	log.add(session, identity.AnchorSchema, anchor, 2500)
	revocation := identity.Revocation{Genesis: testGenesis, Anchor: first}
	proof := signNostrWithdrawal(t, root, revocation)
	revocation.Nostr = &proof
	log.add(submitter, identity.RevokeSchema, revocation, 3000)
	afterWithdrawal := log.act(session, 3000)
	if log.resolve().LookupAt(afterWithdrawal).Anchored {
		t.Fatal("root withdrawal left a pre-withdrawal replay in force")
	}

	// A compromised subject key cannot restore the grant by copying the same
	// root-signed proof into a record with a new Gitseq id.
	replayed := log.add(session, identity.AnchorSchema, anchor, 4000)
	if resolved := log.resolve().LookupAt(replayed); resolved.Anchored {
		t.Fatalf("exact proof replay restored withdrawn authority: %+v", resolved)
	}

	// Regranting requires a genuinely fresh event signed by the root.
	fresh := anchor
	fresh.Nostr = nil
	fresh = signNostrAnchorAt(t, root, fresh, anchor.Nostr.CreatedAt+1)
	freshRecord := log.add(session, identity.AnchorSchema, fresh, 5000)
	resolved := log.resolve().LookupAt(freshRecord)
	if !resolved.Anchored || resolved.Record != freshRecord {
		t.Fatalf("fresh root-signed grant did not take force: %+v", resolved)
	}
}

func TestNostrAnchorRequiresBothRootAndSubjectSignatures(t *testing.T) {
	initializer, _ := testKey(t)
	session, _ := testKey(t)
	other, _ := testKey(t)
	root := nostrKey(t)
	anchor := signNostrAnchor(t, root, identity.Anchor{
		Genesis: testGenesis, Subject: fingerprint(session), Scope: "play", NotAfter: 5000,
	})

	tests := map[string]func(*logBuilder, identity.Anchor){
		"wrong gitseq signer": func(log *logBuilder, anchor identity.Anchor) {
			log.add(other, identity.AnchorSchema, anchor, 2000)
		},
		"changed repository": func(log *logBuilder, anchor identity.Anchor) {
			anchor.Genesis = strings.Repeat("0", 40)
			log.add(session, identity.AnchorSchema, anchor, 2000)
		},
		"changed scope": func(log *logBuilder, anchor identity.Anchor) {
			anchor.Scope = "admin"
			log.add(session, identity.AnchorSchema, anchor, 2000)
		},
		"changed expiry": func(log *logBuilder, anchor identity.Anchor) {
			anchor.NotAfter++
			log.add(session, identity.AnchorSchema, anchor, 2000)
		},
		"changed event content": func(log *logBuilder, anchor identity.Anchor) {
			anchor.Nostr.Content += "x"
			log.add(session, identity.AnchorSchema, anchor, 2000)
		},
		"changed event id": func(log *logBuilder, anchor identity.Anchor) {
			anchor.Nostr.ID = strings.Repeat("0", 64)
			log.add(session, identity.AnchorSchema, anchor, 2000)
		},
		"changed event public key": func(log *logBuilder, anchor identity.Anchor) {
			anchor.Nostr.PubKey = hex.EncodeToString(schnorr.SerializePubKey(nostrKey(t).PubKey()))
			log.add(session, identity.AnchorSchema, anchor, 2000)
		},
		"changed event signature": func(log *logBuilder, anchor identity.Anchor) {
			anchor.Nostr.Sig = strings.Repeat("0", 128)
			log.add(session, identity.AnchorSchema, anchor, 2000)
		},
		"changed event kind": func(log *logBuilder, anchor identity.Anchor) {
			anchor.Nostr.Kind++
			log.add(session, identity.AnchorSchema, anchor, 2000)
		},
		"added event tag": func(log *logBuilder, anchor identity.Anchor) {
			anchor.Nostr.Tags = [][]string{{"p", strings.Repeat("0", 64)}}
			log.add(session, identity.AnchorSchema, anchor, 2000)
		},
		"changed event time": func(log *logBuilder, anchor identity.Anchor) {
			anchor.Nostr.CreatedAt++
			log.add(session, identity.AnchorSchema, anchor, 2000)
		},
		"uppercase public key": func(log *logBuilder, anchor identity.Anchor) {
			anchor.Nostr.PubKey = strings.ToUpper(anchor.Nostr.PubKey)
			log.add(session, identity.AnchorSchema, anchor, 2000)
		},
		"short public key": func(log *logBuilder, anchor identity.Anchor) {
			anchor.Nostr.PubKey = "00"
			log.add(session, identity.AnchorSchema, anchor, 2000)
		},
		"uppercase signature": func(log *logBuilder, anchor identity.Anchor) {
			anchor.Nostr.Sig = strings.ToUpper(anchor.Nostr.Sig)
			log.add(session, identity.AnchorSchema, anchor, 2000)
		},
		"second stated identity": func(log *logBuilder, anchor identity.Anchor) {
			claimed := identity.Identity{Scheme: identity.GitHubScheme, Subject: "1"}
			anchor.Identity = &claimed
			log.add(session, identity.AnchorSchema, anchor, 2000)
		},
		"live verification claim": func(log *logBuilder, anchor identity.Anchor) {
			anchor.Verification = "live-lookup"
			log.add(session, identity.AnchorSchema, anchor, 2000)
		},
	}
	for name, write := range tests {
		t.Run(name, func(t *testing.T) {
			log := newLog(t, initializer)
			copy := anchor
			proof := *anchor.Nostr
			copy.Nostr = &proof
			write(log, copy)
			probe := log.act(session, 3000)
			if resolved := log.resolve().LookupAt(probe); resolved.Anchored {
				t.Fatalf("invalid Nostr anchor took force: %+v", resolved)
			}
		})
	}
}

func TestNostrAnchorAndDelegatedAgentWorkThroughPublicHost(t *testing.T) {
	ctx := context.Background()
	workspace, _ := testWorkspace(t, ctx)
	_, person := testKey(t)
	joined, err := workspace.Append(ctx, person, host.Act{Schema: "chess/join@0", Payload: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	log, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	root := nostrKey(t)
	anchor := signNostrAnchor(t, root, identity.Anchor{
		Genesis: log.Genesis, Subject: joined.Actor, Scope: "play",
	})
	anchored, err := identity.Endorse(ctx, workspace, person, anchor)
	if err != nil {
		t.Fatal(err)
	}

	_, agent := testKey(t)
	agentRecord, err := workspace.Append(ctx, agent, host.Act{Schema: "chess/join@0", Payload: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = identity.Endorse(ctx, workspace, person, identity.Anchor{
		Subject: agentRecord.Actor, Scope: "move",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolution := resolveWorkspace(t, ctx, workspace)
	personResolved := resolution.LookupAt(anchored.ID)
	agentAct, err := workspace.Append(ctx, agent, host.Act{Schema: "chess/move@0", Payload: []byte("e4")})
	if err != nil {
		t.Fatal(err)
	}
	resolution = resolveWorkspace(t, ctx, workspace)
	agentResolved := resolution.LookupAt(agentAct.ID)
	if personResolved.Vouching != identity.SelfSigned || agentResolved.Vouching != identity.SelfSigned {
		t.Fatalf("vouching = person %v, agent %v; want self-signed inheritance", personResolved.Vouching, agentResolved.Vouching)
	}
	if agentResolved.Identity != personResolved.Identity || agentResolved.Verification != identity.InLog {
		t.Fatalf("agent = %+v, want inherited identity %+v and in-log verification", agentResolved, personResolved.Identity)
	}

	_, submitter := testKey(t)
	log, err = workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	proof := signNostrWithdrawal(t, root, identity.Revocation{Genesis: log.Genesis, Anchor: anchored.ID})
	if _, err := identity.RevokeNostr(
		ctx, workspace, submitter, anchored.ID+"x", proof,
	); err == nil || !strings.Contains(err.Error(), "signature is invalid") {
		t.Fatalf("RevokeNostr mutation error = %v, want invalid-signature refusal", err)
	}
	_, err = identity.RevokeNostr(ctx, workspace, submitter, anchored.ID, proof)
	if err != nil {
		t.Fatal(err)
	}
	personAfterWithdrawal, err := workspace.Append(ctx, person, host.Act{Schema: "chess/move@0", Payload: []byte("e5")})
	if err != nil {
		t.Fatal(err)
	}
	agentAfterWithdrawal, err := workspace.Append(ctx, agent, host.Act{Schema: "chess/move@0", Payload: []byte("e5")})
	if err != nil {
		t.Fatal(err)
	}
	resolution = resolveWorkspace(t, ctx, workspace)
	if resolution.LookupAt(personAfterWithdrawal.ID).Anchored || resolution.LookupAt(agentAfterWithdrawal.ID).Anchored {
		t.Fatal("Nostr root withdrawal left the session or its delegated agent anchored")
	}
	replayed, err := identity.Endorse(ctx, workspace, person, anchor)
	if err != nil {
		t.Fatal(err)
	}
	resolution = resolveWorkspace(t, ctx, workspace)
	if resolution.LookupAt(replayed.ID).Anchored {
		t.Fatal("re-appending the exact Nostr grant restored withdrawn authority")
	}

	fresh := anchor
	fresh.Nostr = nil
	fresh = signNostrAnchorAt(t, root, fresh, anchor.Nostr.CreatedAt+1)
	regranted, err := identity.Endorse(ctx, workspace, person, fresh)
	if err != nil {
		t.Fatal(err)
	}
	resolution = resolveWorkspace(t, ctx, workspace)
	if !resolution.LookupAt(regranted.ID).Anchored {
		t.Fatal("a fresh Nostr event could not regrant authority")
	}
}

func TestEndorseRefusesAnInvalidNostrProofBeforeAppend(t *testing.T) {
	ctx := context.Background()
	workspace, _ := testWorkspace(t, ctx)
	sessionPublic, session := testKey(t)
	log, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	anchor := signNostrAnchor(t, nostrKey(t), identity.Anchor{
		Genesis: log.Genesis, Subject: fingerprint(sessionPublic), Scope: "play",
	})
	anchor.Scope = "changed-after-signing"
	if _, err := identity.Endorse(ctx, workspace, session, anchor); err == nil || !strings.Contains(err.Error(), "signature is invalid") {
		t.Fatalf("Endorse error = %v, want invalid-signature refusal", err)
	}
}
