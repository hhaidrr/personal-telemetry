// Package tui is pt's terminal interface: views over what the background
// service has recorded. It only reads pt.db, so it runs alongside the service.
package tui

import (
	"context"
	"os/exec"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hhaidrr/personal-telemetry/internal/ownership"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
	"github.com/hhaidrr/personal-telemetry/internal/store"
)

const (
	refreshEvery = 15 * time.Second
	recentWindow = 24 * time.Hour
	historyDays  = 30
)

type Data interface {
	Issues(ctx context.Context, since time.Time) ([]store.Issue, error)
	Issue(ctx context.Context, key string, since time.Time) (store.IssueDetail, error)
	DailyByIssue(ctx context.Context, since time.Time) ([]store.IssueDay, error)
}

// Fetches an entry's full message from the log source; the database keeps
// metadata only.
type MessageFunc func(ctx context.Context, project, eventID string) (string, error)

type Options struct {
	Environments []string
	// Owned subsystems and ignored paths, as in the config.
	Subsystems map[string][]string
	Ignore     []string
}

func Run(ctx context.Context, data Data, message MessageFunc, opts Options) error {
	_, err := tea.NewProgram(New(ctx, data, message, opts), tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	return err
}

type screen int

const (
	systemsScreen screen = iota
	systemScreen
	issuesScreen
	issueScreen
)

type Model struct {
	ctx     context.Context
	data    Data
	message MessageFunc
	now     func() time.Time
	open    func(url string)
	refresh time.Duration

	width, height int
	screen        screen
	back          screen
	err           error

	envs       []string
	env        int
	subsystems map[string][]string
	ignore     []string
	issues     []store.Issue
	days       []store.IssueDay

	systemsView
	systemView
	issuesView
	issueView
}

func New(ctx context.Context, data Data, message MessageFunc, opts Options) Model {
	return Model{
		ctx:        ctx,
		data:       data,
		message:    message,
		now:        time.Now,
		open:       func(url string) { exec.Command("xdg-open", url).Start() },
		refresh:    refreshEvery,
		envs:       append([]string{"all"}, opts.Environments...),
		subsystems: opts.Subsystems,
		ignore:     opts.Ignore,
	}
}

type loaded struct {
	issues []store.Issue
	days   []store.IssueDay
	err    error
}

type detailLoaded struct {
	key    string
	detail store.IssueDetail
	err    error
}

type bodyLoaded struct {
	eventID string
	body    string
	err     error
}

type tick struct{}

func (m Model) Init() tea.Cmd { return tea.Batch(m.load(), m.tick()) }

func (m Model) tick() tea.Cmd {
	if m.refresh == 0 {
		return nil
	}
	return tea.Tick(m.refresh, func(time.Time) tea.Msg { return tick{} })
}

func (m Model) load() tea.Cmd {
	since := m.now().Add(-recentWindow)
	return func() tea.Msg {
		issues, err := m.data.Issues(m.ctx, since)
		if err != nil {
			return loaded{err: err}
		}
		days, err := m.data.DailyByIssue(m.ctx, time.Time{})
		return loaded{issues, days, err}
	}
}

func (m Model) loadDetail(key string) tea.Cmd {
	since := m.now().AddDate(0, 0, -(historyDays - 1))
	return func() tea.Msg {
		d, err := m.data.Issue(m.ctx, key, since)
		return detailLoaded{key, d, err}
	}
}

func (m Model) loadBody(o store.Occurrence) tea.Cmd {
	return func() tea.Msg {
		body, err := m.message(m.ctx, o.Project, strings.TrimPrefix(o.EventID, pipeline.UnparsedEventPrefix))
		return bodyLoaded{o.EventID, body, err}
	}
}

func (m Model) openIssue(key string, from screen) (Model, tea.Cmd) {
	m.screen, m.back = issueScreen, from
	m.detailKey, m.detail, m.scroll = key, nil, 0
	return m, m.loadDetail(key)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tick:
		cmds := []tea.Cmd{m.load(), m.tick()}
		if m.screen == issueScreen {
			cmds = append(cmds, m.loadDetail(m.detailKey))
		}
		return m, tea.Batch(cmds...)
	case loaded:
		m.err = msg.err
		if msg.err == nil {
			m.issues, m.days = m.owned(msg.issues), msg.days
		}
	case detailLoaded:
		if msg.key != m.detailKey {
			return m, nil
		}
		m.err = msg.err
		if msg.err != nil {
			return m, nil
		}
		fresh := m.detail == nil || m.detail.Latest.EventID != msg.detail.Latest.EventID
		m.detail = &msg.detail
		if fresh && m.message != nil {
			m.body, m.bodyErr, m.bodyLoaded = "", nil, false
			return m, m.loadBody(msg.detail.Latest)
		}
	case bodyLoaded:
		if m.detail != nil && msg.eventID == m.detail.Latest.EventID {
			m.body, m.bodyErr, m.bodyLoaded = msg.body, msg.err, true
		}
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.filtering {
		return m.filterKey(msg), nil
	}
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "1":
		m.screen = systemsScreen
		return m, nil
	case "2":
		m.screen = issuesScreen
		return m, nil
	case "tab":
		m.env = (m.env + 1) % len(m.envs)
		m.resetCursors()
		return m, nil
	case "shift+tab":
		m.env = (m.env + len(m.envs) - 1) % len(m.envs)
		m.resetCursors()
		return m, nil
	}
	switch m.screen {
	case systemsScreen:
		return m.systemsKey(msg)
	case systemScreen:
		return m.systemKey(msg)
	case issueScreen:
		return m.issueKey(msg)
	}
	return m.issuesKey(msg)
}

// History recorded before a path was ignored is hidden too, so the views
// always reflect the current ownership rules.
func (m Model) owned(issues []store.Issue) []store.Issue {
	return slices.DeleteFunc(issues, func(i store.Issue) bool {
		return !isUnparsed(i) && ownership.Ignored(m.ignore, m.subsystems, i.Path)
	})
}

func (m *Model) resetCursors() { m.cursor, m.sysIssue = 0, 0 }

func (m Model) environment() string { return m.envs[m.env] }

func (m Model) halfPage() int { return max(m.height/2, 1) }

// Vim keys and arrows, shared by every list.
func moveCursor(key string, cursor, n, page int) int {
	switch key {
	case "j", "down":
		cursor++
	case "k", "up":
		cursor--
	case "ctrl+d", "pgdown":
		cursor += page
	case "ctrl+u", "pgup":
		cursor -= page
	case "g", "home":
		cursor = 0
	case "G", "end":
		cursor = n - 1
	}
	return max(min(cursor, n-1), 0)
}

func isForward(key string) bool { return key == "enter" || key == "l" || key == "right" }

func isBack(key string) bool {
	return key == "esc" || key == "h" || key == "left" || key == "backspace"
}

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}
	switch m.screen {
	case systemsScreen:
		return m.systemsView.render(m)
	case systemScreen:
		return m.systemView.render(m)
	case issueScreen:
		return m.issueView.render(m)
	}
	return m.issuesView.render(m)
}

// The tab strip shared by the list screens, with the active view and environment.
func (m Model) tabs() string {
	var views []string
	for i, name := range []string{"Systems", "Issues"} {
		label := " " + string(rune('1'+i)) + " " + name + " "
		if (i == 0) == (m.screen == systemsScreen || m.screen == systemScreen) {
			views = append(views, selected.Render(label))
		} else {
			views = append(views, faint.Render(label))
		}
	}
	var envs []string
	for i, e := range m.envs {
		if i == m.env {
			envs = append(envs, bold.Render("["+e+"]"))
		} else {
			envs = append(envs, faint.Render(" "+e+" "))
		}
	}
	return bold.Render("pt ") + strings.Join(views, "") + "   " + strings.Join(envs, "")
}

// Pads to the screen height and adds the footer, or the error if there is one.
func (m Model) frame(lines []string, footer string) string {
	for len(lines) < m.height-1 {
		lines = append(lines, "")
	}
	lines = lines[:max(m.height-1, 0)]
	if m.err != nil {
		footer = errStyle.Render(fit(firstLine(m.err.Error()), m.width))
	} else {
		footer = faint.Render(fit(footer, m.width))
	}
	return strings.Join(append(lines, footer), "\n")
}
