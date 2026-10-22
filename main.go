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
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // the scratch image has no zoneinfo; honour TZ anyway

	"github.com/robfig/cron/v3"

	"magma-viewer/internal/config"
	"magma-viewer/internal/fetcher"
	"magma-viewer/internal/notice"
	"magma-viewer/internal/store"
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
	st, err := store.New(cfg.Storage.Dir)
	if err != nil {
		return fmt.Errorf("open storage dir: %w", err)
	}
	log.Printf("magma-viewer %s", strings.TrimSpace(version))
	if cfg.Admin.SessionSecret == "" {
		log.Printf("WARNING admin.session_secret is empty: a random key is used and every restart logs the admin out")
	}
	log.Printf("storage %s: %d images", cfg.Storage.Dir, st.Count())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	f := fetcher.New(cfg.Magma, cfg.Storage.TempDir, st)
	c := cron.New(cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)))
	entry, err := c.AddFunc(cfg.Schedule, func() { f.RunScheduled(ctx) })
	if err != nil {
		return err
	}
	c.Start()
	var startup sync.WaitGroup
	startup.Go(func() { f.RunScheduled(ctx) })
	log.Printf("fetch schedule: %q", cfg.Schedule)

	go st.Watch(ctx.Done(), time.Minute)
	go func() {
		st.Cleanup(cfg.Storage.TempDir)
		st.Warm(ctx.Done())
	}()

	ns, err := notice.Open(filepath.Join(cfg.Storage.Dir, ".state", "notices.json"))
	if err != nil {
		return fmt.Errorf("open notices: %w", err)
	}

	static, err := fs.Sub(webFS, "web")
	if err != nil {
		return err
	}
	ws := web.New(cfg, st, f, ns, static)
	ws.NextRun = func() time.Time { return c.Entry(entry).Next }
	ws.Version = strings.TrimSpace(version)
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
		fetches := make(chan struct{})
		go func() {
			<-c.Stop().Done()
			startup.Wait()
			close(fetches)
		}()
		if err := srv.Shutdown(stopCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		select {
		case <-fetches:
		case <-stopCtx.Done():
			log.Printf("shutdown: a fetch is still running")
		}
	}()
	log.Printf("listening on %s", cfg.Listen)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-drained
	return nil
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
