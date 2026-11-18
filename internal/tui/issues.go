package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hhaidrr/personal-telemetry/internal/store"
)

type sortOrder int

const (
	byLastSeen sortOrder = iota
	byRecent
	byTotal
)

var sortNames = []string{"last seen", "24h", "total"}

type issuesView struct {
	cursor    int
	sort      sortOrder
	filter    string
	filtering bool
}

func (m Model) issuesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.visible()
	key := msg.String()
	switch {
	case key == "s":
		m.sort, m.cursor = (m.sort+1)%sortOrder(len(sortNames)), 0
	case key == "/":
		m.filtering = true
	case key == "esc":
		m.filter, m.cursor = "", 0
	case key == "r":
		return m, m.load()
	case isForward(key):
		if len(rows) > 0 {
			return m.openIssue(rows[m.cursor].Key, issuesScreen)
		}
	default:
		m.cursor = moveCursor(key, m.cursor, len(rows), m.halfPage())
	}
	return m, nil
}

func (m Model) filterKey(msg tea.KeyMsg) Model {
	switch msg.Type {
	case tea.KeyEnter:
		m.filtering = false
	case tea.KeyEsc:
		m.filtering, m.filter = false, ""
	case tea.KeyBackspace:
		if r := []rune(m.filter); len(r) > 0 {
			m.filter = string(r[:len(r)-1])
		}
	case tea.KeyRunes, tea.KeySpace:
		m.filter += string(msg.Runes)
	}
	m.cursor = 0
	return m
}

// The chosen environment, matching the filter, in the chosen order.
func (m Model) visible() []store.Issue {
	filter := strings.ToLower(m.filter)
	var out []store.Issue
	for _, i := range m.inEnvironment() {
		if filter != "" && !strings.Contains(strings.ToLower(strings.Join([]string{i.Key, i.Service, m.systemOf(i)}, " ")), filter) {
			continue
		}
		out = append(out, i)
	}
	slices.SortStableFunc(out, func(a, b store.Issue) int {
		switch m.sort {
		case byRecent:
			return b.Recent - a.Recent
		case byTotal:
			return b.Count - a.Count
		}
		return b.LastSeen.Compare(a.LastSeen)
	})
	return out
}

func (m Model) inEnvironment() []store.Issue {
	env := m.environment()
	var out []store.Issue
	for _, i := range m.issues {
		if env == "all" || i.Environment == env {
			out = append(out, i)
		}
	}
	return out
}

func (v issuesView) render(m Model) string {
	rows := m.visible()
	now := m.now()
	recent, fresh := 0, 0
	for _, i := range rows {
		if i.Recent > 0 {
			recent++
		}
		if now.Sub(i.FirstSeen) < recentWindow {
			fresh++
		}
	}
	lines := []string{m.tabs(), faint.Render(fmt.Sprintf("%d issues · %d active in 24h · %d new", len(rows), recent, fresh)), ""}
	lines = append(lines, issueTable(rows, v.cursor, m.width, m.height-len(lines)-1, now)...)

	footer := "j/k move · l/enter open · tab environment · s sort: " + sortNames[v.sort] + " · / filter · 1 systems · q quit"
	switch {
	case v.filtering:
		footer = "filter: " + v.filter + "█"
	case v.filter != "":
		footer = "filter: " + v.filter + " (esc clears) · " + footer
	}
	return m.frame(lines, footer)
}

// A header and as many rows as fit in height, scrolled to keep the cursor in view.
func issueTable(rows []store.Issue, cursor, width, height int, now time.Time) []string {
	whereWidth := 0
	if width >= 110 {
		whereWidth = 30
	}
	issueWidth := max(width-2-11-10-6-7-2-9-whereWidth, 10)
	lines := []string{faint.Render(fmt.Sprintf("  %s%s%s%6s%7s  %s%s",
		fit("ENV", 11), fit("TIER", 10), fit("ISSUE", issueWidth), "24H", "TOTAL", fit("LAST", 9), fit("WHERE", whereWidth)))}
	if len(rows) == 0 {
		return append(lines, faint.Render("  nothing here"))
	}

	body := max(height-1, 1)
	start := max(0, cursor-body+1)
	for idx := start; idx < len(rows) && idx < start+body; idx++ {
		i := rows[idx]
		mark := "  "
		if now.Sub(i.FirstSeen) < recentWindow {
			mark = accent.Render("● ")
		}
		cells := []string{fit(i.Environment, 11), fit(tierName(i), 10), fit(issueTitle(i), issueWidth),
			fmt.Sprintf("%6d%7d  ", i.Recent, i.Count), fit(ago(now, i.LastSeen), 9), fit(workload(i.Service, i.Job), whereWidth)}
		var line string
		if idx == cursor {
			line = selected.Render(strings.Join(cells, ""))
		} else {
			line = envStyle(i.Environment).Render(cells[0]) + tierStyle(tierName(i)).Render(cells[1]) +
				cells[2] + cells[3] + cells[4] + faint.Render(cells[5])
		}
		lines = append(lines, mark+line)
	}
	return lines
}
