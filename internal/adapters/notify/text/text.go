// Package text renders the parts of an alert that every channel shares.
package text

import (
	"fmt"
	"strings"

	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

const maxLineLen = 300

// production · primary · KeyError in src/x.py:42 (route)
func Headline(a pipeline.Alert) string {
	if a.System != "" {
		return a.System
	}
	o := a.Occurrence
	var parts []string
	if o.Environment != "" {
		parts = append(parts, o.Environment)
	}
	if a.ParseError != "" {
		return strings.Join(append(parts, "pt couldn't parse a log entry: "+a.ParseError), " · ")
	}
	excType := o.ExcType
	if excType == "" {
		excType = "error"
	}
	m := o.Match
	return strings.Join(append(parts, m.Tier.String(),
		fmt.Sprintf("%s in %s:%d (%s)", excType, m.Frame.Path, m.Frame.Line, m.Frame.Func)), " · ")
}

// payment-channels · 3× since 2026-10-30 12:05 UTC · job rev@sha
func Footer(a pipeline.Alert) string {
	if a.System != "" {
		return ""
	}
	o := a.Occurrence
	var b strings.Builder
	if o.Match.Subsystem != "" {
		fmt.Fprintf(&b, "%s · ", o.Match.Subsystem)
	}
	fmt.Fprintf(&b, "%d× since %s · ", a.State.Count, a.State.FirstSeen.UTC().Format("2006-01-02 15:04 UTC"))
	if o.Job {
		b.WriteString("job ")
	}
	b.WriteString(o.Revision)
	if o.SHA != "" {
		fmt.Fprintf(&b, "@%s", shortSHA(o.SHA))
	}
	if o.Approximate {
		b.WriteString(" (deployed commit unknown, blamed at this ref)")
	}
	return b.String()
}

// Every line, untrimmed, for alerts about pt itself whose message is short.
func Lines(message string) []string {
	var out []string
	for _, l := range strings.Split(message, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, truncate(l))
		}
	}
	return out
}

// The log message's first line says what the code was doing and the last one
// is usually the exception; the traceback between them is on the far side of
// the link. The traceback header and Python's ^~~ markers say nothing.
func Summary(message string) []string {
	var lines []string
	for i, l := range strings.Split(message, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.Trim(l, "^~ ") == "" || (i == 0 && l == "Traceback (most recent call last):") {
			continue
		}
		lines = append(lines, l)
	}
	if len(lines) == 0 {
		return nil
	}
	out := []string{truncate(lines[0])}
	if len(lines) > 1 {
		out = append(out, truncate(lines[len(lines)-1]))
	}
	return out
}

// Every part, for a terminal or the journal.
func Format(a pipeline.Alert) string {
	lines := []string{Headline(a)}
	for _, l := range Summary(a.Message) {
		lines = append(lines, "> "+l)
	}
	if f := Footer(a); f != "" {
		lines = append(lines, f)
	}
	switch o := a.Occurrence; {
	case o.Link != "":
		lines = append(lines, o.Link)
	case o.TraceID != "":
		lines = append(lines, "trace: "+o.TraceID)
	}
	return strings.Join(lines, "\n")
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) <= maxLineLen {
		return s
	}
	return string(r[:maxLineLen]) + "…"
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
