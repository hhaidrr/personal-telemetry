package gcplogging

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/hhaidrr/personal-telemetry/internal/event"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

type fakeAPI struct {
	responses    []response
	requests     []listRequest
	quotaProject string
}

type response struct {
	status int
	body   string
}

func (f *fakeAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	var req listRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, err
	}
	f.requests = append(f.requests, req)
	f.quotaProject = r.Header.Get("X-Goog-User-Project")
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return &http.Response{StatusCode: resp.status, Status: http.StatusText(resp.status), Body: io.NopCloser(strings.NewReader(resp.body))}, nil
}

func newSource(api *fakeAPI) *Source {
	return &Source{
		Environment: "develop",
		Project:     "proj",
		Filter:      `severity>=ERROR`,
		Client:      &http.Client{Transport: api},
		Backoff:     time.Nanosecond,
		Now:         func() time.Time { return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC) },
	}
}

func page(token string, entries ...string) response {
	b, _ := json.Marshal(map[string]any{"entries": json.RawMessage("[" + strings.Join(entries, ",") + "]"), "nextPageToken": token})
	return response{status: http.StatusOK, body: string(b)}
}

func entryAt(id, ts string) string {
	return `{"insertId":"` + id + `","timestamp":"` + ts + `","severity":"ERROR","textPayload":"boom",
		"resource":{"labels":{"service_name":"divdot","revision_name":"divdot-00023"}}}`
}

func TestFetchMapsTextAndJSONPayloads(t *testing.T) {
	traceback := `{"insertId":"a","timestamp":"2026-11-17T11:00:00.5Z","severity":"ERROR",
		"textPayload":"Failed to collect analytics.\nTraceback (most recent call last):\n  File \"/workspace/endpoints/graphql/analytics.py\", line 73, in record_analytics\n    context = {}\nKeyError: 'field_name'",
		"trace":"projects/proj/traces/abc123",
		"sourceLocation":{"file":"/workspace/endpoints/graphql/analytics.py","line":"80","function":"record_analytics"},
		"resource":{"labels":{"project_id":"proj","location":"northamerica-northeast1","service_name":"divdot","revision_name":"divdot-00023"}}}`
	jsonPayload := `{"insertId":"b","timestamp":"2026-11-17T11:00:01Z","severity":"ERROR",
		"jsonPayload":{"message":"Could not resolve refund","webhook":"w_1"},
		"sourceLocation":{"file":"/workspace/adyen/handlers/transfer_handling.py","line":"61","function":"create_transfer"},
		"resource":{"labels":{"service_name":"divdot","revision_name":"divdot-00023"}}}`
	unparsable := `{"insertId":"c","timestamp":"2026-11-17T11:00:02Z","severity":"ERROR",
		"textPayload":"Traceback (most recent call last):\n  at f (/workspace/a.py:1)\nValueError: bad",
		"sourceLocation":{"file":"/workspace/a.py","line":"9","function":"g"},
		"resource":{"labels":{"service_name":"divdot","revision_name":"divdot-00023"}}}`
	job := `{"insertId":"d","timestamp":"2026-11-17T11:00:03Z","severity":"ERROR","textPayload":"boom",
		"labels":{"run.googleapis.com/execution_name":"payouts-zstg5"},
		"resource":{"type":"cloud_run_job","labels":{"project_id":"proj","location":"us-central1","job_name":"payouts"}}}`
	api := &fakeAPI{responses: []response{page("", traceback, jsonPayload, unparsable, job)}}

	events, _, err := newSource(api).Fetch(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}

	want := []event.Event{
		{
			ID: "a@2026-11-17T11:00:00.5Z", Time: time.Date(2026, 11, 17, 11, 0, 0, 5e8, time.UTC), Environment: "develop",
			Link:    "https://console.cloud.google.com/logs/query;query=insertId=%22a%22;cursorTimestamp=2026-11-17T11:00:00.5Z?project=proj",
			Project: "proj", Location: "northamerica-northeast1", Service: "divdot", Revision: "divdot-00023", Severity: "ERROR", TraceID: "abc123", ExcType: "KeyError",
			Message: "Failed to collect analytics.\nTraceback (most recent call last):\n  File \"/workspace/endpoints/graphql/analytics.py\", line 73, in record_analytics\n    context = {}\nKeyError: 'field_name'",
			Frames:  []event.Frame{{Path: "/workspace/endpoints/graphql/analytics.py", Line: 73, Func: "record_analytics"}},
		},
		{
			ID: "b@2026-11-17T11:00:01Z", Time: time.Date(2026, 11, 17, 11, 0, 1, 0, time.UTC), Environment: "develop",
			Link:    "https://console.cloud.google.com/logs/query;query=insertId=%22b%22;cursorTimestamp=2026-11-17T11:00:01Z?project=proj",
			Service: "divdot", Revision: "divdot-00023", Severity: "ERROR", Message: "Could not resolve refund",
			Frames: []event.Frame{{Path: "/workspace/adyen/handlers/transfer_handling.py", Line: 61, Func: "create_transfer"}},
		},
		{
			ID: "c@2026-11-17T11:00:02Z", Time: time.Date(2026, 11, 17, 11, 0, 2, 0, time.UTC), Environment: "develop",
			Link:    "https://console.cloud.google.com/logs/query;query=insertId=%22c%22;cursorTimestamp=2026-11-17T11:00:02Z?project=proj",
			Service: "divdot", Revision: "divdot-00023", Severity: "ERROR",
			Message:    "Traceback (most recent call last):\n  at f (/workspace/a.py:1)\nValueError: bad",
			ParseError: "unrecognized python traceback: traceback 1 has no frames",
			Frames:     []event.Frame{{Path: "/workspace/a.py", Line: 9, Func: "g"}},
		},
		{
			ID: "d@2026-11-17T11:00:03Z", Time: time.Date(2026, 11, 17, 11, 0, 3, 0, time.UTC), Environment: "develop",
			Link:    "https://console.cloud.google.com/logs/query;query=insertId=%22d%22;cursorTimestamp=2026-11-17T11:00:03Z?project=proj",
			Project: "proj", Location: "us-central1", Service: "payouts", Revision: "payouts-zstg5", Job: true, Severity: "ERROR", Message: "boom",
		},
	}
	for i := range want {
		if !reflect.DeepEqual(events[i], want[i]) {
			t.Errorf("event %d:\n got %+v\nwant %+v", i, events[i], want[i])
		}
	}
}

func TestFirstFetchLooksBackAndCursorTrailsNewestByOverlap(t *testing.T) {
	api := &fakeAPI{responses: []response{
		page("p2", entryAt("a", "2026-11-17T11:30:00Z")),
		page("", entryAt("b", "2026-11-17T11:55:00Z")),
	}}

	events, cursor, err := newSource(api).Fetch(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}

	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 across both pages", len(events))
	}
	first := api.requests[0]
	if first.Filter != `resource.type=("cloud_run_revision" OR "cloud_run_job") AND (severity>=ERROR) AND timestamp>="2026-11-17T11:00:00Z"` {
		t.Errorf("filter = %s", first.Filter)
	}
	if first.OrderBy != "timestamp asc" || !reflect.DeepEqual(first.ResourceNames, []string{"projects/proj"}) {
		t.Errorf("unexpected request %+v", first)
	}
	if api.quotaProject != "proj" {
		t.Errorf("quota project = %q, want the environment's own project", api.quotaProject)
	}
	if api.requests[1].PageToken != "p2" {
		t.Errorf("second page token = %q, want p2", api.requests[1].PageToken)
	}
	if cursor != "2026-11-17T11:45:00Z" {
		t.Errorf("cursor = %s, want newest minus 10m overlap", cursor)
	}
}

func TestCursorNeverMovesBackward(t *testing.T) {
	for name, entries := range map[string][]string{
		"no entries":             nil,
		"entries inside overlap": {entryAt("a", "2026-11-17T11:50:00Z")},
	} {
		t.Run(name, func(t *testing.T) {
			api := &fakeAPI{responses: []response{page("", entries...)}}
			_, cursor, err := newSource(api).Fetch(context.Background(), "2026-11-17T11:45:00Z")
			if err != nil {
				t.Fatal(err)
			}
			if cursor != "2026-11-17T11:45:00Z" {
				t.Errorf("cursor = %s, want it unchanged", cursor)
			}
		})
	}
}

func TestBacklogStopsAtMaxEntriesAndResumesWithoutOverlap(t *testing.T) {
	api := &fakeAPI{responses: []response{
		page("p2", entryAt("a", "2026-11-17T11:10:00Z"), entryAt("b", "2026-11-17T11:20:00Z")),
	}}
	src := newSource(api)
	src.MaxEntries = 2

	events, cursor, err := src.Fetch(context.Background(), "2026-11-17T11:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || len(api.requests) != 1 {
		t.Fatalf("got %d events over %d requests, want 2 over 1", len(events), len(api.requests))
	}
	if cursor != "2026-11-17T11:20:00Z" {
		t.Errorf("cursor = %s, want the newest entry", cursor)
	}
}

func TestTransientErrorsAreRetried(t *testing.T) {
	api := &fakeAPI{responses: []response{
		{status: http.StatusServiceUnavailable, body: "unavailable"},
		{status: http.StatusTooManyRequests, body: "quota"},
		page("", entryAt("a", "2026-11-17T11:30:00Z")),
	}}

	events, _, err := newSource(api).Fetch(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Errorf("got %d events, want 1", len(events))
	}
}

type rejectedToken struct{ calls int }

func (r *rejectedToken) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls++
	return nil, &oauth2.RetrieveError{ErrorCode: "invalid_grant"}
}

func TestRejectedCredentialsAreNotRetried(t *testing.T) {
	rt := &rejectedToken{}
	src := newSource(&fakeAPI{})
	src.Client = &http.Client{Transport: rt}
	if _, _, err := src.Fetch(context.Background(), ""); err == nil || rt.calls != 1 {
		t.Errorf("err = %v after %d calls, want an error after 1", err, rt.calls)
	}
}

func TestPermanentErrorsFailWithoutMovingTheCursor(t *testing.T) {
	cases := map[string]*fakeAPI{
		"forbidden": {responses: []response{{status: http.StatusForbidden, body: `{"error":"PERMISSION_DENIED"}`}}},
		"still failing after retries": {responses: []response{
			{status: http.StatusInternalServerError}, {status: http.StatusInternalServerError}, {status: http.StatusInternalServerError},
		}},
	}
	for name, api := range cases {
		t.Run(name, func(t *testing.T) {
			_, cursor, err := newSource(api).Fetch(context.Background(), "2026-11-17T11:00:00Z")
			if err == nil {
				t.Fatal("want error")
			}
			if cursor != "2026-11-17T11:00:00Z" {
				t.Errorf("cursor = %s, want it unchanged", cursor)
			}
		})
	}
}

func TestBadCursorIsAnError(t *testing.T) {
	_, _, err := newSource(&fakeAPI{}).Fetch(context.Background(), pipeline.Cursor("42"))
	if err == nil {
		t.Fatal("want error for a cursor that isn't a timestamp")
	}
}

func TestMessageFetchesOneEntryByEventID(t *testing.T) {
	api := &fakeAPI{responses: []response{page("", entryAt("a", "2026-11-17T11:30:00Z"))}}
	msg, err := Message(context.Background(), &http.Client{Transport: api}, "proj", "a@2026-11-17T11:30:00Z")
	if err != nil || msg != "boom" {
		t.Fatalf("msg = %q, %v", msg, err)
	}
	if f := api.requests[0].Filter; f != `insertId="a" AND timestamp="2026-11-17T11:30:00Z"` {
		t.Errorf("filter = %s", f)
	}
	if api.quotaProject != "proj" {
		t.Errorf("quota project = %q", api.quotaProject)
	}
}
