package tui

import (
	"fmt"
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hhaidrr/personal-telemetry/internal/store"
)

type systemView struct {
	system   string
	sysIssue int
	split    bool
}

func (m Model) currentSystem() systemStats {
	for _, s := range m.systems() {
		if s.Name == m.system {
			return s
		}
	}
	return systemStats{Name: m.system}
}

func (m Model) systemIssues(s systemStats) []store.Issue {
	rows := slices.Clone(s.Issues)
	slices.SortStableFunc(rows, func(a, b store.Issue) int { return b.LastSeen.Compare(a.LastSeen) })
	return rows
}

func (m Model) systemKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.systemIssues(m.currentSystem())
	key := msg.String()
	switch {
	case isBack(key):
		m.screen = systemsScreen
	case key == "p":
		m.period = (m.period + 1) % period(len(periodNames))
	case key == "e":
		m.split = !m.split
	case isForward(key):
		if len(rows) > 0 {
			return m.openIssue(rows[m.sysIssue].Key, systemScreen)
		}
	default:
		m.sysIssue = moveCursor(key, m.sysIssue, len(rows), m.halfPage())
	}
	return m, nil
}

func (v systemView) render(m Model) string {
	s, now := m.currentSystem(), m.now()
	lines := []string{m.tabs(), "", bold.Render(s.Name) + faint.Render(" · last "+periodNames[m.period])}

	days, busiest, busiestDay := 0, 0, s.Start
	for i, c := range s.Daily {
		if c > 0 {
			days++
		}
		if c > busiest {
			busiest, busiestDay = c, s.Start.AddDate(0, 0, i)
		}
	}
	if s.Errors == 0 {
		lines = append(lines, faint.Render("no errors in this period"))
	} else {
		lines = append(lines, fmt.Sprintf("%s · %s (%d new) · errors on %d of %d days · busiest %s (%s) · last error %s",
			plural(s.Errors, "error"), plural(s.Active, "issue"), s.New, days, len(s.Daily), busiestDay.Format("Jan 2"), num(busiest), ago(now, s.Last)))
	}
	lines = append(lines, "")

	if v.split && len(s.EnvsSeen) > 0 {
		lines = append(lines, bold.Render("Errors per day, by environment"))
		width := m.width - 4 - 11
		peak := 0
		for _, e := range s.EnvsSeen {
			peak = max(peak, peakOf(scale(s.ByEnv[e], width)))
		}
		for _, e := range s.EnvsSeen {
			lines = append(lines, "  "+envStyle(e).Render(fit(e, 11))+accent.Render(sparkline(scale(s.ByEnv[e], width), peak)))
		}
		lines = append(lines, faint.Render("  "+fit("", 11)+dateRange(s.Start, width)))
	} else {
		lines = append(lines, chart("Errors per day", s.Daily, s.Start, m.width)...)
	}
	lines = append(lines, "", bold.Render("Issues"))
	lines = append(lines, issueTable(m.systemIssues(s), v.sysIssue, m.width, m.height-len(lines)-1, now)...)
	return m.frame(lines, "j/k move · l/enter open · h/esc back · e split by environment · p period · tab environment · q quit")
}
