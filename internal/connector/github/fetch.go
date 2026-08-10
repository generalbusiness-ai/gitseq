package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is api.github.com. Tests override it; so can anyone running
// against GitHub Enterprise.
const DefaultBaseURL = "https://api.github.com"

// Client reads issues from GitHub. It is read-only: nothing in this file
// writes to the foreign system, because writing is the outbound half and is
// chartered separately.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient returns a client with sensible defaults.
func NewClient(token string) *Client {
	return &Client{
		BaseURL: DefaultBaseURL,
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
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
	for page := 1; ; page++ {
		batch, err := c.issuePage(ctx, owner, repo, query, page)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			return issues, nil
		}
		for _, item := range batch {
			if item.PullRequest != nil {
				continue
			}
			issues = append(issues, item.issue(owner, repo))
		}
	}
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
	body, err := io.ReadAll(response.Body)
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
	if c.HTTP == nil {
		return http.DefaultClient
	}
	return c.HTTP
}
