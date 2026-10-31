package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hhaidrr/personal-telemetry/internal/event"
	"github.com/hhaidrr/personal-telemetry/internal/ownership"
)

const defaultCooldown = time.Hour

type Pipeline struct {
	Resolver Resolver
	Matcher  *ownership.Matcher
	Store    Store
	Notifier Notifier
	Cooldown time.Duration
	Now      func() time.Time
}

// Any failed event holds the cursor so the whole batch is retried. That is safe
// because Record is idempotent, and it makes a permanently failing event fail
// loudly every run instead of being skipped silently.
func (p *Pipeline) RunOnce(ctx context.Context, src Source) error {
	cursor, err := p.Store.Cursor(ctx)
	if err != nil {
		return fmt.Errorf("load cursor: %w", err)
	}
	events, next, err := src.Fetch(ctx, cursor)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	if err := p.Handle(ctx, events); err != nil {
		return err
	}
	if next == cursor {
		return nil
	}
	return p.Store.SaveCursor(ctx, next)
}

func (p *Pipeline) Handle(ctx context.Context, events []event.Event) error {
	var errs []error
	for _, e := range events {
		if err := p.handle(ctx, e); err != nil {
			errs = append(errs, fmt.Errorf("event %s: %w", e.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (p *Pipeline) handle(ctx context.Context, e event.Event) error {
	if len(e.Frames) == 0 {
		return nil
	}
	sha, err := p.Resolver.SHA(ctx, e.Service, e.Revision)
	if err != nil {
		return fmt.Errorf("resolve revision %q: %w", e.Revision, err)
	}
	m, err := p.Matcher.Match(ctx, sha, e.Frames)
	if err != nil {
		return err
	}
	if m.Tier == ownership.None {
		return nil
	}

	o := Occurrence{
		EventID:     e.ID,
		Fingerprint: fingerprint(e.ExcType, m),
		ExcType:     e.ExcType,
		Match:       m,
		Service:     e.Service,
		Revision:    e.Revision,
		SHA:         sha,
		Time:        e.Time,
		TraceID:     e.TraceID,
	}
	state, err := p.Store.Record(ctx, o)
	if err != nil {
		return fmt.Errorf("record: %w", err)
	}

	now := p.now()
	if !p.shouldNotify(state, now) {
		return nil
	}
	if err := p.Notifier.Notify(ctx, Alert{Occurrence: o, State: state, Message: e.Message}); err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	return p.Store.MarkNotified(ctx, o.Fingerprint, now)
}

func (p *Pipeline) shouldNotify(s FingerprintState, now time.Time) bool {
	if s.LastNotified.IsZero() {
		return true
	}
	// A retried event only re-alerts when its previous notify failed (caught above).
	if s.Duplicate {
		return false
	}
	cooldown := p.Cooldown
	if cooldown == 0 {
		cooldown = defaultCooldown
	}
	return now.Sub(s.LastNotified) >= cooldown
}

func (p *Pipeline) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}
