package desktop

import (
	"slices"
	"testing"

	"github.com/hhaidrr/personal-telemetry/internal/event"
	"github.com/hhaidrr/personal-telemetry/internal/ownership"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

func TestArgs(t *testing.T) {
	a := pipeline.Alert{
		Occurrence: pipeline.Occurrence{
			ExcType:     "KeyError",
			Environment: "production",
			Match:       ownership.Match{Tier: ownership.Primary, Frame: event.Frame{Path: "src/job.py", Line: 3, Func: "<module>"}},
			Revision:    "divdot-00042-abc",
			Link:        "https://console.cloud.google.com/x",
		},
		Message: "Failed for <a&b>",
	}
	want := []string{
		"--app-name=pt", "--urgency=critical", "--expire-time=30000", "--action=default=Open in Cloud Logging", "--wait",
		"production · primary · KeyError in src/job.py:3 (<module>)",
		"Failed for &lt;a&amp;b&gt;\n0× since 0001-01-01 00:00 UTC · divdot-00042-abc",
	}
	if got := args(a); !slices.Equal(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}

	a.Occurrence.Environment, a.Occurrence.Link = "develop", ""
	if got := args(a); got[1] != "--urgency=normal" || slices.Contains(got, "--wait") || slices.Contains(got, "--expire-time=30000") {
		t.Errorf("non-production without a link: %q", got)
	}
}
