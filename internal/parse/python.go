package parse

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/hhaidrr/personal-telemetry/internal/event"
)

var ErrUnrecognized = errors.New("unrecognized python traceback")

const tracebackHeader = "Traceback (most recent call last):"

var (
	ansiEscape    = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	frameLine     = regexp.MustCompile(`^File "(.+?)", line (\d+)(?:, in (.+?))?\s*$`)
	exceptionLine = regexp.MustCompile(`^([A-Za-z_][\w.]*)(?::|$)`)
)

type traceback struct {
	frames      []event.Frame
	frameIndent int
	excType     string
}

// Frames of the logged exception come first, then those of its causes, so
// ownership checks the code that logged before the code that broke.
//
// Lines are classified by content and by indentation relative to the first
// frame, never by absolute indentation, so changes to spacing, tabs or the
// locals layout don't break parsing. A traceback that is present but can't be
// fully read returns ErrUnrecognized alongside whatever was parsed.
func Python(message string) (frames []event.Frame, excType string, err error) {
	blocks := tracebacks(message)
	if len(blocks) == 0 {
		return nil, "", nil
	}
	for i, b := range slices.Backward(blocks) {
		if len(b.frames) == 0 {
			err = fmt.Errorf("%w: traceback %d has no frames", ErrUnrecognized, i+1)
		}
		frames = append(frames, reversed(b.frames)...)
	}
	excType = blocks[len(blocks)-1].excType
	if excType == "" && err == nil {
		err = fmt.Errorf("%w: no exception line", ErrUnrecognized)
	}
	return frames, excType, err
}

func tracebacks(message string) []traceback {
	var blocks []traceback
	var current *traceback
	for line := range strings.Lines(ansiEscape.ReplaceAllString(message, "")) {
		text := strings.TrimLeft(strings.TrimRight(line, " \t\r\n"), " \t")
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		switch {
		case strings.HasSuffix(text, tracebackHeader):
			blocks = append(blocks, traceback{})
			current = &blocks[len(blocks)-1]
		case current == nil || current.excType != "" || text == "":
		case frameLine.MatchString(text) && (len(current.frames) == 0 || indent <= current.frameIndent):
			if len(current.frames) == 0 {
				current.frameIndent = indent
			}
			current.frames = append(current.frames, frame(text))
		case len(current.frames) > 0 && indent <= current.frameIndent:
			if m := exceptionLine.FindStringSubmatch(text); m != nil {
				current.excType = m[1]
			}
		}
	}
	return blocks
}

func frame(text string) event.Frame {
	m := frameLine.FindStringSubmatch(text)
	n, _ := strconv.Atoi(m[2])
	return event.Frame{Path: m[1], Line: n, Func: m[3]}
}

func reversed(frames []event.Frame) []event.Frame {
	out := slices.Clone(frames)
	slices.Reverse(out)
	return out
}
