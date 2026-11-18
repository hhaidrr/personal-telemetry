package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hhaidrr/personal-telemetry/internal/store"
)

type issueView struct {
	detail     *store.IssueDetail
	detailKey  string
	body       string
	bodyErr    error
	bodyLoaded bool
	scroll     int
}

func (m Model) issueKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch {
	case isBack(key):
		m.screen, m.detail, m.detailKey = m.back, nil, ""
	case key == "o":
		if m.detail != nil && m.detail.Latest.Link != "" {
			m.open(m.detail.Latest.Link)
		}
	default:
		// The view clamps the scroll to the content, so the cursor bound is loose.
		m.scroll = moveCursor(key, m.scroll, m.scroll+m.height, m.halfPage())
	}
	return m, nil
}

func (v issueView) render(m Model) string {
	footer := "j/k scroll · h/esc back · o open in Cloud Logging · q quit"
	if v.detail == nil {
		return m.frame([]string{faint.Render("loading…")}, footer)
	}
	d, now := v.detail, m.now()

	var lines []string
	add := func(s ...string) { lines = append(lines, s...) }

	head := []string{envStyle(d.Environment).Render(d.Environment), tierStyle(tierName(d.Issue)).Render(tierName(d.Issue))}
	if !isUnparsed(d.Issue) {
		head = append(head, bold.Render(or(d.ExcType, "error")))
	}
	add(strings.Join(head, faint.Render(" · ")))
	if isUnparsed(d.Issue) {
		add("log entries with a stack trace pt couldn't read")
	} else {
		add(fmt.Sprintf("%s:%d in %s", d.Path, d.Line, d.Func))
	}
	facts := fmt.Sprintf("%d× total · %d in 24h · first seen %s · last seen %s · %s",
		d.Count, d.Recent, d.FirstSeen.Local().Format("2006-01-02 15:04"), ago(now, d.LastSeen), workload(d.Service, d.Job))
	if s := m.systemOf(d.Issue); s != otherCode {
		facts = s + " · " + facts
	}
	add(faint.Render(facts), "")

	counts := make([]int, len(d.Daily))
	for i, day := range d.Daily {
		counts[i] = day.Count
	}
	if len(d.Daily) > 0 {
		add(chart(fmt.Sprintf("Last %d days", len(counts)), counts, d.Daily[0].Day, m.width)...)
	}
	add("")

	add(bold.Render("Deploys it appeared in"))
	add(faint.Render(fmt.Sprintf("  %s%s%7s  %s%s", fit("REVISION / EXECUTION", 44), fit("COMMIT", 9), "COUNT", fit("FIRST", 13), "LAST")))
	for _, r := range d.Revisions {
		add(fmt.Sprintf("  %s%s%7d  %s%s", fit(r.Revision, 44), fit(shortSHA(r.SHA), 9), r.Count,
			fit(r.FirstSeen.Local().Format("01-02 15:04"), 13), ago(now, r.LastSeen)))
	}
	add("")

	latest := "Latest occurrence · " + d.Latest.Time.Local().Format("2006-01-02 15:04:05")
	if d.Latest.TraceID != "" {
		latest += " · trace " + d.Latest.TraceID
	}
	add(bold.Render(latest))
	wrap := lipgloss.NewStyle().Width(max(m.width-2, 20))
	switch {
	case m.message == nil:
		add(faint.Render("  (no log source to fetch the message from)"))
	case !v.bodyLoaded:
		add(faint.Render("  fetching from Cloud Logging…"))
	case v.bodyErr != nil:
		for _, l := range wrapLines(wrap, "couldn't fetch it: "+v.bodyErr.Error()) {
			add(errStyle.Render(l))
		}
	default:
		add(wrapLines(wrap, v.body)...)
	}

	height := max(m.height-1, 1)
	scroll := min(v.scroll, max(len(lines)-height, 0))
	return m.frame(lines[scroll:], footer)
}

// Every line of the result is one screen row, so scrolling stays exact.
func wrapLines(style lipgloss.Style, text string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(text, "\t", "    "), "\n") {
		out = append(out, strings.Split(style.Render(" "+l), "\n")...)
	}
	return out
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
