package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hhaidrr/personal-telemetry/internal/event"
	"github.com/hhaidrr/personal-telemetry/internal/ownership"
)

type memStore struct {
	cursor Cursor
	seen   map[string]bool
	fps    map[string]*FingerprintState
}

func newMemStore() *memStore {
	return &memStore{seen: map[string]bool{}, fps: map[string]*FingerprintState{}}
}

func (s *memStore) Cursor(context.Context) (Cursor, error)       { return s.cursor, nil }
func (s *memStore) SaveCursor(_ context.Context, c Cursor) error { s.cursor = c; return nil }

func (s *memStore) Record(_ context.Context, o Occurrence) (FingerprintState, error) {
	fp, ok := s.fps[o.Fingerprint]
	if !ok {
		fp = &FingerprintState{FirstSeen: o.Time}
		s.fps[o.Fingerprint] = fp
	}
	if s.seen[o.EventID] {
		st := *fp
		st.Duplicate = true
		return st, nil
	}
	s.seen[o.EventID] = true
	fp.Count++
	return *fp, nil
}

func (s *memStore) MarkNotified(_ context.Context, fingerprint string, at time.Time) error {
	s.fps[fingerprint].LastNotified = at
	return nil
}

type sliceSource struct {
	events []event.Event
	next   Cursor
}

func (s sliceSource) Fetch(context.Context, Cursor) ([]event.Event, Cursor, error) {
	return s.events, s.next, nil
}

type fixedResolver string

func (r fixedResolver) SHA(context.Context, string, string) (string, error) { return string(r), nil }

type recorder struct {
	alerts []Alert
	err    error
}

func (r *recorder) Notify(_ context.Context, a Alert) error {
	if r.err != nil {
		return r.err
	}
	r.alerts = append(r.alerts, a)
	return nil
}

type ownerBlamer struct{}

func (ownerBlamer) Author(_ context.Context, _, path string, _ int) (string, error) {
	if path == "src/owned.py" {
		return "me@example.com", nil
	}
	return "teammate@example.com", nil
}

func newPipeline(store *memStore, n *recorder, now *time.Time) *Pipeline {
	return &Pipeline{
		Resolver: fixedResolver("abc123"),
		Matcher: &ownership.Matcher{
			Blamer:     ownerBlamer{},
			Identities: map[string]bool{"me@example.com": true},
			PathMap:    ownership.PathMap{"/workspace/": "src/"},
		},
		Store:    store,
		Notifier: n,
		Cooldown: time.Hour,
		Now:      func() time.Time { return *now },
	}
}

func ev(id, path string) event.Event {
	return event.Event{
		ID: id, Service: "api", Revision: "rev-1", ExcType: "ValueError", Message: "boom",
		Frames: []event.Frame{{Path: path, Line: 3, Func: "f"}},
	}
}

func TestRunOnceAlertsOnOwnedErrorsAndAdvancesCursor(t *testing.T) {
	store, n, now := newMemStore(), &recorder{}, time.Unix(0, 0)
	p := newPipeline(store, n, &now)

	src := sliceSource{events: []event.Event{ev("1", "/workspace/owned.py"), ev("2", "/workspace/other.py")}, next: "c1"}
	if err := p.RunOnce(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if len(n.alerts) != 1 {
		t.Fatalf("got %d alerts, want 1 (only the owned error)", len(n.alerts))
	}
	a := n.alerts[0]
	if a.Occurrence.Fingerprint != "ValueError@src/owned.py:f" || a.Occurrence.Match.Tier != ownership.Primary {
		t.Errorf("unexpected alert %+v", a.Occurrence)
	}
	if store.cursor != "c1" {
		t.Errorf("cursor = %q, want c1", store.cursor)
	}
}

func TestCooldownSuppressesRepeatsThenRealerts(t *testing.T) {
	store, n, now := newMemStore(), &recorder{}, time.Unix(0, 0)
	p := newPipeline(store, n, &now)
	ctx := context.Background()

	must(t, p.Handle(ctx, []event.Event{ev("1", "/workspace/owned.py")}))
	now = now.Add(10 * time.Minute)
	must(t, p.Handle(ctx, []event.Event{ev("2", "/workspace/owned.py")}))
	if len(n.alerts) != 1 {
		t.Fatalf("got %d alerts inside cooldown, want 1", len(n.alerts))
	}

	now = now.Add(time.Hour)
	must(t, p.Handle(ctx, []event.Event{ev("3", "/workspace/owned.py")}))
	if len(n.alerts) != 2 {
		t.Fatalf("got %d alerts after cooldown, want 2", len(n.alerts))
	}
	if n.alerts[1].State.Count != 3 {
		t.Errorf("count = %d, want 3", n.alerts[1].State.Count)
	}
}

func TestRetriedBatchDoesNotRealert(t *testing.T) {
	store, n, now := newMemStore(), &recorder{}, time.Unix(0, 0)
	p := newPipeline(store, n, &now)
	ctx := context.Background()

	must(t, p.Handle(ctx, []event.Event{ev("1", "/workspace/owned.py")}))
	now = now.Add(2 * time.Hour) // past cooldown, so only idempotency can suppress it
	must(t, p.Handle(ctx, []event.Event{ev("1", "/workspace/owned.py")}))
	if len(n.alerts) != 1 {
		t.Errorf("got %d alerts, want 1: a retried event must not re-alert", len(n.alerts))
	}
}

func TestFailedNotifyHoldsCursorAndRetries(t *testing.T) {
	store, n, now := newMemStore(), &recorder{err: errors.New("webhook down")}, time.Unix(0, 0)
	p := newPipeline(store, n, &now)
	ctx := context.Background()
	src := sliceSource{events: []event.Event{ev("1", "/workspace/owned.py")}, next: "c1"}

	if err := p.RunOnce(ctx, src); err == nil {
		t.Fatal("want error when notify fails")
	}
	if store.cursor != "" {
		t.Fatalf("cursor advanced to %q despite failure", store.cursor)
	}

	n.err = nil
	must(t, p.RunOnce(ctx, src))
	if len(n.alerts) != 1 {
		t.Errorf("got %d alerts after recovery, want 1", len(n.alerts))
	}
	if store.cursor != "c1" {
		t.Errorf("cursor = %q, want c1", store.cursor)
	}
}

func TestEventsWithoutFramesAreSkipped(t *testing.T) {
	store, n, now := newMemStore(), &recorder{}, time.Unix(0, 0)
	p := newPipeline(store, n, &now)
	must(t, p.Handle(context.Background(), []event.Event{{ID: "1", Message: "no stack"}}))
	if len(n.alerts) != 0 {
		t.Errorf("got %d alerts, want 0", len(n.alerts))
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
