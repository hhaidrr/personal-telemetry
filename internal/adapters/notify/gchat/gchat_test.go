package gchat

import (
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
			Fingerprint: "ValueError@src/payments/channels/router.py:route",
			ExcType:     "ValueError",
			Match: ownership.Match{
				Tier:      ownership.Primary,
				Frame:     event.Frame{Path: "src/payments/channels/router.py", Line: 42, Func: "route"},
				Subsystem: "payment-channels",
			},
			Revision: "internal-api-00042",
			SHA:      "0c1c5ee10283b3bd81d9bc8657078675a3b02a8a",
			TraceID:  "t-1",
		},
		State:   pipeline.FingerprintState{Count: 3, FirstSeen: time.Date(2026, 10, 30, 12, 5, 0, 0, time.UTC)},
		Message: "unmapped channel: interac",
	}
}

func TestMessage(t *testing.T) {
	want := "*primary* · ValueError in `src/payments/channels/router.py:42` (route)\n" +
		"```unmapped channel: interac```\n" +
		"payment-channels · seen 3× since 2026-10-30 12:05 UTC · internal-api-00042@0c1c5ee\n" +
		"trace: t-1"
	if got := message(alert()); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestMessageOmitsEmptyParts(t *testing.T) {
	a := alert()
	a.Message, a.Occurrence.Match.Subsystem, a.Occurrence.TraceID, a.Occurrence.ExcType = "", "", "", ""
	want := "*primary* · error in `src/payments/channels/router.py:42` (route)\n" +
		"seen 3× since 2026-10-30 12:05 UTC · internal-api-00042@0c1c5ee"
	if got := message(a); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestMessageTruncatesLongMessages(t *testing.T) {
	a := alert()
	a.Message = strings.Repeat("é", maxMessageLen+10)
	if got := message(a); !strings.Contains(got, strings.Repeat("é", maxMessageLen)+"…```") {
		t.Errorf("message not truncated to %d runes", maxMessageLen)
	}
}

func TestThreadedURL(t *testing.T) {
	got := threadedURL("https://chat.googleapis.com/v1/spaces/S/messages?key=k&token=t", "ValueError@src/x.py:f")
	want := "https://chat.googleapis.com/v1/spaces/S/messages?key=k&token=t" +
		"&threadKey=ValueError%40src%2Fx.py%3Af&messageReplyOption=REPLY_MESSAGE_FALLBACK_TO_NEW_THREAD"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
