package docset

import (
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// SucceededRetirements lists the retired artifacts whose retirement says where
// the behaviour went: the supersession that withdrew the pointer also rests on
// an artifact standing at the same path, or at a directory covering it.
//
// This is the fact a citation needs. A page names an artifact because that
// artifact vouches for the behaviour the prose describes; when a merge
// republishes that tree, the old pointer is withdrawn and a new one is
// published in the same act, and the supersession names it. Following that one
// link is enough — if the successor is itself later retired, the same link
// exists again on that retirement, so the reader is never left guessing which
// artifact the page should now name.
//
// A retirement that names no covering artifact is a different thing entirely,
// and this function deliberately does not find one for it: the behaviour was
// deleted, or the pointer was withdrawn on its own, and whoever cited it has to
// be told rather than redirected.
func SucceededRetirements(projection workroom.Projection) map[string]bool {
	paths := make(map[string]string, len(projection.Artifacts))
	for _, artifact := range projection.Artifacts {
		paths[artifact.Event] = artifact.Path
	}
	succeeded := make(map[string]bool)
	for _, act := range projection.Acts {
		if act.Type != "supersede" || act.Verdict != workroom.Effective {
			continue
		}
		retiredPath, isArtifact := paths[act.Target]
		if !isArtifact || succeeded[act.Target] {
			continue
		}
		for _, basis := range projection.Provenance[act.Event] {
			if basis == act.Target {
				continue
			}
			if successorPath, ok := paths[basis]; ok && pathCovers(successorPath, retiredPath) {
				succeeded[act.Target] = true
				break
			}
		}
	}
	return succeeded
}

// pathCovers reports whether a successor at one path stands over a predecessor
// at another: the same string, or a directory containing it. Paths are compared
// as exact slash-delimited strings, the same reading the projection gives them.
func pathCovers(successor, predecessor string) bool {
	if successor == "" || predecessor == "" {
		return false
	}
	return successor == predecessor ||
		strings.HasPrefix(predecessor, strings.TrimSuffix(successor, "/")+"/")
}
