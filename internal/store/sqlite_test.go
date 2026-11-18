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

	if c, err := s.Cursor(ctx, "develop"); err != nil || c != "" {
		t.Fatalf("fresh cursor = %q, %v; want empty", c, err)
	}
	for _, want := range []pipeline.Cursor{"c1", "c2"} {
		if err := s.SaveCursor(ctx, "develop", want); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Cursor(ctx, "develop"); got != want {
			t.Errorf("cursor = %q, want %q", got, want)
		}
	}
	if c, _ := s.Cursor(ctx, "production"); c != "" {
		t.Errorf("production cursor = %q, want its own empty cursor", c)
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
	if err := s.MarkNotified(ctx, "ValueError@src/x.py:f", "1", t0); err != nil {
		t.Fatal(err)
	}

	st, err := s.Record(ctx, occurrence("2", t0))
	if err != nil {
		t.Fatal(err)
	}
	if !st.LastNotified.Equal(t0) || st.EventNotified {
		t.Errorf("new occurrence: last notified = %v, event notified = %v", st.LastNotified, st.EventNotified)
	}

	st, err = s.Record(ctx, occurrence("1", t0))
	if err != nil {
		t.Fatal(err)
	}
	if !st.Duplicate || !st.EventNotified {
		t.Errorf("replayed notified occurrence: duplicate = %v, event notified = %v", st.Duplicate, st.EventNotified)
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

func TestIssuesAndIssueDetail(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for i, at := range []time.Time{now.Add(-72 * time.Hour), now.Add(-2 * time.Hour), now.Add(-time.Hour)} {
		o := occurrence(uuid.NewV7().String(), at)
		o.Environment, o.Project, o.Link, o.Job = "production", "divdot-production", "https://l/"+string(rune('a'+i)), true
		if i == 2 {
			o.Revision, o.SHA = "rev-2", "def456"
		}
		if _, err := s.Record(ctx, o); err != nil {
			t.Fatal(err)
		}
	}

	issues, err := s.Issues(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	i := issues[0]
	if i.Count != 3 || i.Recent != 2 || i.Environment != "production" || !i.Job || i.Line != 3 || i.Path != "src/x.py" {
		t.Errorf("issue = %+v", i)
	}

	days, err := s.DailyByIssue(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, d := range days {
		if d.Key != i.Key || d.Environment != "production" {
			t.Errorf("day = %+v", d)
		}
		sum += d.Count
	}
	if sum != 3 || len(days) < 2 {
		t.Errorf("daily rows = %+v, want 3 occurrences over at least 2 days", days)
	}

	d, err := s.Issue(ctx, i.Key, now.AddDate(0, 0, -6))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Daily) != 7 {
		t.Errorf("got %d days, want 7 including empty ones", len(d.Daily))
	}
	total := 0
	for _, day := range d.Daily {
		total += day.Count
	}
	if total != 3 {
		t.Errorf("daily counts sum to %d, want 3", total)
	}
	if len(d.Revisions) != 2 || d.Revisions[0].Revision != "rev-2" || d.Revisions[1].Count != 2 {
		t.Errorf("revisions = %+v", d.Revisions)
	}
	if d.Latest.Link != "https://l/c" || d.Latest.Project != "divdot-production" {
		t.Errorf("latest = %+v", d.Latest)
	}
}
