// Package forge holds the repository-native gate over the optional development
// forge lane's configuration. It reads compose.yaml as text and asserts the two
// properties that, when they were absent, put an unlocked Gitea installer on
// every network the host was attached to. It needs no daemon, no image pull and
// no network, so it runs in the ordinary suite rather than behind the profile
// the lane itself is gated on.
package forge

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const composePath = "../compose.yaml"

// publishedPort matches a compose port mapping entry, capturing whatever
// precedes the container port. Docker treats a bare "3300:3000" as 0.0.0.0,
// so the absence of a host address is the defect, not a formatting preference.
var publishedPort = regexp.MustCompile(`^\s*-\s*"?([^"\s]+)"?\s*$`)

func composeText(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("reading %s: %v", composePath, err)
	}
	return string(data)
}

// portMappings returns every entry under a `ports:` block, ignoring comments.
// Parsing the block rather than grepping for one known string is what makes
// this catch a second service added later with the same mistake.
func portMappings(t *testing.T, text string) []string {
	t.Helper()
	var mappings []string
	inPorts := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}
		if trimmed == "ports:" {
			inPorts = true
			continue
		}
		if inPorts {
			if !strings.HasPrefix(trimmed, "-") {
				inPorts = false
				continue
			}
			if match := publishedPort.FindStringSubmatch(line); match != nil {
				mappings = append(mappings, match[1])
			}
		}
	}
	return mappings
}

// A published port with no host address binds every interface. The development
// forge is reachable only from the host that runs it, and that is the whole of
// its trust boundary: it has no authentication story of its own worth exposing.
func TestEveryPublishedPortBindsLoopback(t *testing.T) {
	mappings := portMappings(t, composeText(t))
	if len(mappings) == 0 {
		t.Fatal("no published ports found; the parser has stopped seeing the ports block")
	}
	for _, mapping := range mappings {
		parts := strings.Split(mapping, ":")
		if len(parts) < 3 {
			t.Errorf("port mapping %q publishes on all interfaces; give it an explicit loopback host address", mapping)
			continue
		}
		if host := parts[0]; host != "127.0.0.1" && host != "::1" {
			t.Errorf("port mapping %q publishes on %q, which is not loopback", mapping, host)
		}
	}
}

// serviceEnvironment returns the environment mapping of one service, with
// comments stripped. Scope and comment-awareness are both load-bearing and
// neither was here before: the first version searched the whole file with
// strings.Contains, and the explanatory comment above the mapping names the
// same key, so commenting the mapping out left the comment matching and the
// test green while the forge booted unlocked. A reviewer found that by making
// exactly the edit a person makes while debugging.
func serviceEnvironment(t *testing.T, text, service string) map[string]string {
	t.Helper()
	environment := map[string]string{}
	inService, inEnvironment := false, false
	serviceIndent, environmentIndent := -1, -1
	for _, line := range strings.Split(text, "\n") {
		if index := strings.Index(line, "#"); index >= 0 {
			line = line[:index]
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))

		if trimmed == service+":" {
			inService, serviceIndent = true, indent
			continue
		}
		if inService && indent <= serviceIndent {
			// Dedented back out of the service: anything further belongs to a
			// sibling, which is the scoping the old check lacked.
			inService, inEnvironment = false, false
			continue
		}
		if !inService {
			continue
		}
		if trimmed == "environment:" {
			inEnvironment, environmentIndent = true, indent
			continue
		}
		if inEnvironment {
			if indent <= environmentIndent {
				inEnvironment = false
				continue
			}
			if key, value, found := strings.Cut(trimmed, ":"); found {
				environment[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"`)
			}
		}
	}
	return environment
}

// The installer is the sharper half. DISABLE_REGISTRATION governs self-signup
// after installation and does nothing about first run, so a fresh data volume
// without this lock hands administrator to whoever loads the page first.
//
// The key is not guesswork: the pinned v1.24.6 image runs environment-to-ini on
// every start, HasInstallLock reads [security] INSTALL_LOCK, and runWeb selects
// the installer or the installed routes from that value.
func TestFirstRunSetupIsLocked(t *testing.T) {
	environment := serviceEnvironment(t, composeText(t), "forge")
	if len(environment) == 0 {
		t.Fatal("no environment found for the forge service; the parser has stopped seeing it")
	}
	if environment["GITEA__security__INSTALL_LOCK"] != "true" {
		t.Error("the forge service does not lock Gitea first-run setup; a fresh data volume would serve the installer")
	}
}

// The regression for the false negative itself. Commenting the mapping out is
// the edit a person makes while debugging, and it is the one the original
// check could not see. Asserting the parser ignores comments here means the
// scoping above cannot quietly rot back into a whole-file text search.
func TestACommentedOutLockDoesNotCountAsLocked(t *testing.T) {
	text := strings.Replace(composeText(t),
		`      GITEA__security__INSTALL_LOCK: "true"`,
		`      # GITEA__security__INSTALL_LOCK: "true"`, 1)
	if strings.Contains(text, `# GITEA__security__INSTALL_LOCK`) == false {
		t.Fatal("could not comment out the lock; the mapping's exact text has moved")
	}
	if serviceEnvironment(t, text, "forge")["GITEA__security__INSTALL_LOCK"] == "true" {
		t.Error("a commented-out mapping was read as an effective lock")
	}
}

// Scoping proof: a lock on some other service must not satisfy the forge.
func TestALockOnAnotherServiceDoesNotCountForTheForge(t *testing.T) {
	text := composeText(t) + `
  decoy:
    environment:
      GITEA__security__INSTALL_LOCK: "true"
`
	if _, found := serviceEnvironment(t, text, "decoy")["GITEA__security__INSTALL_LOCK"]; !found {
		t.Fatal("the decoy service was not parsed; this test proves nothing as written")
	}
	stripped := strings.Replace(text,
		`      GITEA__security__INSTALL_LOCK: "true"
    ports:`, `    ports:`, 1)
	if serviceEnvironment(t, stripped, "forge")["GITEA__security__INSTALL_LOCK"] == "true" {
		t.Error("the forge was read as locked because a different service carries the lock")
	}
}

// Locking setup removes the browser path to the first administrator, so the
// guide must carry the CLI replacement. Hardening the port while leaving the
// documented first step impossible would be a worse outcome than the defect.
func TestGuideCarriesTheAdministratorStepTheLockRemoves(t *testing.T) {
	data, err := os.ReadFile("../FORGE.md")
	if err != nil {
		t.Fatalf("reading FORGE.md: %v", err)
	}
	guide := string(data)
	for _, needed := range []string{"admin user create", "127.0.0.1:3300"} {
		if !strings.Contains(guide, needed) {
			t.Errorf("FORGE.md does not mention %q; the locked installer leaves no other way in", needed)
		}
	}
}
