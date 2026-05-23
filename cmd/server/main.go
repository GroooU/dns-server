package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dns-forwarder/config"
	"dns-forwarder/internal/cache"
	"dns-forwarder/internal/forwarder"
	"dns-forwarder/internal/metrics"
	"dns-forwarder/internal/server"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	log, err := buildLogger(cfg.Log.Level)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to init logger: %v\n", err)
		os.Exit(1)
	}
	defer log.Sync()

	c := cache.New(cfg.Cache.MaxSize, cfg.Cache.MinTTL, cfg.Cache.MaxTTL)

	done := make(chan struct{})
	c.Start(done)

	f, err := forwarder.New(cfg.Upstreams, 5*time.Second)
	if err != nil {
		log.Fatal("forwarder init failed", zap.Error(err))
	}

	h := server.NewHandler(c, f, log)
	srv := server.New(cfg.Listen, h, log)

	if err := srv.Start(); err != nil {
		log.Fatal("server start failed", zap.Error(err))
	}

	metricsSrv := metrics.NewServer(cfg.Metrics.Listen, c, log)
	if err := metricsSrv.Start(); err != nil {
		log.Fatal("metrics server start failed", zap.Error(err))
	}

	log.Info("dns-forwarder ready",
		zap.String("listen", cfg.Listen),
		zap.Strings("upstreams", cfg.Upstreams),
	)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down...")
	close(done)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Error("shutdown error", zap.Error(err))
	}
	if err := metricsSrv.Shutdown(ctx); err != nil {
		log.Error("metrics shutdown error", zap.Error(err))
	}

	stats := c.Stats()
	log.Info("cache stats on exit",
		zap.Int("size", stats.Size),
		zap.Uint64("hits", stats.Hits),
		zap.Uint64("misses", stats.Misses),
	)

	log.Info("bye")
}

func buildLogger(level string) (*zap.Logger, error) {
	var lvl zapcore.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = zapcore.InfoLevel
	}
	zapCfg := zap.NewProductionConfig()
	zapCfg.Level = zap.NewAtomicLevelAt(lvl)
	return zapCfg.Build()
}
