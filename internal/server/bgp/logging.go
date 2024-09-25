package bgp

import (
	"github.com/osrg/gobgp/v3/pkg/log"

	"github.com/lxc/incus/v6/internal/server/daemon"
	"github.com/lxc/incus/v6/shared/logger"
)

type logWrapper struct {
	logger logger.Logger
}

func (l *logWrapper) Panic(msg string, fields log.Fields) {
	l.logger.Panic(msg, logger.Ctx(fields))
}

func (l *logWrapper) Fatal(msg string, fields log.Fields) {
	l.logger.Fatal(msg, logger.Ctx(fields))
}

func (l *logWrapper) Error(msg string, fields log.Fields) {
	l.logger.Error(msg, logger.Ctx(fields))
}

func (l *logWrapper) Warn(msg string, fields log.Fields) {
	l.logger.Warn(msg, logger.Ctx(fields))
}

func (l *logWrapper) Info(msg string, fields log.Fields) {
	l.logger.Info(msg, logger.Ctx(fields))
}

func (l *logWrapper) Debug(msg string, fields log.Fields) {
	l.logger.Debug(msg, logger.Ctx(fields))
}

func (l *logWrapper) SetLevel(level log.LogLevel) {
}

func (l *logWrapper) GetLevel() log.LogLevel {
	if daemon.Debug {
		return log.DebugLevel
	} else if daemon.Verbose {
		return log.InfoLevel
	} else {
		return log.WarnLevel
	}
}
