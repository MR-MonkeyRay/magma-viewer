package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"magma-viewer/internal/config"
)

type hook struct {
	contentType string
	body        []byte
}

func webhook(t *testing.T, status int) (*httptest.Server, chan hook) {
	t.Helper()
	got := make(chan hook, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- hook{r.Header.Get("Content-Type"), b}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func TestPostJSON(t *testing.T) {
	srv, got := webhook(t, http.StatusOK)
	n := New(config.Notify{Webhook: srv.URL, Format: "json"})
	if err := n.Post(context.Background(), "fetch_failing", "boom"); err != nil {
		t.Fatal(err)
	}
	h := <-got
	if h.contentType != "application/json" {
		t.Fatalf("content type %q", h.contentType)
	}
	var m map[string]any
	if err := json.Unmarshal(h.body, &m); err != nil {
		t.Fatal(err)
	}
	if m["event"] != "fetch_failing" || m["message"] != "boom" || m["title"] != titles["fetch_failing"] || m["time"] == nil {
		t.Fatalf("body: %s", h.body)
	}
}

func TestPostText(t *testing.T) {
	srv, got := webhook(t, http.StatusOK)
	n := New(config.Notify{Webhook: srv.URL, Format: "text"})
	if err := n.Post(context.Background(), EventTest, "hello"); err != nil {
		t.Fatal(err)
	}
	h := <-got
	if h.contentType != "text/plain; charset=utf-8" {
		t.Fatalf("content type %q", h.contentType)
	}
	if want := titles[EventTest] + "\nhello"; string(h.body) != want {
		t.Fatalf("body %q, want %q", h.body, want)
	}
}

func TestPostWebhookError(t *testing.T) {
	srv, _ := webhook(t, http.StatusInternalServerError)
	n := New(config.Notify{Webhook: srv.URL, Format: "json"})
	if err := n.Post(context.Background(), EventTest, "x"); err == nil {
		t.Fatal("500 answer reported as success")
	}
}

func TestNewWithoutWebhook(t *testing.T) {
	if New(config.Notify{}) != nil {
		t.Fatal("notifier built without a webhook")
	}
}
