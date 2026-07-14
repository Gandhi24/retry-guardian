package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"retry-guardian/internal/config"
	"retry-guardian/internal/rules"
	"retry-guardian/internal/server"
	"retry-guardian/internal/store"

	"github.com/redis/go-redis/v9"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := config.Load("config.toml")
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	table, err := rules.Load(cfg.Rules.FilePath)
	if err != nil {
		slog.Error("failed to load rules", "error", err)
		os.Exit(1)
	}
	slog.Info("rules loaded",
		"version", table.Version,
		"mac_rules", len(table.MACRules),
		"network_code_entries", len(table.NetworkCodeIndex),
	)

	rdb := redis.NewClient(&redis.Options{
		Addr:         cfg.Redis.Addr,
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.DB,
		DialTimeout:  cfg.Redis.DialTimeout,
		ReadTimeout:  cfg.Redis.ReadTimeout,
		WriteTimeout: cfg.Redis.WriteTimeout,
	})

	st := store.New(rdb)
	if err := st.Ping(context.Background()); err != nil {
		slog.Error("redis ping failed", "addr", cfg.Redis.Addr, "error", err)
		os.Exit(1)
	}
	slog.Info("redis connected", "addr", cfg.Redis.Addr)

	srv := server.New(cfg.Server, st, table)

	go func() {
		slog.Info("server starting", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
	}
	if err := rdb.Close(); err != nil {
		slog.Error("redis close failed", "error", err)
	}
	slog.Info("server stopped")
}
