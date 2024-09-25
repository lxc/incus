//go:build !linux

package logger

import (
	"fmt"

	"github.com/sirupsen/logrus"
)

func setupSyslog(logger *logrus.Logger, syslogName string) error {
	return fmt.Errorf("Syslog logging isn't supported on this platform")
}
