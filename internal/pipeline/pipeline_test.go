package pipeline

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hhaidrr/personal-telemetry/internal/event"
	"github.com/hhaidrr/personal-telemetry/internal/ownership"
)

type memStore struct {
	cursors  map[string]Cursor
	seen     map[string]bool
	notified map[string]bool
	fps      map[string]*FingerprintState
}

func newMemStore() *memStore {
	return &memStore{cursors: map[string]Cursor{}, seen: map[string]bool{}, notified: map[string]bool{}, fps: map[string]*FingerprintState{}}
}

func (s *memStore) Cursor(_ context.Context, name string) (Cursor, error) {
	return s.cursors[name], nil
}

func (s *memStore) SaveCursor(_ context.Context, name string, c Cursor) error {
	s.cursors[name] = c
	return nil
}

func (s *memStore) Record(_ context.Context, o Occurrence) (FingerprintState, error) {
	fp, ok := s.fps[o.Fingerprint]
	if !ok {
		fp = &FingerprintState{FirstSeen: o.Time}
		s.fps[o.Fingerprint] = fp
	}
	if s.seen[o.EventID] {
		st := *fp
		st.Duplicate = true
		st.EventNotified = s.notified[o.EventID]
		return st, nil
	}
	s.seen[o.EventID] = true
	fp.Count++
	return *fp, nil
}

func (s *memStore) MarkNotified(_ context.Context, fingerprint, eventID string, at time.Time) error {
	s.fps[fingerprint].LastNotified = at
	s.notified[eventID] = true
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

func (r fixedResolver) SHA(context.Context, event.Event) (string, error) { return string(r), nil }

type failingResolver struct{}

func (failingResolver) SHA(context.Context, event.Event) (string, error) {
	return "", errors.New("revision deleted")
}

type otherCodeResolver struct{}

func (otherCodeResolver) SHA(context.Context, event.Event) (string, error) {
	return "", fmt.Errorf("bank-engine image: %w", ErrOtherCode)
}

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

func (ownerBlamer) Authors(_ context.Context, _, path string, _ int) ([]string, error) {
	if path == "src/owned.py" {
		return []string{"me@example.com"}, nil
	}
	return []string{"teammate@example.com"}, nil
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
	if err := p.RunOnce(context.Background(), "develop", src); err != nil {
		t.Fatal(err)
	}
	if len(n.alerts) != 1 {
		t.Fatalf("got %d alerts, want 1 (only the owned error)", len(n.alerts))
	}
	a := n.alerts[0]
	if a.Occurrence.Fingerprint != "ValueError@src/owned.py:f" || a.Occurrence.Match.Tier != ownership.Primary {
		t.Errorf("unexpected alert %+v", a.Occurrence)
	}
	if store.cursors["develop"] != "c1" {
		t.Errorf("cursor = %q, want c1", store.cursors["develop"])
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

	if err := p.RunOnce(ctx, "develop", src); err == nil {
		t.Fatal("want error when notify fails")
	}
	if store.cursors["develop"] != "" {
		t.Fatalf("cursor advanced to %q despite failure", store.cursors["develop"])
	}

	n.err = nil
	must(t, p.RunOnce(ctx, "develop", src))
	if len(n.alerts) != 1 {
		t.Errorf("got %d alerts after recovery, want 1", len(n.alerts))
	}
	if store.cursors["develop"] != "c1" {
		t.Errorf("cursor = %q, want c1", store.cursors["develop"])
	}
}

func TestRetryAfterCooldownAlertsWhenPreviousNotifyFailed(t *testing.T) {
	store, n, now := newMemStore(), &recorder{}, time.Unix(0, 0)
	p := newPipeline(store, n, &now)
	ctx := context.Background()

	must(t, p.Handle(ctx, []event.Event{ev("1", "/workspace/owned.py")}))
	now = now.Add(2 * time.Hour)
	n.err = errors.New("webhook down")
	if err := p.Handle(ctx, []event.Event{ev("2", "/workspace/owned.py")}); err == nil {
		t.Fatal("want error when notify fails")
	}

	n.err = nil
	must(t, p.Handle(ctx, []event.Event{ev("2", "/workspace/owned.py")}))
	if len(n.alerts) != 2 {
		t.Errorf("got %d alerts, want 2: the failed alert must be retried", len(n.alerts))
	}
}

func TestParseFailuresAlertOncePerCooldownWithoutFailingTheRun(t *testing.T) {
	store, n, now := newMemStore(), &recorder{}, time.Unix(0, 0)
	p := newPipeline(store, n, &now)

	unparsed := func(id string) event.Event {
		return event.Event{ID: id, Service: "api", Message: "Traceback (most recent call last):\n???", ParseError: "no frames"}
	}
	src := sliceSource{events: []event.Event{unparsed("1"), unparsed("2")}, next: "c1"}
	must(t, p.RunOnce(context.Background(), "develop", src))

	if len(n.alerts) != 1 {
		t.Fatalf("got %d alerts, want 1", len(n.alerts))
	}
	if a := n.alerts[0]; a.ParseError != "no frames" || a.Occurrence.Fingerprint != unparsedFingerprint {
		t.Errorf("unexpected alert %+v", a)
	}
	if store.cursors["develop"] != "c1" {
		t.Errorf("cursor = %q, want c1", store.cursors["develop"])
	}
}

func TestPartiallyParsedEventIsStillAttributed(t *testing.T) {
	store, n, now := newMemStore(), &recorder{}, time.Unix(0, 0)
	p := newPipeline(store, n, &now)

	e := ev("1", "/workspace/owned.py")
	e.ParseError = "no exception line"
	must(t, p.Handle(context.Background(), []event.Event{e}))

	if len(n.alerts) != 2 {
		t.Fatalf("got %d alerts, want a parse-failure alert and an ownership alert", len(n.alerts))
	}
	if n.alerts[1].Occurrence.Match.Tier != ownership.Primary {
		t.Errorf("second alert = %+v, want the owned error", n.alerts[1].Occurrence)
	}
}

func TestUnresolvableRevisionFallsBackToApproximateBlame(t *testing.T) {
	store, n, now := newMemStore(), &recorder{}, time.Unix(0, 0)
	p := newPipeline(store, n, &now)
	p.Resolver, p.FallbackRef = failingResolver{}, "main"

	must(t, p.Handle(context.Background(), []event.Event{ev("1", "/workspace/owned.py")}))

	if len(n.alerts) != 1 {
		t.Fatalf("got %d alerts, want 1", len(n.alerts))
	}
	if o := n.alerts[0].Occurrence; o.SHA != "main" || !o.Approximate {
		t.Errorf("sha = %q, approximate = %v; want main, true", o.SHA, o.Approximate)
	}
}

func TestUnresolvableRevisionWithoutFallbackHoldsTheCursor(t *testing.T) {
	store, n, now := newMemStore(), &recorder{}, time.Unix(0, 0)
	p := newPipeline(store, n, &now)
	p.Resolver = failingResolver{}

	src := sliceSource{events: []event.Event{ev("1", "/workspace/owned.py")}, next: "c1"}
	if err := p.RunOnce(context.Background(), "develop", src); err == nil {
		t.Fatal("want error")
	}
	if store.cursors["develop"] != "" {
		t.Errorf("cursor = %q, want it held", store.cursors["develop"])
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

func TestSameBugInAnotherEnvironmentAlertsSeparately(t *testing.T) {
	store, n, now := newMemStore(), &recorder{}, time.Unix(0, 0)
	p := newPipeline(store, n, &now)

	develop, production := ev("1", "/workspace/owned.py"), ev("2", "/workspace/owned.py")
	develop.Environment, production.Environment = "develop", "production"
	must(t, p.RunOnce(context.Background(), "develop", sliceSource{events: []event.Event{develop}, next: "d1"}))
	must(t, p.RunOnce(context.Background(), "production", sliceSource{events: []event.Event{production}, next: "p1"}))

	if len(n.alerts) != 2 {
		t.Fatalf("got %d alerts, want one per environment despite the cooldown", len(n.alerts))
	}
	if fp := n.alerts[1].Occurrence.Fingerprint; fp != "production/ValueError@src/owned.py:f" {
		t.Errorf("fingerprint = %s", fp)
	}
	if store.cursors["develop"] != "d1" || store.cursors["production"] != "p1" {
		t.Errorf("cursors = %v, want one per environment", store.cursors)
	}
}

func TestEventsFromOtherCodeAreSkippedEvenWithAFallback(t *testing.T) {
	store, n, now := newMemStore(), &recorder{}, time.Unix(0, 0)
	p := newPipeline(store, n, &now)
	p.Resolver, p.FallbackRef = otherCodeResolver{}, "main"

	unparsable := ev("2", "/workspace/owned.py")
	unparsable.Frames, unparsable.ParseError = nil, "no frames"
	src := sliceSource{events: []event.Event{ev("1", "/workspace/owned.py"), unparsable}, next: "c1"}
	must(t, p.RunOnce(context.Background(), "develop", src))

	if len(n.alerts) != 0 || store.cursors["develop"] != "c1" {
		t.Errorf("got %d alerts, cursor %q; want none and the cursor moved on", len(n.alerts), store.cursors["develop"])
	}
}
