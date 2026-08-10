package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The issues endpoint returns pull requests as well as issues, distinguished
// only by a `pull_request` field. A connector that misses that observes every
// pull request as though somebody filed an issue — and once the outbound half
// starts opening pull requests, it would be observing its own writing.
func TestPullRequestsAreNotObservedAsIssues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" {
			fmt.Fprint(w, `[]`)
			return
		}
		fmt.Fprint(w, `[
			{"number":1,"title":"a real issue","user":{"login":"someone"},"html_url":"u1"},
			{"number":2,"title":"a pull request","user":{"login":"gitseq-bot"},"html_url":"u2","pull_request":{"url":"p"}}
		]`)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTP: server.Client()}
	issues, err := client.Issues(context.Background(), "generalbusiness-ai", "gitseq")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1 — pull requests must be excluded", len(issues))
	}
	if issues[0].Number != 1 {
		t.Errorf("kept issue %d, want the one that is not a pull request", issues[0].Number)
	}
}

// Listing must follow pagination, or a repository with more than one page of
// issues is silently half-observed.
func TestIssuesFollowPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			fmt.Fprint(w, `[{"number":1,"title":"one","user":{"login":"a"},"html_url":"u1"}]`)
		case "2":
			fmt.Fprint(w, `[{"number":2,"title":"two","user":{"login":"b"},"html_url":"u2"}]`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTP: server.Client()}
	issues, err := client.Issues(context.Background(), "o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues across pages, want 2", len(issues))
	}
}

// Closed issues are still observations. Whether somebody tidied the tracker
// afterwards is not the record's business.
func TestClosedIssuesAreRequested(t *testing.T) {
	var state string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state = r.URL.Query().Get("state")
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTP: server.Client()}
	if _, err := client.Issues(context.Background(), "o", "r"); err != nil {
		t.Fatal(err)
	}
	if state != "all" {
		t.Errorf("asked github for state=%q, want all", state)
	}
}

// A failing request must be an error, not an empty list. Silently observing
// nothing would look identical to a repository with no issues.
func TestATransportFailureIsNotAnEmptyRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTP: server.Client()}
	if _, err := client.Issues(context.Background(), "o", "r"); err == nil {
		t.Fatal("an unauthorized response produced no error")
	}
}

// The token must reach GitHub, and must not be sent when there is none.
func TestTokenIsSentWhenPresent(t *testing.T) {
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()

	withToken := &Client{BaseURL: server.URL, HTTP: server.Client(), Token: "secret"}
	if _, err := withToken.Issues(context.Background(), "o", "r"); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer secret" {
		t.Errorf("Authorization was %q", auth)
	}

	anonymous := &Client{BaseURL: server.URL, HTTP: server.Client()}
	if _, err := anonymous.Issues(context.Background(), "o", "r"); err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		t.Errorf("Authorization was %q with no token, want none", auth)
	}
}
