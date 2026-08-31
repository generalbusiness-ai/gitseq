package jsonataddl

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/host"
)

const testSessionCredential = "inventory-test-session"

func TestInventoryUIBrowserFlowAcceptsRawUnsortedPayload(t *testing.T) {
	ui, server := testInventoryUI(t)
	defer ui.Close()
	defer server.Close()

	metadata := getMetadata(t, server.URL)
	if fields := metadata.Events["stock_received"].Fields; !reflect.DeepEqual(fields, []string{"id", "sku", "qty"}) {
		t.Fatalf("discovered stock_received fields = %#v", fields)
	}
	if table := metadata.Tables["stock"]; len(table.Columns) != 2 || !reflect.DeepEqual(table.PrimaryKey, []string{"sku"}) {
		t.Fatalf("discovered stock schema = %#v", table)
	}

	waitResult := make(chan waitResponse, 1)
	go func() {
		response, err := http.Get(server.URL + "/api/wait?after=" + metadata.Frontier.VerifiedHead + "&timeout_ms=2000")
		waitResult <- waitResponse{response: response, err: err}
	}()
	select {
	case result := <-waitResult:
		if result.response != nil {
			result.response.Body.Close()
		}
		t.Fatalf("frontier wait returned before an advance: %v", result.err)
	case <-time.After(40 * time.Millisecond):
	}

	response := postRawJSON(t, server.URL+"/api/events", `{"event_type":"stock_received","payload":{"id":"stock-1","sku":"ink","qty":5},"idempotency_key":"ui-stock-1"}`, testSessionCredential)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("submit status = %d", response.StatusCode)
	}
	var submitted eventResponse
	if err := json.NewDecoder(response.Body).Decode(&submitted); err != nil {
		t.Fatal(err)
	}
	if submitted.Decision != "effective" || submitted.Frontier.VerifiedHead == metadata.Frontier.VerifiedHead {
		t.Fatalf("submission did not advance an effective frontier: %#v", submitted)
	}

	select {
	case result := <-waitResult:
		if result.err != nil {
			t.Fatal(result.err)
		}
		defer result.response.Body.Close()
		if result.response.StatusCode != http.StatusOK {
			t.Fatalf("wait status = %d", result.response.StatusCode)
		}
		var observed Frontier
		if err := json.NewDecoder(result.response.Body).Decode(&observed); err != nil {
			t.Fatal(err)
		}
		if !sameFrontier(observed, submitted.Frontier) {
			t.Fatalf("wait observed %#v, submitted frontier %#v", observed, submitted.Frontier)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("frontier wait missed the projection advance")
	}

	query := postJSON(t, server.URL+"/api/query", queryRequest{
		SQL: "SELECT sku, available FROM stock ORDER BY sku", ExpectedFrontier: &submitted.Frontier,
	}, "")
	defer query.Body.Close()
	if query.StatusCode != http.StatusOK {
		t.Fatalf("query status = %d", query.StatusCode)
	}
	var result QueryResult
	if err := json.NewDecoder(query.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Rows, [][]any{{"ink", float64(5)}}) {
		t.Fatalf("re-run query rows = %#v", result.Rows)
	}
}

func TestInventoryUIFrontierWaitTimesOutWithoutAdvance(t *testing.T) {
	ui, server := testInventoryUI(t)
	defer ui.Close()
	defer server.Close()
	frontier := getMetadata(t, server.URL).Frontier

	started := time.Now()
	response, err := http.Get(server.URL + "/api/wait?after=" + frontier.VerifiedHead + "&timeout_ms=25")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent || response.Header.Get("Gitseq-Frontier") != frontier.VerifiedHead {
		t.Fatalf("timeout response = %d, frontier %q", response.StatusCode, response.Header.Get("Gitseq-Frontier"))
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond || elapsed > time.Second {
		t.Fatalf("bounded wait elapsed %v", elapsed)
	}
}

func TestInventoryUIRefusesCredentialAndStaleQueryFrontier(t *testing.T) {
	ui, server := testInventoryUI(t)
	defer ui.Close()
	defer server.Close()
	before := getMetadata(t, server.URL).Frontier

	unauthorized := postJSON(t, server.URL+"/api/events", map[string]any{
		"event_type": "stock_received", "payload": map[string]any{"id": "s", "sku": "ink", "qty": 1}, "idempotency_key": "unauthorized",
	}, "wrong-credential")
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized submit status = %d", unauthorized.StatusCode)
	}

	duplicate := postRawJSON(t, server.URL+"/api/events", `{"event_type":"stock_received","payload":{"id":"s","id":"other","sku":"ink","qty":1},"idempotency_key":"duplicate"}`, testSessionCredential)
	duplicate.Body.Close()
	if duplicate.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate-field submit status = %d", duplicate.StatusCode)
	}

	submitted := postJSON(t, server.URL+"/api/events", map[string]any{
		"event_type": "stock_received", "payload": map[string]any{"id": "s", "sku": "ink", "qty": 1}, "idempotency_key": "authorized",
	}, testSessionCredential)
	submitted.Body.Close()
	if submitted.StatusCode != http.StatusOK {
		t.Fatalf("authorized submit status = %d", submitted.StatusCode)
	}

	stale := postJSON(t, server.URL+"/api/query", queryRequest{SQL: "SELECT sku FROM stock", ExpectedFrontier: &before}, "")
	stale.Body.Close()
	if stale.StatusCode != http.StatusConflict {
		t.Fatalf("stale expected frontier status = %d", stale.StatusCode)
	}
}

func TestInventoryUIStaticPageDoesNotDiscloseCredential(t *testing.T) {
	ui, server := testInventoryUI(t)
	defer ui.Close()
	defer server.Close()
	response, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte(testSessionCredential)) {
		t.Fatal("static page disclosed the session credential")
	}
	if !strings.Contains(response.Header.Get("Content-Security-Policy"), "connect-src 'self'") {
		t.Fatalf("static page has no same-origin content security policy: %q", response.Header.Get("Content-Security-Policy"))
	}
}

type waitResponse struct {
	response *http.Response
	err      error
}

func testInventoryUI(t *testing.T) (*InventoryUI, *httptest.Server) {
	t.Helper()
	ctx := context.Background()
	repository := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.CommandContext(ctx, "git", "init", "-q", repository).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	_, signer, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := host.Init(ctx, repository, InventoryApplication, signer, host.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ui, err := NewInventoryUI(ctx, workspace, signer, filepath.Join(t.TempDir(), "projections"), testSessionCredential)
	if err != nil {
		t.Fatal(err)
	}
	return ui, httptest.NewServer(ui)
}

func getMetadata(t *testing.T, baseURL string) applicationMetadata {
	t.Helper()
	response, err := http.Get(baseURL + "/api/metadata")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("metadata status = %d", response.StatusCode)
	}
	var metadata applicationMetadata
	if err := json.NewDecoder(response.Body).Decode(&metadata); err != nil {
		t.Fatal(err)
	}
	return metadata
}

func postJSON(t *testing.T, url string, value any, credential string) *http.Response {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if credential != "" {
		request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", credential))
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func postRawJSON(t *testing.T, url, body, credential string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if credential != "" {
		request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", credential))
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
