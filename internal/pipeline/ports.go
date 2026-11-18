package pipeline

import (
	"context"
	"errors"
	"time"

	"github.com/hhaidrr/personal-telemetry/internal/event"
	"github.com/hhaidrr/personal-telemetry/internal/ownership"
)

type Cursor string

type Source interface {
	Fetch(ctx context.Context, cursor Cursor) ([]event.Event, Cursor, error)
}

type Resolver interface {
	// ErrOtherCode means the event's workload isn't built from the repo.
	SHA(ctx context.Context, e event.Event) (string, error)
}

var ErrOtherCode = errors.New("workload doesn't run the repo's code")

type Notifier interface {
	Notify(ctx context.Context, a Alert) error
}

type Store interface {
	Cursor(ctx context.Context, source string) (Cursor, error)
	SaveCursor(ctx context.Context, source string, c Cursor) error
	// Must be idempotent on EventID: a repeat returns Duplicate and is not counted.
	Record(ctx context.Context, o Occurrence) (FingerprintState, error)
	MarkNotified(ctx context.Context, fingerprint, eventID string, at time.Time) error
}

type Occurrence struct {
	EventID     string
	Fingerprint string
	ExcType     string
	Match       ownership.Match
	Environment string
	Project     string
	Service     string
	Job         bool
	Revision    string
	SHA         string
	// The deployed commit couldn't be resolved, so blame ran against the fallback ref.
	Approximate bool
	Time        time.Time
	TraceID     string
	Link        string
}

type FingerprintState struct {
	Duplicate bool
	// Whether this occurrence (not just the fingerprint) was already alerted.
	EventNotified bool
	Count         int
	FirstSeen     time.Time
	LastNotified  time.Time
}

type Alert struct {
	Occurrence Occurrence
	State      FingerprintState
	Message    string
	// Set for alerts about entries pt could not parse rather than owned errors.
	ParseError string
	// Set for alerts about pt itself, e.g. it can't read logs; Occurrence is
	// then empty and Message has the details.
	System string
}
