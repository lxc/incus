//go:build !linux

package logger

import (
	"errors"

	"github.com/sirupsen/logrus"
)

func setupSyslog(logger *logrus.Logger, syslogName string) error {
	return errors.New("Syslog logging isn't supported on this platform")
}
