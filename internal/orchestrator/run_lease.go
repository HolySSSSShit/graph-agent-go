package orchestrator

import (
	"context"
	"errors"
	"time"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

func (r *Runner) renewLease(ctx context.Context, lease core.RunLease, done chan<- struct{}, cancel context.CancelFunc) {
	defer close(done)
	duration := r.LeaseDuration
	if duration <= 0 {
		duration = 30 * time.Second
	}
	interval := duration / 3
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			next, err := renewLeaseWithRetry(ctx, r.Leases, lease, duration)
			if err != nil {
				cancel()
				return
			}
			lease = next
		}
	}
}

func renewLeaseWithRetry(ctx context.Context, store core.RunLeaseStore, lease core.RunLease, duration time.Duration) (core.RunLease, error) {
	next, err := store.RenewRunLease(ctx, lease, duration)
	if err == nil || errors.Is(err, core.ErrRunLeaseLost) {
		return next, err
	}
	safetyWindow := duration / 4
	if safetyWindow < time.Second {
		safetyWindow = time.Second
	}
	deadline := lease.LeaseUntil.Add(-safetyWindow)
	lastErr := err
	for attempt := 1; attempt <= 3; attempt++ {
		delay := time.Duration(attempt) * time.Second
		if delay > 2*time.Second {
			delay = 2 * time.Second
		}
		if time.Now().Add(delay).After(deadline) {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return core.RunLease{}, ctx.Err()
		case <-timer.C:
		}
		next, err = store.RenewRunLease(ctx, lease, duration)
		if err == nil || errors.Is(err, core.ErrRunLeaseLost) {
			return next, err
		}
		lastErr = err
	}
	return core.RunLease{}, lastErr
}
