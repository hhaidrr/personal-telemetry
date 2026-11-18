package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/hhaidrr/personal-telemetry/internal/store"
)

// ANSI colours, so the TUI follows the terminal's theme.
var (
	bold       = lipgloss.NewStyle().Bold(true)
	faint      = lipgloss.NewStyle().Faint(true)
	selected   = lipgloss.NewStyle().Reverse(true)
	accent     = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	tierStyles = map[string]lipgloss.Style{
		"primary":   lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		"secondary": lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		"subsystem": lipgloss.NewStyle().Foreground(lipgloss.Color("4")),
	}
	production = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
)

const unparsedKey = "pt:unparsed"

func envStyle(env string) lipgloss.Style {
	if env == "production" {
		return production
	}
	return lipgloss.NewStyle()
}

func isUnparsed(i store.Issue) bool { return strings.HasSuffix(i.Key, unparsedKey) }

func tierName(i store.Issue) string {
	if isUnparsed(i) {
		return "unparsed"
	}
	return i.Tier
}

func tierStyle(tier string) lipgloss.Style {
	if s, ok := tierStyles[tier]; ok {
		return s
	}
	return faint
}

// KeyError src/core/models/user.py:60 -> KeyError core/models/user.py:60
func issueTitle(i store.Issue) string {
	if isUnparsed(i) {
		return "log entries pt couldn't parse"
	}
	exc := i.ExcType
	if exc == "" {
		exc = "error"
	}
	exc = exc[strings.LastIndex(exc, ".")+1:]
	return fmt.Sprintf("%s %s:%d", exc, strings.TrimPrefix(i.Path, "src/"), i.Line)
}

func workload(service string, job bool) string {
	if job {
		return "job " + service
	}
	return service
}

func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

var bars = []rune("▁▂▃▄▅▆▇█")

// Fits counts into exactly width cells: days are summed into buckets when
// there are more days than cells, and stretched when there are fewer.
func scale(counts []int, width int) []int {
	if len(counts) == 0 || width <= 0 {
		return nil
	}
	cells := make([]int, width)
	if len(counts) <= width {
		for j := range cells {
			cells[j] = counts[j*len(counts)/width]
		}
		return cells
	}
	for i, c := range counts {
		cells[i*width/len(counts)] += c
	}
	return cells
}

func peakOf(counts []int) int {
	peak := 0
	for _, c := range counts {
		peak = max(peak, c)
	}
	return peak
}

// Zero gets the lowest bar so the timeline stays readable as a line. Sharing
// a peak keeps several sparklines comparable.
func sparkline(counts []int, peak int) string {
	var b strings.Builder
	for _, c := range counts {
		r := bars[0]
		if peak > 0 && c > 0 {
			r = bars[max(1, (c*(len(bars)-1)+peak-1)/peak)]
		}
		b.WriteRune(r)
	}
	return b.String()
}

// A titled daily chart with its date range underneath.
func chart(title string, counts []int, start time.Time, width int) []string {
	cells := scale(counts, width-4)
	line := sparkline(cells, peakOf(cells))
	return []string{
		bold.Render(title) + faint.Render(fmt.Sprintf("  peak %s/day", num(peakOf(counts)))),
		"  " + accent.Render(line),
		faint.Render("  " + dateRange(start, len([]rune(line)))),
	}
}

func dateRange(start time.Time, width int) string {
	first, last := start.Format("Jan 2"), "today"
	return first + strings.Repeat(" ", max(width-len(first)-len(last), 1)) + last
}

// 1176 -> 1,176
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return num(n) + " " + word + "s"
}

func num(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// Cuts to width cells, marking the cut.
func fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s + strings.Repeat(" ", width-len(r))
	}
	return string(r[:width-1]) + "…"
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
