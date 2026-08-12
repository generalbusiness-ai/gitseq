// Package observe defines Gitseq's bounded, exporter-neutral observation seam.
// It deliberately contains no OpenTelemetry or deployment configuration.
package observe

import (
	"context"
	"time"
)

// Operation is a closed, low-cardinality unit of work.
type Operation string

const (
	OperationHTTP       Operation = "http.server"
	OperationGit        Operation = "git.process"
	OperationSnapshot   Operation = "snapshot"
	OperationVerify     Operation = "verify"
	OperationFold       Operation = "fold"
	OperationStatusView Operation = "statusview"
	OperationEncode     Operation = "encode"
	OperationSubmit     Operation = "submit"
	OperationWait       Operation = "wait"
)

// Path is a closed discriminator within an operation. It must never contain
// repository paths, object IDs, actor names, request text, or payload data.
type Path string

const (
	PathNone          Path = "none"
	PathCache         Path = "cache"
	PathIncremental   Path = "incremental"
	PathCheckpoint    Path = "checkpoint"
	PathCold          Path = "cold"
	PathRead          Path = "read"
	PathWrite         Path = "write"
	PathRef           Path = "ref"
	PathScan          Path = "scan"
	PathSignature     Path = "signature"
	PathOther         Path = "other"
	PathStatusSummary Path = "status_summary"
	PathSubmission    Path = "submission"
	PathLongPoll      Path = "long_poll"
)

// Outcome is intentionally coarser than an error string.
type Outcome string

const (
	OutcomeOK        Outcome = "ok"
	OutcomeCancelled Outcome = "cancelled"
	OutcomeTimeout   Outcome = "timeout"
	OutcomeRefused   Outcome = "refused"
	OutcomeError     Outcome = "error"
)

// Measurement contains bounded numeric work evidence. Route is used only for
// registered HTTP route templates. Status is an HTTP status class value.
type Measurement struct {
	Operation Operation
	Path      Path
	Outcome   Outcome
	Route     string
	Method    string
	Status    int
	Duration  time.Duration
	Items     int64
}

// Observer consumes already-sanitized measurements. Implementations must not
// retain the context beyond Record.
type Observer interface {
	Record(context.Context, Measurement)
}

type observerKey struct{}

// WithObserver carries the composition-owned observer through request code.
func WithObserver(ctx context.Context, observer Observer) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, observerKey{}, observer)
}

// FromContext returns the composition-owned observer, if observation is on.
func FromContext(ctx context.Context) Observer {
	observer, _ := ctx.Value(observerKey{}).(Observer)
	return observer
}

// Begin records one duration without allocating a closure when disabled.
func Begin(ctx context.Context, observer Observer, operation Operation, path Path) func(error) {
	if observer == nil {
		return nil
	}
	started := time.Now()
	return func(err error) {
		observer.Record(ctx, Measurement{
			Operation: operation, Path: path, Outcome: Classify(ctx, err), Duration: time.Since(started), Items: 1,
		})
	}
}

// Classify maps arbitrary errors onto a finite vocabulary.
func Classify(ctx context.Context, err error) Outcome {
	if err == nil {
		return OutcomeOK
	}
	if ctx != nil {
		switch ctx.Err() {
		case context.Canceled:
			return OutcomeCancelled
		case context.DeadlineExceeded:
			return OutcomeTimeout
		}
	}
	return OutcomeError
}

// GitPath reduces Git's command vocabulary to bounded cost classes.
func GitPath(arguments []string) Path {
	for _, argument := range arguments {
		switch argument {
		case "rev-list", "cat-file", "ls-tree", "show":
			return PathScan
		case "rev-parse", "update-ref":
			return PathRef
		case "verify-commit":
			return PathSignature
		case "hash-object", "mktree", "commit-tree":
			return PathWrite
		}
	}
	return PathOther
}
