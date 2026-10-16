package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"syscall"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
)

// How long the resident has to answer a submission is a property of one gs
// invocation, not of the process and not of any one act: a command that appends
// several acts gives each of them the same answer. It therefore travels on the
// invocation's context, which already reaches every path that dials the
// resident, rather than in a package variable that every parallel test in this
// package would share.

type submitDeadlineKey struct{}

// actFlags is flags() for a command that appends. The deadline flag is
// registered only here, so a command that merely reads does not advertise a
// setting it would ignore.
func actFlags(name string, arguments []string) (*flag.FlagSet, *string) {
	set, repo := flags(name, arguments)
	set.String("deadline", "", "how long the resident has to answer each submission, such as 30s; default "+
		residentclient.DefaultSubmitDeadline.String()+", or "+residentclient.SubmitDeadlineEnvironment)
	return set, repo
}

// withSubmitDeadline reads the deadline this invocation was given and binds it
// to the context every submission below it will use. It is called after the
// flags are parsed and before the first act is built, so a refusal reaches the
// author before anything is signed.
func withSubmitDeadline(ctx context.Context, set *flag.FlagSet) (context.Context, error) {
	explicit := ""
	if registered := set.Lookup("deadline"); registered != nil {
		explicit = registered.Value.String()
	}
	deadline, err := residentclient.ResolveSubmitDeadline(explicit)
	if err != nil {
		return ctx, usageErrorf(set, "%s", err)
	}
	return context.WithValue(ctx, submitDeadlineKey{}, deadline), nil
}

// submitDeadline answers with this invocation's deadline. A context that was
// never given one — a test calling a helper directly, or a path that reaches a
// submission without going through a command's flags — gets the same default
// every caller had before the deadline could be set at all. There is no guard
// against a non-positive value here: withSubmitDeadline is the only writer and
// it refuses one, so a second check could never be shown to do anything.
func submitDeadline(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Value(submitDeadlineKey{}).(time.Duration); ok {
		return deadline
	}
	return residentclient.DefaultSubmitDeadline
}

// mergeSuccessionDeadline is how long the resident has to answer each
// submission that records a merge whose target has already moved: the receipt
// assertion, its retirements and its successors. Before the move, a slow
// resident costs nothing, because the merge refuses and nothing has changed.
// After the move, giving up early leaves the target carrying a merge that has
// no durable receipt until somebody resumes it by hand. So this path waits for
// the resident to finish the work it was given, and stops only at this bound,
// which is far above what a cold resident needs to fold a large log.
const mergeSuccessionDeadline = 10 * time.Minute

// withMergeSuccessionDeadline gives a merge's durable suffix at least
// mergeSuccessionDeadline per submission. A longer --deadline still wins.
func withMergeSuccessionDeadline(ctx context.Context) context.Context {
	if submitDeadline(ctx) >= mergeSuccessionDeadline {
		return ctx
	}
	return context.WithValue(ctx, submitDeadlineKey{}, mergeSuccessionDeadline)
}

// mergeSuccessionRefusal says how to recover when a merge's durable suffix
// stops after the target has moved. When the resident's answer was lost — the
// wait ran out, or the connection closed before a response — the act it was
// sent may already have landed, and nothing about a batch file applies: the
// merge resumes from the receipt sealed in Git. Any other failure is reported
// as it came.
func mergeSuccessionRefusal(err error, receipt mergeReceipt) error {
	var failure *batchError
	if !errors.As(err, &failure) || failure.cause == nil || !answerLost(failure.cause) {
		return fmt.Errorf("record merge succession: %w", err)
	}
	return fmt.Errorf("record merge succession: the target already carries merge %s, but the resident's answer was lost, "+
		"so the act it was sent may already have landed (%w). Run the same gs merge or gs land again: it finds the "+
		"receipt sealed in Git and appends only what is still missing", receipt.MergeHead, failure.cause)
}

// answerLost reports whether a submission failed without a definite answer:
// the deadline expired, or the exchange broke after the request was sent. A
// refused dial reached no resident, and a complete refusal is an answer.
func answerLost(err error) bool {
	if residentclient.TimedOut(err) {
		return true
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return false
	}
	return residentclient.IsTransportError(err) || residentclient.IsReadError(err)
}
