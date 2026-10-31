package event

import "time"

type Event struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Service  string    `json:"service"`
	Revision string    `json:"revision"`
	Severity string    `json:"severity"`
	Message  string    `json:"message"`
	TraceID  string    `json:"trace_id,omitempty"`
	ExcType  string    `json:"exc_type,omitempty"`
	// Innermost first, whatever order the language prints them in. An error
	// logged without a traceback has a single frame: the log call site.
	Frames []Frame `json:"frames"`
}

type Frame struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Func string `json:"func"`
}
