package gcplogging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/hhaidrr/personal-telemetry/internal/event"
	"github.com/hhaidrr/personal-telemetry/internal/parse"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

const (
	defaultEndpoint   = "https://logging.googleapis.com/v2/entries:list"
	defaultOverlap    = 10 * time.Minute
	defaultLookback   = time.Hour
	defaultMaxEntries = 5000
	defaultBackoff    = time.Second
	pageSize          = 1000
	maxAttempts       = 3
)

// Reads one environment: the logs of every Cloud Run service and job in one
// project. Workloads running other code are dropped later, by image.
//
// The cursor is the lower timestamp bound of the next query. It trails the
// newest entry seen by Overlap because Cloud Logging can make an entry
// queryable after newer ones; the store drops the entries read twice.
type Source struct {
	Environment string
	Project     string
	// Narrows the workloads' logs, e.g. to errors.
	Filter     string
	Client     *http.Client
	Overlap    time.Duration
	Lookback   time.Duration
	MaxEntries int
	Endpoint   string
	Backoff    time.Duration
	Now        func() time.Time
}

var _ pipeline.Source = (*Source)(nil)

type listRequest struct {
	ResourceNames []string `json:"resourceNames"`
	Filter        string   `json:"filter"`
	OrderBy       string   `json:"orderBy"`
	PageSize      int      `json:"pageSize"`
	PageToken     string   `json:"pageToken,omitempty"`
}

type listResponse struct {
	Entries       []entry `json:"entries"`
	NextPageToken string  `json:"nextPageToken"`
}

type entry struct {
	InsertID       string            `json:"insertId"`
	Timestamp      time.Time         `json:"timestamp"`
	Severity       string            `json:"severity"`
	TextPayload    string            `json:"textPayload"`
	JSONPayload    json.RawMessage   `json:"jsonPayload"`
	Trace          string            `json:"trace"`
	Labels         map[string]string `json:"labels"`
	SourceLocation *struct {
		File     string `json:"file"`
		Line     string `json:"line"`
		Function string `json:"function"`
	} `json:"sourceLocation"`
	Resource struct {
		Type   string            `json:"type"`
		Labels map[string]string `json:"labels"`
	} `json:"resource"`
}

func (s *Source) Fetch(ctx context.Context, cursor pipeline.Cursor) ([]event.Event, pipeline.Cursor, error) {
	since, err := s.since(cursor)
	if err != nil {
		return nil, cursor, err
	}

	req := listRequest{
		ResourceNames: []string{"projects/" + s.Project},
		Filter:        s.filter(since),
		OrderBy:       "timestamp asc",
		PageSize:      pageSize,
	}
	var events []event.Event
	newest := time.Time{}
	for {
		page, err := s.list(ctx, req)
		if err != nil {
			return nil, cursor, err
		}
		for _, e := range page.Entries {
			events = append(events, s.toEvent(e))
			if e.Timestamp.After(newest) {
				newest = e.Timestamp
			}
		}
		if page.NextPageToken == "" {
			break
		}
		if len(events) >= s.maxEntries() {
			// The overlap is skipped so a backlog bigger than one run still advances.
			return events, format(newest), nil
		}
		req.PageToken = page.NextPageToken
	}

	next := since
	if trailing := newest.Add(-s.overlap()); trailing.After(next) {
		next = trailing
	}
	return events, format(next), nil
}

func (s *Source) filter(since time.Time) string {
	clauses := []string{`resource.type=("cloud_run_revision" OR "cloud_run_job")`}
	if s.Filter != "" {
		clauses = append(clauses, "("+s.Filter+")")
	}
	clauses = append(clauses, fmt.Sprintf("timestamp>=%q", since.Format(time.RFC3339Nano)))
	return strings.Join(clauses, " AND ")
}

func (s *Source) since(cursor pipeline.Cursor) (time.Time, error) {
	if cursor == "" {
		return s.now().Add(-s.lookback()).UTC(), nil
	}
	t, err := time.Parse(time.RFC3339Nano, string(cursor))
	if err != nil {
		return time.Time{}, fmt.Errorf("gcplogging: bad cursor %q", cursor)
	}
	return t, nil
}

func (s *Source) list(ctx context.Context, req listRequest) (listResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return listResponse{}, err
	}
	var lastErr error
	for attempt := range maxAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return listResponse{}, ctx.Err()
			case <-time.After(s.backoff() << (attempt - 1)):
			}
		}
		var page listResponse
		var retry bool
		page, retry, lastErr = s.post(ctx, body)
		if lastErr == nil || !retry {
			return page, lastErr
		}
	}
	return listResponse{}, lastErr
}

func (s *Source) post(ctx context.Context, body []byte) (page listResponse, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint(), bytes.NewReader(body))
	if err != nil {
		return page, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Reads count against a per-project quota. Charging each environment's
	// reads to its own project keeps one busy project from starving the rest.
	req.Header.Set("X-Goog-User-Project", s.Project)
	resp, err := s.client().Do(req)
	if err != nil {
		// A rejected credential won't be accepted on a retry.
		var tokenErr *oauth2.RetrieveError
		return page, ctx.Err() == nil && !errors.As(err, &tokenErr), fmt.Errorf("gcplogging: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		retry = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return page, retry, fmt.Errorf("gcplogging: %s: %s", resp.Status, bytes.TrimSpace(detail))
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return page, false, fmt.Errorf("gcplogging: decode: %w", err)
	}
	return page, false, nil
}

func (s *Source) toEvent(e entry) event.Event {
	ts := e.Timestamp.UTC().Format(time.RFC3339Nano)
	ev := event.Event{
		ID:          e.InsertID + "@" + ts,
		Time:        e.Timestamp,
		Environment: s.Environment,
		Project:     e.Resource.Labels["project_id"],
		Location:    e.Resource.Labels["location"],
		Service:     e.Resource.Labels["service_name"],
		Revision:    e.Resource.Labels["revision_name"],
		Job:         e.Resource.Type == "cloud_run_job",
		Severity:    e.Severity,
		Message:     message(e),
		TraceID:     e.Trace[strings.LastIndex(e.Trace, "/")+1:],
		Link:        Link(s.Project, e.InsertID+"@"+ts),
	}
	if ev.Job {
		ev.Service, ev.Revision = e.Resource.Labels["job_name"], e.Labels["run.googleapis.com/execution_name"]
	}
	frames, excType, err := parse.Python(ev.Message)
	ev.Frames, ev.ExcType = frames, excType
	if err != nil {
		ev.ParseError = err.Error()
	}
	if len(ev.Frames) == 0 && e.SourceLocation != nil {
		line, _ := strconv.Atoi(e.SourceLocation.Line)
		ev.Frames = []event.Frame{{Path: e.SourceLocation.File, Line: line, Func: e.SourceLocation.Function}}
	}
	return ev
}

// Link opens one entry, by the event ID toEvent gave it, in Logs Explorer.
// The timestamp bounds are in the query because otherwise Logs Explorer only
// searches its default range, the last hour.
func Link(project, eventID string) string {
	insertID, ts, ok := cutEventID(eventID)
	if !ok {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ""
	}
	query := url.PathEscape(fmt.Sprintf("insertId=%q timestamp>=%q timestamp<=%q", insertID,
		t.Add(-time.Second).UTC().Format(time.RFC3339), t.Add(time.Second).UTC().Format(time.RFC3339)))
	return "https://console.cloud.google.com/logs/query;query=" + query + ";cursorTimestamp=" + ts + "?project=" + url.QueryEscape(project)
}

func cutEventID(eventID string) (insertID, ts string, ok bool) {
	i := strings.LastIndex(eventID, "@")
	if i < 0 {
		return "", "", false
	}
	return eventID[:i], eventID[i+1:], true
}

func message(e entry) string {
	if e.TextPayload != "" || len(e.JSONPayload) == 0 {
		return e.TextPayload
	}
	var payload struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(e.JSONPayload, &payload) == nil && payload.Message != "" {
		return payload.Message
	}
	return string(e.JSONPayload)
}

func format(t time.Time) pipeline.Cursor { return pipeline.Cursor(t.UTC().Format(time.RFC3339Nano)) }

func (s *Source) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Source) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

func (s *Source) endpoint() string { return or(s.Endpoint, defaultEndpoint) }

func (s *Source) overlap() time.Duration  { return or(s.Overlap, defaultOverlap) }
func (s *Source) lookback() time.Duration { return or(s.Lookback, defaultLookback) }
func (s *Source) backoff() time.Duration  { return or(s.Backoff, defaultBackoff) }
func (s *Source) maxEntries() int         { return or(s.MaxEntries, defaultMaxEntries) }

func or[T comparable](v, fallback T) T {
	var zero T
	if v == zero {
		return fallback
	}
	return v
}

// Message fetches the full message of one entry, by the event ID toEvent gave it.
func Message(ctx context.Context, client *http.Client, project, eventID string) (string, error) {
	insertID, ts, ok := cutEventID(eventID)
	if !ok {
		return "", fmt.Errorf("gcplogging: %q is not an insertId@timestamp event ID", eventID)
	}
	s := &Source{Project: project, Client: client}
	page, err := s.list(ctx, listRequest{
		ResourceNames: []string{"projects/" + project},
		Filter:        fmt.Sprintf("insertId=%q AND timestamp=%q", insertID, ts),
		PageSize:      1,
	})
	if err != nil {
		return "", err
	}
	if len(page.Entries) == 0 {
		return "", fmt.Errorf("gcplogging: entry %s not found (past log retention?)", eventID)
	}
	return message(page.Entries[0]), nil
}
