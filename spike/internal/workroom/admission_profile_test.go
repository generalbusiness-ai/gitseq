package workroom

import (
	"strings"
	"testing"
)

const testGenesis = "5d2622748872b7e2dec3fe5c59e4be73a35e0bc8"

func admissionProfileHistory(t testing.TB) []Record {
	t.Helper()
	return []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Operator", "role": "operator"}}),
		event(t, "old", operator, SchemaState, State{Kind: KindAdmissionProfile, Text: "old profile", Body: map[string]string{"bundle": "bundle-old", "contract": "contract-old", "genesis": testGenesis}}, "e0"),
		event(t, "old-ratified", operator, SchemaRatify, Ratify{Target: "old"}, "old"),
		event(t, "new", operator, SchemaState, State{Kind: KindAdmissionProfile, Text: "new profile", Body: map[string]string{"bundle": "bundle-new", "contract": "contract-new", "genesis": testGenesis}}, "e0"),
		event(t, "new-ratified", operator, SchemaRatify, Ratify{Target: "new"}, "new"),
		event(t, "new-retired", operator, SchemaSupersede, Supersede{Target: "new", Text: "return to the prior profile"}, "new"),
	}
}

func TestAdmissionProfileRequiresGovernanceFields(t *testing.T) {
	for _, missing := range []string{"bundle", "contract", "genesis"} {
		body := map[string]string{"bundle": "bundle", "contract": "contract", "genesis": testGenesis}
		delete(body, missing)
		_, err := Encode(State{Kind: KindAdmissionProfile, Text: "profile", Body: body})
		if err == nil || !strings.Contains(err.Error(), "body."+missing) {
			t.Fatalf("missing %s: %v", missing, err)
		}
	}
}

func TestAdmissionProfileIsRatifiableGovernance(t *testing.T) {
	records := admissionProfileHistory(t)[:3]
	projection := Fold(records)
	decision, ok := projection.Decision("old-ratified")
	if !ok || decision.Verdict != Effective || decision.Reason != "authorized ratification" {
		t.Fatalf("ratification = %+v, found=%v", decision, ok)
	}
	if _, opaque := projection.OpaqueKinds[string(KindAdmissionProfile)]; opaque {
		t.Fatalf("admission profile projected as opaque: %+v", projection.OpaqueKinds)
	}
}

func TestAdmissionProfileSelectionAtPrefixesAndRetirementFallback(t *testing.T) {
	records := admissionProfileHistory(t)
	tests := []struct {
		name      string
		prefix    int
		bundle    string
		contract  string
		bootstrap bool
	}{
		{name: "before ratification", prefix: 2, bundle: "6ad9570a5b4f824304c855a25511001e126f6a3c", contract: GenesisAdmissionContract, bootstrap: true},
		{name: "first activation", prefix: 3, bundle: "bundle-old", contract: "contract-old"},
		{name: "last activation", prefix: 5, bundle: "bundle-new", contract: "contract-new"},
		{name: "retirement restores predecessor", prefix: 6, bundle: "bundle-old", contract: "contract-old"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile, err := SelectAdmissionProfile(records[:test.prefix], testGenesis)
			if err != nil {
				t.Fatal(err)
			}
			if profile.Bundle != test.bundle || profile.Contract != test.contract || profile.Bootstrap != test.bootstrap {
				t.Fatalf("profile = %+v", profile)
			}
		})
	}
}

func TestAdmissionProfileStalenessDoesNotDeselect(t *testing.T) {
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Operator", "role": "operator"}}),
		event(t, "basis", operator, SchemaState, State{Kind: KindAssert, Text: "basis"}, "e0"),
		event(t, "profile", operator, SchemaState, State{Kind: KindAdmissionProfile, Text: "profile", Body: map[string]string{"bundle": "bundle", "contract": "contract", "genesis": testGenesis}}, "basis"),
		event(t, "profile-ratified", operator, SchemaRatify, Ratify{Target: "profile"}, "profile"),
		event(t, "basis-retired", operator, SchemaSupersede, Supersede{Target: "basis", Text: "basis changed"}, "basis"),
	}
	projection := Fold(records)
	for _, statement := range projection.Statements {
		if statement.Event == "profile" && !statement.Stale {
			t.Fatal("profile did not project stale")
		}
	}
	profile, err := SelectAdmissionProfile(records, testGenesis)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Event != "profile" || profile.Bundle != "bundle" {
		t.Fatalf("stale activation was deselected: %+v", profile)
	}
}

func TestAdmissionProfileUnavailableIsTypedAndDoesNotFallback(t *testing.T) {
	records := admissionProfileHistory(t)[:3]
	resolution, err := ResolveAdmissionProfile(records, testGenesis, func(contract string) bool {
		return contract == GenesisAdmissionContract
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != AdmissionProfileUnavailable {
		t.Fatalf("status = %q", resolution.Status)
	}
	if resolution.Profile.Contract != "contract-old" || resolution.Profile.Bootstrap {
		t.Fatalf("resolver fell back from unavailable activation: %+v", resolution.Profile)
	}

	bootstrap, err := ResolveAdmissionProfile(records[:2], testGenesis, func(contract string) bool {
		return contract == GenesisAdmissionContract
	})
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap.Status != AdmissionProfileAvailable || !bootstrap.Profile.Bootstrap {
		t.Fatalf("bootstrap resolution = %+v", bootstrap)
	}
}
