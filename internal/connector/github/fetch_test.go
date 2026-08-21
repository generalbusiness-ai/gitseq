package github

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
	issues, err := client.List(context.Background(), "generalbusiness-ai", "gitseq", (Clause{}).Query())
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
	issues, err := client.List(context.Background(), "o", "r", (Clause{}).Query())
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues across pages, want 2", len(issues))
	}
}

func TestIssueListingStopsAtThePageLimitAndLogsWhatWasDropped(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		fmt.Fprintf(w, `[{"number":%s,"pull_request":{"url":"p"}}]`, r.URL.Query().Get("page"))
	}))
	defer server.Close()

	var logs bytes.Buffer
	client := &Client{BaseURL: server.URL, HTTP: server.Client(), Logger: log.New(&logs, "", 0)}
	issues, err := client.List(context.Background(), "o", "r", (Clause{}).Query())
	if err != nil {
		t.Fatal(err)
	}
	if requests != maxIssuePages || len(issues) != 0 {
		t.Fatalf("requests=%d issues=%d, want %d and 0", requests, len(issues), maxIssuePages)
	}
	if got := logs.String(); !strings.Contains(got, "after 100 pages and 100 records (0 issues)") || !strings.Contains(got, "dropped all later pages") {
		t.Fatalf("truncation log = %q", got)
	}
}

func TestIssueListingStopsAtTheRecordLimitAndLogsThePartialPage(t *testing.T) {
	batch := make([]apiIssue, maxIssueRecords+1)
	for index := range batch {
		batch[index].Number = index + 1
	}
	encoded, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write(encoded)
	}))
	defer server.Close()

	var logs bytes.Buffer
	client := &Client{BaseURL: server.URL, HTTP: server.Client(), Logger: log.New(&logs, "", 0)}
	issues, err := client.List(context.Background(), "o", "r", (Clause{}).Query())
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || len(issues) != maxIssueRecords {
		t.Fatalf("requests=%d issues=%d, want 1 and %d", requests, len(issues), maxIssueRecords)
	}
	if got := logs.String(); !strings.Contains(got, "dropped 1 records from that page and all later pages") {
		t.Fatalf("truncation log = %q", got)
	}
}

// Closed issues are still observations. Whether somebody tidied the tracker
// afterwards is not the record's business. The state now comes from the clause
// rather than being fixed in the client, so this pins that a clause stating no
// state still reaches GitHub asking for all of them.
func TestClosedIssuesAreRequested(t *testing.T) {
	var state string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state = r.URL.Query().Get("state")
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTP: server.Client()}
	if _, err := client.List(context.Background(), "o", "r", (Clause{}).Query()); err != nil {
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
	if _, err := client.List(context.Background(), "o", "r", (Clause{}).Query()); err == nil {
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
	if _, err := withToken.List(context.Background(), "o", "r", (Clause{}).Query()); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer secret" {
		t.Errorf("Authorization was %q", auth)
	}

	anonymous := &Client{BaseURL: server.URL, HTTP: server.Client()}
	if _, err := anonymous.List(context.Background(), "o", "r", (Clause{}).Query()); err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		t.Errorf("Authorization was %q with no token, want none", auth)
	}
}

func TestRedirectsAreRefusedWithoutForwardingTheToken(t *testing.T) {
	tests := []struct {
		name   string
		source func(http.Handler) (*httptest.Server, *http.Client)
		target func(*httptest.Server) string
	}{
		{
			name: "same-host scheme downgrade",
			source: func(handler http.Handler) (*httptest.Server, *http.Client) {
				server := httptest.NewTLSServer(handler)
				return server, server.Client()
			},
			target: func(server *httptest.Server) string { return server.URL },
		},
		{
			name: "unrelated hostname",
			source: func(handler http.Handler) (*httptest.Server, *http.Client) {
				server := httptest.NewServer(handler)
				return server, server.Client()
			},
			target: func(server *httptest.Server) string {
				parsed, err := url.Parse(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				parsed.Host = "localhost:" + parsed.Port()
				return parsed.String()
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var targetRequests int
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				targetRequests++
				if authorization := r.Header.Get("Authorization"); authorization != "" {
					t.Errorf("redirect target received Authorization %q", authorization)
				}
				fmt.Fprint(w, `[]`)
			}))
			defer target.Close()

			var sourceAuthorization string
			source, httpClient := test.source(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sourceAuthorization = r.Header.Get("Authorization")
				http.Redirect(w, r, test.target(target), http.StatusFound)
			}))
			defer source.Close()
			// Even an injected client that was configured to follow redirects must
			// not weaken the connector's transport boundary.
			httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return nil }
			client := &Client{BaseURL: source.URL, HTTP: httpClient, Token: "secret"}
			if _, err := client.List(context.Background(), "o", "r", (Clause{}).Query()); err == nil || !strings.Contains(err.Error(), errRedirectRefused.Error()) {
				t.Fatalf("redirect response error = %v", err)
			}
			if sourceAuthorization != "Bearer secret" {
				t.Fatalf("source Authorization = %q", sourceAuthorization)
			}
			if targetRequests != 0 {
				t.Fatalf("redirect target received %d requests", targetRequests)
			}
		})
	}
}

func TestResponseBodyLimitAppliesAfterDecompression(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(bytes.Repeat([]byte("x"), maxResponseBodyBytes+1)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(compressed.Bytes())
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTP: server.Client()}
	if _, _, err := client.Number(context.Background(), "o", "r", 1); err == nil || !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("oversized decompressed response error = %v", err)
	}
}

func TestPullRequestResponseBodyIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(bytes.Repeat([]byte("x"), maxResponseBodyBytes+1))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTP: server.Client()}
	_, err := client.Open(context.Background(), PullRequest{Owner: "o", Repo: "r"})
	if err == nil || !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("oversized pull response error = %v", err)
	}
}

// A selection clause names a number, and GitHub will happily return a pull
// request for one. Excluding pull requests only on the list path would leave
// the inbound half able to ingest the outbound half's own writing by number.
func TestAPullRequestNamedByNumberIsNotAnIssue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"number":2,"title":"a pull request","user":{"login":"gitseq-bot"},"pull_request":{"url":"p"}}`)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTP: server.Client()}
	_, found, err := client.Number(context.Background(), "o", "r", 2)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error("a pull request was returned as an issue")
	}
}

// A clause may name an issue that was deleted or never existed. That is a
// missing issue, not a transport failure, and the caller reports it rather than
// the run stopping.
func TestAMissingIssueIsNotAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTP: server.Client()}
	_, found, err := client.Number(context.Background(), "o", "r", 404)
	if err != nil {
		t.Fatalf("a missing issue was reported as an error: %v", err)
	}
	if found {
		t.Error("a 404 was reported as a found issue")
	}
}

// Labels have to survive the wire, because a criteria clause cannot decide
// whether an issue matches without them.
func TestLabelsArriveOnTheIssue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"number":7,"title":"t","user":{"login":"a"},"state":"open","labels":[{"name":"bug"},{"name":"ui"}]}`)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTP: server.Client()}
	issue, found, err := client.Number(context.Background(), "o", "r", 7)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if len(issue.Labels) != 2 || issue.Labels[0] != "bug" || issue.Labels[1] != "ui" {
		t.Errorf("labels = %v, want [bug ui]", issue.Labels)
	}
}

// A clause's query must reach GitHub. If it did not, a criteria clause would be
// filtering locally over the whole tracker, which is the cost the doorstep
// exists to avoid.
func TestTheClauseQueryReachesGitHub(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTP: server.Client()}
	query := Clause{State: "open", Labels: []string{"bug"}}.Query()
	if _, err := client.List(context.Background(), "o", "r", query); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "labels=bug") || !strings.Contains(got, "state=open") {
		t.Errorf("request query was %q, want the clause's state and labels", got)
	}
}
