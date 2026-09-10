// Package forge holds the repository-native gate over the optional development
// forge lane's configuration. It reads compose.yaml as text and asserts the two
// properties that, when they were absent, put an unlocked Gitea installer on
// every network the host was attached to. It needs no daemon, no image pull and
// no network, so it runs in the ordinary suite rather than behind the profile
// the lane itself is gated on.
package forge

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const composePath = "../compose.yaml"

// loopbackHosts is the whole allowed set. The forge is reachable only from the
// host that runs it, so an address that is not one of these two is either a
// wildcard or a real interface, and both are the defect.
var loopbackHosts = map[string]bool{"127.0.0.1": true, "::1": true}

func composeText(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("reading %s: %v", composePath, err)
	}
	return string(data)
}

// binding is one published port entry, carrying the service that published it
// so a second service added later cannot hide behind the first, and the entry
// as written so a failure names something a reader can find in the file.
type binding struct {
	service string
	entry   string
	host    string // empty when the entry names no host at all
}

// publishedPorts decodes every service's ports block and returns one binding
// per entry, plus a reason for each entry it will not interpret.
//
// Refusing to interpret is the point. The check this replaced matched entries
// with a regular expression anchored to the end of the line and dropped
// everything that did not fit, silently. Docker's long form does not fit, so a
// mapping with host_ip 0.0.0.0 sat in the file and passed the gate whose only
// purpose was to catch exactly that. Anything unrecognised now fails loudly
// rather than passing quietly, which is the property that survives the next
// syntax nobody here anticipated.
//
// The whole stream is read, not the first document. Compose processes documents
// to end of file and merges them, so a safe first document followed by a second
// one carrying an exposed port is a real configuration, and reading only the
// first would reproduce the silent omission this check exists to remove. A
// document that will not decode is itself reported.
func publishedPorts(text string) (bindings []binding, unreadable []string) {
	decoder := yaml.NewDecoder(strings.NewReader(text))
	for document := 0; ; document++ {
		var file struct {
			Services map[string]struct {
				Ports []yaml.Node `yaml:"ports"`
			} `yaml:"services"`
		}
		switch err := decoder.Decode(&file); {
		case errors.Is(err, io.EOF):
			return bindings, unreadable
		case err != nil:
			// Not silently skipped: a document that will not parse is a
			// document whose ports nobody has seen.
			unreadable = append(unreadable, fmt.Sprintf("document %d does not parse: %v", document+1, err))
			return bindings, unreadable
		}
		found, why := documentPorts(file.Services)
		bindings = append(bindings, found...)
		unreadable = append(unreadable, why...)
	}
}

func documentPorts(services map[string]struct {
	Ports []yaml.Node `yaml:"ports"`
}) (bindings []binding, unreadable []string) {
	names := make([]string, 0, len(services))
	for service := range services {
		names = append(names, service)
	}
	sort.Strings(names)
	for _, service := range names {
		for _, node := range services[service].Ports {
			switch node.Kind {
			case yaml.ScalarNode:
				host, why := shortFormHost(node.Value)
				if why != "" {
					unreadable = append(unreadable, service+" port "+node.Value+" "+why)
					continue
				}
				bindings = append(bindings, binding{service: service, entry: node.Value, host: host})
			case yaml.MappingNode:
				host, written, why := longFormHost(node)
				if why != "" {
					unreadable = append(unreadable, service+" port "+written+" "+why)
					continue
				}
				bindings = append(bindings, binding{service: service, entry: written, host: host})
			default:
				unreadable = append(unreadable, service+" has a port entry that is neither a string nor a mapping")
			}
		}
	}
	return bindings, unreadable
}

// The supported grammar, stated once. A short entry is
// [HOST:[HOST_PORT]:]CONTAINER_PORT[/PROTOCOL], where a port is a number or a
// range of two, and the protocol is one Docker names. Anything else is
// reported rather than read: the point of this check is that it never clears
// what it does not understand, and a host address in front of a port it cannot
// parse is not a mapping anyone has verified.
var supportedProtocols = map[string]bool{"tcp": true, "udp": true}

// shortFormHost reads the host address out of a short entry, returning a reason
// instead when the entry is not one this check supports. A missing host is not
// a reason: it is the original defect, returned as the empty address so the
// gate rejects it with the others.
//
// The host is whatever precedes the last two colons. Reading from the right is
// what lets an unbracketed IPv6 loopback work, which Docker documents alongside
// the bracketed spelling and which the first version of this parser refused.
func shortFormHost(entry string) (host, why string) {
	if slash := strings.LastIndex(entry, "/"); slash >= 0 {
		protocol := entry[slash+1:]
		if !supportedProtocols[protocol] {
			return "", "names protocol " + protocol + ", which is not one this check knows"
		}
		entry = entry[:slash]
	}
	ports := entry
	if separator := strings.LastIndex(entry, ":"); separator >= 0 {
		if second := strings.LastIndex(entry[:separator], ":"); second >= 0 {
			host, ports = entry[:second], entry[second+1:]
		}
	}
	// The container port is the last slot and is never optional. Only the host
	// port may be left empty, which is how a short entry asks for an ephemeral
	// one, so a rule letting any slot be empty clears 127.0.0.1:3300: as though
	// it published something.
	slots := strings.Split(ports, ":")
	if slots[len(slots)-1] == "" {
		return "", "names no container port"
	}
	container, why := parsePortRange(slots[len(slots)-1])
	if why != "" {
		return "", why
	}
	if len(slots) > 1 && slots[0] != "" {
		// An empty host slot asks for an ephemeral port and names no range to
		// compare. A host range serving one container port is Docker's way of
		// saying "any of these"; anything else must match width for width.
		published, why := parsePortRange(slots[0])
		if why != "" {
			return "", why
		}
		if container.width() > 1 && published.width() != container.width() {
			return "", "pairs " + slots[0] + " with " + slots[len(slots)-1] + ", which are ranges of different widths"
		}
	}
	return bareOrBracketed(host)
}

// bareOrBracketed accepts an address written plainly or wrapped in exactly one
// pair of brackets, and nothing else. Trimming brackets off whatever arrives
// instead clears [::1:3300:3000, ::1]:3300:3000 and [[::1]]:3300:3000, none of
// which Docker writes, and each of which would then read as a loopback host.
func bareOrBracketed(host string) (string, string) {
	opened, closed := strings.Count(host, "["), strings.Count(host, "]")
	switch {
	case opened == 0 && closed == 0:
		return host, ""
	case opened == 1 && closed == 1 && strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]"):
		return host[1 : len(host)-1], ""
	default:
		return "", "writes the host address as " + host + ", which is not a bare address or one bracketed one"
	}
}

// portRange is one port, or a range of two, once it is known to be in bounds
// and in order. The width is what lets a paired range be compared without
// enumerating the ports between its endpoints.
type portRange struct{ low, high int }

func (r portRange) width() int { return r.high - r.low + 1 }

// parsePortRange reads "3000" or "3000-3010". Checking that every character is
// a digit is not the same as checking the number: 65536 and 99999999999999 are
// all digits and neither is a port, and 3010-3000 is two real ports in an
// order Docker refuses.
func parsePortRange(port string) (portRange, string) {
	low, high, ranged := strings.Cut(port, "-")
	first, why := parsePort(low, port)
	if why != "" {
		return portRange{}, why
	}
	if !ranged {
		return portRange{low: first, high: first}, ""
	}
	last, why := parsePort(high, port)
	if why != "" {
		return portRange{}, why
	}
	if last < first {
		return portRange{}, "names range " + port + ", which ends before it starts"
	}
	return portRange{low: first, high: last}, ""
}

// parsePort reads one decimal port. strconv settles both questions at once -
// that the text is a number, and that it fits 0-65535 - and it accepts the
// zero-padded spellings Docker accepts, which a cap on the written length
// would refuse for representing 3000 with four extra characters.
func parsePort(port, whole string) (int, string) {
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return 0, "names port " + whole + ", which is not a number in 0-65535"
	}
	return int(number), ""
}

// longFormMappingFields is what Docker documents for a long port mapping. A key
// outside this set means the file is using syntax this check has never read,
// and reading it as if the unknown key changed nothing would be a guess.
var longFormMappingFields = map[string]bool{
	"target": true, "published": true, "host_ip": true,
	"protocol": true, "app_protocol": true, "mode": true, "name": true,
}

// longFormHost reads host_ip out of a long mapping. An omitted host_ip is
// Docker's documented "every interface", so it returns the empty address rather
// than a reason: the entry is understood perfectly and is unsafe.
func longFormHost(node yaml.Node) (host, written, why string) {
	var hostIP, target, published string
	// Decoding into a struct rejects a repeated key. Reading the node by hand
	// does not, and last value wins, so a mapping naming host_ip 0.0.0.0 and
	// then 127.0.0.1 would read as loopback.
	seen := make(map[string]bool, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if seen[key.Value] {
			return "", "mapping", "names " + key.Value + " more than once"
		}
		seen[key.Value] = true
		if !longFormMappingFields[key.Value] {
			return "", "mapping", "names " + key.Value + ", which is not a documented long-form field"
		}
		if value.Kind != yaml.ScalarNode {
			return "", "mapping", "gives " + key.Value + " a value that is not a single scalar"
		}
		switch key.Value {
		case "host_ip":
			hostIP = value.Value
		case "target":
			target = value.Value
		case "published":
			published = value.Value
		case "protocol":
			if !supportedProtocols[value.Value] {
				return "", "mapping", "names protocol " + value.Value + ", which is not one this check knows"
			}
		}
	}
	written = "target " + target
	if published != "" {
		written = "published " + published + " to " + written
	}
	if target == "" {
		return "", written, "names no target port"
	}
	for _, port := range []string{target, published} {
		if port == "" {
			continue
		}
		if _, why := parsePortRange(port); why != "" {
			return "", written, why
		}
	}
	return hostIP, written, ""
}

// A published port with no host address binds every interface. The development
// forge is reachable only from the host that runs it, and that is the whole of
// its trust boundary: it has no authentication story of its own worth exposing.
func TestEveryPublishedPortBindsLoopback(t *testing.T) {
	for _, finding := range unsafeBindings(t, composeText(t)) {
		t.Error(finding)
	}
}

// unsafeBindings is the judgement itself, kept apart from the file it reads so
// the fixtures below exercise the same code the shipped configuration does.
// An entry this check cannot read counts as unsafe: the alternative is the
// silent skip that let the long form through in the first place.
func unsafeBindings(t *testing.T, text string) []string {
	t.Helper()
	bindings, unreadable := publishedPorts(text)
	if len(bindings) == 0 && len(unreadable) == 0 {
		t.Fatal("no published ports found; the parser has stopped seeing the ports block")
	}
	var findings []string
	for _, entry := range unreadable {
		findings = append(findings, entry+"; a port entry this check cannot read is not a port entry it can clear")
	}
	for _, published := range bindings {
		switch {
		case published.host == "":
			findings = append(findings, published.service+" publishes "+published.entry+" with no host address, so it binds every interface; give it an explicit loopback host")
		case !loopbackHosts[published.host]:
			findings = append(findings, published.service+" publishes "+published.entry+" on "+published.host+", which is not loopback")
		}
	}
	return findings
}

// The fixtures are written here rather than derived from the shipped file, so
// that a change to the shipped file cannot quietly weaken what they assert.
// Each one names the boundary it sits on.
func TestThePortGateReadsBothDocumentedForms(t *testing.T) {
	const safeShort = `services:
  forge:
    ports:
      - "127.0.0.1:3300:3000"
`
	cases := []struct {
		name   string
		text   string
		unsafe bool
		says   string
	}{
		{name: "the shipped short form", text: safeShort},
		{name: "bracketed IPv6 loopback", text: `services:
  forge:
    ports:
      - "[::1]:3300:3000"
`},
		{name: "short form with a protocol suffix", text: `services:
  forge:
    ports:
      - "127.0.0.1:3300:3000/tcp"
`},
		{name: "safe long form", text: `services:
  forge:
    ports:
      - target: 3000
        published: "3300"
        host_ip: 127.0.0.1
        protocol: tcp
`},
		{name: "wildcard short form", text: `services:
  forge:
    ports:
      - "0.0.0.0:3300:3000"
`, unsafe: true, says: "which is not loopback"},
		{name: "short form naming no host", text: `services:
  forge:
    ports:
      - "3300:3000"
`, unsafe: true, says: "binds every interface"},
		{name: "container port alone", text: `services:
  forge:
    ports:
      - "3000"
`, unsafe: true, says: "binds every interface"},
		{name: "wildcard long form after a safe entry", text: safeShort + `      - target: 3000
        published: "3301"
        host_ip: 0.0.0.0
        protocol: tcp
`, unsafe: true, says: "which is not loopback"},
		{name: "long form omitting host_ip", text: `services:
  forge:
    ports:
      - target: 3000
        published: "3301"
        protocol: tcp
`, unsafe: true, says: "binds every interface"},
		{name: "a second service carrying the unsafe mapping", text: safeShort + `  other:
    ports:
      - "0.0.0.0:9000:9000"
`, unsafe: true, says: "other publishes"},
		{name: "a long form using an undocumented field", text: `services:
  forge:
    ports:
      - target: 3000
        published: "3300"
        host_ip: 127.0.0.1
        bind: everywhere
`, unsafe: true, says: "cannot read"},
		{name: "a port entry that is a sequence", text: `services:
  forge:
    ports:
      - ["127.0.0.1", 3300, 3000]
`, unsafe: true, says: "cannot read"},
		{name: "a short form with too many fields", text: `services:
  forge:
    ports:
      - "127.0.0.1:3300:3000:extra"
`, unsafe: true, says: "cannot read"},
		// The three findings of the first review, each with the control that
		// would have caught it. A safe first document hiding an unsafe second
		// one is the same silent omission this whole check exists to remove,
		// arriving one layer further out.
		{name: "an unsafe second document", text: safeShort + `---
services:
  other:
    ports:
      - "0.0.0.0:9000:9000"
`, unsafe: true, says: "other publishes"},
		{name: "a second document that does not parse", text: safeShort + `---
: : not yaml
`, unsafe: true, says: "does not parse"},
		{name: "a container port that is not a number", text: `services:
  forge:
    ports:
      - "127.0.0.1:3300:not-a-port"
`, unsafe: true, says: "cannot read"},
		{name: "a protocol Docker does not name", text: `services:
  forge:
    ports:
      - "127.0.0.1:3300:3000/not-a-protocol"
`, unsafe: true, says: "cannot read"},
		{name: "unbracketed IPv6 loopback", text: `services:
  forge:
    ports:
      - "::1:6000:6000"
`},
		{name: "an IPv4 port range", text: `services:
  forge:
    ports:
      - "127.0.0.1:3300-3310:3000-3010"
`},
		{name: "a bracketed IPv6 port range", text: `services:
  forge:
    ports:
      - "[::1]:3300-3310:3000-3010"
`},
		{name: "an ephemeral host port", text: `services:
  forge:
    ports:
      - "127.0.0.1::3000"
`},
		{name: "a long form target that is not a number", text: `services:
  forge:
    ports:
      - target: not-a-port
        published: "3300"
        host_ip: 127.0.0.1
`, unsafe: true, says: "cannot read"},
		{name: "a long form field given a list", text: `services:
  forge:
    ports:
      - target: 3000
        published: "3300"
        host_ip: [127.0.0.1]
`, unsafe: true, says: "cannot read"},
		// The five findings of the second review. Each was cleared by the
		// previous head: an allowed host in the first slot was enough to carry
		// the rest of the entry past the check.
		{name: "an empty container port", text: `services:
  forge:
    ports:
      - "127.0.0.1:3300:"
`, unsafe: true, says: "names no container port"},
		{name: "an unclosed bracket", text: `services:
  forge:
    ports:
      - "[::1:3300:3000"
`, unsafe: true, says: "not a bare address or one bracketed one"},
		{name: "a closing bracket with no opening one", text: `services:
  forge:
    ports:
      - "::1]:3300:3000"
`, unsafe: true, says: "not a bare address or one bracketed one"},
		{name: "doubled brackets", text: `services:
  forge:
    ports:
      - "[[::1]]:3300:3000"
`, unsafe: true, says: "not a bare address or one bracketed one"},
		{name: "a long form naming host_ip twice", text: `services:
  forge:
    ports:
      - target: 3000
        published: "3300"
        host_ip: 0.0.0.0
        host_ip: 127.0.0.1
`, unsafe: true, says: "more than once"},
		// The third review's findings. Every character being a digit is not the
		// same as the number being a port, and two real ports can be written in
		// an order or a pairing Docker refuses.
		{name: "the highest real port", text: `services:
  forge:
    ports:
      - "127.0.0.1:65535:65535"
`},
		{name: "a host range serving one container port", text: `services:
  forge:
    ports:
      - "127.0.0.1:3300-3310:3000"
`},
		// Docker parses these as 3000 and 3300. A cap on how many characters a
		// port is written with refuses them, which is a false refusal of a
		// configuration that works.
		{name: "a zero-padded container port", text: `services:
  forge:
    ports:
      - "127.0.0.1:3300:003000"
`},
		{name: "a zero-padded long form published port", text: `services:
  forge:
    ports:
      - target: 3000
        published: "003300"
        host_ip: 127.0.0.1
`},
		{name: "a host port above the range", text: `services:
  forge:
    ports:
      - "127.0.0.1:65536:3000"
`, unsafe: true, says: "0-65535"},
		{name: "a container port above the range", text: `services:
  forge:
    ports:
      - "127.0.0.1:3300:65536"
`, unsafe: true, says: "0-65535"},
		{name: "a digit string far too long", text: `services:
  forge:
    ports:
      - "127.0.0.1:3300:99999999999999"
`, unsafe: true, says: "0-65535"},
		{name: "a container range that ends before it starts", text: `services:
  forge:
    ports:
      - "127.0.0.1:3000-3010:3010-3000"
`, unsafe: true, says: "ends before it starts"},
		{name: "paired ranges of different widths", text: `services:
  forge:
    ports:
      - "127.0.0.1:3300-3310:3000-3005"
`, unsafe: true, says: "different widths"},
		{name: "a long form target above the range", text: `services:
  forge:
    ports:
      - target: 65536
        published: "3300"
        host_ip: 127.0.0.1
`, unsafe: true, says: "0-65535"},
		{name: "a long form published range that ends before it starts", text: `services:
  forge:
    ports:
      - target: 3000
        published: "3310-3300"
        host_ip: 127.0.0.1
`, unsafe: true, says: "ends before it starts"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			findings := unsafeBindings(t, test.text)
			switch {
			case test.unsafe && len(findings) == 0:
				t.Error("the gate cleared a mapping it should have rejected")
			case !test.unsafe && len(findings) > 0:
				t.Errorf("the gate rejected a supported safe mapping: %s", strings.Join(findings, "; "))
			case test.unsafe && !strings.Contains(strings.Join(findings, "; "), test.says):
				t.Errorf("the gate rejected it for the wrong reason: wanted %q in %q", test.says, strings.Join(findings, "; "))
			}
		})
	}
}

// The exact addition the request attached, kept in the tree so the claim that
// it fails is checkable rather than remembered. It is the shipped file with one
// long-form entry appended after the safe short-form one.
func TestTheAttachedLongFormAdditionIsRejected(t *testing.T) {
	data, err := os.ReadFile("testdata/unsafe-long-addition.yaml")
	if err != nil {
		t.Fatalf("reading the attached fixture: %v", err)
	}
	findings := unsafeBindings(t, string(data))
	if len(findings) != 1 {
		t.Fatalf("wanted exactly the one added entry rejected, got %d: %s", len(findings), strings.Join(findings, "; "))
	}
	if !strings.Contains(findings[0], "0.0.0.0") {
		t.Errorf("the rejection does not name the wildcard address: %q", findings[0])
	}
}

// Why the fixture above is not redundant with the table: this reintroduces the
// parser that shipped, and asserts what it did with that same file. It found
// the safe entry, found nothing else, and reported no error — a green gate over
// an exposed port. The gap was a syntax blind spot, not an unexecuted check,
// and only an assertion against the old behaviour says so.
func TestTheParserThisReplacedSawOnlyTheSafeEntry(t *testing.T) {
	data, err := os.ReadFile("testdata/unsafe-long-addition.yaml")
	if err != nil {
		t.Fatalf("reading the attached fixture: %v", err)
	}
	// The original: a regular expression over the lines of a ports block,
	// anchored to the end of the line.
	scalarOnly := regexp.MustCompile(`^\s*-\s*"?([^"\s]+)"?\s*$`)
	var seen []string
	inPorts := false
	for _, line := range strings.Split(string(data), "\n") {
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
			if match := scalarOnly.FindStringSubmatch(line); match != nil {
				seen = append(seen, match[1])
			}
		}
	}
	if len(seen) != 1 || seen[0] != "127.0.0.1:3300:3000" {
		t.Fatalf("the reintroduced parser did not behave as it shipped: %q", seen)
	}
	if findings := unsafeBindings(t, string(data)); len(findings) == 0 {
		t.Error("the replacement finds nothing either, so this change fixes nothing")
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
