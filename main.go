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
	"syscall"
	"time"
	_ "time/tzdata" // the scratch image has no zoneinfo; honour TZ anyway

	"github.com/robfig/cron/v3"

	"magma-viewer/internal/config"
	"magma-viewer/internal/fetcher"
	"magma-viewer/internal/store"
	"magma-viewer/internal/web"
)

//go:embed web
var webFS embed.FS

func main() {
	defaultConfig := os.Getenv("CONFIG")
	if defaultConfig == "" {
		defaultConfig = "/config/config.yaml"
	}
	configPath := flag.String("config", defaultConfig, "path to config.yaml (env CONFIG)")
	flag.Parse()

	if flag.Arg(0) == "healthcheck" {
		os.Exit(healthcheck(*configPath))
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
	log.Printf("storage %s: %d images", cfg.Storage.Dir, st.Count())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	f := fetcher.New(cfg.Magma, cfg.Storage.TempDir, st)
	c := cron.New(cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)))
	if _, err := c.AddFunc(cfg.Schedule, func() { f.RunScheduled(ctx) }); err != nil {
		return err
	}
	c.Start()
	defer c.Stop()
	go f.RunScheduled(ctx)
	log.Printf("fetch schedule: %q", cfg.Schedule)

	go st.Watch(ctx.Done(), time.Minute)
	go st.Warm(ctx.Done())

	static, err := fs.Sub(webFS, "web")
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           web.New(cfg, st, f, static).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	log.Printf("listening on %s", cfg.Listen)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// healthcheck lets the scratch image run a Docker HEALTHCHECK without curl.
func healthcheck(configPath string) int {
	port := "8080"
	if cfg, err := config.Load(configPath); err == nil {
		if _, p, err := net.SplitHostPort(cfg.Listen); err == nil && p != "" {
			port = p
		}
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
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
