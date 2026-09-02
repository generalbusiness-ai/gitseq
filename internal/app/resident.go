package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/apphost"
)

// Resident says where a repository's resident service is listening. The record
// sits beside the workroom config inside the repository's own git directory,
// so a client finds the service by naming the repository instead of being
// told a URL out of band. The genesis travels with the address because an
// address alone cannot say which workroom answers there.
type Resident struct {
	URL     string `json:"url"`
	Genesis string `json:"genesis"`
	PID     int    `json:"pid"`
}

const residentFile = "resident.json"

// PublishResident advertises this process as the repository's resident service
// and returns the withdrawal. It is endpoint metadata, not authority: the last
// writer wins, and ownership of the repository lives in the claim ref instead —
// see ClaimResident — with a serving process advertising only after it holds
// one.
//
// A record left behind by a dead process is still a trustworthy advertisement:
// it reads, parses, names this workroom and carries a usable address. What
// happens next depends on the caller. A read may name the failed request and
// answer from the verified local fold instead. For a durable act the two
// surfaces differ: `gs` refuses when the advertised address does not answer,
// naming `--server -` as the deliberate way to fold locally, while the MCP
// adapter re-reads this record after transport loss and folds the act
// locally, marked degraded, when the re-read still yields something it can
// act on — the same record, a valid replacement, or no record at all. Only a
// re-read that cannot be trusted refuses that fallback.
//
// An advertisement that is not trustworthy — unreadable, oversized, not a
// record, addressless, or naming another workroom — refuses every durable
// act before a signing key is read or anything is built, and
// ResidentAdvertisement carries the reason. Reads differ again: `gs` refuses
// the whole command, while the MCP adapter answers a read from the verified
// local fold.
func (w *Workspace) PublishResident(url string) (withdraw func(), err error) {
	record := Resident{URL: strings.TrimRight(url, "/"), Genesis: w.config.Genesis, PID: os.Getpid()}
	content, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(w.MetaDir, residentFile)
	if err := apphost.WriteFileAtomically(path, content); err != nil {
		return nil, err
	}
	return func() {
		// Only withdraw our own advertisement. A later service that took the
		// repository over is still serving it, and removing its record would
		// send clients into degraded mode for no reason.
		if current, err := readResident(path); err == nil && current.PID == record.PID {
			_ = os.Remove(path)
		}
	}, nil
}

// AdvertisementState is what the repository's resident record resolves to. The
// three states are deliberately distinct, because collapsing them into one
// boolean is what let a corrupt, truncated, empty or foreign record read as
// "nothing is advertised" and send a durable act quietly into the local fold.
// A caller that must fail closed can only do so if the read tells it that a
// record is there and cannot be trusted.
type AdvertisementState int

const (
	// NoAdvertisement means the record is not there at all. Nothing has been
	// published, so a client acts locally exactly as it did before residents
	// existed.
	NoAdvertisement AdvertisementState = iota
	// AdvertisementPublished means a record naming this workroom carries an
	// address. The address itself is still untrusted input and must be
	// validated before it is dialled.
	AdvertisementPublished
	// AdvertisementUnusable means a record is present and cannot be trusted:
	// unreadable, oversized, not a record, addressless, or naming another
	// workroom. Reason says which.
	AdvertisementUnusable
)

// ResidentAdvertisement is one reading of the record. URL is set only when the
// state is AdvertisementPublished, or when an unusable record still carried an
// address worth naming in a refusal. Reason is set only when the state is
// AdvertisementUnusable.
type ResidentAdvertisement struct {
	State  AdvertisementState
	URL    string
	Reason error
}

// advertisementLimit bounds the record read. The advertisement is an ordinary
// file inside the repository that any local process can write, so its size is
// untrusted; a genuine record is a couple of hundred bytes. The bound is what
// stops an oversized file being read whole before anything about it has been
// checked.
const advertisementLimit = 8 << 10

// ResidentAdvertisement reads the record that says which service holds this
// workroom. Every way the record can fail is reported as AdvertisementUnusable
// with the reason, and only a genuinely missing file is absence. A record
// naming a different genesis was left by a workroom this repository no longer
// has: acting through it would append to another log, so it is a refusal
// rather than a vacancy.
func (w *Workspace) ResidentAdvertisement() ResidentAdvertisement {
	path := filepath.Join(w.MetaDir, residentFile)
	record, err := readResident(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ResidentAdvertisement{State: NoAdvertisement}
		}
		return ResidentAdvertisement{State: AdvertisementUnusable, Reason: fmt.Errorf("the record at %s %w", path, err)}
	}
	if record.URL == "" {
		return ResidentAdvertisement{State: AdvertisementUnusable, Reason: fmt.Errorf("the record at %s advertises no address", path)}
	}
	if record.Genesis != w.config.Genesis {
		return ResidentAdvertisement{
			State:  AdvertisementUnusable,
			URL:    record.URL,
			Reason: fmt.Errorf("the record at %s names workroom %q, not %q", path, record.Genesis, w.config.Genesis),
		}
	}
	return ResidentAdvertisement{State: AdvertisementPublished, URL: record.URL}
}

// readResident reads and decodes the record, reporting why rather than whether.
// The bound is applied before decoding, and one byte past the limit is read so
// that a file exactly at the limit still parses and a larger one is refused
// without being held whole.
func readResident(path string) (Resident, error) {
	file, err := os.Open(path)
	if err != nil {
		return Resident{}, fmt.Errorf("cannot be read: %w", err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, advertisementLimit+1))
	if err != nil {
		return Resident{}, fmt.Errorf("cannot be read: %w", err)
	}
	if len(content) > advertisementLimit {
		return Resident{}, fmt.Errorf("is larger than the %d bytes a resident record may be", advertisementLimit)
	}
	var record Resident
	if err := json.Unmarshal(content, &record); err != nil {
		return Resident{}, fmt.Errorf("is not a resident record: %w", err)
	}
	return record, nil
}

// ResidentRef names the ownership claim for one workroom. It is an ordinary
// shared Git ref in the repository's common directory, so every path alias,
// every symlink, and every linked worktree of one repository contends for the
// same claim, while two different repositories never contend at all. It sits
// beside refs/gitseq/checkpoints/<genesis> and never touches the durable event
// log.
func ResidentRef(genesis string) string { return "refs/gitseq/resident/" + genesis }

const (
	// claimLimit bounds the claim read. A claim is a few hundred bytes; the
	// bound is what stops an oversized object left at the ref from being read
	// whole before anything about it has been checked.
	claimLimit = 8 << 10

	// claimAttempts bounds contention. Each attempt is one compare-and-swap,
	// and exhausting them refuses rather than looping: a claim that keeps
	// moving under us is a repository other processes are starting on, and
	// spinning there would trade a clear refusal for an unbounded wait.
	claimAttempts = 8

	// releaseRetryDelay gives a transient update-ref lock time to clear during
	// orderly shutdown. Release has a different purpose from acquisition
	// contention, so it retries until the caller's cleanup context expires.
	releaseRetryDelay = 25 * time.Millisecond
)

// ResidentClaim is the ownership record a serving process holds. The nonce is
// what gives it an identity: sixteen fresh random bytes make the claim blob's
// object id unique to this one acquisition, so a compare-and-swap against that
// id can never match a later claim, and the A-B-A that defeats value
// compare-and-swap cannot arise. The PID is a diagnostic for a human reading
// the record and confers nothing.
type ResidentClaim struct {
	Genesis string `json:"genesis"`
	URL     string `json:"url"`
	Nonce   string `json:"nonce"`
	PID     int    `json:"pid"`
}

// Liveness is what a probe learned about an existing claim. The zero value is
// Ambiguous deliberately: a prober that decided nothing has not shown the
// incumbent to be gone, and only a definitive negative may authorize a
// takeover.
type Liveness int

const (
	Ambiguous Liveness = iota
	Alive
	Dead
)

// ResidentProbe is one bounded liveness answer. PID is advisory and is set
// only when the service itself returned it; it never authorizes takeover.
type ResidentProbe struct {
	Liveness Liveness
	PID      int
}

// Prober answers whether the resident a claim names is still serving. It is
// supplied by the caller rather than reached from here: the client that knows
// how to speak to a resident is built on this package, and it also owns the
// duty to refuse any address that is not loopback, because a claim is an
// ordinary file that any local process able to write the repository can put
// a URL into.
type Prober func(context.Context, ResidentClaim) ResidentProbe

// ResidentOwnership is a held claim. It carries the exact object id this
// process wrote, which is the only thing that can withdraw it.
type ResidentOwnership struct {
	workspace *Workspace
	ref       string
	blob      string
	claim     ResidentClaim
	reclaimed *ResidentClaim
}

// Claim reports what this process published as its ownership record.
func (o *ResidentOwnership) Claim() ResidentClaim { return o.claim }

// Reclaimed reports the dead claim this acquisition replaced. The serving
// command uses it to make automatic recovery visible to its operator.
func (o *ResidentOwnership) Reclaimed() (ResidentClaim, bool) {
	if o == nil || o.reclaimed == nil {
		return ResidentClaim{}, false
	}
	return *o.reclaimed, true
}

// ResidentHeldError refuses to serve because ownership could not be taken
// safely. Serving anyway would split leased presence and ephemeral
// conversation into two rooms whose participants cannot see each other and are
// never told, which is the whole failure this claim exists to prevent.
type ResidentHeldError struct {
	Ref      string
	URL      string
	PID      int
	Reason   string
	Recovery string
}

func (e *ResidentHeldError) Error() string {
	message := "refusing to serve: " + e.Reason
	if e.URL != "" {
		message += " (" + e.URL
		if e.PID > 0 {
			message += fmt.Sprintf(", pid %d", e.PID)
		}
		message += ")"
	} else if e.PID > 0 {
		message += fmt.Sprintf(" (pid %d)", e.PID)
	}
	if e.Recovery != "" {
		message += "; " + e.Recovery
	}
	return message
}

func residentRecovery(ref string) string {
	return "last-resort override: first prove no service is answering; deleting a live service's claim can start a second resident and race the log; only then remove the claim with `git update-ref -d " + ref + "`"
}

func unreadableResident(ref, observed string, err error) error {
	return &ResidentHeldError{
		Ref:      ref,
		Reason:   fmt.Sprintf("the ownership claim at %s (%s) cannot be read as a claim: %v", ref, observed, err),
		Recovery: residentRecovery(ref),
	}
}

func heldResident(ref string, incumbent ResidentClaim, answer ResidentProbe) error {
	switch answer.Liveness {
	case Alive:
		return &ResidentHeldError{
			Ref: ref, URL: incumbent.URL, PID: answer.PID,
			Reason: "another service already holds this repository's workroom and is answering",
		}
	default:
		return &ResidentHeldError{
			Ref: ref, URL: incumbent.URL,
			Reason:   "the service holding this repository could not be shown to be gone, so its claim is left alone",
			Recovery: residentRecovery(ref),
		}
	}
}

// ResidentVacancy is a one-use liveness proof bound to one repository and one
// exact claim position. Its private pointer makes copies share the consumed
// bit: only CheckResident can produce one, and only one ClaimResidentAfter CAS
// attempt can spend it.
type ResidentVacancy struct {
	proof *residentVacancyProof
}

type residentVacancyProof struct {
	commonDir string
	ref       string
	observed  string
	reclaimed *ResidentClaim
	consumed  atomic.Bool
}

// CheckResident is the read-only pre-bind half of starting a resident. It
// makes a live or ambiguous incumbent visible before binding the requested
// port, which matters when the incumbent already owns that same port. On a
// vacancy it returns a proof authorizing one exact post-bind CAS attempt.
// Neither that proof nor a bound listener authorizes serving; only the
// ownership returned by ClaimResidentAfter does.
func (w *Workspace) CheckResident(ctx context.Context, probe Prober) (*ResidentVacancy, error) {
	if probe == nil {
		return nil, errors.New("checking a resident needs a prober; without one no incumbent could ever be tested")
	}
	ref := ResidentRef(w.config.Genesis)
	observed, present, err := w.Store.RefValue(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("read resident claim %s: %w", ref, err)
	}
	if !present {
		return &ResidentVacancy{proof: &residentVacancyProof{commonDir: w.CommonDir, ref: ref}}, nil
	}
	incumbent, err := w.readResidentClaim(ctx, observed)
	if err != nil {
		return nil, unreadableResident(ref, observed, err)
	}
	answer := probe(ctx, incumbent)
	if answer.Liveness == Dead {
		return &ResidentVacancy{proof: &residentVacancyProof{
			commonDir: w.CommonDir, ref: ref, observed: observed, reclaimed: &incumbent,
		}}, nil
	}
	return nil, heldResident(ref, incumbent, answer)
}

// ClaimResident takes exclusive ownership of this repository's resident, and
// is what authorizes serving. Acquisition, takeover and release are one
// primitive: a Git ref update carrying the expected old value, the same
// compare-and-swap the durable log's own append already depends on. Create
// fails if the ref exists; a takeover fails unless the ref still names exactly
// the claim that was read and probed; release deletes only this process's own
// object.
//
// Nothing here is trusted as live without a probe, and nothing ambiguous
// authorizes a takeover: an unreadable, malformed, or foreign claim is a
// refusal, never a vacancy. That is deliberate asymmetry. Refusing to start
// costs one operator a message naming the recovery step; starting anyway costs
// everybody a silently divided room.
func (w *Workspace) ClaimResident(ctx context.Context, url string, probe Prober) (*ResidentOwnership, error) {
	return w.claimResident(ctx, url, probe, nil)
}

// ClaimResidentAfter consumes one pre-bind vacancy proof. It attempts exactly
// one compare-and-swap against the position CheckResident observed. If that
// position moved, the proof is spent and acquisition falls back to the normal
// read, probe, and CAS loop. Holding the returned ownership, not the proof,
// authorizes serving.
func (w *Workspace) ClaimResidentAfter(ctx context.Context, url string, probe Prober, vacancy *ResidentVacancy) (*ResidentOwnership, error) {
	return w.claimResident(ctx, url, probe, vacancy)
}

func (w *Workspace) claimResident(ctx context.Context, url string, probe Prober, vacancy *ResidentVacancy) (*ResidentOwnership, error) {
	if w.config.ReadOnly {
		return nil, errors.New("a read-only attachment cannot own a resident")
	}
	if probe == nil {
		return nil, errors.New("claiming a resident needs a prober; without one no incumbent could ever be tested")
	}
	address := strings.TrimRight(url, "/")
	if address == "" {
		return nil, errors.New("a resident claim needs the address it is serving")
	}
	ref := ResidentRef(w.config.Genesis)
	if vacancy != nil {
		if vacancy.proof == nil || vacancy.proof.commonDir != w.CommonDir || vacancy.proof.ref != ref {
			return nil, errors.New("resident vacancy proof belongs to another repository")
		}
		if !vacancy.proof.consumed.CompareAndSwap(false, true) {
			return nil, errors.New("resident vacancy proof was already consumed")
		}
		ownership, taken, err := w.takeResident(ctx, ref, address, vacancy.proof.observed)
		if err != nil {
			return nil, err
		}
		if taken {
			ownership.reclaimed = vacancy.proof.reclaimed
			return ownership, nil
		}
	}
	for attempt := 0; attempt < claimAttempts; attempt++ {
		observed, present, err := w.Store.RefValue(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("read resident claim %s: %w", ref, err)
		}
		if !present {
			ownership, taken, err := w.takeResident(ctx, ref, address, "")
			if err != nil {
				return nil, err
			}
			if taken {
				return ownership, nil
			}
			continue
		}
		incumbent, err := w.readResidentClaim(ctx, observed)
		if err != nil {
			return nil, unreadableResident(ref, observed, err)
		}
		answer := probe(ctx, incumbent)
		switch answer.Liveness {
		case Alive:
			return nil, heldResident(ref, incumbent, answer)
		case Dead:
			ownership, taken, err := w.takeResident(ctx, ref, address, observed)
			if err != nil {
				return nil, err
			}
			if taken {
				ownership.reclaimed = &incumbent
				return ownership, nil
			}
			continue
		default:
			return nil, heldResident(ref, incumbent, answer)
		}
	}
	return nil, &ResidentHeldError{
		Ref:      ref,
		Reason:   fmt.Sprintf("the ownership claim at %s changed under every one of %d attempts", ref, claimAttempts),
		Recovery: "another service is starting on this repository; try again once it has settled",
	}
}

// takeResident writes a claim and swaps it in against the value that was
// observed. An empty expected value means the ref must not exist yet. A fresh
// nonce is generated on every attempt, so a retry never re-offers an object id
// that a concurrent process could already have seen.
//
// A failed swap reports contention rather than an error. Git refuses for one
// reason that matters here — the ref is no longer what we read — and treating
// every refusal that way keeps the caller from parsing Git's prose. A store
// that is genuinely broken exhausts the attempts and then refuses, which is the
// same fail-closed outcome by a slower route.
func (w *Workspace) takeResident(ctx context.Context, ref, url, expected string) (*ResidentOwnership, bool, error) {
	nonce, err := newResidentNonce()
	if err != nil {
		return nil, false, err
	}
	claim := ResidentClaim{Genesis: w.config.Genesis, URL: url, Nonce: nonce, PID: os.Getpid()}
	content, err := json.Marshal(claim)
	if err != nil {
		return nil, false, err
	}
	blob, err := w.Store.WriteBlob(ctx, content)
	if err != nil {
		return nil, false, fmt.Errorf("write resident claim: %w", err)
	}
	if err := w.Store.UpdateRef(ctx, ref, blob, expected); err != nil {
		return nil, false, nil
	}
	return &ResidentOwnership{workspace: w, ref: ref, blob: blob, claim: claim}, true, nil
}

// readResidentClaim reads and validates the claim an object holds. A claim
// missing any of its parts, or naming a workroom other than the one whose ref
// it sits at, is corruption rather than an incumbent, and saying so is safer
// than guessing: the ref name already carries the genesis, so a mismatch inside
// it cannot be an honest record.
func (w *Workspace) readResidentClaim(ctx context.Context, oid string) (ResidentClaim, error) {
	content, err := w.Store.BlobLimit(ctx, oid, claimLimit)
	if err != nil {
		return ResidentClaim{}, err
	}
	var claim ResidentClaim
	if err := json.Unmarshal(content, &claim); err != nil {
		return ResidentClaim{}, err
	}
	if claim.Genesis == "" || claim.URL == "" || claim.Nonce == "" {
		return ResidentClaim{}, errors.New("claim is missing its genesis, address, or nonce")
	}
	if claim.Genesis != w.config.Genesis {
		return ResidentClaim{}, fmt.Errorf("claim names workroom %s, not %s", claim.Genesis, w.config.Genesis)
	}
	return claim, nil
}

// Release withdraws the claim, and can only ever withdraw this process's own:
// the delete carries the object id written at acquisition, so a successor that
// already took the repository over keeps it. Withdrawal is an optimization, not
// a correctness requirement — a claim left behind by a crash costs the next
// starter one refused dial, because no claim is ever trusted as live without a
// probe.
func (o *ResidentOwnership) Release(ctx context.Context) error {
	if o == nil {
		return nil
	}
	var last error
	for {
		if err := o.workspace.Store.DeleteRef(ctx, o.ref, o.blob); err == nil {
			return nil
		} else {
			last = err
		}
		current, present, err := o.workspace.Store.RefValue(ctx, o.ref)
		if err != nil {
			last = err
		} else if !present || current != o.blob {
			// Absence means the delete landed despite a lost reply. A different
			// object belongs to a successor and must be left in place.
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("release resident claim %s: %w", o.ref, errors.Join(last, ctx.Err()))
		case <-time.After(releaseRetryDelay):
		}
	}
}

func newResidentNonce() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate resident claim nonce: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}
