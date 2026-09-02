package docset

import (
	"sort"
	"strings"
	"testing"
)

// Gate 4, the flare test. Retiring one governing act must make exactly the
// pages that name it stale, and no others. That is the whole promise of the
// anchoring: a flare names a page a reader can go and re-check, not the set.
//
// Both directions matter. A page that stays current when the behaviour it
// describes has moved is the failure this project exists to prevent. A page
// that flares because something unrelated moved is the failure that teaches
// readers to ignore flares, which comes to the same thing more slowly.

func TestGateRetiringOneActFlaresExactlyItsPages(t *testing.T) {
	root := mustRoot(t)
	pages := mustPages(t, root)
	acts := declaredActs(pages)
	if len(acts) == 0 {
		t.Fatal("the set names no governing acts at all")
	}

	for _, act := range acts {
		t.Run(shortAct(act), func(t *testing.T) {
			t.Parallel()
			built := buildModel(t, pages)
			built.retire(t, act)
			artifacts := built.artifacts(t)

			var flared []string
			for _, page := range pages {
				artifact, ok := artifacts[built.artifact[page.Path]]
				if !ok {
					t.Fatalf("%s: modelled artifact missing from the projection", page.Path)
				}
				if artifact.Stale || artifact.DescribesSupersededWorld {
					flared = append(flared, page.Path)
				}
			}
			sort.Strings(flared)
			want := dependents(pages, act)
			if diff := difference(flared, want); diff != "" {
				t.Errorf("retiring %s flared the wrong pages:\n%s", act, strings.ReplaceAll(diff,
					"undocumented", "named the act but did not flare"))
			}
		})
	}
}

// The acceptance instance named by the request is the open work on an honest
// `gs verify` equivocation posture: when it lands, the `gs verify` page should
// flare and nothing else should. That property is structural — it holds only
// while some act governs that page alone — so it is checked here rather than
// waited for.
func TestGateVerifyPageCanFlareAlone(t *testing.T) {
	root := mustRoot(t)
	pages := mustPages(t, root)
	const verifyPage = DocsDir + "/reference/gs/verify.md"

	var sole string
	for _, act := range declaredActs(pages) {
		named := dependents(pages, act)
		if len(named) == 1 && named[0] == verifyPage {
			sole = act
			break
		}
	}
	if sole == "" {
		t.Fatalf("no act governs %s alone, so retiring one could never flare it by itself", verifyPage)
	}

	built := buildModel(t, pages)
	built.retire(t, sole)
	artifacts := built.artifacts(t)
	var flared []string
	for _, page := range pages {
		artifact := artifacts[built.artifact[page.Path]]
		if artifact.Stale || artifact.DescribesSupersededWorld {
			flared = append(flared, page.Path)
		}
	}
	if len(flared) != 1 || flared[0] != verifyPage {
		t.Errorf("retiring %s flared %v, want only %s", sole, flared, verifyPage)
	}
}

// A flare has to name a page a reader can act on. An act that governs most of
// the set produces a flare naming the set, which is the failure the anchoring
// exists to prevent — and unlike the test above, this one is falsified by the
// front matter rather than by the fold, so a careless anchor is caught here.
func TestGateNoActFlaresMostOfTheSet(t *testing.T) {
	root := mustRoot(t)
	pages := mustPages(t, root)
	limit := len(pages) / 4
	if limit < 1 {
		limit = 1
	}
	for _, act := range declaredActs(pages) {
		named := dependents(pages, act)
		if len(named) > limit {
			t.Errorf("%s governs %d of %d pages, over the limit of %d; split it, or anchor those pages to something narrower:\n  %s",
				act, len(named), len(pages), limit, strings.Join(named, "\n  "))
		}
	}
}

func shortAct(act string) string {
	_, event, ok := strings.Cut(act, "#")
	if !ok {
		return act
	}
	if index := strings.LastIndex(event, ":"); index >= 0 && len(event) > index+9 {
		return event[index+1 : index+9]
	}
	return event
}
