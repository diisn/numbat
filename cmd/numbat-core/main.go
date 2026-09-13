package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/youngyangyang04/numbat/internal/app"
	"github.com/youngyangyang04/numbat/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLevel(cfg.LogLevel),
	}))
	slog.SetDefault(logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	a := app.New(cfg)
	if err := a.Run(ctx); err != nil {
		slog.Error("app run failed", "error", err)
		os.Exit(1)
	}
}

func parseLevel(level string) slog.Level {
	switch level {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
