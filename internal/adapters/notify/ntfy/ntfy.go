// Package ntfy pushes alerts to a phone through an ntfy server.
package ntfy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hhaidrr/personal-telemetry/internal/adapters/notify/text"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

const defaultServer = "https://ntfy.sh"

// Pushes carry metadata only. Log messages hold customer data, and the topic
// lives on a third-party server where its name is the only secret.
type Notifier struct {
	Server string
	Topic  string
	Client *http.Client
}

var _ pipeline.Notifier = (*Notifier)(nil)

type message struct {
	Topic    string   `json:"topic"`
	Title    string   `json:"title"`
	Message  string   `json:"message"`
	Priority int      `json:"priority"`
	Tags     []string `json:"tags,omitempty"`
	Click    string   `json:"click,omitempty"`
}

func (n *Notifier) Notify(ctx context.Context, a pipeline.Alert) error {
	body, err := json.Marshal(payload(n.Topic, a))
	if err != nil {
		return err
	}
	server := n.Server
	if server == "" {
		server = defaultServer
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(server, "/"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := n.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ntfy: %s: %s", resp.Status, bytes.TrimSpace(detail))
	}
	return nil
}

// Production and pt's own failures break through Do Not Disturb (priority 4);
// the rest arrive normally (3). pt's own messages hold no customer data.
func payload(topic string, a pipeline.Alert) message {
	priority := 3
	if a.Occurrence.Environment == "production" || a.System != "" {
		priority = 4
	}
	if a.System != "" {
		return message{Topic: topic, Title: a.System, Message: strings.Join(text.Lines(a.Message), "\n"), Priority: priority, Tags: []string{"warning"}}
	}
	return message{
		Topic:    topic,
		Title:    text.Headline(a),
		Message:  text.Footer(a),
		Priority: priority,
		Tags:     []string{"rotating_light"},
		Click:    a.Occurrence.Link,
	}
}
