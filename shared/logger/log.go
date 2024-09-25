package logger

import (
	"io"
	"os"

	"github.com/sirupsen/logrus"
	lWriter "github.com/sirupsen/logrus/hooks/writer"

	"github.com/lxc/incus/v6/shared/termios"
)

// Setup a basic empty logger on init.
func init() {
	logger := logrus.New()
	logger.SetOutput(io.Discard)

	Log = newWrapper(logger)
}

// InitLogger intializes a full logging instance.
func InitLogger(filepath string, syslogName string, verbose bool, debug bool, hook logrus.Hook) error {
	logger := logrus.New()
	logger.Level = logrus.DebugLevel
	logger.SetOutput(io.Discard)

	// Setup the formatter.
	logger.Formatter = &logrus.TextFormatter{PadLevelText: true, FullTimestamp: true, ForceColors: termios.IsTerminal(int(os.Stderr.Fd()))}

	// Setup log level.
	levels := []logrus.Level{logrus.PanicLevel, logrus.FatalLevel, logrus.ErrorLevel, logrus.WarnLevel}
	if debug {
		levels = append(levels, logrus.InfoLevel, logrus.DebugLevel)
	} else if verbose {
		levels = append(levels, logrus.InfoLevel)
	}

	// Setup writers.
	writers := []io.Writer{os.Stderr}

	if filepath != "" {
		f, err := os.OpenFile(filepath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			return err
		}

		writers = append(writers, f)
	}

	logger.AddHook(&lWriter.Hook{
		Writer:    io.MultiWriter(writers...),
		LogLevels: levels,
	})

	// Setup syslog.
	if syslogName != "" {
		err := setupSyslog(logger, syslogName)
		if err != nil {
			return err
		}
	}

	// Add hooks.
	if hook != nil {
		logger.AddHook(hook)
	}

	// Set the logger.
	Log = newWrapper(logger)

	return nil
}
