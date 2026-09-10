package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gitseqhost "github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/gitseq/internal/apphost"
)

// The private record one checkout keeps of the durable work it was made for,
// and the commit-message template derived from it.
//
// Both live in the checkout's own private Git directory, which
// apphost.ResolveGitDirs already separates from the common directory: for a
// linked checkout that is <commonDir>/worktrees/<name>, and it is removed with
// the checkout by the machinery that made it. The adopted design considered
// `git config --worktree gitseq.request` and chose a file on cost rather than
// on prohibition: worktree-scoped configuration is read only once the
// repository sets extensions.worktreeConfig, a repository-wide change to
// config reading for every checkout and every actor in aid of one advisory
// value; the atomic-write and flock pair already exists here and already
// holds per-checkout state; and that configuration scope is executable rather
// than merely readable, so anything kept in it stays an execution surface to
// reason about.
//
// The record is evidence and never permission. Nothing in the fold reads it,
// no admission decision consults it, and a value the actor can write is a
// value the actor can unset — so a refusal keyed on it would stop honest
// actors and nobody else.

// checkoutRecordDir is the record's home inside one checkout's private Git
// directory, and checkoutRecordFile and checkoutTemplateFile are what it
// holds.
const (
	checkoutRecordDir    = "gitseq"
	checkoutRecordFile   = "checkout.json"
	checkoutTemplateFile = "rests-on"
	// checkoutRecordLock serialises writers of one checkout's record through
	// the one advisory-lock primitive this repository has. The name is bare,
	// as that primitive requires, and is distinct from the configuration lock
	// that may be taken in the same directory when the served checkout is the
	// one being stamped.
	checkoutRecordLock = ".checkout.lock"
	// checkoutRecordLimit bounds the read. A record is a few hundred bytes;
	// the bound is what stops an oversized file left at that path from being
	// read whole before anything about it has been checked.
	checkoutRecordLimit = 8 << 10
)

// CheckoutRecord is what one checkout says about the durable work it was
// created for.
//
// Genesis and ObjectFormat are this workroom's, and they are the point of the
// record rather than decoration: a record copied byte for byte out of another
// repository names that repository's genesis, and a reader here can say so.
// The same shape already guards the resident ownership claim, where a record
// naming a workroom other than the one whose ref it sits at is corruption
// rather than an incumbent.
type CheckoutRecord struct {
	Genesis      string `json:"genesis"`
	ObjectFormat string `json:"object_format"`
	// Governing is the full canonical identifier of the request or adopted
	// decision this checkout was created for. Only the full identifier is ever
	// written: a record number or a hash fragment is a way of typing one at a
	// boundary, never a way of storing one, and a `Rests-On:` trailer takes
	// the full identifier only.
	Governing string `json:"governing"`
	Branch    string `json:"branch,omitempty"`
	// AttemptRef and Attempt name the ref allocated for this checkout's work.
	// They are written for a reader who wants to find the ref again; nothing
	// reads them back to make a decision.
	AttemptRef string `json:"attempt_ref,omitempty"`
	Attempt    int    `json:"attempt,omitempty"`
}

// Trailer is the exact `Rests-On:` line an implementing commit in this
// checkout carries.
func (r CheckoutRecord) Trailer() string { return "Rests-On: " + r.Governing }

// commitTemplate is the commit-message template the record produces: an empty
// subject line, a blank line, and the trailer. A person points `git commit -t`
// at the file, or copies the line out of it. The blank lines are what make the
// trailer a trailer rather than the last sentence of a paragraph.
//
// The template is derived from the record and never the reverse. One value is
// authored, in one place, and everything else is a rendering of it.
func (r CheckoutRecord) commitTemplate() string { return "\n\n" + r.Trailer() + "\n" }

// checkoutRecordHome is the directory holding one checkout's record: the
// checkout's own private Git directory, not the repository's common one.
//
// It is resolved against the checkout path rather than reused from the
// workspace, because the workspace's GitDir is the invoking checkout's. A
// second creation that reused it would overwrite the first checkout's record.
//
// The common directory is compared at the same time, which is what makes
// "this repository's checkout" a checked fact rather than an assumption: a
// path that resolves to some other repository's common directory is not one
// this workspace may stamp.
func (w *Workspace) checkoutRecordHome(ctx context.Context, checkout string) (string, error) {
	gitDir, commonDir, err := apphost.ResolveGitDirs(ctx, checkout)
	if err != nil {
		return "", err
	}
	if !sameDirectory(commonDir, w.CommonDir) {
		return "", fmt.Errorf("checkout %s belongs to the repository at %s, not to this workroom at %s", checkout, commonDir, w.CommonDir)
	}
	return filepath.Join(gitDir, checkoutRecordDir), nil
}

// sameDirectory compares two directory paths after resolving symbolic links,
// so that two spellings of one directory are one directory. An unresolvable
// path is compared as cleaned, which can only make the answer "different" and
// so can only refuse.
func sameDirectory(a, b string) bool {
	resolve := func(path string) string {
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return filepath.Clean(resolved)
		}
		return filepath.Clean(path)
	}
	return resolve(a) == resolve(b)
}

// writeCheckoutRecord stores one checkout's record and its derived template,
// through the atomic-file and flock pair this host layer already owns.
//
// The lock covers both files, so a reader never sees a record from one
// creation beside a template from another. Each file is replaced by a rename
// from the same directory, so a reader never sees half of either.
func (w *Workspace) writeCheckoutRecord(ctx context.Context, checkout string, record CheckoutRecord) (recordPath, templatePath string, err error) {
	if !gitseqhost.ValidEventID(record.Governing) {
		return "", "", fmt.Errorf("governing record %q is not a canonical event identifier", record.Governing)
	}
	home, err := w.checkoutRecordHome(ctx, checkout)
	if err != nil {
		return "", "", err
	}
	// The lock primitive opens a file inside this directory and does not
	// create the directory itself.
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", "", err
	}
	recordPath = filepath.Join(home, checkoutRecordFile)
	templatePath = filepath.Join(home, checkoutTemplateFile)
	content, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", "", err
	}
	content = append(content, '\n')
	_, err = apphost.WithMetaLock(home, checkoutRecordLock, func() (struct{}, error) {
		if err := apphost.WriteFileAtomically(recordPath, content); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, apphost.WriteFileAtomically(templatePath, []byte(record.commitTemplate()))
	})
	if err != nil {
		return "", "", err
	}
	return recordPath, templatePath, nil
}

// readCheckoutRecord reads back the record one checkout carries.
//
// It is the decoder the writer is judged against: creation reads its own
// record back before reporting it, so what the command says the checkout
// carries is what the checkout actually carries rather than what the command
// meant to write. Reading is all it does. Nothing here grants anything, and no
// caller in this stage turns a record into a permission.
//
// A record naming another workroom is refused rather than carried. The file
// sits in this repository's own private directory, and the genesis inside it
// cannot honestly disagree with the repository it is in: a record that does is
// a copy from somewhere else, and saying so is the whole reason the genesis is
// written down.
func (w *Workspace) readCheckoutRecord(ctx context.Context, checkout string) (CheckoutRecord, error) {
	home, err := w.checkoutRecordHome(ctx, checkout)
	if err != nil {
		return CheckoutRecord{}, err
	}
	return readCheckoutRecordAt(filepath.Join(home, checkoutRecordFile), w.config.Genesis, w.config.ObjectFormat)
}

func readCheckoutRecordAt(path, genesis, objectFormat string) (CheckoutRecord, error) {
	record, err := decodeCheckoutRecord(path)
	if err != nil {
		return CheckoutRecord{}, err
	}
	if record.Genesis != genesis || record.ObjectFormat != objectFormat {
		return CheckoutRecord{}, fmt.Errorf("%s names workroom git:%s:%s, not git:%s:%s; it was copied from another repository",
			path, record.ObjectFormat, record.Genesis, objectFormat, genesis)
	}
	return record, nil
}

// decodeCheckoutRecord reads and shape-checks a record without deciding whose
// workroom it belongs to.
//
// The two are separate because two callers want different things from a
// record naming somewhere else. A caller about to report what a checkout
// carries refuses it, because a record in this repository's own private
// directory cannot honestly name another room. The reverse association grades
// it instead: a value naming another genesis is carried as typed and never
// resolved locally, which is what every other foreign citation in this
// repository gets, and dropping it would hide the very thing worth seeing.
func decodeCheckoutRecord(path string) (CheckoutRecord, error) {
	info, err := os.Stat(path)
	if err != nil {
		return CheckoutRecord{}, err
	}
	if info.Size() > checkoutRecordLimit {
		return CheckoutRecord{}, fmt.Errorf("%s is larger than the %d bytes a checkout record may be", path, checkoutRecordLimit)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return CheckoutRecord{}, err
	}
	var record CheckoutRecord
	if err := json.Unmarshal(content, &record); err != nil {
		return CheckoutRecord{}, fmt.Errorf("%s is not a checkout record: %w", path, err)
	}
	if record.Governing == "" || record.Genesis == "" {
		return CheckoutRecord{}, fmt.Errorf("%s is missing its governing record or its genesis", path)
	}
	return record, nil
}

// checkoutPrivateGitDir names one checkout's own private Git directory,
// without starting a Git process: a linked checkout's `.git` entry is a file
// naming that directory, and the served checkout's is the directory itself.
// Reading it here is what lets the captured read pick up every checkout's
// record at the cost of one small file read each.
func checkoutPrivateGitDir(checkout string) (string, bool) {
	if checkout == "" {
		return "", false
	}
	entry := filepath.Join(checkout, ".git")
	info, err := os.Lstat(entry)
	if err != nil {
		return "", false
	}
	if info.IsDir() {
		return entry, true
	}
	if !info.Mode().IsRegular() || info.Size() > checkoutRecordLimit {
		return "", false
	}
	content, err := os.ReadFile(entry)
	if err != nil {
		return "", false
	}
	value, ok := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir:")
	if !ok {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(checkout, value)
	}
	return filepath.Clean(value), true
}

// checkoutRecordPath is where one checkout keeps its record, given the
// checkout's own path.
func checkoutRecordPath(checkout string) (string, bool) {
	gitDir, found := checkoutPrivateGitDir(checkout)
	if !found {
		return "", false
	}
	return filepath.Join(gitDir, checkoutRecordDir, checkoutRecordFile), true
}
