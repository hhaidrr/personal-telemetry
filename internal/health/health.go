// Package health notices when pt itself stops working, so a broken pt doesn't
// look the same as a quiet production.
package health

import (
	"strings"
	"time"

	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

const (
	defaultFailAfter = 3
	defaultRepeat    = time.Hour
)

// Monitor turns poll results into alerts: one after FailAfter consecutive
// failures, again every Repeat while they continue, and one on recovery.
type Monitor struct {
	FailAfter int
	Repeat    time.Duration

	failures  int
	since     time.Time
	alertedAt time.Time
}

func (m *Monitor) Observe(err error, now time.Time) *pipeline.Alert {
	if err == nil {
		recovered := !m.alertedAt.IsZero()
		down := now.Sub(m.since).Round(time.Minute)
		*m = Monitor{FailAfter: m.FailAfter, Repeat: m.Repeat}
		if recovered {
			return &pipeline.Alert{System: "pt is reading logs again", Message: "after " + down.String() + " of failures"}
		}
		return nil
	}

	if m.failures == 0 {
		m.since = now
	}
	m.failures++
	if m.failures < or(m.FailAfter, defaultFailAfter) {
		return nil
	}
	if !m.alertedAt.IsZero() && now.Sub(m.alertedAt) < orDuration(m.Repeat, defaultRepeat) {
		return nil
	}
	m.alertedAt = now
	return &pipeline.Alert{System: "pt can't read logs", Message: describe(err, now.Sub(m.since))}
}

// Credentials expire on their own (Workspace reauth), so that case gets the fix.
func describe(err error, failing time.Duration) string {
	msg := err.Error()
	first, _, _ := strings.Cut(msg, "\n")
	if isAuth(msg) {
		return "Google credentials expired or were revoked. Run: gcloud auth application-default login\n" + first
	}
	return "Failing for " + failing.Round(time.Minute).String() + "; check journalctl --user -u pt\n" + first
}

func isAuth(msg string) bool {
	for _, s := range []string{"oauth2:", "invalid_grant", "invalid_rapt", "reauth", "401 Unauthorized", "UNAUTHENTICATED", "could not find default credentials"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func or(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}

func orDuration(v, fallback time.Duration) time.Duration {
	if v == 0 {
		return fallback
	}
	return v
}
