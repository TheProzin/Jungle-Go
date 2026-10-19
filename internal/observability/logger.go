package observability

import (
	"log/slog"
	"os"
	"strings"

	"github.com/TheProzin/Jungle-Go/internal/config"
)

func NewLogger(cfg *config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	logger := slog.New(handler).With("service", "jungle-go", "env", cfg.Env)
	slog.SetDefault(logger)
	return logger
}
