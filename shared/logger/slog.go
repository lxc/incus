package logger

import (
	"context"
	"log/slog"
	"slices"
)

// NewSlogHandler returns a slog.Handler implementation that forwards to our own logger.
func NewSlogHandler(prefix string, target func(msg string, ctx ...Ctx)) slog.Handler {
	return &slogHandler{attrs: []slog.Attr{}, prefix: prefix, target: target}
}

type slogHandler struct {
	prefix string
	target func(msg string, ctx ...Ctx)
	attrs  []slog.Attr
}

// Enabled checks whether a given log level is supported.
func (s *slogHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return true
}

// Handle processes an actual log message.
func (s *slogHandler) Handle(ctx context.Context, rec slog.Record) error {
	logCtx := Ctx{}

	if slices.Contains([]slog.Level{slog.LevelDebug, slog.LevelInfo}, rec.Level) {
		return nil
	}

	for _, attr := range s.attrs {
		logCtx[attr.Key] = attr.Value
	}

	s.target(s.prefix+" "+rec.Message, logCtx)

	return nil
}

// WithAttrs creates a sub-logger with some specific attributes set.
func (s *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	sub := &slogHandler{
		attrs:  append(s.attrs, attrs...),
		prefix: s.prefix,
		target: s.target,
	}

	return sub
}

// WithGroup creates a sub-logger with a specific group set.
func (s *slogHandler) WithGroup(name string) slog.Handler {
	// Ignore grouping for now.

	return s
}
