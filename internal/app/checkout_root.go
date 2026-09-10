package app

import (
	"context"
	"path/filepath"
	"strings"
)

// The checkout root: the one directory this repository expects its checkouts
// to live under, and the one value both readers of it share.
//
// The adopted design has always named a "configured checkout root", and until
// now no such key existed in this source, so layer 1 derived one and used it
// only to protect. A derived root is safe in that direction and unsafe in the
// other: a root that is too narrow can only protect a checkout that did not
// need it, but refusing to create at a destination for being outside a
// boundary nobody chose would refuse ordinary destinations. The key exists
// now, in the least machinery that could carry it, and one value answers both
// questions.
//
// It is `gitseq.checkoutRoot` in the repository's own Git configuration, set
// with `git config gitseq.checkoutRoot <path>` and read through the same
// closed Git environment as every other identity read this package makes, so
// ambient routing can neither choose the repository nor add configuration to
// what is found in it.
//
// Unset is the ordinary case, and it is the absence of a boundary rather than
// a second boundary. Protection keeps the fallback it has always had, the
// directory holding the served checkout, so nothing a reader relies on
// changes when nobody configures anything. Creation refuses nothing, because
// there is no configured boundary for a destination to be outside of, and it
// says so as a check it could not establish rather than as one that passed.
// The derived fallback is never treated as though somebody had chosen it.
//
// A relative value resolves against the repository's top level. A value that
// does not exist yet is still a boundary: containment resolves whichever
// leading part of a path exists. Comparison is on resolved paths and by path
// elements, so a symbolic link cannot carry a destination out of the root
// while its name says otherwise, and "/a/bc" is not inside "/a/b".
//
// What the key can do is worth saying plainly. A wider root marks fewer
// checkouts as outside it, and being outside it is one of the things that
// protects a checkout from cleanup advice, so a value written into this
// repository's configuration can enlarge the set of checkouts that advice
// offers. That sits inside the trust boundary this package already documents
// and does not widen it: a caller who can write this repository's
// configuration can already run a program of their choosing through
// `core.fsmonitor` during an ordinary read.
const checkoutRootKey = "gitseq.checkoutRoot"

// checkoutRoot answers the root and whether anybody configured it. top is the
// repository's own top-level working directory, which the caller has usually
// resolved already.
func (w *Workspace) checkoutRoot(ctx context.Context, top string) (root string, configured bool) {
	output, err := repositoryLocalGit(ctx, w.Repo, "config", "--local", "--get", checkoutRootKey).Output()
	value := strings.TrimSpace(string(output))
	if err != nil || value == "" {
		return filepath.Dir(top), false
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(top, value)
	}
	return filepath.Clean(value), true
}

// repositoryTop is the served checkout's own top-level working directory,
// falling back to the path this workspace was opened at when Git cannot say —
// a bare repository has no working tree, and its checkouts are all linked.
func (w *Workspace) repositoryTop(ctx context.Context) string {
	if output, err := repositoryLocalGit(ctx, w.Repo, "rev-parse", "--show-toplevel").Output(); err == nil {
		if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
			return trimmed
		}
	}
	return w.Repo
}

// resolvePathForContainment makes a path absolute and resolves the symbolic
// links of whichever leading part of it exists, so that containment is judged
// about directories rather than about names. A path that does not exist yet
// still gets its existing ancestors resolved, which is what a destination
// about to be created needs.
func resolvePathForContainment(path string) string {
	if path == "" {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	remainder := ""
	current := filepath.Clean(absolute)
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Clean(filepath.Join(resolved, remainder))
		}
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(absolute)
		}
		remainder = filepath.Join(filepath.Base(current), remainder)
		current = parent
	}
}
