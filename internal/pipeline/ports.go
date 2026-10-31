package pipeline

import (
	"context"
	"time"

	"github.com/hhaidrr/personal-telemetry/internal/event"
	"github.com/hhaidrr/personal-telemetry/internal/ownership"
)

type Cursor string

type Source interface {
	Fetch(ctx context.Context, cursor Cursor) ([]event.Event, Cursor, error)
}

type Resolver interface {
	SHA(ctx context.Context, service, revision string) (string, error)
}

type Notifier interface {
	Notify(ctx context.Context, a Alert) error
}

type Store interface {
	Cursor(ctx context.Context) (Cursor, error)
	SaveCursor(ctx context.Context, c Cursor) error
	// Must be idempotent on EventID: a repeat returns Duplicate and is not counted.
	Record(ctx context.Context, o Occurrence) (FingerprintState, error)
	MarkNotified(ctx context.Context, fingerprint string, at time.Time) error
}

type Occurrence struct {
	EventID     string
	Fingerprint string
	ExcType     string
	Match       ownership.Match
	Service     string
	Revision    string
	SHA         string
	Time        time.Time
	TraceID     string
}

type FingerprintState struct {
	Duplicate    bool
	Count        int
	FirstSeen    time.Time
	LastNotified time.Time
}

type Alert struct {
	Occurrence Occurrence
	State      FingerprintState
	Message    string
}
