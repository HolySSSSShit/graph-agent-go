package policy

import (
	"context"
	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

type Guard struct{}

func (Guard) AuthorizeAPI(context.Context, core.Identity, string) error { return nil }

type Retry struct{}

func (Retry) Next(attempt int, _ error, op core.Operation) (core.RetryDecision, error) {
	return core.RetryDecision{Retry: op.Idempotent && attempt < 2}, nil
}
