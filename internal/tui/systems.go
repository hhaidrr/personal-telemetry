package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hhaidrr/personal-telemetry/internal/ownership"
	"github.com/hhaidrr/personal-telemetry/internal/store"
)

const (
	otherCode  = "other code you wrote"
	cardMin    = 44
	cardHeight = 7
)

type period int

const (
	last30 period = iota
	last90
	allTime
)

var periodNames = []string{"30 days", "90 days", "all time"}

type systemsView struct {
	sysCursor int
	period    period
}

type systemStats struct {
	Name     string
	Issues   []store.Issue
	Start    time.Time
	Daily    []int
	ByEnv    map[string][]int
	Errors   int
	Active   int
	New      int
	Recent   int
	Prod     int
	Last     time.Time
	EnvsSeen []string
}

// Where an issue belongs, by the current config, so editing the subsystems
// regroups history too.
func (m Model) systemOf(i store.Issue) string {
	if s := ownership.SubsystemFor(m.subsystems, i.Path); s != "" {
		return s
	}
	return otherCode
}

func (m Model) periodStart(today time.Time) time.Time {
	switch m.period {
	case last90:
		return today.AddDate(0, 0, -89)
	case allTime:
		start := today.AddDate(0, 0, -(historyDays - 1))
		for _, d := range m.days {
			if d.Day.Before(start) {
				start = d.Day
			}
		}
		return start
	}
	return today.AddDate(0, 0, -(historyDays - 1))
}

// Every owned subsystem, alphabetically so cards keep their place, then the
// code you wrote outside them. Unparsed entries aren't anyone's system.
func (m Model) systems() []systemStats {
	today := m.now().UTC().Truncate(24 * time.Hour)
	start := m.periodStart(today)
	n := int(today.Sub(start)/(24*time.Hour)) + 1

	names := make([]string, 0, len(m.subsystems)+1)
	for name := range m.subsystems {
		names = append(names, name)
	}
	slices.Sort(names)
	names = append(names, otherCode)
	byName := map[string]*systemStats{}
	out := make([]systemStats, len(names))
	for i, name := range names {
		out[i] = systemStats{Name: name, Start: start, Daily: make([]int, n), ByEnv: map[string][]int{}}
		byName[name] = &out[i]
	}

	// Keys are per environment, so only the chosen environment's days match.
	system := map[string]*systemStats{}
	for _, i := range m.inEnvironment() {
		if isUnparsed(i) {
			continue
		}
		s := byName[m.systemOf(i)]
		system[i.Key] = s
		s.Issues = append(s.Issues, i)
		if !i.FirstSeen.Before(start) {
			s.New++
		}
		s.Recent += i.Recent
		if i.Environment == "production" {
			s.Prod += i.Recent
		}
		if i.LastSeen.After(s.Last) {
			s.Last = i.LastSeen
		}
	}

	active := map[string]bool{}
	for _, d := range m.days {
		s, ok := system[d.Key]
		idx := int(d.Day.Sub(start) / (24 * time.Hour))
		if !ok || idx < 0 || idx >= n {
			continue
		}
		s.Daily[idx] += d.Count
		if s.ByEnv[d.Environment] == nil {
			s.ByEnv[d.Environment] = make([]int, n)
			s.EnvsSeen = append(s.EnvsSeen, d.Environment)
		}
		s.ByEnv[d.Environment][idx] += d.Count
		s.Errors += d.Count
		if !active[d.Key] {
			active[d.Key] = true
			s.Active++
		}
	}
	for i := range out {
		slices.SortFunc(out[i].EnvsSeen, m.envOrder)
	}
	return out
}

// Environments in config order.
func (m Model) envOrder(a, b string) int {
	return slices.Index(m.envs, a) - slices.Index(m.envs, b)
}

func (s systemStats) health() (lipgloss.Color, string) {
	switch {
	case s.Prod > 0:
		return lipgloss.Color("1"), "●"
	case s.Recent > 0:
		return lipgloss.Color("3"), "●"
	case s.Errors > 0:
		return lipgloss.Color("7"), "○"
	}
	return lipgloss.Color("2"), "✓"
}

func (m Model) columns() int { return max(1, m.width/cardMin) }

func (m Model) systemsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	systems := m.systems()
	cols, n := m.columns(), len(systems)
	switch msg.String() {
	case "h", "left":
		if m.sysCursor%cols > 0 {
			m.sysCursor--
		}
	case "l", "right":
		if m.sysCursor%cols < cols-1 && m.sysCursor+1 < n {
			m.sysCursor++
		}
	case "j", "down":
		if m.sysCursor+cols < n {
			m.sysCursor += cols
		}
	case "k", "up":
		if m.sysCursor-cols >= 0 {
			m.sysCursor -= cols
		}
	case "g", "home":
		m.sysCursor = 0
	case "G", "end":
		m.sysCursor = n - 1
	case "p":
		m.period = (m.period + 1) % period(len(periodNames))
	case "enter":
		m.screen, m.system, m.sysIssue = systemScreen, systems[m.sysCursor].Name, 0
	}
	return m, nil
}

func (v systemsView) render(m Model) string {
	systems := m.systems()
	lines := []string{m.tabs(), faint.Render(fmt.Sprintf("%d systems you own · last %s", len(systems)-1, periodNames[v.period])), ""}

	cols := m.columns()
	cardW := m.width / cols
	var rows []string
	for start := 0; start < len(systems); start += cols {
		var cards []string
		for i := start; i < min(start+cols, len(systems)); i++ {
			cards = append(cards, card(systems[i], i == v.sysCursor, cardW, periodNames[v.period], m.now()))
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, cards...))
	}
	fits := max((m.height-len(lines)-1)/cardHeight, 1)
	first := max(0, v.sysCursor/cols-fits+1)
	for _, row := range rows[first:min(first+fits, len(rows))] {
		lines = append(lines, strings.Split(row, "\n")...)
	}
	return m.frame(lines, "hjkl move · enter open · p period · tab environment · 2 issues · q quit")
}

func card(s systemStats, isSelected bool, width int, periodName string, now time.Time) string {
	inner := max(width-4, 10)
	color, symbol := s.health()

	status := lipgloss.NewStyle().Foreground(color).Render("✓ no errors in " + periodName)
	if s.Errors > 0 {
		status = lipgloss.NewStyle().Foreground(color).Render(symbol) +
			fmt.Sprintf(" %s · %s", plural(s.Active, "issue"), plural(s.Errors, "error"))
	}
	cells := scale(s.Daily, inner)
	spark := faint.Render(sparkline(cells, peakOf(cells)))
	if s.Errors > 0 {
		spark = accent.Render(sparkline(cells, peakOf(cells)))
	}
	envs := faint.Render("no environments affected")
	if len(s.EnvsSeen) > 0 {
		envs = envList(s.EnvsSeen, inner)
	}
	last := "nothing recorded yet"
	if !s.Last.IsZero() {
		last = fmt.Sprintf("last error %s · %d new", ago(now, s.Last), s.New)
	}

	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(color).Padding(0, 1).Width(width - 2)
	name := bold.Render(fit(s.Name, inner))
	if isSelected {
		style = style.Border(lipgloss.ThickBorder())
		name = selected.Render(fit(s.Name, inner))
	}
	return style.Render(strings.Join([]string{name, status, spark, envs, faint.Render(last)}, "\n"))
}

// As many environment names as fit on one line, then how many more.
// Production goes first so it's never the one cut off.
func envList(envs []string, width int) string {
	if i := slices.Index(envs, "production"); i > 0 {
		envs = append([]string{"production"}, slices.Delete(slices.Clone(envs), i, i+1)...)
	}
	var parts []string
	used := 0
	for i, e := range envs {
		more := ""
		if rest := len(envs) - i - 1; rest > 0 {
			more = fmt.Sprintf(" +%d", rest)
		}
		if used+len(e)+len(more) > width {
			return strings.Join(parts, faint.Render(" · ")) + faint.Render(fmt.Sprintf(" +%d", len(envs)-i))
		}
		parts = append(parts, envStyle(e).Render(e))
		used += len(e) + 3
	}
	return strings.Join(parts, faint.Render(" · "))
}
