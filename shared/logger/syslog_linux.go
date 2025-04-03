//go:build linux

package logger

import (
	"log/syslog"

	"github.com/sirupsen/logrus"
	lSyslog "github.com/sirupsen/logrus/hooks/syslog"
)

type syslogHandler struct {
	handler logrus.Hook
}

// Fire sends a logging entry through syslog.
func (h syslogHandler) Fire(entry *logrus.Entry) error {
	return h.handler.Fire(entry)
}

// Levels returns the list of supported log levels for syslog.
func (h syslogHandler) Levels() []logrus.Level {
	return []logrus.Level{
		logrus.PanicLevel,
		logrus.FatalLevel,
		logrus.ErrorLevel,
		logrus.WarnLevel,
		logrus.InfoLevel,
	}
}

func setupSyslog(logger *logrus.Logger, syslogName string) error {
	syslogHook, err := lSyslog.NewSyslogHook("", "", syslog.LOG_INFO, syslogName)
	if err != nil {
		return err
	}

	logger.AddHook(syslogHandler{syslogHook})
	return nil
}
