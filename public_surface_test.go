package gitseq_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"unicode"
)

const publicRepository = "https://github.com/generalbusiness-ai/gitseq"

var emailAddress = regexp.MustCompile(`(?i)\b[[:alnum:]._%+-]+@[[:alnum:].-]+\.[[:alpha:]]{2,}\b`)

// TestPublicRepositorySurface pins the links and least-privilege settings
// that make this checkout safe to present as a technical preview.
func TestPublicRepositorySurface(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate public surface test")
	}
	root := filepath.Dir(filename)

	read := func(path string) string {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return string(content)
	}

	for path, required := range map[string][]string{
		"README.md": {
			publicRepository + ".git",
			"[MIT License](LICENSE)",
			"[security policy](SECURITY.md)",
			"Report vulnerabilities privately and directly to the maintainer",
			"never use a public issue or gitseq workroom",
		},
		"docs/getting-started.md": {
			"git clone " + publicRepository + ".git",
		},
		"SECURITY.md": {
			publicRepository + "/issues",
			"technical preview",
			"There are no supported release branches",
			"GitHub Issues are public",
			"suitable for ordinary use and support questions",
			"Do not put a vulnerability, exploit, credential, private repository content, or personal data in an issue or in a gitseq workroom",
			"Report vulnerabilities privately and directly to the maintainer",
			"not open a public issue for a security report",
			"GitHub settings outside this source tree",
			"Source CI cannot guarantee those settings",
			"Recheck them before every public update",
		},
		".github/workflows/ci.yml": {
			"permissions:\n  contents: read",
			"set -o pipefail",
			"go test -race -count=1 -json ./...",
			"make spike SPIKE_TEST_JSON=",
			"git diff --exit-code",
			".github/scripts/verify-preview-clone.sh",
		},
		".github/scripts/verify-preview-clone.sh": {
			"$head:refs/heads/main",
			"test \"$refs\" = \"refs/heads/main\"",
			"make -C \"$checkout\" vet build",
		},
	} {
		content := read(path)
		compact := strings.Join(strings.Fields(content), " ")
		for _, fragment := range required {
			if !strings.Contains(compact, strings.Join(strings.Fields(fragment), " ")) {
				t.Errorf("%s does not contain required public surface %q", path, fragment)
			}
		}
	}

	for path, forbidden := range map[string][]string{
		"README.md":   {"security/advisories/new", "private advisory channel"},
		"SECURITY.md": {"security/advisories/new", "private advisory channel"},
	} {
		content := strings.Join(strings.Fields(read(path)), " ")
		if emailAddress.MatchString(content) {
			t.Errorf("%s contains a personal email address", path)
		}
		for _, fragment := range forbidden {
			if strings.Contains(content, fragment) {
				t.Errorf("%s contains obsolete or personal disclosure channel %q", path, fragment)
			}
		}
	}

	workflow := read(".github/workflows/ci.yml")
	checkoutStart := strings.Index(workflow, "uses: actions/checkout@")
	if checkoutStart < 0 {
		t.Fatal("CI workflow does not contain an actions/checkout step")
	}
	checkoutStep := workflow[checkoutStart:]
	if nextStep := strings.Index(checkoutStep, "\n      - name:"); nextStep >= 0 {
		checkoutStep = checkoutStep[:nextStep]
	}
	for _, setting := range []string{"persist-credentials: false", "fetch-depth: 0"} {
		if !strings.Contains(checkoutStep, setting) {
			t.Errorf("actions/checkout step does not contain %q", setting)
		}
	}

	ignore := read(".gitignore")
	for _, pattern := range []string{"/.claude/", "/.tmp/"} {
		if !strings.Contains(ignore, pattern) {
			t.Errorf(".gitignore does not contain root-local pattern %q", pattern)
		}
	}
}

// A manual performance dispatch starts from the branch selected in GitHub's
// UI, which does not make a separately named base or candidate branch a local
// ref. Keep the workflow's resolver explicit: the old direct pass-through let
// base=main and a feature candidate reach gitseq-perf with only one checkout,
// so the documented default was not a truthful contract.
func TestPerformanceWorkflowResolvesManualRefs(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate performance workflow test")
	}
	content, err := os.ReadFile(filepath.Join(filepath.Dir(filename), ".github", "workflows", "performance.yml"))
	if err != nil {
		t.Fatalf("read performance workflow: %v", err)
	}
	workflow := strings.Join(strings.Fields(string(content)), " ")
	for _, fragment := range []string{
		"name: Resolve manual comparison refs",
		"if: github.event_name == 'workflow_dispatch'",
		"PERF_BASE_INPUT: ${{ inputs.base }}",
		"PERF_CANDIDATE_INPUT: ${{ inputs.candidate }}",
		"resolve_ref()",
		"if [[ \"$input\" =~ ^[0-9a-fA-F]{40}$ ]]; then",
		"git rev-parse --verify --end-of-options \"$input^{commit}\" || return",
		"git check-ref-format \"refs/heads/$input\" >/dev/null || return",
		"git fetch --no-tags origin \"refs/heads/$input:refs/remotes/origin/$input\" || return",
		"resolved=$(resolve_ref \"$PERF_BASE_INPUT\")",
		"printf 'PERF_BASE=%s\\n' \"$resolved\" >> \"$GITHUB_ENV\"",
		"resolved=$(resolve_ref \"$PERF_CANDIDATE_INPUT\")",
		"printf 'PERF_CANDIDATE=%s\\n' \"$resolved\" >> \"$GITHUB_ENV\"",
		"PERF_BASE=HEAD^",
		"PERF_CANDIDATE=HEAD",
	} {
		if !strings.Contains(workflow, strings.Join(strings.Fields(fragment), " ")) {
			t.Errorf("performance workflow does not resolve manual refs with %q", fragment)
		}
	}
	if strings.Contains(workflow, "PERF_BASE: ${{ github.event_name == 'workflow_dispatch'") ||
		strings.Contains(workflow, "PERF_CANDIDATE: ${{ github.event_name == 'workflow_dispatch'") {
		t.Error("performance workflow still passes unverified manual refs straight to gitseq-perf")
	}
	if strings.Contains(workflow, "git fetch --no-tags origin \"$input\"") {
		t.Error("performance workflow fetches exact SHA input instead of verifying the full checkout")
	}
}

var inlineMarkdownLink = regexp.MustCompile(`\[[^]]*\]\(([^)[:space:]]+)\)`)

// TestLocalMarkdownLinks keeps file links and their heading anchors checkable
// without granting CI a network credential or depending on a third-party bot.
func TestLocalMarkdownLinks(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate link test")
	}
	root := filepath.Dir(filename)

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".tmp", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}

		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, match := range inlineMarkdownLink.FindAllStringSubmatch(string(content), -1) {
			target := match[1]
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") ||
				strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "#") {
				continue
			}

			filePart, fragment, _ := strings.Cut(target, "#")
			linkedPath := filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(filePart)))
			info, statErr := os.Stat(linkedPath)
			if statErr != nil {
				t.Errorf("%s links to missing %s: %v", filepath.ToSlash(path), target, statErr)
				continue
			}
			if fragment == "" {
				continue
			}
			if info.IsDir() {
				t.Errorf("%s links to heading in directory %s", filepath.ToSlash(path), target)
				continue
			}
			linked, linkedErr := os.ReadFile(linkedPath)
			if linkedErr != nil {
				return linkedErr
			}
			if !markdownHasHeading(linked, fragment) {
				t.Errorf("%s links to missing heading %s", filepath.ToSlash(path), target)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func markdownHasHeading(content []byte, wanted string) bool {
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#") {
			continue
		}
		heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
		var slug strings.Builder
		lastHyphen := false
		for _, char := range strings.ToLower(heading) {
			switch {
			case unicode.IsLetter(char) || unicode.IsDigit(char):
				slug.WriteRune(char)
				lastHyphen = false
			case char == '-' || unicode.IsSpace(char):
				if slug.Len() > 0 && !lastHyphen {
					slug.WriteByte('-')
					lastHyphen = true
				}
			}
		}
		if strings.TrimSuffix(slug.String(), "-") == wanted {
			return true
		}
	}
	return false
}
