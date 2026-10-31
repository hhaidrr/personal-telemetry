package file

import (
	"strings"
	"testing"

	"github.com/hhaidrr/personal-telemetry/internal/event"
)

const jsonl = `{"id":"1","service":"api","revision":"rev-1","exc_type":"ValueError","frames":[{"path":"/workspace/x.py","line":3,"func":"f"}]}

{"id":"2","service":"api","revision":"rev-1","message":"plain log","frames":[{"path":"/workspace/y.py","line":9,"func":"g"}]}
`

func TestDecode(t *testing.T) {
	events, lines, err := decode(strings.NewReader(jsonl), 0)
	if err != nil {
		t.Fatal(err)
	}
	if lines != 3 || len(events) != 2 {
		t.Fatalf("got %d events over %d lines, want 2 over 3", len(events), lines)
	}
	want := event.Frame{Path: "/workspace/x.py", Line: 3, Func: "f"}
	if events[0].ID != "1" || events[0].ExcType != "ValueError" || events[0].Frames[0] != want {
		t.Errorf("first event = %+v", events[0])
	}
}

func TestDecodeSkipsConsumedLines(t *testing.T) {
	events, lines, err := decode(strings.NewReader(jsonl), 2)
	if err != nil {
		t.Fatal(err)
	}
	if lines != 3 || len(events) != 1 || events[0].ID != "2" {
		t.Errorf("got %+v over %d lines, want only event 2", events, lines)
	}
}

func TestDecodeReportsBadLine(t *testing.T) {
	_, lines, err := decode(strings.NewReader("{\"id\":\"1\"}\nnot json\n"), 0)
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("err = %v, want failure on line 2", err)
	}
	if lines != 0 {
		t.Errorf("lines = %d, want cursor left at 0 on failure", lines)
	}
}
