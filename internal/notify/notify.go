// Package notify posts alerts to a webhook, so an operator hears about an
// expiring cookie or a failing fetch without watching the logs.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"magma-viewer/internal/config"
)

// EventTest is sent by the admin page to check the configuration.
const EventTest = "test"

var titles = map[string]string{
	"fetch_failing":   "Magma 画板存档：抓取连续失败",
	"fetch_recovered": "Magma 画板存档：抓取已恢复",
	"cookie_expiring": "Magma 画板存档：Cookie 即将过期",
	EventTest:         "Magma 画板存档：测试通知",
}

type Notifier struct {
	url, format string
	client      *http.Client
}

// New returns nil when no webhook is configured.
func New(cfg config.Notify) *Notifier {
	if cfg.Webhook == "" {
		return nil
	}
	return &Notifier{url: cfg.Webhook, format: cfg.Format, client: &http.Client{Timeout: 10 * time.Second}}
}

// Send delivers in the background; failures are logged, not retried.
func (n *Notifier) Send(event, message string) {
	go func() {
		if err := n.Post(context.Background(), event, message); err != nil {
			log.Printf("notify %s: %v", event, err)
		}
	}()
}

// Post delivers one alert and waits for the answer.
func (n *Notifier) Post(ctx context.Context, event, message string) error {
	title := titles[event]
	if title == "" {
		title = "Magma 画板存档：" + event
	}
	var body []byte
	contentType := "application/json"
	if n.format == "text" {
		// Plain text suits services that show the body as is (ntfy, simple bots).
		body, contentType = []byte(title+"\n"+message), "text/plain; charset=utf-8"
	} else {
		body, _ = json.Marshal(map[string]any{"event": event, "title": title, "message": message, "time": time.Now().UTC()})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook answered %s", resp.Status)
	}
	return nil
}
