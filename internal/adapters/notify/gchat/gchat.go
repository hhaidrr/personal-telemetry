package gchat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

const maxMessageLen = 500

type Notifier struct {
	WebhookURL string
	Client     *http.Client
}

var _ pipeline.Notifier = (*Notifier)(nil)

func (n *Notifier) Notify(ctx context.Context, a pipeline.Alert) error {
	body, err := json.Marshal(map[string]string{"text": message(a)})
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

func message(a pipeline.Alert) string {
	o := a.Occurrence
	m := o.Match
	excType := o.ExcType
	if excType == "" {
		excType = "error"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "*%s* · %s in `%s:%d` (%s)\n", m.Tier, excType, m.Frame.Path, m.Frame.Line, m.Frame.Func)
	if msg := truncate(a.Message, maxMessageLen); msg != "" {
		fmt.Fprintf(&b, "```%s```\n", msg)
	}
	if m.Subsystem != "" {
		fmt.Fprintf(&b, "%s · ", m.Subsystem)
	}
	fmt.Fprintf(&b, "seen %d× since %s · %s@%s", a.State.Count, a.State.FirstSeen.UTC().Format("2006-01-02 15:04 UTC"), o.Revision, shortSHA(o.SHA))
	if o.TraceID != "" {
		fmt.Fprintf(&b, "\ntrace: %s", o.TraceID)
	}
	return b.String()
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
