package logger

import (
	"github.com/sirupsen/logrus"
)

// ctxLogger returns a logger target with all provided ctx applied.
func (lw *logWrapper) ctxLogger(ctx ...Ctx) targetLogger {
	logger := lw.target
	for _, c := range ctx {
		logger = logger.WithFields(logrus.Fields(c))
	}

	return logger
}

func newWrapper(target targetLogger) Logger {
	return &logWrapper{target}
}

type logWrapper struct {
	target targetLogger
}

func (lw *logWrapper) Panic(msg string, ctx ...Ctx) {
	lw.ctxLogger(ctx...).Panic(msg)
}

func (lw *logWrapper) Fatal(msg string, ctx ...Ctx) {
	lw.ctxLogger(ctx...).Fatal(msg)
}

func (lw *logWrapper) Error(msg string, ctx ...Ctx) {
	lw.ctxLogger(ctx...).Error(msg)
}

func (lw *logWrapper) Warn(msg string, ctx ...Ctx) {
	lw.ctxLogger(ctx...).Warn(msg)
}

func (lw *logWrapper) Info(msg string, ctx ...Ctx) {
	lw.ctxLogger(ctx...).Info(msg)
}

func (lw *logWrapper) Debug(msg string, ctx ...Ctx) {
	lw.ctxLogger(ctx...).Debug(msg)
}

func (lw *logWrapper) Trace(msg string, ctx ...Ctx) {
	lw.ctxLogger(ctx...).Trace(msg)
}

func (lw *logWrapper) AddContext(ctx Ctx) Logger {
	return &logWrapper{lw.ctxLogger(ctx)}
}
