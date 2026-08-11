package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func proposal() Proposal {
	return Proposal{
		Issue:    Issue{Owner: "generalbusiness-ai", Repo: "gitseq", Number: 1, Title: "a filed issue"},
		Request:  "git:sha1:g#git:sha1:request",
		Artifact: "git:sha1:g#git:sha1:artifact",
		Commit:   "6ca1266b21306cb96726d345eac9021a91488fe7",
		Branch:   "request/fix-one",
		Base:     "main",
		Title:    "Fix the filed issue",
	}
}

// The rendering carries identifiers rather than a summary, because a reader on
// GitHub has to be able to find the durable record, and a summary would be a
// second copy of it that can disagree with the first.
func TestTheRenderingPointsAtTheDurableRecord(t *testing.T) {
	body := Render(proposal()).Body
	for _, want := range []string{
		"generalbusiness-ai/gitseq#1",
		"git:sha1:g#git:sha1:request",
		"git:sha1:g#git:sha1:artifact",
		"6ca1266b21306cb96726d345eac9021a91488fe7",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the rendering omits %q, so a reader cannot find the record", want)
		}
	}
	// Authority is stated rather than implied, because a pull request looks like
	// the place a decision is made and here it is not.
	if !strings.Contains(body, "gs merge") {
		t.Error("the rendering does not say where the merge decision actually lives")
	}
}

// The connector must never ingest its own writing. A connector that reads back
// what it renders observes its own observation, and the two halves feed each
// other for ever.
func TestRenderingIsNotObservedBackAsWork(t *testing.T) {
	rendered := Render(proposal())
	if !Owned(rendered.Body) {
		t.Fatal("the connector does not recognize its own rendering")
	}

	// The inverse matters as much: an ordinary body is not mistaken for ours,
	// or the connector would silently ignore what people actually wrote.
	if Owned("I hit this too") {
		t.Error("an ordinary body was treated as the connector's own")
	}
	// Leading whitespace is not a way past the check, and naming the marker
	// mid-sentence is not a way to be mistaken for it.
	if !Owned("\n  " + Marker + "\nrendered") {
		t.Error("leading whitespace defeated ownership")
	}
	if Owned("why does it emit " + Marker + "?") {
		t.Error("quoting the marker made an ordinary body look owned")
	}
}

// End to end for the loop that matters: render, feed the rendering back through
// the inbound half as though GitHub had returned it, and show that nothing new
// is admitted. This is the property the two-operation split exists to give.
func TestRenderThenReObserveProducesNoNewAct(t *testing.T) {
	rendered := Render(proposal())

	// GitHub returns the connector's own pull request among the issues, which
	// is what would happen on a repository the connector has written to. It is
	// excluded because it is a pull request — the one defence that does not
	// depend on what the body says. The marker is not consulted here and this
	// test does not rely on it; see Marker for why filtering on it would let an
	// author hide an issue by typing it.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		payload := []map[string]any{{
			"number": 2, "title": rendered.Title, "body": rendered.Body,
			"user": map[string]string{"login": "gitseq-bot"}, "state": "open",
			"pull_request": map[string]string{"url": "p"},
		}}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTP: server.Client()}
	issues, err := client.List(context.Background(), "generalbusiness-ai", "gitseq", (Clause{State: "all"}).Query())
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("the connector read back %d of its own renderings as issues", len(issues))
	}

	observations, _, err := Fetch(context.Background(), client, "generalbusiness-ai", "gitseq",
		[]Clause{{Event: "git:sha1:g#git:sha1:clause", State: "all"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 0 {
		t.Fatalf("re-observing after rendering produced %d acts, want none", len(observations))
	}
}

// Opening a pull request is a discrete side effect, so its failure has to be a
// failure. A silent one would leave a promise looking kept.
func TestOpenReportsWhatItCreated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":7,"html_url":"https://github.com/o/r/pull/7"}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTP: server.Client()}
	delivery, err := client.Open(context.Background(), Render(proposal()))
	if err != nil {
		t.Fatal(err)
	}
	if delivery.Number != 7 || delivery.URL == "" {
		t.Errorf("delivery = %+v, want the created number and URL for evidence", delivery)
	}
}

func TestOpenRefusesWhatItCannotReportAsEvidence(t *testing.T) {
	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Validation Failed"}`, http.StatusUnprocessableEntity)
	}))
	defer refused.Close()
	client := &Client{BaseURL: refused.URL, HTTP: refused.Client()}
	if _, err := client.Open(context.Background(), Render(proposal())); err == nil {
		t.Error("a refused pull request was reported as delivered")
	}

	// Accepted, and yet nothing usable came back. Reporting success here would
	// put a promise beyond reach of the evidence that should close it.
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer empty.Close()
	client = &Client{BaseURL: empty.URL, HTTP: empty.Client()}
	if _, err := client.Open(context.Background(), Render(proposal())); err == nil {
		t.Error("a pull request with no number was reported as delivered")
	}
}

var _ = url.Values{}
