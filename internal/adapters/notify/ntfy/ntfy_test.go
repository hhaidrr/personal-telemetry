package ntfy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hhaidrr/personal-telemetry/internal/event"
	"github.com/hhaidrr/personal-telemetry/internal/ownership"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

type fakeServer struct {
	url    string
	sent   message
	status int
}

func (f *fakeServer) RoundTrip(r *http.Request) (*http.Response, error) {
	f.url = r.URL.String()
	if err := json.NewDecoder(r.Body).Decode(&f.sent); err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: f.status, Status: http.StatusText(f.status), Body: io.NopCloser(strings.NewReader("{}"))}, nil
}

func alert(env string) pipeline.Alert {
	return pipeline.Alert{
		Occurrence: pipeline.Occurrence{
			ExcType:     "KeyError",
			Environment: env,
			Match:       ownership.Match{Tier: ownership.Primary, Frame: event.Frame{Path: "src/x.py", Line: 3, Func: "f"}},
			Revision:    "divdot-00042-abc",
			Link:        "https://console.cloud.google.com/x",
		},
		Message: "customer jane@example.com failed",
	}
}

func TestPushesMetadataOnly(t *testing.T) {
	srv := &fakeServer{status: http.StatusOK}
	n := &Notifier{Topic: "pt-abc", Client: &http.Client{Transport: srv}}
	if err := n.Notify(context.Background(), alert("production")); err != nil {
		t.Fatal(err)
	}

	if srv.url != "https://ntfy.sh" {
		t.Errorf("posted to %s", srv.url)
	}
	got := srv.sent
	if got.Topic != "pt-abc" || got.Priority != 4 || got.Click != "https://console.cloud.google.com/x" ||
		got.Title != "production · primary · KeyError in src/x.py:3 (f)" {
		t.Errorf("unexpected push %+v", got)
	}
	if strings.Contains(got.Title+got.Message, "jane@example.com") {
		t.Error("push carries the log message")
	}
}

func TestSystemAlertsCarryTheirMessage(t *testing.T) {
	p := payload("t", pipeline.Alert{System: "pt can't read logs", Message: "Run: gcloud auth application-default login"})
	if p.Title != "pt can't read logs" || p.Message != "Run: gcloud auth application-default login" || p.Priority != 4 {
		t.Errorf("payload = %+v", p)
	}
}

func TestPriorityAndErrors(t *testing.T) {
	if p := payload("t", alert("develop")).Priority; p != 3 {
		t.Errorf("develop priority = %d, want 3", p)
	}
	n := &Notifier{Topic: "t", Client: &http.Client{Transport: &fakeServer{status: http.StatusTooManyRequests}}}
	if err := n.Notify(context.Background(), alert("develop")); err == nil {
		t.Error("want error on a non-2xx response")
	}
}
