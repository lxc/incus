package logger

import (
	"context"
	"log/slog"
	"maps"
)

type (
	// SlogLevel is a convenience for slog.Level.
	SlogLevel = slog.Level

	// SlogFunc represents the logging function.
	SlogFunc func(lvl SlogLevel, msg string, ctx Ctx, attrs Ctx)
)

const (
	// SlogWarn is a convenience for slog.LevelWarn.
	SlogWarn SlogLevel = slog.LevelWarn

	// SlogError is a convenience for SlogLevelError.
	SlogError SlogLevel = slog.LevelError

	// SlogDebug is a convenience for SlogLevelDebug.
	SlogDebug SlogLevel = slog.LevelDebug

	// SlogInfo is a convenience for SlogLevelInfo.
	SlogInfo SlogLevel = slog.LevelInfo
)

// NewSlogHandler returns a slog.Handler implementation that forwards to our own logger.
func NewSlogHandler(target SlogFunc) slog.Handler {
	return &slogHandler{attrs: []slog.Attr{}, target: target}
}

// DefaultSlogLogger is the default wrapper for slog parameters to our own logger.
func DefaultSlogLogger(prefix string) SlogFunc {
	return func(lvl SlogLevel, msg string, ctx Ctx, attrs Ctx) {
		maps.Copy(ctx, attrs)

		if prefix != "" {
			msg = prefix + " " + msg
		}

		switch lvl {
		case SlogInfo:
			Info(msg, ctx)
		case SlogWarn:
			Warn(msg, ctx)
		case SlogError:
			Error(msg, ctx)
		default:
			Debug(msg, ctx)
		}
	}
}

type slogHandler struct {
	target SlogFunc
	attrs  []slog.Attr
}

// Enabled checks whether a given log level is supported.
func (s *slogHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return true
}

// Handle processes an actual log message.
func (s *slogHandler) Handle(ctx context.Context, rec slog.Record) error {
	logCtx := Ctx{}

	for _, attr := range s.attrs {
		logCtx[attr.Key] = attr.Value
	}

	attrCtx := Ctx{}

	rec.Attrs(func(a slog.Attr) bool {
		attrCtx[a.Key] = a.Value.Any()
		return true
	})

	s.target(rec.Level, rec.Message, logCtx, attrCtx)

	return nil
}

// WithAttrs creates a sub-logger with some specific attributes set.
func (s *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	sub := &slogHandler{
		attrs:  append(s.attrs, attrs...),
		target: s.target,
	}

	return sub
}

// WithGroup creates a sub-logger with a specific group set.
func (s *slogHandler) WithGroup(name string) slog.Handler {
	// Ignore grouping for now.

	return s
}
