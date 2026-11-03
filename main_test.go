package main

import (
	"context"
	"testing"
	"time"
)

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
