// Package residentclient is the one client-side boundary for a repository's
// trusted local resident. Commands decide when to use a resident and whether
// to fall back; this package decides which URL is safe and how bytes cross it.
package residentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

const (
	// ActorEnvironment is the process-level identity used when a command does
	// not receive its actor flag explicitly.
	ActorEnvironment = "GITSEQ_ACTOR"

	// SubmissionResponseLimit is deliberately larger than a normal receipt but
	// still finite. Request payload size is governed separately by the workroom.
	SubmissionResponseLimit int64 = 2 << 20

	// IdentityLimit bounds the liveness answer. The whole reply is one short
	// hex string, so anything larger is not a resident answering.
	IdentityLimit int64 = 4 << 10
)

// ResolveActor gives an explicit flag precedence over the process identity.
// There is no implicit actor: attribution must name one identity somebody
// deliberately provisioned.
func ResolveActor(flagName, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if name := strings.TrimSpace(os.Getenv(ActorEnvironment)); name != "" {
		return name, nil
	}
	return "", errors.New("no actor identity: pass " + flagName + ", or set " + ActorEnvironment + " to the identity this instance signs as")
}

// ValidateURL accepts only the loopback HTTP origin shape the resident
// publishes. Paths are supplied separately to Client methods so a caller
// cannot smuggle credentials, a query, or a different endpoint into the base.
func ValidateURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("resident service must be an http loopback URL without credentials")
	}
	host := parsed.Hostname()
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", errors.New("resident service must name a loopback address")
		}
	}
	return strings.TrimRight(raw, "/"), nil
}

// UntrustedAdvertisement and UnusableAdvertisedURL are the two sentences every
// surface says about a published resident record it will not act through. They
// live here, beside the validation that produces the second one, because the
// cost of each surface wording its own was exactly the divergence this pair
// removes: two accounts of the same six failures, drifting apart unobserved.
// Each names which failure it is and leaves the way out to the caller, since
// that is the only part that honestly differs between a command with flags and
// a long-running adapter without them.
//
// UntrustedAdvertisement covers the five the record itself fails: unreadable,
// larger than the 8 KiB bound, not a record, carrying no address, or naming
// another workroom. Reason comes from app.ResidentAdvertisement.
func UntrustedAdvertisement(reason error) error {
	return fmt.Errorf("this repository advertises a resident that cannot be trusted: %w", reason)
}

// UnusableAdvertisedURL covers the sixth: a record that reads and names this
// workroom, but carries an address ValidateURL will not dial.
func UnusableAdvertisedURL(advertised string, reason error) error {
	return fmt.Errorf("this repository advertises %q, which is not usable: %w", advertised, reason)
}

// TransportError marks failure to exchange an HTTP response with the resident.
// Callers such as MCP use this distinction to decide whether local fallback is
// honest; malformed or refused resident answers are not transport failures.
type TransportError struct{ Err error }

func (e *TransportError) Error() string { return e.Err.Error() }
func (e *TransportError) Unwrap() error { return e.Err }

// ReadError marks a connection that answered but stopped while its bounded
// body was being read. A deadline or cancellation inside it is transport loss;
// another read error remains a malformed response.
type ReadError struct{ Err error }

func (e *ReadError) Error() string { return e.Err.Error() }
func (e *ReadError) Unwrap() error { return e.Err }

// HTTPError is a complete resident refusal, not a reason to write locally.
type HTTPError struct {
	StatusCode int
	Status     string
	Message    string
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "resident returned " + e.Status
}

func IsTransportError(err error) bool {
	var target *TransportError
	return errors.As(err, &target)
}

func IsReadError(err error) bool {
	var target *ReadError
	return errors.As(err, &target)
}

// Client owns the bounded, no-redirect resident transport. The per-operation
// context is the primary deadline; the HTTP timeout is a whole-request backstop
// that also covers a response body which stalls after its headers arrive.
type Client struct {
	http *http.Client
}

func New(timeout time.Duration) *Client {
	return NewWithHTTP(nil, timeout)
}

// NewWithHTTP preserves a caller's transport (useful for test TLS servers) but
// replaces redirect and timeout policy with the resident boundary's policy.
func NewWithHTTP(base *http.Client, timeout time.Duration) *Client {
	var configured http.Client
	if base != nil {
		configured = *base
	}
	configured.Timeout = timeout
	configured.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return errors.New("resident redirects are not allowed")
	}
	return &Client{http: &configured}
}

func (c *Client) Timeout() time.Duration { return c.http.Timeout }

func (c *Client) GetJSON(ctx context.Context, baseURL, path string, limit int64, target any) error {
	return c.doJSON(ctx, http.MethodGet, baseURL, path, nil, limit, target)
}

func (c *Client) PostJSON(ctx context.Context, baseURL, path string, value any, limit int64, target any) error {
	return c.doJSON(ctx, http.MethodPost, baseURL, path, value, limit, target)
}

func (c *Client) GetValue(ctx context.Context, baseURL, path string, limit int64) (any, error) {
	return c.doValue(ctx, http.MethodGet, baseURL, path, nil, limit)
}

func (c *Client) PostValue(ctx context.Context, baseURL, path string, value any, limit int64) (any, error) {
	return c.doValue(ctx, http.MethodPost, baseURL, path, value, limit)
}

// Delete performs the bounded departure request. Its response body has no
// meaning, but it is still closed and redirects are still refused.
func (c *Client) Delete(ctx context.Context, baseURL, path string) error {
	response, err := c.roundTrip(ctx, http.MethodDelete, baseURL, path, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		return &HTTPError{StatusCode: response.StatusCode, Status: response.Status}
	}
	return nil
}

func (c *Client) doJSON(ctx context.Context, method, baseURL, path string, value any, limit int64, target any) error {
	data, response, err := c.read(ctx, method, baseURL, path, value, limit)
	if err != nil {
		return err
	}
	if response.StatusCode >= http.StatusBadRequest {
		return responseError(response, data)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("resident returned trailing JSON")
	}
	return nil
}

func (c *Client) doValue(ctx context.Context, method, baseURL, path string, value any, limit int64) (any, error) {
	data, response, err := c.read(ctx, method, baseURL, path, value, limit)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= http.StatusBadRequest {
		return nil, responseError(response, data)
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("resident returned trailing JSON")
	}
	return decoded, nil
}

func (c *Client) read(ctx context.Context, method, baseURL, path string, value any, limit int64) ([]byte, *http.Response, error) {
	if limit <= 0 {
		return nil, nil, errors.New("resident response limit must be positive")
	}
	response, err := c.roundTrip(ctx, method, baseURL, path, value)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, nil, &ReadError{Err: err}
	}
	if int64(len(data)) > limit {
		return nil, nil, fmt.Errorf("resident response exceeds %d bytes", limit)
	}
	return data, response, nil
}

func (c *Client) roundTrip(ctx context.Context, method, baseURL, path string, value any) (*http.Response, error) {
	base, err := ValidateURL(baseURL)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil, errors.New("resident path must begin with one slash")
	}
	var body io.Reader
	if value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return nil, err
	}
	if value != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, &TransportError{Err: err}
	}
	return response, nil
}

func responseError(response *http.Response, data []byte) error {
	var failure map[string]any
	message := ""
	if json.Unmarshal(data, &failure) == nil {
		message, _ = failure["error"].(string)
	}
	if message == "" {
		message = "HTTP " + response.Status
	}
	return &HTTPError{StatusCode: response.StatusCode, Status: response.Status, Message: message}
}

// ProbeResident says whether the resident an ownership claim names is still
// serving that workroom. Only a refused dial is a definitive negative: nothing
// is listening on that address at all, so the process that wrote the claim is
// gone. Everything else is ambiguous and leaves the claim alone — a timeout, a
// connection that accepts and then says nothing, an unparseable or oversized
// answer, an answer naming a different workroom, or a URL this client will not
// dial. The asymmetry is the point. A false negative starts a second resident
// beside a live one, which is the split room this mechanism exists to prevent;
// a false positive only refuses to start, and says how to clear the claim.
//
// The claim is an ordinary file that any local process able to write the
// repository can put a URL into, so the address is untrusted input and goes
// through the same loopback validation as every other resident call. Dialing
// whatever it named would turn starting a service into a request forgery.
func (c *Client) ProbeResident(ctx context.Context, claim app.ResidentClaim) app.Liveness {
	base, err := ValidateURL(claim.URL)
	if err != nil {
		return app.Ambiguous
	}
	var identity struct {
		Genesis string `json:"genesis"`
	}
	if err := c.GetJSON(ctx, base, "/v0/identity", IdentityLimit, &identity); err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) {
			return app.Dead
		}
		return app.Ambiguous
	}
	if identity.Genesis == "" || identity.Genesis != claim.Genesis {
		return app.Ambiguous
	}
	return app.Alive
}

// Submit sequences a fully signed request through the named resident, or
// accepts it locally when the caller deliberately supplies no resident URL.
// The caller still owns that routing decision.
func (c *Client) Submit(ctx context.Context, workspace *app.Workspace, serverURL string, request kernel.Request) (app.Submission, error) {
	if serverURL == "" {
		return workspace.AcceptSubmission(ctx, request)
	}
	var submission app.Submission
	if err := c.PostJSON(ctx, serverURL, "/v0/submit", request, SubmissionResponseLimit, &submission); err != nil {
		return app.Submission{}, err
	}
	return submission, nil
}

// UndefinedKindWarning answers the one vocabulary question all authoring
// surfaces ask after a state act lands. Presentation stays with each binary.
func UndefinedKindWarning(ctx context.Context, workspace *app.Workspace, kind workroom.Kind) string {
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return fmt.Sprintf("cannot tell whether kind %q is defined here: %v", kind, err)
	}
	return snapshot.Vocabulary.UndefinedKindWarning(kind)
}
