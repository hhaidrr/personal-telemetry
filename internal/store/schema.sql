CREATE TABLE IF NOT EXISTS cursor (
    id    INTEGER PRIMARY KEY CHECK (id = 1),
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS fingerprints (
    id               TEXT PRIMARY KEY,
    key              TEXT NOT NULL UNIQUE,
    exc_type         TEXT NOT NULL,
    frame_path       TEXT NOT NULL,
    frame_func       TEXT NOT NULL,
    tier             TEXT NOT NULL,
    subsystem        TEXT NOT NULL DEFAULT '',
    first_seen       TEXT NOT NULL,
    last_seen        TEXT NOT NULL,
    count            INTEGER NOT NULL DEFAULT 0,
    last_notified_at TEXT,
    status                   TEXT NOT NULL DEFAULT 'open',
    escape_category          TEXT NOT NULL DEFAULT '',
    should_have_caught_layer TEXT NOT NULL DEFAULT '',
    fix_ref                  TEXT NOT NULL DEFAULT ''
);

-- Metadata only: messages may contain customer data.
CREATE TABLE IF NOT EXISTS occurrences (
    id             TEXT PRIMARY KEY,
    event_id       TEXT NOT NULL UNIQUE,
    fingerprint_id TEXT NOT NULL REFERENCES fingerprints(id),
    service        TEXT NOT NULL,
    revision       TEXT NOT NULL,
    sha            TEXT NOT NULL,
    line           INTEGER NOT NULL,
    time           TEXT NOT NULL,
    trace_id       TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS occurrences_fingerprint ON occurrences(fingerprint_id, time);
