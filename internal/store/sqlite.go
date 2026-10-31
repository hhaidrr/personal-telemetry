package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"time"
	"uuid"

	_ "modernc.org/sqlite"

	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

//go:embed schema.sql
var schema string

// Fixed width so timestamps compare correctly as text, including in SQL min/max.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

type SQLite struct {
	db *sql.DB
}

var _ pipeline.Store = (*SQLite)(nil)

// No WAL: the database is shipped between runs as a single file, and WAL keeps
// committed data in a sidecar file until checkpoint.
func Open(path string) (*SQLite, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(DELETE)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: apply schema: %w", err)
	}
	return &SQLite{db: db}, nil
}

func (s *SQLite) Close() error { return s.db.Close() }

func (s *SQLite) Cursor(ctx context.Context) (pipeline.Cursor, error) {
	var c string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM cursor WHERE id = 1`).Scan(&c)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return pipeline.Cursor(c), err
}

func (s *SQLite) SaveCursor(ctx context.Context, c pipeline.Cursor) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO cursor (id, value) VALUES (1, ?) ON CONFLICT (id) DO UPDATE SET value = excluded.value`,
		string(c))
	return err
}

func (s *SQLite) Record(ctx context.Context, o pipeline.Occurrence) (pipeline.FingerprintState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return pipeline.FingerprintState{}, err
	}
	defer tx.Rollback()

	fingerprintID, err := upsertFingerprint(ctx, tx, o)
	if err != nil {
		return pipeline.FingerprintState{}, err
	}

	at := formatTime(o.Time)
	m := o.Match
	res, err := tx.ExecContext(ctx, `
		INSERT INTO occurrences (id, event_id, fingerprint_id, service, revision, sha, line, time, trace_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (event_id) DO NOTHING`,
		newID(), o.EventID, fingerprintID, o.Service, o.Revision, o.SHA, m.Frame.Line, at, o.TraceID,
	)
	if err != nil {
		return pipeline.FingerprintState{}, err
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return pipeline.FingerprintState{}, err
	}
	duplicate := inserted == 0

	if !duplicate {
		if _, err := tx.ExecContext(ctx, `
			UPDATE fingerprints SET
				count = count + 1,
				first_seen = min(first_seen, ?1),
				last_seen = max(last_seen, ?1),
				tier = ?2,
				subsystem = ?3
			WHERE id = ?4`,
			at, m.Tier.String(), m.Subsystem, fingerprintID,
		); err != nil {
			return pipeline.FingerprintState{}, err
		}
	}

	state, err := fingerprintState(ctx, tx, fingerprintID)
	if err != nil {
		return pipeline.FingerprintState{}, err
	}
	state.Duplicate = duplicate
	return state, tx.Commit()
}

func (s *SQLite) MarkNotified(ctx context.Context, fingerprint string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE fingerprints SET last_notified_at = ? WHERE key = ?`, formatTime(at), fingerprint)
	return err
}

func upsertFingerprint(ctx context.Context, tx *sql.Tx, o pipeline.Occurrence) (string, error) {
	at := formatTime(o.Time)
	m := o.Match
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO fingerprints (id, key, exc_type, frame_path, frame_func, tier, subsystem, first_seen, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (key) DO NOTHING`,
		newID(), o.Fingerprint, o.ExcType, m.Frame.Path, m.Frame.Func, m.Tier.String(), m.Subsystem, at, at,
	); err != nil {
		return "", err
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM fingerprints WHERE key = ?`, o.Fingerprint).Scan(&id)
	return id, err
}

func fingerprintState(ctx context.Context, tx *sql.Tx, id string) (pipeline.FingerprintState, error) {
	var (
		state        pipeline.FingerprintState
		firstSeen    string
		lastNotified sql.NullString
	)
	err := tx.QueryRowContext(ctx,
		`SELECT count, first_seen, last_notified_at FROM fingerprints WHERE id = ?`, id,
	).Scan(&state.Count, &firstSeen, &lastNotified)
	if err != nil {
		return state, err
	}
	if state.FirstSeen, err = parseTime(firstSeen); err != nil {
		return state, err
	}
	if lastNotified.Valid {
		state.LastNotified, err = parseTime(lastNotified.String)
	}
	return state, err
}

func newID() string { return uuid.NewV7().String() }

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(s string) (time.Time, error) { return time.Parse(timeLayout, s) }
