package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// DefaultGitHubAPI is where GitHub answers.
const DefaultGitHubAPI = "https://api.github.com"

// maxProviderResponse bounds what is read from a provider. The body is
// somebody else's, and a reader with no ceiling is a reader whose memory
// somebody else chooses.
const maxProviderResponse = 1 << 20

// GitHub turns a GitHub user access token into an [Identity].
//
// This is the lowest-friction rung, and the whole reason it exists: a newcomer
// arrives with one redirect round-trip and one first-time consent screen, and
// walks away with a persistent identity. No extension, no key backup, no ssh
// key provisioned by hand — the signing key stays the one their browser minted.
//
// # Where this sits
//
// The check runs outside the fold, and only its result is signed into the log.
// A provider's token is short-lived and cannot itself be a record: it says
// something true now, which is not the same as something a reader can check in
// five years. So the deployment asks GitHub once, and then says what it found,
// under its own witness key, in a record that will still verify when the token
// and the deployment are both long gone.
//
// Nothing in the fold ever calls this. Replaying a log makes no network
// request, and a clone with no access to GitHub reads exactly the same
// identities.
//
// # The token
//
// The token is a bearer credential belonging to the person who presented it.
// It is used once, for one request, and it is never recorded and never
// returned. What lands in the log is the account identifier and the login —
// the answer, not the credential.
//
// Keeping it out of errors is done by a rule with no exceptions rather than by
// care at each return: no byte of provider- or transport-controlled text ever
// reaches an error from here. A refusal is reported as the numeric status and
// this program's own phrase for it; a transport failure and an unreadable
// answer are reported in fixed words of this package's own. Redacting the
// credential out of somebody else's string would have been the weaker rule,
// since it only removes the spelling it went looking for, and a provider
// chooses how to spell what it echoes.
//
// The redirect, the consent screen and the code-for-token exchange belong to
// the deployment's own front end, along with the client secret that exchange
// needs. This package deliberately holds none of that: it starts where a token
// already exists.
type GitHub struct {
	// API is the API root to ask. Empty means [DefaultGitHubAPI]. Override it
	// for a GitHub Enterprise installation, or for a test server.
	API string
	// Client is the HTTP client to ask with. Empty means
	// [http.DefaultClient]. A deployment that wants a timeout, a proxy, or a
	// pinned certificate sets one here.
	Client *http.Client
}

// Check asks GitHub who the token belongs to.
//
// The identity's subject is GitHub's numeric account id, because that is the
// identifier GitHub does not reissue. A login can be changed, released, and
// taken by somebody else, so anchoring to one would let a later account
// inherit an earlier one's history; the login is carried alongside as a
// handle, for a display to show, and never as the thing that is equal.
func (g GitHub) Check(ctx context.Context, token string) (Identity, error) {
	if err := validBearerToken(token); err != nil {
		return Identity{}, err
	}
	root := g.API
	if root == "" {
		root = DefaultGitHubAPI
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(root, "/")+"/user", nil)
	if err != nil {
		return Identity{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := g.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		// Not one byte of the transport's error is repeated. The client is the
		// deployment's to set, so the transport is not one this package chose
		// and its text is not text this package wrote; a round-tripper that
		// reports the request it was handed — a debugging wrapper, a proxy
		// library, something hostile — would otherwise publish the
		// Authorization header through this return. The single thing worth
		// telling apart is the caller's own cancellation, and that answer
		// belongs to the caller's context rather than to the transport.
		if cause := ctx.Err(); cause != nil {
			return Identity{}, fmt.Errorf("github user lookup: %w", cause)
		}
		return Identity{}, errors.New("github user lookup: the request to the provider failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// The code and this program's own words for it, and nothing that came
		// off the wire. A provider writes its own reason phrase, HTTP/1.1
		// carries it verbatim, and a provider that quotes back what it was
		// sent would otherwise put the caller's credential into an error — and
		// from there into somebody's log.
		return Identity{}, fmt.Errorf("github user lookup: %s", statusText(response.StatusCode))
	}
	var answer struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	body := io.LimitReader(response.Body, maxProviderResponse)
	if err := json.NewDecoder(body).Decode(&answer); err != nil {
		// A decoding failure is described in terms of the provider's own
		// bytes, so it is somebody else's text on the same footing as the
		// rest, and none of it is repeated either.
		return Identity{}, errors.New("github user lookup: the provider's answer could not be read")
	}
	if answer.ID <= 0 {
		return Identity{}, errors.New("github user lookup: answer names no account id")
	}
	found := Identity{
		Scheme:  GitHubScheme,
		Subject: strconv.FormatInt(answer.ID, 10),
		Handle:  answer.Login,
	}
	// A provider's answer is untrusted input on its way to a signed record and
	// then to a display. Refusing it here means a malformed one fails at the
	// boundary it entered, rather than at the fold that has to be deterministic.
	if err := found.validate(); err != nil {
		return Identity{}, fmt.Errorf("github user lookup: %w", err)
	}
	return found, nil
}

// statusText describes a refusal using only what this program chose: the
// numeric code, and Go's own table of standard reason phrases. Nothing from
// the status line is used, because the reason phrase belongs to the provider
// and HTTP/1.1 delivers it unaltered.
func statusText(code int) string {
	if text := http.StatusText(code); text != "" {
		return strconv.Itoa(code) + " " + text
	}
	return strconv.Itoa(code)
}

// validBearerToken refuses what cannot legally be a bearer credential. A token
// carrying a newline would end the Authorization header and begin a line the
// caller did not write, so it is refused here rather than deep in a transport.
func validBearerToken(token string) error {
	if token == "" {
		return errors.New("github token is required")
	}
	if len(token) > 512 {
		return errors.New("github token is implausibly long")
	}
	for i := 0; i < len(token); i++ {
		if token[i] <= ' ' || token[i] >= 0x7f {
			return errors.New("github token contains a character a bearer credential cannot carry")
		}
	}
	return nil
}
