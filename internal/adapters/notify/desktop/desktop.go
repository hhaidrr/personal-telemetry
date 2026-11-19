// Package desktop shows alerts through the desktop's notification daemon.
package desktop

import (
	"context"
	"fmt"
	"html"
	"os/exec"
	"strings"

	"github.com/hhaidrr/personal-telemetry/internal/adapters/notify/text"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

type Notifier struct{}

var _ pipeline.Notifier = Notifier{}

// notify-send waits for a click so the link can be opened; that wait runs in
// the background so a notification left on screen doesn't hold up the poll.
func (Notifier) Notify(ctx context.Context, a pipeline.Alert) error {
	cmd := exec.Command("notify-send", args(a)...)
	var out strings.Builder
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("desktop: %w", err)
	}
	go func() {
		if cmd.Wait() == nil && strings.TrimSpace(out.String()) == "default" {
			exec.Command("xdg-open", a.Occurrence.Link).Run()
		}
	}()
	return nil
}

func args(a pipeline.Alert) []string {
	urgency := "normal"
	if a.Occurrence.Environment == "production" || a.System != "" {
		urgency = "critical"
	}
	// Critical notifications stay up for a while, without the daemon's
	// default for critical (often until clicked) applying to pt's alerts.
	out := []string{"--app-name=pt", "--urgency=" + urgency}
	if urgency == "critical" {
		out = append(out, "--expire-time=30000")
	}
	if a.Occurrence.Link != "" {
		out = append(out, "--action=default=Open in Cloud Logging", "--wait")
	}
	// Notification bodies are markup, and frames like <module> would vanish.
	body := append(text.Summary(a.Message), text.Footer(a))
	if a.System != "" {
		body = text.Lines(a.Message)
	}
	return append(out, text.Headline(a), html.EscapeString(strings.Join(body, "\n")))
}
