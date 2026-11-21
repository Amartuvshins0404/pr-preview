package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Amartuvshins0404/pr-preview/internal/build"
	"github.com/Amartuvshins0404/pr-preview/internal/config"
	"github.com/Amartuvshins0404/pr-preview/internal/github"
	"github.com/Amartuvshins0404/pr-preview/internal/manager"
	"github.com/Amartuvshins0404/pr-preview/internal/state"
	"github.com/Amartuvshins0404/pr-preview/internal/web"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(); err != nil {
		slog.Error("exit", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "previews"), 0o755); err != nil {
		return err
	}
	store, hadState, err := state.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	m := manager.New(cfg, store, hadState, github.New(cfg.GitHubToken), build.New(cfg))
	srv, err := web.New(cfg, m)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	httpSrv := &http.Server{Addr: cfg.Listen, Handler: srv, ReadHeaderTimeout: 15 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- httpSrv.ListenAndServe() }()
	slog.Info("started", "listen", cfg.Listen, "domain", cfg.BaseDomain, "dataDir", cfg.DataDir, "firstRun", !hadState,
		"maxRunning", cfg.MaxRunning, "buildConcurrency", cfg.BuildConcurrency)
	done := make(chan struct{})
	go func() {
		m.Run(ctx)
		close(done)
	}()
	select {
	case err := <-errc:
		stop()
		<-done
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = httpSrv.Shutdown(sctx)
	<-done
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
