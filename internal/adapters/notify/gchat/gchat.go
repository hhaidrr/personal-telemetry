package gchat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/hhaidrr/personal-telemetry/internal/adapters/notify/text"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

type Notifier struct {
	WebhookURL string
	Client     *http.Client
}

var _ pipeline.Notifier = (*Notifier)(nil)

func (n *Notifier) Notify(ctx context.Context, a pipeline.Alert) error {
	body, err := json.Marshal(map[string]string{"text": text.Format(a)})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, threadedURL(n.WebhookURL, a.Occurrence.Fingerprint), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	client := n.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("gchat: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("gchat: %s: %s", resp.Status, snippet)
	}
	return nil
}

// Threading by fingerprint keeps repeat alerts for one bug in a single thread.
func threadedURL(webhook, fingerprint string) string {
	return webhook + "&threadKey=" + url.QueryEscape(fingerprint) +
		"&messageReplyOption=REPLY_MESSAGE_FALLBACK_TO_NEW_THREAD"
}
