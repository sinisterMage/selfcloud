// Package log wraps log/slog with selfcloud defaults: structured JSON in
// production, human readable text in dev.
package log

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

var (
	mu      sync.RWMutex
	current = newDefault()
)

func newDefault() *slog.Logger {
	level := slog.LevelInfo
	if v := os.Getenv("SELFCLOUD_LOG_LEVEL"); v != "" {
		switch strings.ToLower(v) {
		case "debug":
			level = slog.LevelDebug
		case "info":
			level = slog.LevelInfo
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		}
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// Configure replaces the global logger. Pass dev=true for human readable
// output. Output writer defaults to stderr when nil.
func Configure(dev bool, level string, out io.Writer) {
	if out == nil {
		out = os.Stderr
	}
	lvl := slog.LevelInfo
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	var h slog.Handler
	if dev {
		h = slog.NewTextHandler(out, &slog.HandlerOptions{Level: lvl})
	} else {
		h = slog.NewJSONHandler(out, &slog.HandlerOptions{Level: lvl})
	}
	mu.Lock()
	current = slog.New(h)
	mu.Unlock()
	slog.SetDefault(current)
}

// L returns the active logger.
func L() *slog.Logger {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// With returns a logger annotated with the given attributes.
func With(args ...any) *slog.Logger {
	return L().With(args...)
}

// FromContext extracts a logger from ctx, falling back to the default.
type ctxKey struct{}

func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return L()
}
