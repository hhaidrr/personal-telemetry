package text

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hhaidrr/personal-telemetry/internal/event"
	"github.com/hhaidrr/personal-telemetry/internal/ownership"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

func alert() pipeline.Alert {
	return pipeline.Alert{
		Occurrence: pipeline.Occurrence{
			ExcType: "KeyError",
			Match: ownership.Match{
				Tier:      ownership.Primary,
				Frame:     event.Frame{Path: "src/payments/channels/router.py", Line: 42, Func: "route"},
				Subsystem: "payment-channels",
			},
			Environment: "production",
			Revision:    "divdot-00042-abc",
			SHA:         "0c1c5ee10283b3bd81d9bc8657078675a3b02a8a",
			TraceID:     "t-1",
			Link:        "https://console.cloud.google.com/logs/query;query=x",
		},
		State: pipeline.FingerprintState{Count: 3, FirstSeen: time.Date(2026, 10, 30, 12, 5, 0, 0, time.UTC)},
		Message: "Failed to route payment.\nTraceback (most recent call last):\n" +
			"  File \"/workspace/payments/channels/router.py\", line 42, in route\n    return table[channel]\n" +
			"KeyError: 'interac'\n",
	}
}

func TestFormat(t *testing.T) {
	want := "production · primary · KeyError in src/payments/channels/router.py:42 (route)\n" +
		"> Failed to route payment.\n" +
		"> KeyError: 'interac'\n" +
		"payment-channels · 3× since 2026-10-30 12:05 UTC · divdot-00042-abc@0c1c5ee\n" +
		"https://console.cloud.google.com/logs/query;query=x"
	if got := Format(alert()); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestFormatOmitsEmptyParts(t *testing.T) {
	a := alert()
	a.Message, a.Occurrence.Match.Subsystem, a.Occurrence.Environment, a.Occurrence.ExcType = "", "", "", ""
	a.Occurrence.Link, a.Occurrence.TraceID = "", ""
	want := "primary · error in src/payments/channels/router.py:42 (route)\n" +
		"3× since 2026-10-30 12:05 UTC · divdot-00042-abc@0c1c5ee"
	if got := Format(a); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestFormatFallsBackToTrace(t *testing.T) {
	a := alert()
	a.Occurrence.Link = ""
	if got := Format(a); !strings.HasSuffix(got, "\ntrace: t-1") {
		t.Errorf("got\n%s", got)
	}
}

func TestSummarySkipsTracebackHeaderAndMarkers(t *testing.T) {
	got := Summary("Traceback (most recent call last):\n  File \"/workspace/a.py\", line 54, in f\n" +
		"    x = q.get()\n        ^^^^^^^\n")
	want := []string{`File "/workspace/a.py", line 54, in f`, "x = q.get()"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSummaryTruncatesLongLines(t *testing.T) {
	got := Summary(strings.Repeat("é", maxLineLen+10))
	if want := strings.Repeat("é", maxLineLen) + "…"; len(got) != 1 || got[0] != want {
		t.Errorf("line not truncated to %d runes", maxLineLen)
	}
}

func TestHeadlineForParseFailure(t *testing.T) {
	a := alert()
	a.ParseError = "unrecognized python traceback: no exception line"
	if got := Headline(a); got != "production · pt couldn't parse a log entry: unrecognized python traceback: no exception line" {
		t.Errorf("got %s", got)
	}
}

func TestFormatSystemAlert(t *testing.T) {
	a := pipeline.Alert{System: "pt can't read logs", Message: "Google credentials expired. Run: gcloud auth application-default login\noauth2: invalid_grant"}
	want := "pt can't read logs\n> Google credentials expired. Run: gcloud auth application-default login\n> oauth2: invalid_grant"
	if got := Format(a); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestFooter(t *testing.T) {
	a := alert()
	a.Occurrence.Job, a.Occurrence.Revision = true, "adyen-initiate-payouts-divdot-x7k2p"
	if got := Footer(a); !strings.HasSuffix(got, "· job adyen-initiate-payouts-divdot-x7k2p@0c1c5ee") {
		t.Errorf("got %s", got)
	}
	a.Occurrence.SHA, a.Occurrence.Approximate = "main", true
	if got := Footer(a); !strings.HasSuffix(got, "@main (deployed commit unknown, blamed at this ref)") {
		t.Errorf("got %s", got)
	}
}
