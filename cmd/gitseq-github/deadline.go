package main

import (
	"context"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
)

// The deadline one connector run submits under travels on its context, for the
// same reason it does in gs: one run gives every act it appends the same
// answer, and a package variable would be shared by every test in this package
// at once.

type submitDeadlineKey struct{}

func withSubmitDeadline(ctx context.Context, deadline time.Duration) context.Context {
	return context.WithValue(ctx, submitDeadlineKey{}, deadline)
}

// submitDeadline answers with this run's deadline, or with the value every
// caller had before the deadline could be set at all.
func submitDeadline(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Value(submitDeadlineKey{}).(time.Duration); ok {
		return deadline
	}
	return residentclient.DefaultSubmitDeadline
}
