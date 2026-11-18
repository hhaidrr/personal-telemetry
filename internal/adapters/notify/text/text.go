// Package text renders an alert in Google Chat's markup, which also reads
// fine in a terminal.
package text

import (
	"fmt"
	"strings"

	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

const maxLineLen = 300

func Format(a pipeline.Alert) string {
	o := a.Occurrence
	m := o.Match
	excType := o.ExcType
	if excType == "" {
		excType = "error"
	}

	var b strings.Builder
	if o.Environment != "" {
		fmt.Fprintf(&b, "*%s* · ", o.Environment)
	}
	if a.ParseError != "" {
		fmt.Fprintf(&b, "pt couldn't parse a log entry: %s\n", a.ParseError)
	} else {
		fmt.Fprintf(&b, "%s · *%s* in `%s:%d` (%s)\n", m.Tier, excType, m.Frame.Path, m.Frame.Line, m.Frame.Func)
	}
	for _, line := range summary(a.Message) {
		fmt.Fprintf(&b, "> %s\n", line)
	}
	if m.Subsystem != "" {
		fmt.Fprintf(&b, "%s · ", m.Subsystem)
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
	switch {
	case o.Link != "":
		fmt.Fprintf(&b, "\n<%s|Open in Cloud Logging>", o.Link)
	case o.TraceID != "":
		fmt.Fprintf(&b, "\ntrace: %s", o.TraceID)
	}
	return b.String()
}

// The log message's first line says what the code was doing and the last one
// is usually the exception; the traceback between them is on the far side of
// the link. The traceback header and Python's ^~~ markers say nothing.
func summary(message string) []string {
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
	if last := lines[len(lines)-1]; len(lines) > 1 {
		out = append(out, truncate(last))
	}
	return out
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
