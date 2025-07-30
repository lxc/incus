package logging

import (
	"fmt"

	"github.com/lxc/incus/v6/internal/server/events"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/logger"
)

// Controller is responsible for managing a set of loggers.
type Controller struct {
	listener *events.InternalListener
	loggers  map[string]Logger
}

// NewLoggingController instantiates a new LoggerController object.
func NewLoggingController(listener *events.InternalListener) *Controller {
	return &Controller{
		listener: listener,
		loggers:  map[string]Logger{},
	}
}

// AddLogger adds a new logger to the controller.
func (c *Controller) AddLogger(s *state.State, name string, loggerType string) error {
	loggerClient, err := LoggerFromType(s, name, loggerType)
	if err != nil {
		return err
	}

	c.loggers[name] = loggerClient
	c.listener.AddHandler(name, loggerClient.HandleEvent)

	return nil
}

// RemoveLogger removes a logger from the controller.
func (c *Controller) RemoveLogger(name string) {
	loggerClient, ok := c.loggers[name]
	if ok {
		loggerClient.Stop()
		delete(c.loggers, name)
		c.listener.RemoveHandler(name)
	}
}

// Setup is responsible for preparing a new set of loggers.
func (c *Controller) Setup(s *state.State) error {
	loggingConfig, err := s.GlobalConfig.Loggers()
	if err != nil {
		return err
	}

	for loggerName, loggerType := range loggingConfig {
		err = c.AddLogger(s, loggerName, loggerType)
		if err != nil {
			logger.Error("Error creating a logger", logger.Ctx{"err": err})
		}
	}

	return nil
}

// Reconfigure handles the reinitialization of loggers after configuration changes.
func (c *Controller) Reconfigure(s *state.State, config map[string]struct{}) error {
	loggingConfig, err := s.GlobalConfig.Loggers()
	if err != nil {
		return err
	}

	for loggerName := range config {
		c.RemoveLogger(loggerName)

		loggerType, ok := loggingConfig[loggerName]
		if !ok {
			continue
		}

		err = c.AddLogger(s, loggerName, loggerType)
		if err != nil {
			logger.Error("Error creating logger", logger.Ctx{"err": err})
		}
	}

	return nil
}

// Shutdown cleans up loggers.
func (c *Controller) Shutdown() {
	for _, loggerClient := range c.loggers {
		loggerClient.Stop()
	}
}

// LoggerFromType returns a new logger based on its type.
func LoggerFromType(s *state.State, loggerName string, loggerType string) (Logger, error) {
	if loggerType == "" {
		return nil, fmt.Errorf("No type definition for logger %s", loggerName)
	}

	var loggerClient Logger
	var err error

	switch loggerType {
	case "syslog":
		loggerClient, err = NewSyslogLogger(s, loggerName)
	case "loki":
		loggerClient, err = NewLokiLogger(s, loggerName)
	case "webhook":
		loggerClient, err = NewWebhookLogger(s, loggerName)
	default:
		return nil, fmt.Errorf("%s is not supported logger type", loggerType)
	}

	if err != nil {
		return nil, err
	}

	err = loggerClient.Validate()
	if err != nil {
		return nil, err
	}

	err = loggerClient.Start()
	if err != nil {
		return nil, err
	}

	return loggerClient, nil
}
