// Package ciguard holds the repository's checks over its own automation. The
// workflows decide what runs before code lands, so they are security surface,
// and nothing else in the build inspects them.
//
// Everything here decodes YAML rather than matching text. An earlier revision
// used regular expressions and a hand-written line parser, which cannot tell a
// value from a comment, an alias from a literal, a nested key from a top-level
// one, or a duplicate from an override - so it could report a workflow safe on
// the strength of a string that never takes effect.
//
// The rules live in this file rather than in the tests so that the tests can
// hold them against fixtures they construct: a rule that exists only inline in
// a test over the real workflows can never be shown to reject anything,
// because the real workflows are kept clean.
package ciguard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type workflow struct {
	Name        string         `yaml:"name"`
	Permissions any            `yaml:"permissions"`
	Jobs        map[string]job `yaml:"jobs"`
	On          any            `yaml:"on"`
}

type job struct {
	Permissions any    `yaml:"permissions"`
	Steps       []step `yaml:"steps"`
	// A reusable-workflow job carries `uses` at the job level and has no
	// steps. Leaving it unmodelled meant an unpinned reusable workflow passed
	// the pin check by being invisible to it.
	Uses string `yaml:"uses"`
	// Job-level containers and service containers pull images this gate does
	// not model or pin, so their presence is refused outright rather than
	// silently ignored. Modelled as `any` so that every YAML shape - string,
	// mapping, anything - is seen and refused, not just the shapes a stricter
	// type would happen to decode.
	Container any `yaml:"container"`
	Services  any `yaml:"services"`
}

type step struct {
	Name string `yaml:"name"`
	Uses string `yaml:"uses"`
	Run  string `yaml:"run"`
}

// parseWorkflow decodes one workflow file.
//
// Deliberately not KnownFields: workflows legitimately carry keys these checks
// do not own - concurrency, env, timeouts - and rejecting them would make this
// gate an obstacle rather than a check. The cost is that a security-relevant
// construct these structs do not model is invisible here, so every such
// location must be modelled explicitly. An earlier revision claimed
// KnownFields was enabled; it was not.
func parseWorkflow(data []byte) (workflow, error) {
	var decoded workflow
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		return workflow{}, err
	}
	return decoded, nil
}

// permissionsAllowlist is the exact token grant every declaring boundary must
// carry: each named scope must be present with exactly the named level, and no
// other scope may appear. These jobs read code and nothing else, so the list
// is one entry long; a job that ever needs more must widen this list in
// review, not widen a workflow quietly.
var permissionsAllowlist = map[string]string{
	"contents": "read",
}

// checkPermissions holds one declared permissions value - workflow-level or a
// job-level override, the same rule at both boundaries, since an override is
// exactly where a wider scope would be introduced - to the exact allowlist.
//
// A scalar form is refused whatever it says: `write-all` grants everything,
// `read-all` grants read on scopes these jobs never touch, and `none` is not a
// scoped grant either - the required `contents: read` cannot be expressed as a
// scalar. Only a mapping that matches the allowlist exactly passes: an
// unexpected scope is refused, a write grant of any kind is refused, and an
// omitted required scope is refused rather than inherited.
func checkPermissions(where string, value any) []string {
	var violations []string
	switch declared := value.(type) {
	case string:
		violations = append(violations, fmt.Sprintf(
			"%s declares permissions as the scalar %q; a scalar cannot be scoped, and only the exact mapping {contents: read} is allowed",
			where, declared))
	case map[string]any:
		scopes := make([]string, 0, len(declared))
		for scope := range declared {
			scopes = append(scopes, scope)
		}
		sort.Strings(scopes)
		for _, scope := range scopes {
			want, allowed := permissionsAllowlist[scope]
			if !allowed {
				violations = append(violations, fmt.Sprintf(
					"%s grants unexpected scope %q; the allowlist is exactly {contents: read}",
					where, scope))
				continue
			}
			level, ok := declared[scope].(string)
			if !ok {
				violations = append(violations, fmt.Sprintf(
					"%s grants %q a non-string level %v", where, scope, declared[scope]))
				continue
			}
			if level != want {
				violations = append(violations, fmt.Sprintf(
					"%s grants %q level %q; the allowlist admits only %q",
					where, scope, level, want))
			}
		}
		required := make([]string, 0, len(permissionsAllowlist))
		for scope := range permissionsAllowlist {
			required = append(required, scope)
		}
		sort.Strings(required)
		for _, scope := range required {
			if _, present := declared[scope]; !present {
				violations = append(violations, fmt.Sprintf(
					"%s omits the required grant %s: %s; an absent scope inherits the default, which is wider",
					where, scope, permissionsAllowlist[scope]))
			}
		}
	default:
		violations = append(violations, fmt.Sprintf(
			"%s declares permissions in an unrecognised form %T", where, value))
	}
	return violations
}

var commitPin = regexp.MustCompile(`^[^@]+@[0-9a-f]{40}$`)

// classifyUses is the one admission rule for every `uses` reference in the
// automation. Step `uses` and job-level `uses` both come here: a duplicated
// rule is the thing that drifts, and the job-level copy is the one that would
// drift unnoticed.
//
// Admitted: a same-repository reusable workflow or local action (they are this
// reviewed tree), and an external reference pinned to a full 40-hex commit
// SHA. A tag or branch reference can be moved by whoever controls the action,
// so a commit pin is the difference between a supply chain we chose and one
// that can be changed under us after review.
//
// Refused explicitly rather than ignored: `docker://` image references, local
// actions whose manifest cannot be read, docker-based local actions, and any
// nested reference inside a composite manifest that this same rule would
// refuse. `seen` breaks manifest cycles; pass a fresh map at each top-level
// call.
func classifyUses(root, ref string, seen map[string]bool) error {
	if strings.HasPrefix(ref, "docker://") {
		return fmt.Errorf("%q is a container image reference, which this gate does not model or pin", ref)
	}
	if strings.HasPrefix(ref, "./") {
		return classifyLocal(root, ref, seen)
	}
	if !commitPin.MatchString(ref) {
		return fmt.Errorf("%q is external and not pinned to a full 40-hex commit SHA", ref)
	}
	return nil
}

// classifyLocal admits same-repository references. A reusable workflow under
// .github/workflows is admitted if it exists, because the workflow walk in
// this package checks it like any other workflow. Anything else is a local
// action, whose manifest is read: a composite manifest is admitted only when
// every nested `uses` passes classifyUses - otherwise a composite action would
// be the unwatched door external references walk in through - and a manifest
// that is missing, unreadable, docker-based, or of an unknown kind is refused
// because this gate cannot model what it pulls.
func classifyLocal(root, ref string, seen map[string]bool) error {
	rel := filepath.Clean(strings.TrimPrefix(ref, "./"))
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%q escapes the repository", ref)
	}
	if strings.HasPrefix(rel, ".github/workflows/") &&
		(strings.HasSuffix(rel, ".yml") || strings.HasSuffix(rel, ".yaml")) {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			return fmt.Errorf("%q names a reusable workflow that does not exist: %v", ref, err)
		}
		return nil
	}
	if seen[rel] {
		return nil
	}
	seen[rel] = true
	var manifest struct {
		Runs struct {
			Using string `yaml:"using"`
			Steps []step `yaml:"steps"`
		} `yaml:"runs"`
	}
	data, err := os.ReadFile(filepath.Join(root, rel, "action.yml"))
	if err != nil {
		data, err = os.ReadFile(filepath.Join(root, rel, "action.yaml"))
	}
	if err != nil {
		return fmt.Errorf("%q has no readable action manifest, so this gate cannot model what it runs", ref)
	}
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("%q has an action manifest that is not valid YAML: %v", ref, err)
	}
	switch using := manifest.Runs.Using; {
	case using == "composite":
		for _, s := range manifest.Runs.Steps {
			if s.Uses == "" {
				continue
			}
			if err := classifyUses(root, s.Uses, seen); err != nil {
				return fmt.Errorf("%q is a composite action whose step is refused: %v", ref, err)
			}
		}
		return nil
	case strings.HasPrefix(using, "node"):
		return nil
	default:
		return fmt.Errorf("%q runs via %q, which this gate does not model or pin", ref, manifest.Runs.Using)
	}
}

// checkWorkflow returns every admissibility violation in one parsed workflow:
// the exact permissions allowlist at the workflow boundary and at every
// job-level override, the one `uses` classifier over job-level and step-level
// references alike, and the explicit refusal of job containers and service
// containers. root is the repository root, needed to resolve local references.
func checkWorkflow(root, name string, flow workflow) []string {
	var violations []string
	if flow.Permissions == nil {
		// An absent block inherits the default token scope, far wider than
		// any of these jobs needs; a job-level declaration can stand in for
		// the missing workflow-level one, held to the same allowlist below.
		for jobName, j := range flow.Jobs {
			if j.Permissions == nil {
				violations = append(violations, fmt.Sprintf(
					"%s declares no permissions at the workflow level and job %q declares none either, so the job inherits the default token scope",
					name, jobName))
			}
		}
	} else {
		violations = append(violations, checkPermissions(name, flow.Permissions)...)
	}
	jobNames := make([]string, 0, len(flow.Jobs))
	for jobName := range flow.Jobs {
		jobNames = append(jobNames, jobName)
	}
	sort.Strings(jobNames)
	for _, jobName := range jobNames {
		j := flow.Jobs[jobName]
		where := name + " job " + jobName
		if j.Permissions != nil {
			violations = append(violations, checkPermissions(where, j.Permissions)...)
		}
		if j.Container != nil {
			violations = append(violations, fmt.Sprintf(
				"%s declares a job container image, which this gate does not model or pin", where))
		}
		if j.Services != nil {
			violations = append(violations, fmt.Sprintf(
				"%s declares service container images, which this gate does not model or pin", where))
		}
		seen := map[string]bool{}
		if j.Uses != "" {
			// The job's own `uses`, for a reusable workflow, is as much a
			// supply-chain edge as any step's, and goes through the same
			// classifier.
			if err := classifyUses(root, j.Uses, seen); err != nil {
				violations = append(violations, fmt.Sprintf("%s: %v", where, err))
			}
		}
		for _, s := range j.Steps {
			if s.Uses == "" {
				continue
			}
			if err := classifyUses(root, s.Uses, seen); err != nil {
				violations = append(violations, fmt.Sprintf("%s: %v", where, err))
			}
		}
	}
	return violations
}
