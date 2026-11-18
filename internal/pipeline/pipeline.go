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
	// Blamed instead of the deployed commit when it can't be resolved; empty
	// makes resolution failures hold the cursor like any other error.
	FallbackRef string
	Matcher     *ownership.Matcher
	Store       Store
	Notifier    Notifier
	Cooldown    time.Duration
	Now         func() time.Time
}

// Any failed event holds the cursor so the whole batch is retried. That is safe
// because Record is idempotent, and it makes a permanently failing event fail
// loudly every run instead of being skipped silently. Each source has its own
// cursor, so one stuck environment doesn't hold back the others.
func (p *Pipeline) RunOnce(ctx context.Context, name string, src Source) error {
	cursor, err := p.Store.Cursor(ctx, name)
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
	return p.Store.SaveCursor(ctx, name, next)
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

const unparsedFingerprint = "pt:unparsed"

func (p *Pipeline) handle(ctx context.Context, e event.Event) error {
	if e.ParseError == "" && len(e.Frames) == 0 {
		return nil
	}
	sha, approximate, err := p.resolve(ctx, e)
	if errors.Is(err, ErrOtherCode) {
		return nil
	}
	if err != nil {
		return err
	}
	if e.ParseError != "" {
		if err := p.reportUnparsed(ctx, e); err != nil {
			return err
		}
	}
	if len(e.Frames) == 0 {
		return nil
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
		Fingerprint: fingerprint(e.Environment, e.ExcType, m),
		ExcType:     e.ExcType,
		Match:       m,
		Environment: e.Environment,
		Service:     e.Service,
		Job:         e.Job,
		Revision:    e.Revision,
		SHA:         sha,
		Approximate: approximate,
		Time:        e.Time,
		TraceID:     e.TraceID,
		Link:        e.Link,
	}
	return p.recordAndAlert(ctx, Alert{Occurrence: o, Message: e.Message})
}

func (p *Pipeline) resolve(ctx context.Context, e event.Event) (sha string, approximate bool, err error) {
	sha, err = p.Resolver.SHA(ctx, e)
	if err == nil || errors.Is(err, ErrOtherCode) {
		return sha, false, err
	}
	if p.FallbackRef == "" {
		return "", false, fmt.Errorf("resolve revision %q: %w", e.Revision, err)
	}
	return p.FallbackRef, true, nil
}

// Parse failures share one fingerprint so a format change alerts once per
// cooldown instead of once per entry. They never fail the run: that would hold
// the cursor on an entry that can't ever succeed.
func (p *Pipeline) reportUnparsed(ctx context.Context, e event.Event) error {
	o := Occurrence{
		EventID:     "unparsed:" + e.ID,
		Fingerprint: unparsedFingerprint,
		ExcType:     e.ExcType,
		Environment: e.Environment,
		Service:     e.Service,
		Job:         e.Job,
		Revision:    e.Revision,
		Time:        e.Time,
		TraceID:     e.TraceID,
		Link:        e.Link,
	}
	return p.recordAndAlert(ctx, Alert{Occurrence: o, Message: e.Message, ParseError: e.ParseError})
}

func (p *Pipeline) recordAndAlert(ctx context.Context, a Alert) error {
	o := a.Occurrence
	state, err := p.Store.Record(ctx, o)
	if err != nil {
		return fmt.Errorf("record: %w", err)
	}

	now := p.now()
	if !p.shouldNotify(state, now) {
		return nil
	}
	a.State = state
	if err := p.Notifier.Notify(ctx, a); err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	return p.Store.MarkNotified(ctx, o.Fingerprint, o.EventID, now)
}

// The cooldown is checked again on a retry, so a replayed occurrence alerts only
// if its earlier notify failed and no other occurrence has alerted since.
func (p *Pipeline) shouldNotify(s FingerprintState, now time.Time) bool {
	if s.EventNotified {
		return false
	}
	if s.LastNotified.IsZero() {
		return true
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
