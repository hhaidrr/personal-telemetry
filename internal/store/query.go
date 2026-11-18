package store

import (
	"context"
	"time"
)

// One fingerprint, with where it last happened.
type Issue struct {
	Key         string
	ExcType     string
	Path        string
	Func        string
	Line        int
	Tier        string
	Subsystem   string
	Environment string
	Service     string
	Job         bool
	Count       int
	Recent      int
	FirstSeen   time.Time
	LastSeen    time.Time
}

type IssueDetail struct {
	Issue
	Daily     []DayCount
	Revisions []RevisionCount
	Latest    Occurrence
}

type DayCount struct {
	Day   time.Time
	Count int
}

type RevisionCount struct {
	Revision  string
	SHA       string
	Job       bool
	Count     int
	FirstSeen time.Time
	LastSeen  time.Time
}

type IssueDay struct {
	Key         string
	Environment string
	Day         time.Time
	Count       int
}

type Occurrence struct {
	EventID string
	Project string
	Link    string
	TraceID string
	Time    time.Time
}

const issueColumns = `
	f.key, f.exc_type, f.frame_path, f.frame_func, f.tier, f.subsystem, f.count, f.first_seen, f.last_seen,
	(SELECT count(*) FROM occurrences r WHERE r.fingerprint_id = f.id AND r.time >= ?),
	o.environment, o.service, o.job, o.line
	FROM fingerprints f
	JOIN occurrences o ON o.id = (SELECT id FROM occurrences WHERE fingerprint_id = f.id ORDER BY time DESC LIMIT 1)`

// Recent counts occurrences since the given time.
func (s *SQLite) Issues(ctx context.Context, since time.Time) ([]Issue, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+issueColumns+` ORDER BY f.last_seen DESC`, formatTime(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var issues []Issue
	for rows.Next() {
		i, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		issues = append(issues, i)
	}
	return issues, rows.Err()
}

// Daily covers the days since the given time, oldest first, including empty ones.
func (s *SQLite) Issue(ctx context.Context, key string, since time.Time) (IssueDetail, error) {
	var d IssueDetail
	i, err := scanIssue(s.db.QueryRowContext(ctx, `SELECT `+issueColumns+` WHERE f.key = ?`, formatTime(since), key))
	if err != nil {
		return d, err
	}
	d.Issue = i
	if d.Daily, err = s.daily(ctx, key, since); err != nil {
		return d, err
	}
	if d.Revisions, err = s.revisions(ctx, key); err != nil {
		return d, err
	}
	var at string
	err = s.db.QueryRowContext(ctx, `
		SELECT o.event_id, o.project, o.link, o.trace_id, o.time
		FROM occurrences o JOIN fingerprints f ON f.id = o.fingerprint_id
		WHERE f.key = ? ORDER BY o.time DESC LIMIT 1`, key,
	).Scan(&d.Latest.EventID, &d.Latest.Project, &d.Latest.Link, &d.Latest.TraceID, &at)
	if err != nil {
		return d, err
	}
	d.Latest.Time, err = parseTime(at)
	return d, err
}

func (s *SQLite) daily(ctx context.Context, key string, since time.Time) ([]DayCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT substr(o.time, 1, 10), count(*)
		FROM occurrences o JOIN fingerprints f ON f.id = o.fingerprint_id
		WHERE f.key = ? AND o.time >= ? GROUP BY 1`, key, formatTime(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var day string
		var n int
		if err := rows.Scan(&day, &n); err != nil {
			return nil, err
		}
		counts[day] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var days []DayCount
	for day := since.UTC().Truncate(24 * time.Hour); !day.After(time.Now().UTC()); day = day.AddDate(0, 0, 1) {
		days = append(days, DayCount{Day: day, Count: counts[day.Format("2006-01-02")]})
	}
	return days, nil
}

func (s *SQLite) revisions(ctx context.Context, key string) ([]RevisionCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.revision, o.sha, max(o.job), count(*), min(o.time), max(o.time)
		FROM occurrences o JOIN fingerprints f ON f.id = o.fingerprint_id
		WHERE f.key = ? GROUP BY o.revision, o.sha ORDER BY max(o.time) DESC LIMIT 50`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RevisionCount
	for rows.Next() {
		var r RevisionCount
		var first, last string
		if err := rows.Scan(&r.Revision, &r.SHA, &r.Job, &r.Count, &first, &last); err != nil {
			return nil, err
		}
		if r.FirstSeen, err = parseTime(first); err != nil {
			return nil, err
		}
		if r.LastSeen, err = parseTime(last); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanIssue(row interface{ Scan(...any) error }) (Issue, error) {
	var i Issue
	var first, last string
	if err := row.Scan(&i.Key, &i.ExcType, &i.Path, &i.Func, &i.Tier, &i.Subsystem, &i.Count, &first, &last,
		&i.Recent, &i.Environment, &i.Service, &i.Job, &i.Line); err != nil {
		return i, err
	}
	var err error
	if i.FirstSeen, err = parseTime(first); err != nil {
		return i, err
	}
	i.LastSeen, err = parseTime(last)
	return i, err
}

// Occurrences per issue, environment and day since the given time (zero for
// all of them): the raw material for every chart.
func (s *SQLite) DailyByIssue(ctx context.Context, since time.Time) ([]IssueDay, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.key, o.environment, substr(o.time, 1, 10), count(*)
		FROM occurrences o JOIN fingerprints f ON f.id = o.fingerprint_id
		WHERE o.time >= ? GROUP BY 1, 2, 3`, formatTime(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IssueDay
	for rows.Next() {
		var d IssueDay
		var day string
		if err := rows.Scan(&d.Key, &d.Environment, &day, &d.Count); err != nil {
			return nil, err
		}
		if d.Day, err = time.Parse("2006-01-02", day); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
