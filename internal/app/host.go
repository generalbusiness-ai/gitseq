package app

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"

	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// This file is where the host layer meets the one application profile this
// build holds. The vocabulary it reads — what a binding is, who may record
// one, and which one is in force — lives in internal/apphost, because a
// program that has never heard of Workroom must be able to read it. What is
// left here is the part that only a build holding an interpreter can do:
// choose one. See docs/reference/architecture.md, "Application host binding".

// host is one application interpreter this build holds. newFolder is the
// selection's whole point: the host layer chooses the fold, once, at open.
type host struct {
	application string
	foldVersion string
	newFolder   func([]workroom.Record) *workroom.Folder
}

// workroomHost is the interpreter this build holds, and the one an absent
// binding names. A build holds the applications it was compiled with, and
// nothing installs one at runtime.
var workroomHost = host{
	application: apphost.DefaultApplication,
	foldVersion: workroom.ProfileVersion,
	newFolder:   workroom.NewFolder,
}

// heldHost answers whether this build can interpret one named application.
// There is exactly one, so this is the whole registry. A map would be an index
// over a single entry, and a misleading one: newFolder is spelled in Workroom's
// own types, so no second application could be registered through it anyway.
// An application outside this module is not registered here at all: it holds
// its own fold and reads its own records through the public host package.
func heldHost(application string) (host, bool) {
	if application == workroomHost.application {
		return workroomHost, true
	}
	return host{}, false
}

// selection is one resolved answer to "which interpreter". A refusal is part
// of the answer rather than a failure to answer: a repository bound to an
// application this build does not hold stays kernel-verifiable, and only its
// application meaning is unavailable.
type selection struct {
	host host
	err  error
}

// selectHost reads the binding and selects the interpreter. The returned error
// means the log could not be read at all, which is not an answer: the
// workspace does not open, rather than opening with the question left over.
//
// The scan is the ref, because this workspace has verified nothing yet: its
// audit runs when the fold first reads the log, after the interpreter is
// chosen. The choice is still made once and never revisited, so a binding
// recorded afterwards cannot change what an open workspace means.
func (w *Workspace) selectHost(ctx context.Context) (selection, error) {
	recorded, err := apphost.BindingInForce(ctx, w.Store, w.Config.Genesis, kernel.Ref(w.Config.Genesis))
	if err != nil {
		return selection{}, err
	}
	if recorded == nil {
		return selection{host: workroomHost}, nil
	}
	held, exists := heldHost(recorded.Application)
	if !exists {
		return selection{err: fmt.Errorf("repository is verifiable but uninterpretable: it is bound to application %q, which this build does not hold", recorded.Application)}, nil
	}
	if held.foldVersion != recorded.FoldVersion {
		return selection{err: fmt.Errorf("repository is verifiable but uninterpretable: it is bound to %s fold %q, and this build holds %q", recorded.Application, recorded.FoldVersion, held.foldVersion)}, nil
	}
	return selection{host: held}, nil
}

// interpreter reports the fold chosen when this workspace was made, and the
// refusal if there is one. The answer is immutable, so every operation on one
// workspace means the same thing however the log moves under it.
//
// A workspace assembled as a struct literal never selected one. It is told so
// rather than handed the default, because a guessed interpreter is the one
// thing the open-time order exists to prevent.
func (w *Workspace) interpreter() (host, error) {
	if w.selected.host.newFolder == nil && w.selected.err == nil {
		return host{}, errors.New("workspace has no interpreter: open it with Open, Init, or AttachConfig")
	}
	return w.selected.host, w.selected.err
}

func (h host) projectionProfile() string {
	return h.application + "\x00" + h.foldVersion
}

// selfBinding is what this build says it is: the host it runs, the fold that
// host holds, and the commit Go stamped in at build time. No build knows the
// URL it was cloned from, so provenance stays empty until something can state
// it truly.
func selfBinding(running host) apphost.Binding {
	return apphost.Binding{Application: running.application, SourceCommit: apphost.SourceCommit(), FoldVersion: running.foldVersion}
}

// buildBindingRequest signs the repository's binding with the initializing
// key. Recording a binding runs no application code and fetches nothing.
func (w *Workspace) buildBindingRequest(ctx context.Context, private ed25519.PrivateKey, operatorName string, recorded apphost.Binding) (kernel.Request, error) {
	payload, err := recorded.Payload()
	if err != nil {
		return kernel.Request{}, err
	}
	// Recording a binding is an act at a position in the log, so that is what
	// the idempotency key names. Retrying one submission is the same act and
	// appends once; binding again later is a new act whatever it names. A
	// fixed key would let a repository record only its first binding ever, and
	// a key over the binding's own bytes would silently swallow the rollback
	// that re-records an earlier binding — reporting success while leaving the
	// binding it replaced in force.
	head, err := w.Store.Head(ctx, kernel.Ref(w.Config.Genesis))
	if err != nil {
		return kernel.Request{}, err
	}
	return w.signRequest(ctx, private, operatorName, apphost.BindingSchema, payload, nil, nil, "binding/"+head)
}
