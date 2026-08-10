package github

import (
	"context"
	"encoding/json"
	"fmt"
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
}

// Issues lists issues in a repository, excluding pull requests.
//
// State is "all" deliberately. A closed issue is still something that was
// observed, and the record should not depend on whether somebody tidied the
// tracker after the fact.
func (c *Client) Issues(ctx context.Context, owner, repo string) ([]Issue, error) {
	var issues []Issue
	for page := 1; ; page++ {
		batch, err := c.issuePage(ctx, owner, repo, page)
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
			issues = append(issues, Issue{
				Owner: owner, Repo: repo, Number: item.Number,
				Title: item.Title, Body: item.Body,
				Author: item.User.Login, URL: item.HTMLURL, State: item.State,
			})
		}
	}
}

func (c *Client) issuePage(ctx context.Context, owner, repo string, page int) ([]apiIssue, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues", strings.TrimRight(c.baseURL(), "/"),
		url.PathEscape(owner), url.PathEscape(repo))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	query := request.URL.Query()
	query.Set("state", "all")
	query.Set("per_page", "100")
	query.Set("page", strconv.Itoa(page))
	request.URL.RawQuery = query.Encode()
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		request.Header.Set("Authorization", "Bearer "+c.Token)
	}

	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: listing %s/%s returned %s", owner, repo, response.Status)
	}
	var batch []apiIssue
	if err := json.NewDecoder(response.Body).Decode(&batch); err != nil {
		return nil, fmt.Errorf("github: decoding issues: %w", err)
	}
	return batch, nil
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
