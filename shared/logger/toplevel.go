package logger

import (
	"fmt"
)

// Trace logs a message (with optional context) at the TRACE log level.
func Trace(msg string, ctx ...Ctx) {
	Log.Trace(msg, ctx...)
}

// Debug logs a message (with optional context) at the DEBUG log level.
func Debug(msg string, ctx ...Ctx) {
	Log.Debug(msg, ctx...)
}

// Info logs a message (with optional context) at the INFO log level.
func Info(msg string, ctx ...Ctx) {
	Log.Info(msg, ctx...)
}

// Warn logs a message (with optional context) at the WARNING log level.
func Warn(msg string, ctx ...Ctx) {
	Log.Warn(msg, ctx...)
}

// Error logs a message (with optional context) at the ERROR log level.
func Error(msg string, ctx ...Ctx) {
	Log.Error(msg, ctx...)
}

// Panic logs a message (with optional context) at the PANIC log level.
func Panic(msg string, ctx ...Ctx) {
	Log.Panic(msg, ctx...)
}

// Tracef logs at the TRACE log level using a standard printf format string.
func Tracef(format string, args ...any) {
	Log.Trace(fmt.Sprintf(format, args...))
}

// Debugf logs at the DEBUG log level using a standard printf format string.
func Debugf(format string, args ...any) {
	Log.Debug(fmt.Sprintf(format, args...))
}

// Infof logs at the INFO log level using a standard printf format string.
func Infof(format string, args ...any) {
	Log.Info(fmt.Sprintf(format, args...))
}

// Warnf logs at the WARNING log level using a standard printf format string.
func Warnf(format string, args ...any) {
	Log.Warn(fmt.Sprintf(format, args...))
}

// Errorf logs at the ERROR log level using a standard printf format string.
func Errorf(format string, args ...any) {
	Log.Error(fmt.Sprintf(format, args...))
}

// Panicf logs at the PANIC log level using a standard printf format string.
func Panicf(format string, args ...any) {
	Log.Panic(fmt.Sprintf(format, args...))
}

// AddContext returns a new logger with the context added.
func AddContext(ctx Ctx) Logger {
	return Log.AddContext(ctx)
}
