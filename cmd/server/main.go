// Command server runs the Viewer-Counter HTTP service.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"viewer-counter/internal/auth"
	"viewer-counter/internal/config"
	"viewer-counter/internal/counter"
	"viewer-counter/internal/dedup"
	"viewer-counter/internal/httpapi"
	"viewer-counter/internal/privacy"
	"viewer-counter/internal/ratelimit"
	"viewer-counter/internal/store"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("load config", "err", err)
		os.Exit(1)
	}

	st, err := store.Open(cfg.DB)
	if err != nil {
		logger.Error("open store", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	if err := st.Migrate(context.Background()); err != nil {
		logger.Error("migrate", "err", err)
		os.Exit(1)
	}

	totals, err := st.LoadCounters(context.Background())
	if err != nil {
		logger.Error("load counters", "err", err)
		os.Exit(1)
	}

	agg := counter.New(st, cfg.Events.Record, cfg.Flush.Interval.Std(), cfg.Flush.Batch, logger)
	agg.LoadTotals(totals)
	agg.Start()

	priv := privacy.New(cfg.Privacy.IPMode, cfg.Privacy.Salt, cfg.Privacy.RecordUA, cfg.Bot.Enabled, cfg.Bot.Keywords)

	var dd *dedup.Dedup
	if cfg.Dedup.Enabled {
		dd = dedup.New(cfg.Dedup.Window.Std())
	}

	var lim *ratelimit.Limiter
	if cfg.RateLimit.Enabled {
		lim = ratelimit.New(cfg.RateLimit.RPS, cfg.RateLimit.Burst)
	}

	authn := auth.New(cfg.Auth.AdminTokens)
	if !authn.Enabled() {
		logger.Warn("no admin tokens configured; admin endpoints will return 503")
	}

	srv := httpapi.NewServer(cfg, agg, st, priv, dd, authn, lim, logger)
	httpServer := &http.Server{
		Addr:         cfg.Server.Addr,
		Handler:      srv.Handler(),
		ReadTimeout:  cfg.Server.ReadTimeout.Std(),
		WriteTimeout: cfg.Server.WriteTimeout.Std(),
		IdleTimeout:  cfg.Server.IdleTimeout.Std(),
	}

	go func() {
		logger.Info("server starting", "addr", cfg.Server.Addr,
			"events_recording", cfg.Events.Record, "ip_mode", cfg.Privacy.IPMode)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown", "err", err)
	}
	agg.Stop() // drain and final flush
	if dd != nil {
		dd.Stop()
	}
	if lim != nil {
		lim.Stop()
	}
	logger.Info("stopped")
}
