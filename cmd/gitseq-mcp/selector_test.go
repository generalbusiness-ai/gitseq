package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestPerCallSelectorsChooseRepositoryAndAccessibleAgent(t *testing.T) {
	ctx := context.Background()
	first, _ := signedWorkspace(t, 1)
	second, genesis := signedWorkspace(t, 1)
	selected, _, err := second.AddActor(ctx, "human", "builder", "agent")
	if err != nil {
		t.Fatal(err)
	}

	server := newServer("human", first.Repo)
	value, acted, err := server.call(ctx, toolCall{Name: "whoami", Arguments: map[string]any{
		"repo": second.Repo, "agent": "builder",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if acted.workspace.CommonDir != second.CommonDir || acted.actor != "builder" {
		t.Fatalf("call selected %+v, want builder in %s", acted, second.CommonDir)
	}
	if got := value.(map[string]any)["actor"].(map[string]string)["fingerprint"]; got != selected.Fingerprint {
		t.Fatalf("whoami fingerprint = %s, want %s", got, selected.Fingerprint)
	}

	before, err := second.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	submitted, _, err := server.call(ctx, toolCall{Name: "state", Arguments: map[string]any{
		"repo": second.Repo, "agent": "builder", "kind": "assert",
		"text": "selected actor signs", "rests_on": []any{genesis.ID},
		"idempotency_key": "selected-actor-signs",
	}})
	if err != nil {
		t.Fatal(err)
	}
	record := submitted.(map[string]any)["record"].(workroom.Record)
	if record.Actor != selected.Fingerprint {
		t.Fatalf("record actor = %s, want selected actor %s", record.Actor, selected.Fingerprint)
	}
	after, err := second.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Depth != before.Depth+1 {
		t.Fatalf("selected repository depth = %d, want %d", after.Depth, before.Depth+1)
	}
	untouched, err := first.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if untouched.Depth != 1 {
		t.Fatalf("default repository changed to depth %d", untouched.Depth)
	}
}

func TestSelectedAgentDrivesDegradedStatusWorkAndWait(t *testing.T) {
	ctx := context.Background()
	workspace, genesis := signedWorkspace(t, 1)
	selected, _, err := workspace.AddActor(ctx, "human", "builder", "agent")
	if err != nil {
		t.Fatal(err)
	}
	request, err := workspace.Act(ctx, "human", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "builder work",
		Body:    map[string]string{"to": "builder", "conditions": "selected reads it"},
		RestsOn: []string{genesis.ID}, IdempotencyKey: "builder-work",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := newServer("human", workspace.Repo)

	statusValue, _, err := server.call(ctx, toolCall{Name: "status", Arguments: map[string]any{"agent": "builder"}})
	if err != nil {
		t.Fatal(err)
	}
	status := statusValue.(actorStatus)
	if status.You.Fingerprint != selected.Fingerprint || !status.Live.Degraded {
		t.Fatalf("selected degraded status = %+v", status)
	}

	workValue, _, err := server.call(ctx, toolCall{Name: "work", Arguments: map[string]any{"agent": "builder"}})
	if err != nil {
		t.Fatal(err)
	}
	page := workValue.(statusview.WorkPage)
	if page.Actor.Fingerprint != selected.Fingerprint || !page.Degraded {
		t.Fatalf("selected degraded work page = %+v", page)
	}

	waitValue, _, err := server.call(ctx, toolCall{Name: "wait", Arguments: map[string]any{
		"agent": "builder", "cursor": map[string]any{}, "timeout_ms": 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	delta := waitValue.(waitDelta)
	found := false
	for _, item := range delta.CurrentAvailableToYou {
		if item.Request == request.Record.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("selected degraded wait omitted builder request %s: %+v", request.Record.ID, delta.CurrentAvailableToYou)
	}
}

func TestResidentLeaseStateIsScopedByRepositoryAndAgent(t *testing.T) {
	ctx := context.Background()
	workspace, _ := signedWorkspace(t, 1)
	builder, _, err := workspace.AddActor(ctx, "human", "builder", "agent")
	if err != nil {
		t.Fatal(err)
	}
	residentService, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	resident := httptest.NewServer(residentService.Handler())
	defer resident.Close()

	server := newServer("human", workspace.Repo)
	server.client = residentclient.NewWithHTTP(resident.Client(), residentHTTPTimeout)
	humanRoom, err := server.attachAs(ctx, workspace.Repo, "human")
	if err != nil {
		t.Fatal(err)
	}
	builderRoom, err := server.attachAs(ctx, workspace.Repo, "builder")
	if err != nil {
		t.Fatal(err)
	}
	humanRoom.baseURL = resident.URL
	builderRoom.baseURL = resident.URL

	if _, _, err := server.call(ctx, toolCall{Name: "presence"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.call(ctx, toolCall{Name: "presence", Arguments: map[string]any{"agent": "builder"}}); err != nil {
		t.Fatal(err)
	}
	statusValue, _, err := server.call(ctx, toolCall{Name: "status", Arguments: map[string]any{"agent": "builder"}})
	if err != nil {
		t.Fatal(err)
	}
	status := statusValue.(actorStatus)
	if status.You.Fingerprint != builder.Fingerprint || status.Live.Degraded {
		t.Fatalf("resident selected status = %+v", status)
	}
	if _, _, err := server.call(ctx, toolCall{Name: "wait", Arguments: map[string]any{
		"agent": "builder", "cursor": status.Cursor, "timeout_ms": 1,
	}}); err != nil {
		t.Fatalf("resident selected wait leaked agent to strict decoder: %v", err)
	}
	if humanRoom == builderRoom {
		t.Fatal("two actors in one repository shared a room")
	}
	if humanRoom.credentialValue() == "" || builderRoom.credentialValue() == "" || humanRoom.credentialValue() == builderRoom.credentialValue() {
		t.Fatalf("resident credentials crossed actors: human=%q builder=%q", humanRoom.credentialValue(), builderRoom.credentialValue())
	}
	if !humanRoom.inboxAvailable() || !builderRoom.inboxAvailable() {
		t.Fatalf("actor-scoped inbox registration missing: human=%v builder=%v", humanRoom.inboxAvailable(), builderRoom.inboxAvailable())
	}
	if len(server.byCommonDir) != 2 {
		t.Fatalf("common-dir cache holds %d selections, want one per actor", len(server.byCommonDir))
	}
}

func TestSelectorsDoNotEnterResidentArgumentsOrAttentionEvents(t *testing.T) {
	const event = "git:sha1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa#git:sha1:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	arguments := residentArguments(map[string]any{"repo": event, "agent": event, "cursor": map[string]any{}})
	if _, held := arguments["repo"]; held {
		t.Fatalf("resident arguments retained repo: %+v", arguments)
	}
	if _, held := arguments["agent"]; held {
		t.Fatalf("resident arguments retained agent: %+v", arguments)
	}
	if events := attentionEvents(toolCall{Name: "status", Arguments: map[string]any{"repo": event, "agent": event}}, nil); len(events) != 0 {
		t.Fatalf("selector values became attention events: %v", events)
	}
}

func TestSupplyingOnlyRepoValidatesStartupActorInThatRepository(t *testing.T) {
	ctx := context.Background()
	defaultRepo, _ := signedWorkspace(t, 1)
	other, _ := signedWorkspace(t, 1)
	server := newServer("human", defaultRepo.Repo)

	key := other.View().Actors["human"].KeyFile
	held := key + ".held"
	if err := os.Rename(key, held); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(held, key) })
	_, acted, err := server.call(ctx, toolCall{Name: "whoami", Arguments: map[string]any{"repo": other.Repo}})
	if err == nil || !strings.Contains(err.Error(), "key is not accessible") {
		t.Fatalf("repo-only selector error = %v", err)
	}
	if acted != nil {
		t.Fatalf("refused repo-only selection exposed an acted room: %+v", acted)
	}
}

func TestSelectedAgentRefusesMissingMismatchedUnknownAndRetiredIdentity(t *testing.T) {
	ctx := context.Background()

	t.Run("missing key has no fallback or path disclosure", func(t *testing.T) {
		workspace, genesis := signedWorkspace(t, 1)
		actor, _, err := workspace.AddActor(ctx, "human", "builder", "agent")
		if err != nil {
			t.Fatal(err)
		}
		key := actor.KeyFile
		held := key + ".held"
		if err := os.Rename(key, held); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Rename(held, key) })
		before, err := workspace.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		server := newServer("human", workspace.Repo)
		_, acted, err := server.call(ctx, toolCall{Name: "state", Arguments: map[string]any{
			"agent": "builder", "kind": "assert", "text": "must refuse",
			"rests_on": []any{genesis.ID}, "idempotency_key": "missing-key",
		}})
		if err == nil || !strings.Contains(err.Error(), "key is not accessible") {
			t.Fatalf("missing-key error = %v", err)
		}
		if acted != nil {
			t.Fatalf("refused identity exposed an acted room: %+v", acted)
		}
		if strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "key_file") {
			t.Fatalf("selector refusal disclosed custody path: %v", err)
		}
		after, err := workspace.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if after.Depth != before.Depth {
			t.Fatalf("missing key fell back and appended: depth %d -> %d", before.Depth, after.Depth)
		}
	})

	t.Run("mismatched key", func(t *testing.T) {
		workspace, _ := signedWorkspace(t, 1)
		builder, _, err := workspace.AddActor(ctx, "human", "builder", "agent")
		if err != nil {
			t.Fatal(err)
		}
		other, _, err := workspace.AddActor(ctx, "human", "other", "agent")
		if err != nil {
			t.Fatal(err)
		}
		original, err := os.ReadFile(builder.KeyFile)
		if err != nil {
			t.Fatal(err)
		}
		replacement, err := os.ReadFile(other.KeyFile)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(builder.KeyFile, replacement, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.WriteFile(builder.KeyFile, original, 0o600) })
		server := newServer("human", workspace.Repo)
		_, acted, err := server.call(ctx, toolCall{Name: "whoami", Arguments: map[string]any{"agent": "builder"}})
		if err == nil || !strings.Contains(err.Error(), "does not match configured fingerprint") {
			t.Fatalf("mismatched-key error = %v", err)
		}
		if acted != nil {
			t.Fatalf("mismatched identity exposed an acted room: %+v", acted)
		}
	})

	t.Run("custody without roster", func(t *testing.T) {
		workspace, _ := signedWorkspace(t, 1)
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		key := filepath.Join(workspace.MetaDir, "actors", "orphan.key")
		if err := os.WriteFile(key, []byte(base64.RawURLEncoding.EncodeToString(private)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = apphost.UpdateConfig(workspace.MetaDir, apphost.Config{}, func(config *apphost.Config) (bool, error) {
			config.Actors["orphan"] = apphost.Actor{Name: "orphan", Fingerprint: intent.ActorFingerprint(public), KeyFile: key}
			return true, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		server := newServer("human", workspace.Repo)
		_, acted, err := server.call(ctx, toolCall{Name: "whoami", Arguments: map[string]any{"agent": "orphan"}})
		if err == nil || !strings.Contains(err.Error(), "not a live participant") {
			t.Fatalf("unknown-roster error = %v", err)
		}
		if acted != nil {
			t.Fatalf("unknown roster identity exposed an acted room: %+v", acted)
		}
	})

	t.Run("retired roster actor", func(t *testing.T) {
		workspace, _ := signedWorkspace(t, 1)
		_, records, err := workspace.AddActor(ctx, "human", "builder", "agent")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := workspace.Act(ctx, "human", app.Act{
			Verb: app.VerbSupersede, Target: records[0].ID, Text: "retire builder",
			RestsOn: []string{records[0].ID}, IdempotencyKey: "retire-builder",
		}); err != nil {
			t.Fatal(err)
		}
		server := newServer("human", workspace.Repo)
		_, acted, err := server.call(ctx, toolCall{Name: "whoami", Arguments: map[string]any{"agent": "builder"}})
		if err == nil || !strings.Contains(err.Error(), "not a live participant") {
			t.Fatalf("retired-actor error = %v", err)
		}
		if acted != nil {
			t.Fatalf("retired identity exposed an acted room: %+v", acted)
		}
	})
}

func TestMalformedSelectorsRefuseFallbackAndOmittedSelectorsKeepDefaults(t *testing.T) {
	ctx := context.Background()
	workspace, _ := signedWorkspace(t, 1)
	server := newServer("human", workspace.Repo)
	value, _, err := server.call(ctx, toolCall{Name: "whoami"})
	if err != nil {
		t.Fatal(err)
	}
	want := workspace.View().Actors["human"].Fingerprint
	if got := value.(map[string]any)["actor"].(map[string]string)["fingerprint"]; got != want {
		t.Fatalf("default actor = %s, want %s", got, want)
	}
	for _, arguments := range []map[string]any{{"agent": ""}, {"agent": 7}, {"repo": ""}, {"repo": 7}} {
		_, acted, err := server.call(ctx, toolCall{Name: "whoami", Arguments: arguments})
		if err == nil || !strings.Contains(err.Error(), "refusing to fall back") {
			t.Fatalf("selector %#v error = %v", arguments, err)
		}
		if acted != nil {
			t.Fatalf("malformed selector %#v exposed room %+v", arguments, acted)
		}
	}
	_, _, err = server.call(ctx, toolCall{Name: "whoami", Arguments: map[string]any{"repo": 7, "agent": 7}})
	if err == nil || !strings.HasPrefix(err.Error(), "repo must name") {
		t.Fatalf("paired malformed selectors did not refuse repo first: %v", err)
	}
}

func TestAliasOnlySelectorKeepsCustodyLookupAndSigningBoundToTheMapKey(t *testing.T) {
	ctx := context.Background()
	workspace, genesis := signedWorkspace(t, 1)
	human := workspace.View().Actors["human"]
	if _, err := apphost.UpdateConfig(workspace.MetaDir, apphost.Config{}, func(config *apphost.Config) (bool, error) {
		delete(config.Actors, "human")
		config.Actors["operator-alias"] = human
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}

	server := newServer("operator-alias", workspace.Repo)
	value, acted, err := server.call(ctx, toolCall{Name: "whoami"})
	if err != nil {
		t.Fatal(err)
	}
	if acted.actor != "operator-alias" || acted.fingerprint != human.Fingerprint {
		t.Fatalf("alias-only selection attached %+v", acted)
	}
	if got := value.(map[string]any)["actor"].(map[string]string)["fingerprint"]; got != human.Fingerprint {
		t.Fatalf("alias-only whoami fingerprint = %s, want %s", got, human.Fingerprint)
	}
	result, _, err := server.call(ctx, toolCall{Name: "state", Arguments: map[string]any{
		"kind": "assert", "text": "alias signs with held custody", "rests_on": []any{genesis.ID},
		"idempotency_key": "alias-only-selector-signs",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if record := result.(map[string]any)["record"].(workroom.Record); record.Actor != human.Fingerprint {
		t.Fatalf("alias-only record actor = %s, want %s", record.Actor, human.Fingerprint)
	}
}

func TestSelectorRotationForwardAndBackNeverReusesLeaseState(t *testing.T) {
	ctx := context.Background()
	workspace, _ := signedWorkspace(t, 1)
	builder, _, err := workspace.AddActor(ctx, "human", "builder", "agent")
	if err != nil {
		t.Fatal(err)
	}
	human := workspace.View().Actors["human"]
	server := newServer("builder", workspace.Repo)

	first, err := server.attachAs(ctx, workspace.Repo, "builder")
	if err != nil {
		t.Fatal(err)
	}
	first.setCredential("credential:first")
	first.joined()
	first.setInboxAvailable(true)

	remap := func(actor apphost.Actor) {
		t.Helper()
		if _, err := apphost.UpdateConfig(workspace.MetaDir, apphost.Config{}, func(config *apphost.Config) (bool, error) {
			config.Actors["builder"] = actor
			return true, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	remap(human)
	second, err := server.attachAs(ctx, workspace.Repo, "builder")
	if err != nil {
		t.Fatal(err)
	}
	if second == first || second.fingerprint != human.Fingerprint {
		t.Fatalf("forward rotation reused old room: first=%p second=%p fp=%s", first, second, second.fingerprint)
	}
	if first.credentialValue() != "" || first.present() || first.inboxAvailable() {
		t.Fatalf("forward rotation retained old lease state: credential=%q present=%v inbox=%v", first.credentialValue(), first.present(), first.inboxAvailable())
	}
	second.setCredential("credential:second")
	second.joined()
	second.setInboxAvailable(true)

	remap(builder)
	third, err := server.attachAs(ctx, workspace.Repo, "builder")
	if err != nil {
		t.Fatal(err)
	}
	if third != first || third.fingerprint != builder.Fingerprint {
		t.Fatalf("reverse rotation did not return fingerprint-scoped room: first=%p third=%p fp=%s", first, third, third.fingerprint)
	}
	if third.credentialValue() != "" || third.present() || third.inboxAvailable() {
		t.Fatalf("reverse rotation reused first-generation lease state: credential=%q present=%v inbox=%v", third.credentialValue(), third.present(), third.inboxAvailable())
	}
	if second.credentialValue() != "" || second.present() || second.inboxAvailable() {
		t.Fatalf("reverse rotation retained second-generation lease state: credential=%q present=%v inbox=%v", second.credentialValue(), second.present(), second.inboxAvailable())
	}
}

func TestRetargetedSelectorPathDoesNotReuseRoomState(t *testing.T) {
	ctx := context.Background()
	workspace, _ := signedWorkspace(t, 1)
	selector := filepath.Join(t.TempDir(), "selected-repo")
	if err := os.Symlink(workspace.Repo, selector); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	server := newServer("human", workspace.Repo)
	_, first, err := server.call(ctx, toolCall{Name: "whoami", Arguments: map[string]any{"repo": selector}})
	if err != nil {
		t.Fatal(err)
	}
	first.setCredential("credential:first-target")
	first.joined()
	first.setInboxAvailable(true)
	first.markSharedIdentityNoticeChecked()
	first.mu.Lock()
	first.baseURL = "http://old-resident.invalid"
	first.mu.Unlock()

	moved := workspace.Repo + "-moved"
	oldRepo := workspace.Repo
	oldMeta := workspace.MetaDir
	if err := os.Rename(oldRepo, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(selector); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, selector); err != nil {
		t.Fatal(err)
	}
	newMeta := filepath.Join(moved, ".git", "gitseq")
	relocate := func(path string) string {
		relative, err := filepath.Rel(oldMeta, path)
		if err != nil || strings.HasPrefix(relative, "..") {
			return path
		}
		return filepath.Join(newMeta, relative)
	}
	if _, err := apphost.UpdateConfig(newMeta, apphost.Config{}, func(config *apphost.Config) (bool, error) {
		config.SequencerKey = relocate(config.SequencerKey)
		for name, actor := range config.Actors {
			actor.KeyFile = relocate(actor.KeyFile)
			config.Actors[name] = actor
		}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}

	value, second, err := server.call(ctx, toolCall{Name: "whoami", Arguments: map[string]any{"repo": selector}})
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("retargeted selector path reused its old room")
	}
	if second.workspace.CommonDir == first.workspace.CommonDir {
		t.Fatalf("retargeted selector did not change common directory: %s", second.workspace.CommonDir)
	}
	if second.fingerprint != first.fingerprint || second.workspace.View().Genesis != first.workspace.View().Genesis {
		t.Fatalf("test did not preserve actor/genesis across retarget: first=(%s,%s) second=(%s,%s)", first.fingerprint, first.workspace.View().Genesis, second.fingerprint, second.workspace.View().Genesis)
	}
	if got := value.(map[string]any)["repo"]; got != second.workspace.CommonDir {
		t.Fatalf("retargeted whoami reported repo %v, want %s", got, second.workspace.CommonDir)
	}
	if second.credentialValue() != "" || second.present() || second.inboxAvailable() || second.sharedIdentityNoticeChecked() {
		t.Fatalf("new target inherited old session state: credential=%q present=%v inbox=%v warning=%v", second.credentialValue(), second.present(), second.inboxAvailable(), second.sharedIdentityNoticeChecked())
	}
	second.mu.Lock()
	baseURL := second.baseURL
	second.mu.Unlock()
	if baseURL != "" {
		t.Fatalf("new target inherited old resident %q", baseURL)
	}
	if first.credentialValue() != "" || first.present() || first.inboxAvailable() {
		t.Fatalf("old target retained reusable lease state: credential=%q present=%v inbox=%v", first.credentialValue(), first.present(), first.inboxAvailable())
	}
}

func TestPersistentInvalidCredentialVerificationFailsOnceWithoutRecursion(t *testing.T) {
	ctx := context.Background()
	workspace, _ := signedWorkspace(t, 1)
	residentService, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	var presenceCalls atomic.Int64
	var statusCalls atomic.Int64
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v0/presence":
			presenceCalls.Add(1)
			_ = json.NewEncoder(writer).Encode(map[string]any{"credential": "credential:untrusted", "change": map[string]any{}})
		case request.Method == http.MethodPost && request.URL.Path == "/v0/actor-status":
			statusCalls.Add(1)
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"error":"credential is not valid"}`))
		case request.Method == http.MethodPost && request.URL.Path == "/v0/presence/depart":
			_ = json.NewEncoder(writer).Encode(map[string]any{})
		default:
			residentService.Handler().ServeHTTP(writer, request)
		}
	}))
	defer httpServer.Close()
	if _, err := workspace.PublishResident(httpServer.URL); err != nil {
		t.Fatal(err)
	}
	server := newServer("human", workspace.Repo)
	server.client = residentclient.NewWithHTTP(httpServer.Client(), residentHTTPTimeout)
	current, err := server.attachAs(ctx, workspace.Repo, "human")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.announce(ctx, current); err == nil {
		t.Fatal("persistent invalid credential verification succeeded")
	}
	if presenceCalls.Load() != 1 || statusCalls.Load() != 1 {
		t.Fatalf("credential establishment recursed: presence=%d status=%d", presenceCalls.Load(), statusCalls.Load())
	}
	if current.credentialValue() != "" || current.present() || current.inboxAvailable() {
		t.Fatalf("failed verification retained session state: credential=%q present=%v inbox=%v", current.credentialValue(), current.present(), current.inboxAvailable())
	}
}

func TestInvalidStartupDefaultsNeverPreAttend(t *testing.T) {
	ctx := context.Background()

	assertRefused := func(t *testing.T, workspace *app.Workspace, actor string) {
		t.Helper()
		residentService, err := service.New(workspace)
		if err != nil {
			t.Fatal(err)
		}
		var presenceCalls atomic.Int64
		httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method == http.MethodPost && request.URL.Path == "/v0/presence" {
				presenceCalls.Add(1)
			}
			residentService.Handler().ServeHTTP(writer, request)
		}))
		defer httpServer.Close()
		if _, err := workspace.PublishResident(httpServer.URL); err != nil {
			t.Fatal(err)
		}
		server := newServer(actor, workspace.Repo)
		server.client = residentclient.NewWithHTTP(httpServer.Client(), residentHTTPTimeout)
		if current, err := server.attend(ctx, ""); err == nil || current != nil {
			t.Fatalf("invalid startup identity attended: current=%+v err=%v", current, err)
		}
		if presenceCalls.Load() != 0 {
			t.Fatalf("invalid startup identity made %d presence calls", presenceCalls.Load())
		}
		if len(server.byPath) != 0 || len(server.byCommonDir) != 0 {
			t.Fatalf("invalid startup identity seeded room caches: paths=%d common=%d", len(server.byPath), len(server.byCommonDir))
		}
	}

	t.Run("unknown", func(t *testing.T) {
		workspace, _ := signedWorkspace(t, 1)
		assertRefused(t, workspace, "unknown")
	})
	t.Run("retired", func(t *testing.T) {
		workspace, _ := signedWorkspace(t, 1)
		_, records, err := workspace.AddActor(ctx, "human", "builder", "agent")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := workspace.Act(ctx, "human", app.Act{
			Verb: app.VerbSupersede, Target: records[0].ID, Text: "retire builder",
			RestsOn: []string{records[0].ID}, IdempotencyKey: "retire-startup-builder",
		}); err != nil {
			t.Fatal(err)
		}
		assertRefused(t, workspace, "builder")
	})
	t.Run("mismatched key", func(t *testing.T) {
		workspace, _ := signedWorkspace(t, 1)
		builder, _, err := workspace.AddActor(ctx, "human", "builder", "agent")
		if err != nil {
			t.Fatal(err)
		}
		other, _, err := workspace.AddActor(ctx, "human", "other", "agent")
		if err != nil {
			t.Fatal(err)
		}
		original, err := os.ReadFile(builder.KeyFile)
		if err != nil {
			t.Fatal(err)
		}
		replacement, err := os.ReadFile(other.KeyFile)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(builder.KeyFile, replacement, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.WriteFile(builder.KeyFile, original, 0o600) })
		assertRefused(t, workspace, "builder")
	})
}
