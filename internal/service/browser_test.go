package service

import (
	"context"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
)

func browserTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	// Composed as the resident command composes it: the trusted-host refusal
	// sits between the policy and the server, and must carry the policy too.
	httpServer := httptest.NewServer(BrowserHeaders(TrustedHostHandler(server.Handler())))
	t.Cleanup(httpServer.Close)
	return httpServer
}

// The exact policy, pinned as a literal so a loosened directive fails here
// rather than passing by comparison with the constant it came from.
const pinnedPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; object-src 'none'"

// Every class of response the service gives a browser carries the whole
// browser policy: the UI page, a bundled asset, a JSON view, a refusal and a
// not-found answer. Dropping any header from any class fails here.
func TestEveryResponseClassCarriesTheBrowserPolicy(t *testing.T) {
	t.Parallel()
	httpServer := browserTestServer(t)
	want := map[string]string{
		"Content-Security-Policy": pinnedPolicy,
		"X-Frame-Options":         "DENY",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
	}
	for _, class := range []struct {
		name, method, path string
		status             int
		host               string
	}{
		{name: "ui page", method: http.MethodGet, path: "/", status: http.StatusOK},
		{name: "icon asset", method: http.MethodGet, path: "/favicon.svg", status: http.StatusOK},
		{name: "json view", method: http.MethodGet, path: "/v0/status", status: http.StatusOK},
		{name: "json refusal", method: http.MethodPost, path: "/v0/act", status: 0},
		{name: "not found", method: http.MethodGet, path: "/no-such-page", status: http.StatusNotFound},
		{name: "trusted-host refusal", method: http.MethodGet, path: "/", status: http.StatusBadRequest, host: "evil.example:7777"},
	} {
		t.Run(class.name, func(t *testing.T) {
			request, err := http.NewRequest(class.method, httpServer.URL+class.path, strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			if class.host != "" {
				request.Host = class.host
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			// A refusal's exact status is the guard's business; any refusal
			// must still carry the policy.
			if class.status == 0 && response.StatusCode < 400 {
				t.Fatalf("%s %s = %d, want a refusal", class.method, class.path, response.StatusCode)
			}
			if class.status != 0 && response.StatusCode != class.status {
				t.Fatalf("%s %s = %d, want %d", class.method, class.path, response.StatusCode, class.status)
			}
			for header, value := range want {
				if got := response.Header.Get(header); got != value {
					t.Fatalf("%s on %s = %q, want %q", header, class.path, got, value)
				}
			}
			if strings.Contains(contentSecurityPolicy, "'unsafe-inline'") || strings.Contains(contentSecurityPolicy, "'unsafe-eval'") {
				t.Fatalf("policy admits unsafe sources: %s", contentSecurityPolicy)
			}
			if !strings.Contains(contentSecurityPolicy, "frame-ancestors 'none'") {
				t.Fatalf("policy does not deny framing: %s", contentSecurityPolicy)
			}
		})
	}
}

// The embedded UI loads under its own policy. The page may carry no inline
// script, style or event handler; every script, stylesheet, icon and font it
// names is a same-origin path the service serves with the content type a
// nosniff browser will accept for that role.
func TestEmbeddedUILoadsUnderTheBrowserPolicy(t *testing.T) {
	t.Parallel()
	httpServer := browserTestServer(t)
	page := fetch(t, httpServer, "/", "text/html")
	for _, forbidden := range []string{"<script>", "<style", " style=", " onload=", " onclick=", "javascript:"} {
		if strings.Contains(page, forbidden) {
			t.Fatalf("index.html carries %q, which the policy blocks", forbidden)
		}
	}
	references := regexp.MustCompile(`(?:src|href)="([^"]+)"`).FindAllStringSubmatch(page, -1)
	if len(references) < 3 {
		t.Fatalf("index.html names %d resources, want the script, stylesheet and icon", len(references))
	}
	for _, reference := range references {
		path := reference[1]
		if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
			t.Fatalf("index.html references %q, which is not a same-origin path", path)
		}
		fetch(t, httpServer, path, roleContentType(t, path))
	}
	stylesheet := regexp.MustCompile(`href="(/assets/[^"]+\.css)"`).FindStringSubmatch(page)
	if stylesheet == nil {
		t.Fatal("index.html names no stylesheet")
	}
	css := fetch(t, httpServer, stylesheet[1], "text/css")
	for _, match := range regexp.MustCompile(`url\(([^)]+)\)`).FindAllStringSubmatch(css, -1) {
		target := strings.Trim(match[1], `"'`)
		if strings.HasPrefix(target, "data:") || strings.Contains(target, "://") {
			t.Fatalf("stylesheet loads %q, which font-src and img-src 'self' block", target)
		}
		if strings.HasPrefix(target, "/") {
			fetch(t, httpServer, target, roleContentType(t, target))
		}
	}
	entries, err := fs.ReadDir(uidist, "uidist/assets")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".js") {
			fetch(t, httpServer, "/assets/"+entry.Name(), "text/javascript")
		}
	}
}

func roleContentType(t *testing.T, path string) string {
	t.Helper()
	switch {
	case strings.HasSuffix(path, ".js"):
		return "text/javascript"
	case strings.HasSuffix(path, ".css"):
		return "text/css"
	case strings.HasSuffix(path, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(path, ".woff2"):
		// A font load is not governed by nosniff, and the type comes from
		// the host's mime table; being served is what matters.
		return ""
	}
	t.Fatalf("no content-type expectation for %s", path)
	return ""
}

func fetch(t *testing.T, httpServer *httptest.Server, path, contentType string) string {
	t.Helper()
	response, err := http.Get(httpServer.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", path, response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, contentType) {
		t.Fatalf("GET %s content type = %q, want %s so a nosniff browser accepts it", path, got, contentType)
	}
	if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("GET %s lacks nosniff", path)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
