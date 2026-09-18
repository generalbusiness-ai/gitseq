package main

import (
	"context"
	"flag"
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
// every caller had before the deadline could be set at all.
func submitDeadline(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Value(submitDeadlineKey{}).(time.Duration); ok && deadline > 0 {
		return deadline
	}
	return residentclient.DefaultSubmitDeadline
}
