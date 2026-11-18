package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hhaidrr/personal-telemetry/internal/store"
)

var (
	now   = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	today = now.Truncate(24 * time.Hour)
)

type fakeData struct {
	issues []store.Issue
	days   []store.IssueDay
}

func (f fakeData) Issues(context.Context, time.Time) ([]store.Issue, error) { return f.issues, nil }

func (f fakeData) DailyByIssue(context.Context, time.Time) ([]store.IssueDay, error) {
	return f.days, nil
}

func (f fakeData) Issue(_ context.Context, key string, _ time.Time) (store.IssueDetail, error) {
	for _, i := range f.issues {
		if i.Key == key {
			return store.IssueDetail{
				Issue:     i,
				Daily:     []store.DayCount{{Day: today.AddDate(0, 0, -1), Count: 2}, {Day: today, Count: 5}},
				Revisions: []store.RevisionCount{{Revision: "divdot-00204-4mz", SHA: "f1673b8aaaa", Count: 7, FirstSeen: now, LastSeen: now}},
				Latest:    store.Occurrence{EventID: "e1@t", Project: "divdot-production", Link: "https://l/e1", Time: now},
			}, nil
		}
	}
	return store.IssueDetail{}, errors.New("not found")
}

const (
	prodKey    = "production/KeyError@src/payments/channels/a.py:f"
	payoutsKey = "develop/error@src/adyen/payouts.py:run"
)

func data() fakeData {
	return fakeData{
		issues: []store.Issue{
			{Key: prodKey, ExcType: "KeyError", Path: "src/payments/channels/a.py", Line: 3, Tier: "primary",
				Environment: "production", Service: "divdot", Count: 7, Recent: 1, FirstSeen: now.Add(-time.Hour), LastSeen: now.Add(-time.Hour)},
			{Key: payoutsKey, Path: "src/adyen/payouts.py", Line: 9, Tier: "primary", Environment: "develop",
				Service: "adyen-initiate-payouts", Job: true, Count: 600, Recent: 40, FirstSeen: now.AddDate(0, 0, -9), LastSeen: now.Add(-2 * time.Hour)},
		},
		days: []store.IssueDay{
			{Key: prodKey, Environment: "production", Day: today, Count: 7},
			{Key: payoutsKey, Environment: "develop", Day: today.AddDate(0, 0, -9), Count: 560},
			{Key: payoutsKey, Environment: "develop", Day: today, Count: 40},
		},
	}
}

var subsystems = map[string][]string{
	"payment-channels": {"src/payments/channels/**"},
	"batch-payouts":    {"src/payments/batch_payouts/**"},
}

// Drives the model as the runtime would: each command's message is fed back in.
func send(t *testing.T, m tea.Model, msgs ...tea.Msg) tea.Model {
	t.Helper()
	for _, msg := range msgs {
		var cmd tea.Cmd
		m, cmd = m.Update(msg)
		m = drain(m, cmd)
	}
	return m
}

func drain(m tea.Model, cmd tea.Cmd) tea.Model {
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			m = drain(m, c)
		}
	case nil:
	default:
		var next tea.Cmd
		m, next = m.Update(msg)
		m = drain(m, next)
	}
	return m
}

func keys(s ...string) []tea.Msg {
	var out []tea.Msg
	for _, k := range s {
		switch k {
		case "enter":
			out = append(out, tea.KeyMsg{Type: tea.KeyEnter})
		case "esc":
			out = append(out, tea.KeyMsg{Type: tea.KeyEsc})
		case "tab":
			out = append(out, tea.KeyMsg{Type: tea.KeyTab})
		case "down":
			out = append(out, tea.KeyMsg{Type: tea.KeyDown})
		case "left":
			out = append(out, tea.KeyMsg{Type: tea.KeyLeft})
		default:
			out = append(out, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		}
	}
	return out
}

func newModel(t *testing.T, message MessageFunc, width int) Model {
	m := New(context.Background(), data(), message, Options{Environments: []string{"develop", "production"}, Subsystems: subsystems})
	m.now, m.refresh = func() time.Time { return now }, 0
	return send(t, drain(m, m.Init()), tea.WindowSizeMsg{Width: width, Height: 40}).(Model)
}

func TestSystemsGroupsIssuesByCurrentSubsystems(t *testing.T) {
	systems := newModel(t, nil, 140).systems()
	names := []string{}
	for _, s := range systems {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "batch-payouts,payment-channels,"+otherCode {
		t.Fatalf("systems = %v, want subsystems alphabetically then other code", names)
	}
	batch, channels, other := systems[0], systems[1], systems[2]
	if batch.Errors != 0 || len(batch.Issues) != 0 {
		t.Errorf("batch-payouts = %+v, want healthy", batch)
	}
	if channels.Errors != 7 || channels.Prod != 1 || channels.Daily[len(channels.Daily)-1] != 7 {
		t.Errorf("payment-channels = %+v", channels)
	}
	if other.Errors != 600 || other.Active != 1 || other.EnvsSeen[0] != "develop" {
		t.Errorf("other code = %+v", other)
	}
	if c, _ := channels.health(); c != "1" {
		t.Errorf("errors in production today should be red, got %v", c)
	}
}

func TestIgnoredPathsDisappearFromHistory(t *testing.T) {
	m := New(context.Background(), data(), nil, Options{Subsystems: subsystems, Ignore: []string{"src/adyen/**", "src/payments/**"}})
	m.now, m.refresh = func() time.Time { return now }, 0
	m = send(t, drain(m, m.Init()), tea.WindowSizeMsg{Width: 140, Height: 40}, keys("2")[0]).(Model)
	if view := m.View(); strings.Contains(view, "payouts.py") || !strings.Contains(view, "a.py:3") {
		t.Errorf("ignored adyen issue shown, or subsystem issue under an ignored parent hidden:\n%s", view)
	}
}

func TestSystemsFollowTheEnvironment(t *testing.T) {
	m := send(t, newModel(t, nil, 140), keys("tab")...).(Model) // develop
	for _, s := range m.systems() {
		if s.Name == "payment-channels" && s.Errors != 0 {
			t.Errorf("payment-channels in develop = %d errors, want 0", s.Errors)
		}
	}
}

func TestCardsRenderAndHjklMovesAcrossThem(t *testing.T) {
	m := newModel(t, nil, 140) // three cards per row
	view := m.View()
	for _, want := range []string{"1 Systems", "batch-payouts", "✓ no errors in 30 days", "1 issue · 7 errors", "600 errors"} {
		if !strings.Contains(view, want) {
			t.Errorf("systems view lacks %q:\n%s", want, view)
		}
	}
	m = send(t, m, keys("l", "l", "l")...).(Model)
	if m.sysCursor != 2 {
		t.Errorf("cursor = %d after lll, want 2 (stops at the row end)", m.sysCursor)
	}
	m = send(t, m, keys("h", "left")...).(Model)
	if m.sysCursor != 0 {
		t.Errorf("cursor = %d after h and ←, want 0", m.sysCursor)
	}
}

func TestDrillDownAndBackUpWithVimKeys(t *testing.T) {
	m := send(t, newModel(t, func(context.Context, string, string) (string, error) { return "KeyError: 'x'", nil }, 140),
		keys("l", "enter")...).(Model)
	if m.screen != systemScreen || m.system != "payment-channels" {
		t.Fatalf("screen %v system %q", m.screen, m.system)
	}
	if view := m.View(); !strings.Contains(view, "7 errors · 1 issue") || !strings.Contains(view, "KeyError payments/channels/a.py:3") {
		t.Errorf("system view:\n%s", view)
	}
	if view := send(t, m, keys("e")...).View(); !strings.Contains(view, "by environment") {
		t.Errorf("split view:\n%s", view)
	}

	m = send(t, m, keys("l")...).(Model)
	if m.screen != issueScreen || !strings.Contains(m.View(), "KeyError: 'x'") {
		t.Fatalf("issue view:\n%s", m.View())
	}
	m = send(t, m, keys("h")...).(Model)
	if m.screen != systemScreen {
		t.Errorf("h from an issue opened in a system should return to it, got %v", m.screen)
	}
	m = send(t, m, keys("h")...).(Model)
	if m.screen != systemsScreen {
		t.Errorf("h from a system should return to the grid, got %v", m.screen)
	}
}

func TestIssuesListSortsFiltersAndSwitchesEnvironment(t *testing.T) {
	m := send(t, newModel(t, nil, 140), keys("2")...)
	view := m.View()
	if a, b := strings.Index(view, "a.py:3"), strings.Index(view, "payouts.py:9"); a < 0 || b < 0 || a > b {
		t.Fatalf("want both issues, most recent first:\n%s", view)
	}
	if !strings.Contains(view, "job adyen-initiate-payouts") {
		t.Errorf("jobs not labelled:\n%s", view)
	}
	if view := send(t, m, keys("s")...).View(); strings.Index(view, "payouts.py:9") > strings.Index(view, "a.py:3") {
		t.Errorf("sorting by 24h should put the busier issue first:\n%s", view)
	}
	if view := send(t, m, keys("tab")...).View(); strings.Contains(view, "a.py:3") {
		t.Errorf("develop tab shows a production issue:\n%s", view)
	}
	if view := send(t, m, keys("/", "c", "h", "a", "n", "enter")...).View(); strings.Contains(view, "payouts.py:9") || !strings.Contains(view, "a.py:3") {
		t.Errorf("filtering by subsystem name:\n%s", view)
	}
}

func TestIssueDetailFromTheList(t *testing.T) {
	var fetched, opened string
	message := func(_ context.Context, project, eventID string) (string, error) {
		fetched = project + " " + eventID
		return "Traceback (most recent call last):\nKeyError: 'interac'", nil
	}
	m := newModel(t, message, 140)
	m.open = func(url string) { opened = url }

	m = send(t, m, keys("2", "l")...).(Model)
	view := m.View()
	for _, want := range []string{"src/payments/channels/a.py:3 in", "payment-channels ·", "Last 2 days", "divdot-00204-4mz", "f1673b8", "KeyError: 'interac'"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail lacks %q:\n%s", want, view)
		}
	}
	if fetched != "divdot-production e1@t" {
		t.Errorf("fetched %q", fetched)
	}
	if send(t, m, keys("o")...); opened != "https://l/e1" {
		t.Errorf("opened %q", opened)
	}
	if m = send(t, m, keys("esc")...).(Model); m.screen != issuesScreen {
		t.Errorf("esc should return to the list, got %v", m.screen)
	}
}

func TestHelpers(t *testing.T) {
	if got := sparkline([]int{0, 1, 4, 8}, 8); got != "▁▂▅█" {
		t.Errorf("sparkline = %s", got)
	}
	if got := sparkline([]int{0, 0}, 0); got != "▁▁" {
		t.Errorf("empty sparkline = %s", got)
	}
	if cells := scale([]int{1, 2, 3, 4}, 2); len(cells) != 2 || cells[0] != 3 || cells[1] != 7 {
		t.Errorf("scale down = %v", cells)
	}
	if cells := scale([]int{1, 2}, 4); len(cells) != 4 || cells[0] != 1 || cells[3] != 2 {
		t.Errorf("scale up = %v", cells)
	}
	if got := envList([]string{"develop", "testing", "demo", "staging", "production"}, 30); strings.Count(got, "\n") > 0 || !strings.Contains(got, "+") {
		t.Errorf("envList = %q, want one line ending in +N", got)
	}
	if got := moveCursor("G", 0, 5, 2); got != 4 {
		t.Errorf("G = %d", got)
	}
	if got := moveCursor("ctrl+d", 3, 5, 2); got != 4 {
		t.Errorf("ctrl+d past the end = %d", got)
	}
	for d, want := range map[time.Duration]string{30 * time.Second: "just now", 5 * time.Minute: "5m ago", 30 * time.Hour: "30h ago", 72 * time.Hour: "3d ago"} {
		if got := ago(now, now.Add(-d)); got != want {
			t.Errorf("ago(%v) = %s, want %s", d, got, want)
		}
	}
	if got := num(1234567); got != "1,234,567" {
		t.Errorf("num = %s", got)
	}
	if got := fit("abcdef", 4); got != "abc…" {
		t.Errorf("fit = %q", got)
	}
	if got := issueTitle(store.Issue{ExcType: "django.db.utils.OperationalError", Path: "src/core/user.py", Line: 60}); got != "OperationalError core/user.py:60" {
		t.Errorf("title = %s", got)
	}
}
