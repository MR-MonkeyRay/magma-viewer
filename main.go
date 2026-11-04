package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // the scratch image has no zoneinfo; honour TZ anyway

	"magma-viewer/internal/board"
	"magma-viewer/internal/config"
	"magma-viewer/internal/fetcher"
	"magma-viewer/internal/notice"
	"magma-viewer/internal/notify"
	"magma-viewer/internal/web"
)

//go:embed web
var webFS embed.FS

//go:embed VERSION
var version string

// stopBudget is how long shutdown may wait; it stays under Docker's default
// 10s stop grace period and the systemd unit's TimeoutStopSec=15s.
const stopBudget = 8 * time.Second

func main() {
	defaultConfig := os.Getenv("CONFIG")
	if defaultConfig == "" {
		defaultConfig = "/config/config.yaml"
	}
	configPath := flag.String("config", defaultConfig, "path to config.yaml (env CONFIG)")
	flag.Parse()

	switch flag.Arg(0) {
	case "healthcheck":
		os.Exit(healthcheck(*configPath))
	case "checkconfig":
		if _, err := config.Load(*configPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("config ok")
		return
	}
	if err := run(*configPath); err != nil {
		log.Fatal(err)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.Storage.TempDir, 0o755); err != nil {
		return err
	}
	log.Printf("magma-viewer %s", strings.TrimSpace(version))
	if cfg.Admin.SessionSecret == "" {
		log.Printf("WARNING admin.session_secret is empty: a random key is used and every restart logs the admin out")
	}

	creds, err := sharedCredentials(cfg)
	if err != nil {
		return err
	}
	n := notify.New(cfg.Notify)
	var alert func(event, message string)
	if n != nil {
		alert = n.Send
	}
	mgr, err := board.NewManager(board.Options{
		Root:      cfg.Storage.Dir,
		LegacyURL: cfg.Magma.URL,
		TempDir:   cfg.Storage.TempDir,
		Magma:     cfg.Magma,
		Schedule:  cfg.Schedule,
		Shared:    creds,
		Alert:     alert,
	})
	if err != nil {
		return fmt.Errorf("open boards: %w", err)
	}
	for _, b := range mgr.List(true) {
		log.Printf("board %q (%s): %d images", b.Name, b.ID, b.Count)
	}

	ns, err := notice.Open(filepath.Join(cfg.Storage.Dir, ".state", "notices.json"))
	if err != nil {
		return fmt.Errorf("open notices: %w", err)
	}

	static, err := fs.Sub(webFS, "web")
	if err != nil {
		return err
	}
	ws := web.New(cfg, mgr, creds, ns, static)
	ws.Version = strings.TrimSpace(version)
	ws.Notifier = n

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	mgr.Start(ctx)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           ws.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	// ListenAndServe returns as soon as Shutdown begins, so wait here for
	// in-flight downloads and for fetches (which see ctx cancelled) to finish
	// writing, all within one deadline.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		<-ctx.Done()
		stopCtx, cancel := context.WithTimeout(context.Background(), stopBudget)
		defer cancel()
		shutdown(stopCtx, srv.Shutdown, mgr.Stop)
	}()
	log.Printf("listening on %s", cfg.Listen)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-drained
	return nil
}

// sharedCredentials migrates the legacy cookie before boards.json commits the
// upgrade, so a failed migration or interrupted startup can be retried.
func sharedCredentials(cfg *config.Config) (*fetcher.Credentials, error) {
	_, err := os.Lstat(board.RegistryPath(cfg.Storage.Dir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("check board registry: %w", err)
	}
	creds := fetcher.NewCredentials(filepath.Join(cfg.Storage.Dir, ".state", "cookie.json"))
	if err == nil {
		if cfg.Magma.URL != "" || cfg.Magma.Cookie != "" {
			log.Printf("WARNING magma.url and magma.cookie are ignored: boards and cookies are managed in /admin; remove these legacy keys from config.yaml")
		}
	} else if err := creds.MigrateLegacy(cfg.Magma.Cookie); err != nil {
		return nil, fmt.Errorf("migrate legacy cookie: %w", err)
	}
	return creds, nil
}

// shutdown drains HTTP requests and board work concurrently within one deadline.
func shutdown(ctx context.Context, shutdownServer func(context.Context) error, stop func()) {
	httpDone := make(chan error, 1)
	stopped := make(chan struct{})
	go func() { httpDone <- shutdownServer(ctx) }()
	go func() {
		stop()
		close(stopped)
	}()
	for httpDone != nil || stopped != nil {
		select {
		case err := <-httpDone:
			if err != nil {
				log.Printf("shutdown: %v", err)
			}
			httpDone = nil
		case <-stopped:
			stopped = nil
		case <-ctx.Done():
			if httpDone != nil {
				log.Printf("shutdown: HTTP requests are still running")
			}
			if stopped != nil {
				log.Printf("shutdown: a fetch is still running")
			}
			return
		}
	}
}

// healthcheck lets the scratch image run a Docker HEALTHCHECK without curl.
func healthcheck(configPath string) int {
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + probeAddr(cfg.Listen) + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// probeAddr turns a listen address into one a local client can dial: a
// wildcard host becomes loopback, a specific address is used as is.
func probeAddr(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return net.JoinHostPort(host, port)
}
