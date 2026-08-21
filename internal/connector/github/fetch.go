package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is api.github.com. Tests override it; so can anyone running
// against GitHub Enterprise.
const DefaultBaseURL = "https://api.github.com"

const (
	// A GitHub issue body is at most 64 KiB, so an eight-MiB decoded response
	// leaves room for a full 100-item page and its JSON envelope. The reader
	// applies this after net/http's transparent decompression.
	maxResponseBodyBytes = 8 << 20
	// A clause observes at most 100 pages and 10,000 foreign records. Both
	// limits are enforced because a hostile endpoint need not honor per_page.
	maxIssuePages   = 100
	maxIssueRecords = 10_000
)

var errRedirectRefused = errors.New("github: redirects are refused")

// Client reads issues from GitHub. It is read-only: nothing in this file
// writes to the foreign system, because writing is the outbound half and is
// chartered separately.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
	Logger  *log.Logger
}

// NewClient returns a client with sensible defaults.
func NewClient(token string) *Client {
	return &Client{
		BaseURL: DefaultBaseURL,
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second, CheckRedirect: refuseRedirect},
		Logger:  log.Default(),
	}
}

// apiIssue is the wire shape, kept unexported so the rest of the connector
// depends on our own small Issue rather than on GitHub's schema.
type apiIssue struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
	// PullRequest is present, and non-nil, when this "issue" is really a pull
	// request. The issues endpoint returns both, which is a trap: a connector
	// that ignores this field observes every pull request as though somebody
	// had filed an issue, and the inbound half starts duplicating the outbound
	// half's own writing.
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (a apiIssue) issue(owner, repo string) Issue {
	labels := make([]string, 0, len(a.Labels))
	for _, label := range a.Labels {
		labels = append(labels, label.Name)
	}
	return Issue{
		Owner: owner, Repo: repo, Number: a.Number,
		Title: a.Title, Body: a.Body,
		Author: a.User.Login, URL: a.HTMLURL, State: a.State,
		Labels: labels,
	}
}

// List reads the issues matching one criteria clause's query, excluding pull
// requests.
//
// The query comes from the clause rather than from here. That is the whole
// point: a clause asking for open issues labelled bug costs one bounded walk of
// what matches, not a walk of the tracker. A repository with a hundred thousand
// issues costs whatever its clauses ask for, and a connector that enumerated
// first would have paid the full cost and read all the hostile input before any
// filter applied.
func (c *Client) List(ctx context.Context, owner, repo string, query url.Values) ([]Issue, error) {
	var issues []Issue
	observed := 0
	for page := 1; page <= maxIssuePages; page++ {
		batch, err := c.issuePage(ctx, owner, repo, query, page)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			return issues, nil
		}
		remaining := maxIssueRecords - observed
		kept := batch
		if len(kept) > remaining {
			kept = kept[:remaining]
		}
		observed += len(kept)
		for _, item := range kept {
			if item.PullRequest != nil {
				continue
			}
			issues = append(issues, item.issue(owner, repo))
		}
		if len(batch) > len(kept) {
			c.logf("github: truncated issue observation for %s/%s at %d records (%d issues) after page %d; dropped %d records from that page and did not fetch later pages", owner, repo, observed, len(issues), page, len(batch)-len(kept))
			return issues, nil
		}
		if observed == maxIssueRecords {
			c.logf("github: stopped issue observation for %s/%s at the %d-record limit (%d issues) after page %d; did not fetch later pages", owner, repo, observed, len(issues), page)
			return issues, nil
		}
		if page == maxIssuePages {
			c.logf("github: stopped issue observation for %s/%s at the %d-page limit with %d records (%d issues); did not fetch later pages", owner, repo, page, observed, len(issues))
			return issues, nil
		}
	}
	return issues, nil
}

// Number reads one issue by number, for a selection clause.
//
// A selection clause names what it wants, so the connector asks for exactly
// that. The missing report is not an error: a clause may name an issue that was
// deleted or never existed, and the caller decides what to say about it — but a
// pull request named by number is refused here, because admitting one would let
// the inbound half ingest the outbound half's own writing.
func (c *Client) Number(ctx context.Context, owner, repo string, number int) (Issue, bool, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues/%d", strings.TrimRight(c.baseURL(), "/"),
		url.PathEscape(owner), url.PathEscape(repo), number)
	body, status, err := c.get(ctx, endpoint)
	if err != nil {
		return Issue{}, false, err
	}
	if status == http.StatusNotFound || status == http.StatusGone {
		return Issue{}, false, nil
	}
	if status != http.StatusOK {
		return Issue{}, false, fmt.Errorf("github: reading %s/%s#%d returned %d", owner, repo, number, status)
	}
	var item apiIssue
	if err := json.Unmarshal(body, &item); err != nil {
		return Issue{}, false, fmt.Errorf("github: decoding issue: %w", err)
	}
	if item.PullRequest != nil {
		return Issue{}, false, nil
	}
	return item.issue(owner, repo), true, nil
}

func (c *Client) issuePage(ctx context.Context, owner, repo string, query url.Values, page int) ([]apiIssue, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues", strings.TrimRight(c.baseURL(), "/"),
		url.PathEscape(owner), url.PathEscape(repo))
	paged := url.Values{}
	for key, values := range query {
		paged[key] = values
	}
	paged.Set("per_page", "100")
	paged.Set("page", strconv.Itoa(page))

	body, status, err := c.get(ctx, endpoint+"?"+paged.Encode())
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("github: listing %s/%s returned %d", owner, repo, status)
	}
	var batch []apiIssue
	if err := json.Unmarshal(body, &batch); err != nil {
		return nil, fmt.Errorf("github: decoding issues: %w", err)
	}
	return batch, nil
}

func (c *Client) get(ctx context.Context, endpoint string) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		request.Header.Set("Authorization", "Bearer "+c.Token)
	}
	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	body, err := readResponseBody(response.Body)
	if err != nil {
		return nil, 0, err
	}
	return body, response.StatusCode, nil
}

func (c *Client) baseURL() string {
	if c.BaseURL == "" {
		return DefaultBaseURL
	}
	return c.BaseURL
}

func (c *Client) httpClient() *http.Client {
	base := c.HTTP
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	client.CheckRedirect = refuseRedirect
	return &client
}

func refuseRedirect(_ *http.Request, _ []*http.Request) error {
	return errRedirectRefused
}

func readResponseBody(body io.Reader) ([]byte, error) {
	limited := io.LimitReader(body, maxResponseBodyBytes+1)
	contents, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(contents) > maxResponseBodyBytes {
		return nil, fmt.Errorf("github: response body exceeds %d-byte limit", maxResponseBodyBytes)
	}
	return contents, nil
}

func (c *Client) logf(format string, args ...any) {
	logger := c.Logger
	if logger == nil {
		logger = log.Default()
	}
	logger.Printf(format, args...)
}

// Open asks GitHub to open a pull request and reports what it created.
//
// This is the one place the connector writes something discrete rather than
// rendering a surface it owns, which is why it is a command in the work loop
// rather than a projection: a pull request cannot be idempotently overwritten,
// and a second call makes a second one. The caller is expected to have promised
// this and to report the delivery as evidence, so a failure to deliver shows up
// as an unkept promise rather than a line in a log nobody reads.
func (c *Client) Open(ctx context.Context, request PullRequest) (Delivery, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/pulls", strings.TrimRight(c.baseURL(), "/"),
		url.PathEscape(request.Owner), url.PathEscape(request.Repo))
	encoded, err := json.Marshal(map[string]string{
		"title": request.Title, "head": request.Head, "base": request.Base, "body": request.Body,
	})
	if err != nil {
		return Delivery{}, err
	}
	body, status, err := c.post(ctx, endpoint, encoded)
	if err != nil {
		return Delivery{}, err
	}
	if status != http.StatusCreated {
		return Delivery{}, fmt.Errorf("github: opening a pull request on %s/%s returned %d: %s",
			request.Owner, request.Repo, status, strings.TrimSpace(string(body)))
	}
	var created struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		return Delivery{}, fmt.Errorf("github: decoding the created pull request: %w", err)
	}
	// Both halves of the evidence are required, and a non-positive number is as
	// useless as none: a report closing the promise to deliver has to name
	// something a reader can open. Accepting silently here would leave a promise
	// looking kept with nothing able to close it.
	if created.Number <= 0 {
		return Delivery{}, fmt.Errorf("github: accepted the pull request and returned no usable number (%d), so there is nothing to report as evidence", created.Number)
	}
	if strings.TrimSpace(created.HTMLURL) == "" {
		return Delivery{}, fmt.Errorf("github: accepted pull request %d and returned no html_url, so the delivery cannot be cited", created.Number)
	}
	return Delivery{Number: created.Number, URL: created.HTMLURL}, nil
}

func (c *Client) post(ctx context.Context, endpoint string, payload []byte) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		request.Header.Set("Authorization", "Bearer "+c.Token)
	}
	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	body, err := readResponseBody(response.Body)
	if err != nil {
		return nil, 0, err
	}
	return body, response.StatusCode, nil
}
