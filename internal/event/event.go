package event

import "time"

type Event struct {
	ID          string    `json:"id"`
	Time        time.Time `json:"time"`
	Environment string    `json:"environment,omitempty"`
	Project     string    `json:"project"`
	Location    string    `json:"location"`
	// A Cloud Run service and revision, or with Job set, a job and execution.
	Service  string `json:"service"`
	Revision string `json:"revision"`
	Job      bool   `json:"job,omitempty"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	TraceID  string `json:"trace_id,omitempty"`
	// Where a person can open the original entry, if the source has a viewer.
	Link    string `json:"link,omitempty"`
	ExcType string `json:"exc_type,omitempty"`
	// Set when a stack trace was present but couldn't be fully read; Frames
	// then holds what was parsed, or the log call site.
	ParseError string `json:"parse_error,omitempty"`
	// Innermost first, whatever order the language prints them in. An error
	// logged without a traceback has a single frame: the log call site.
	Frames []Frame `json:"frames"`
}

type Frame struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Func string `json:"func"`
}
