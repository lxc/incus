package logging

import (
	"encoding/json"
	"fmt"
	"log/syslog"
	"strings"

	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/api"
)

var facilityMap = map[string]syslog.Priority{
	"kern":     syslog.LOG_KERN,
	"user":     syslog.LOG_USER,
	"mail":     syslog.LOG_MAIL,
	"daemon":   syslog.LOG_DAEMON,
	"auth":     syslog.LOG_AUTH,
	"syslog":   syslog.LOG_SYSLOG,
	"lpr":      syslog.LOG_LPR,
	"news":     syslog.LOG_NEWS,
	"uucp":     syslog.LOG_UUCP,
	"cron":     syslog.LOG_CRON,
	"authpriv": syslog.LOG_AUTHPRIV,
	"ftp":      syslog.LOG_FTP,
}

// SyslogLogger represents a syslog logger.
type SyslogLogger struct {
	common
	address  string
	facility syslog.Priority
	network  string
	tag      string
	writer   *syslog.Writer
}

// NewSyslogLogger instantiates a new syslog logger.
func NewSyslogLogger(s *state.State, name string) (*SyslogLogger, error) {
	addr, facility := s.GlobalConfig.LoggingConfigForSyslog(name)
	network, address := parseAddress(addr)

	return &SyslogLogger{
		common:   newCommonLogger(name, s.GlobalConfig),
		address:  address,
		facility: parseFacility(facility),
		network:  network,
		tag:      "incus",
	}, nil
}

func (c *SyslogLogger) write(event api.Event) error {
	msg := fmt.Sprintf("type: %s log: %s", event.Type, string(event.Metadata))
	lvl := "info"

	if event.Type == api.EventTypeLogging {
		logEvent := api.EventLogging{}

		err := json.Unmarshal(event.Metadata, &logEvent)
		if err != nil {
			return err
		}

		lvl = logEvent.Level
	}

	switch strings.ToLower(lvl) {
	case "panic":
		return c.writer.Err(msg)
	case "fatal":
		return c.writer.Err(msg)
	case "error":
		return c.writer.Err(msg)
	case "warn", "warning":
		return c.writer.Warning(msg)
	case "trace":
		return c.writer.Warning(msg)
	case "info":
		return c.writer.Info(msg)
	case "debug":
		return c.writer.Debug(msg)
	}

	return nil
}

// HandleEvent handles the event received from the internal event listener.
func (c *SyslogLogger) HandleEvent(event api.Event) {
	if !c.processEvent(event) {
		return
	}

	_ = c.write(event)
}

// Start starts the syslog logger.
func (c *SyslogLogger) Start() error {
	writer, err := syslog.Dial(c.network, c.address, c.facility, c.tag)
	if err != nil {
		return err
	}

	c.writer = writer
	return nil
}

// Stop cleans up the syslog logger.
func (c *SyslogLogger) Stop() {
	if c.writer != nil {
		_ = c.writer.Close()
	}
}

// Validate checks whether the logger configuration is correct.
func (c *SyslogLogger) Validate() error {
	if c.address == "" {
		return fmt.Errorf("%s: Address cannot be empty", c.name)
	}

	return nil
}

// parseAddress parses a syslog address into two parts: protocol and the address itself.
func parseAddress(address string) (string, string) {
	protocol := "udp"
	port := "514"

	if strings.Contains(address, "://") {
		parts := strings.SplitN(address, "://", 2)
		protocol = parts[0]
		address = parts[1]
	}

	if !strings.Contains(address, ":") {
		address = fmt.Sprintf("%s:%s", address, port)
	}

	return protocol, address
}

// parseFacility parses a string into a syslog.Priority.
func parseFacility(facility string) syslog.Priority {
	facility = strings.ToLower(strings.TrimSpace(facility))

	val, ok := facilityMap[facility]
	if ok {
		return val
	}

	return syslog.LOG_DAEMON
}
