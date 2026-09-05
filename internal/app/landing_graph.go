package app

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

// These bounds cover one advisory read, independently of workroom size. A
// missing object or exhausted traversal is unknown, never negative evidence.
const (
	landingRefLimit    = 4096
	landingObjectLimit = 4096
	landingTipLimit    = 256
	landingNodeLimit   = 20000
	landingWalkLimit   = 300000
)

type landingAncestors struct {
	nodes    map[string]bool
	complete bool
}

// landingGraph captures immutable object IDs once, then answers all rows from
// the same bounded graph. No query starts a per-row Git process.
type landingGraph struct {
	refs          map[string]string
	refsKnown     bool
	objects       map[string]bool
	parents       map[string][]string
	ancestors     map[string]landingAncestors
	shallow       bool
	walkRemaining int
}

func exactObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func landingGit(ctx context.Context, repo, input string, limit int, args ...string) ([]byte, error) {
	cmd := repositoryLocalGit(ctx, repo, args...)
	// Advisory inspection must not fetch missing promisor objects or let a
	// replacement object change the ancestry of the named commit.
	cmd.Env = append(cmd.Env, "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1")
	cmd.Stdin = strings.NewReader(input)
	output := &boundedBuffer{limit: limit}
	cmd.Stdout = output
	cmd.Stderr = &boundedBuffer{limit: 4096}
	err := cmd.Run()
	return output.buffer.Bytes(), err
}

func readLandingRefs(ctx context.Context, repo string) *landingGraph {
	g := &landingGraph{refs: map[string]string{}, objects: map[string]bool{}, parents: map[string][]string{}, ancestors: map[string]landingAncestors{}, walkRemaining: landingWalkLimit}
	data, err := landingGit(ctx, repo, "", 2<<20, "for-each-ref", "--count="+strconv.Itoa(landingRefLimit+1), "--format=%(refname)%00%(objectname)", "refs/heads/", "refs/remotes/")
	if err != nil {
		return g
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) > landingRefLimit {
		return g
	}
	for _, line := range lines {
		if line == "" {
			continue
		}
		ref, oid, ok := strings.Cut(line, "\x00")
		if !ok || !exactObjectID(oid) {
			return g
		}
		g.refs[ref] = oid
	}
	g.refsKnown = true
	return g
}

func (g *landingGraph) load(ctx context.Context, repo string, tips, objects []string) {
	unique := func(values []string, limit int) []string {
		set := map[string]bool{}
		for _, value := range values {
			if exactObjectID(value) {
				set[value] = true
			}
		}
		out := make([]string, 0, len(set))
		for value := range set {
			out = append(out, value)
		}
		sort.Strings(out)
		if len(out) > limit {
			out = out[:limit]
		}
		return out
	}
	tips = unique(tips, landingTipLimit)
	objects = unique(append(objects, tips...), landingObjectLimit)
	if len(objects) == 0 {
		return
	}
	data, err := landingGit(ctx, repo, strings.Join(objects, "\n")+"\n", 1<<20, "cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		id, kind, ok := strings.Cut(line, " ")
		if ok && exactObjectID(id) && kind == "commit" {
			g.objects[id] = true
		}
	}
	if len(tips) == 0 {
		return
	}
	shallow, err := landingGit(ctx, repo, "", 32, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return
	}
	g.shallow = strings.TrimSpace(string(shallow)) != "false"
	args := append([]string{"rev-list", "--parents", "--max-count=" + strconv.Itoa(landingNodeLimit)}, tips...)
	args = append(args, "--")
	data, err = landingGit(ctx, repo, "", 4<<20, args...)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		for _, id := range fields {
			if !exactObjectID(id) {
				return
			}
		}
		g.parents[fields[0]] = fields[1:]
	}
}

// contains returns nil when available objects cannot settle the answer. A
// complete traversal can prove absence; merely hitting the node cap cannot.
func (g *landingGraph) contains(tip, object string) *bool {
	if !g.objects[object] || !g.objects[tip] {
		return nil
	}
	if tip == object {
		value := true
		return &value
	}
	a, cached := g.ancestors[tip]
	if !cached {
		a = landingAncestors{nodes: map[string]bool{}, complete: !g.shallow}
		pending := []string{tip}
		for len(pending) > 0 {
			if g.walkRemaining == 0 {
				a.complete = false
				break
			}
			id := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			if a.nodes[id] {
				continue
			}
			g.walkRemaining--
			a.nodes[id] = true
			parents, known := g.parents[id]
			if !known {
				a.complete = false
				continue
			}
			pending = append(pending, parents...)
		}
		g.ancestors[tip] = a
	}
	if a.nodes[object] {
		value := true
		return &value
	}
	if a.complete {
		value := false
		return &value
	}
	return nil
}

// landingRemote chooses the same configured remote as the local repository
// display. Only its name leaves this helper; credential-bearing URLs do not.
func landingRemote(ctx context.Context, repo string) string {
	remotes := gitRemotes(ctx, repo)
	if _, ok := remotes["origin"]; ok {
		return "origin"
	}
	names := make([]string, 0, len(remotes))
	for name := range remotes {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// remoteTrackingRef respects the configured fetch mapping. An unsupported or
// ambiguous mapping is unknown; guessing refs/remotes/<name> would misreport
// repositories with custom refspecs. Negative refspecs conservatively refuse.
func remoteTrackingRefs(ctx context.Context, repo, remote string, targets []string) map[string]string {
	result := map[string]string{}
	if remote == "" {
		return result
	}
	data, err := landingGit(ctx, repo, "", maxRemoteConfigBytes, "config", "--local", "--no-includes", "--get-all", "remote."+remote+".fetch")
	if err != nil {
		return result
	}
	for _, target := range targets {
		matched := ""
		for _, spec := range strings.Fields(string(data)) {
			if strings.HasPrefix(spec, "^") {
				matched = ""
				break
			}
			source, destination, ok := strings.Cut(strings.TrimPrefix(spec, "+"), ":")
			if !ok {
				continue
			}
			resolved := ""
			if source == target && !strings.ContainsAny(destination, "*?") {
				resolved = destination
			} else if strings.Count(source, "*") == 1 && strings.Count(destination, "*") == 1 {
				prefix, suffix, _ := strings.Cut(source, "*")
				if strings.HasPrefix(target, prefix) && strings.HasSuffix(target, suffix) && len(target) >= len(prefix)+len(suffix) {
					resolved = strings.Replace(destination, "*", target[len(prefix):len(target)-len(suffix)], 1)
				}
			}
			if resolved != "" {
				if matched != "" && matched != resolved {
					matched = ""
					break
				}
				matched = resolved
			}
		}
		if strings.HasPrefix(matched, "refs/remotes/") {
			result[target] = matched
		}
	}
	return result
}
