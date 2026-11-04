package main

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"magma-viewer/internal/board"
	"magma-viewer/internal/config"
)

func TestSharedCredentialsLegacyMigration(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Storage:  config.Storage{Dir: root, TempDir: t.TempDir()},
		Magma:    config.Magma{URL: "https://example.com/legacy", Cookie: "sid=config"},
		Schedule: "* * * * *",
	}
	for range 2 { // Retry a startup interrupted before registry creation.
		creds, err := sharedCredentials(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if cookie, _ := creds.Cookie(); cookie != cfg.Magma.Cookie {
			t.Fatalf("shared cookie = %q", cookie)
		}
		if _, err := os.Stat(board.RegistryPath(root)); !os.IsNotExist(err) {
			t.Fatalf("cookie migration created registry: %v", err)
		}
	}
	creds, err := sharedCredentials(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := board.NewManager(board.Options{
		Root: root, TempDir: cfg.Storage.TempDir, LegacyURL: cfg.Magma.URL,
		Magma: cfg.Magma, Schedule: cfg.Schedule, Shared: creds,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop()
	if boards := mgr.List(true); len(boards) != 1 || boards[0].URL != cfg.Magma.URL {
		t.Fatalf("migrated boards = %+v", boards)
	}

	// Subsequent starts keep admin-managed credentials and warn once even if
	// the legacy config values have changed.
	if err := creds.Set("sid=admin"); err != nil {
		t.Fatal(err)
	}
	cfg.Magma.Cookie = "sid=changed-config"
	cfg.Magma.URL = "https://example.com/changed-config"
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	creds, err = sharedCredentials(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cookie, _ := creds.Cookie(); cookie != "sid=admin" {
		t.Fatalf("existing registry replaced shared cookie: %q", cookie)
	}
	if got := logs.String(); strings.Count(got, "WARNING") != 1 || !strings.Contains(got, "magma.url and magma.cookie are ignored") || !strings.Contains(got, "/admin") || !strings.Contains(got, "config.yaml") {
		t.Fatalf("legacy warning = %q", got)
	}
	if err := creds.Clear(); err != nil {
		t.Fatal(err)
	}
	creds, err = sharedCredentials(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cookie, _ := creds.Cookie(); cookie != "" {
		t.Fatalf("existing registry resurrected cleared cookie: %q", cookie)
	}
}

func TestSharedCredentialsLegacyFailureLeavesRegistryAbsent(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Storage: config.Storage{Dir: root},
		Magma:   config.Magma{Cookie: "sid="},
	}
	if _, err := sharedCredentials(cfg); err == nil {
		t.Fatal("invalid legacy cookie did not fail migration")
	}
	if _, err := os.Stat(board.RegistryPath(root)); !os.IsNotExist(err) {
		t.Fatalf("failed cookie migration created registry: %v", err)
	}
	cfg.Magma.Cookie = "sid=fixed"
	creds, err := sharedCredentials(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cookie, _ := creds.Cookie(); cookie != cfg.Magma.Cookie {
		t.Fatalf("retried cookie = %q", cookie)
	}
}

func waitForShutdown(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown operation did not complete")
	}
}

func TestShutdownStartsBothAndWaitsForBoth(t *testing.T) {
	for _, first := range []string{"server", "manager"} {
		t.Run(first+" finishes first", func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			serverStarted, managerStarted := make(chan struct{}), make(chan struct{})
			serverRelease, managerRelease := make(chan struct{}), make(chan struct{})
			serverFinished, managerFinished := make(chan struct{}), make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(done)
				shutdown(ctx, func(got context.Context) error {
					if got != ctx {
						t.Error("server did not receive the shared shutdown context")
					}
					close(serverStarted)
					select {
					case <-serverRelease:
					case <-ctx.Done():
					}
					close(serverFinished)
					return nil
				}, func() {
					close(managerStarted)
					select {
					case <-managerRelease:
					case <-ctx.Done():
					}
					close(managerFinished)
				})
			}()
			waitForShutdown(t, serverStarted)
			waitForShutdown(t, managerStarted)
			if first == "server" {
				close(serverRelease)
				waitForShutdown(t, serverFinished)
			} else {
				close(managerRelease)
				waitForShutdown(t, managerFinished)
			}
			select {
			case <-done:
				t.Fatal("shutdown returned before both operations finished")
			default:
			}
			if first == "server" {
				close(managerRelease)
			} else {
				close(serverRelease)
			}
			waitForShutdown(t, done)
		})
	}
}

func TestShutdownDeadlineBoundsBothOperations(t *testing.T) {
	for _, blocked := range []string{"server", "manager", "both"} {
		t.Run(blocked, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			release := make(chan struct{})
			defer close(release)
			serverStarted, managerStarted := make(chan struct{}), make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(done)
				shutdown(ctx, func(context.Context) error {
					close(serverStarted)
					if blocked != "manager" {
						<-release
					}
					return nil
				}, func() {
					close(managerStarted)
					if blocked != "server" {
						<-release
					}
				})
			}()
			waitForShutdown(t, serverStarted)
			waitForShutdown(t, managerStarted)
			waitForShutdown(t, done)
			if ctx.Err() != context.DeadlineExceeded {
				t.Fatalf("shutdown returned before deadline: %v", ctx.Err())
			}
		})
	}
}

func TestProbeAddr(t *testing.T) {
	for listen, want := range map[string]string{
		":8080":          "127.0.0.1:8080",
		"0.0.0.0:8080":   "127.0.0.1:8080",
		"[::]:8080":      "[::1]:8080",
		"127.0.0.1:9000": "127.0.0.1:9000",
		"10.0.0.5:8080":  "10.0.0.5:8080",
		"[::1]:8080":     "[::1]:8080",
	} {
		if got := probeAddr(listen); got != want {
			t.Errorf("probeAddr(%q) = %q, want %q", listen, got, want)
		}
	}
}
