package store

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/hhaidrr/personal-telemetry/internal/event"
	"github.com/hhaidrr/personal-telemetry/internal/ownership"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

func open(t *testing.T) *SQLite {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func occurrence(eventID string, at time.Time) pipeline.Occurrence {
	return pipeline.Occurrence{
		EventID:     eventID,
		Fingerprint: "ValueError@src/x.py:f",
		ExcType:     "ValueError",
		Match: ownership.Match{
			Tier:  ownership.Primary,
			Frame: event.Frame{Path: "src/x.py", Line: 3, Func: "f"},
		},
		Service:  "api",
		Revision: "rev-1",
		SHA:      "abc123",
		Time:     at,
	}
}

func TestCursor(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	if c, err := s.Cursor(ctx); err != nil || c != "" {
		t.Fatalf("fresh cursor = %q, %v; want empty", c, err)
	}
	for _, want := range []pipeline.Cursor{"c1", "c2"} {
		if err := s.SaveCursor(ctx, want); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Cursor(ctx); got != want {
			t.Errorf("cursor = %q, want %q", got, want)
		}
	}
}

func TestRecordCountsAndDetectsDuplicates(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

	st, err := s.Record(ctx, occurrence("1", t0))
	if err != nil {
		t.Fatal(err)
	}
	if st.Duplicate || st.Count != 1 || !st.FirstSeen.Equal(t0) || !st.LastNotified.IsZero() {
		t.Fatalf("first record = %+v", st)
	}

	st, _ = s.Record(ctx, occurrence("1", t0))
	if !st.Duplicate || st.Count != 1 {
		t.Errorf("repeat record = %+v; want duplicate, count 1", st)
	}

	earlier := t0.Add(-time.Hour)
	st, _ = s.Record(ctx, occurrence("2", earlier))
	if st.Duplicate || st.Count != 2 || !st.FirstSeen.Equal(earlier) {
		t.Errorf("out-of-order record = %+v; want count 2, first seen %v", st, earlier)
	}
}

func TestMarkNotified(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 30, 12, 0, 0, 123, time.UTC)

	if _, err := s.Record(ctx, occurrence("1", t0)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotified(ctx, "ValueError@src/x.py:f", t0); err != nil {
		t.Fatal(err)
	}
	st, err := s.Record(ctx, occurrence("2", t0))
	if err != nil {
		t.Fatal(err)
	}
	if !st.LastNotified.Equal(t0) {
		t.Errorf("last notified = %v, want %v", st.LastNotified, t0)
	}
}

func TestIDsAreUUIDv7AndLinkOccurrenceToFingerprint(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, err := s.Record(ctx, occurrence("1", time.Now())); err != nil {
		t.Fatal(err)
	}

	var fingerprintID, occurrenceID, linkedID string
	if err := s.db.QueryRow(`SELECT id FROM fingerprints`).Scan(&fingerprintID); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id, fingerprint_id FROM occurrences`).Scan(&occurrenceID, &linkedID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{fingerprintID, occurrenceID} {
		u, err := uuid.Parse(id)
		if err != nil {
			t.Fatalf("id %q: %v", id, err)
		}
		if version := u[6] >> 4; version != 7 {
			t.Errorf("id %q is version %d, want 7", id, version)
		}
	}
	if linkedID != fingerprintID {
		t.Errorf("occurrence links to %q, want %q", linkedID, fingerprintID)
	}
}
