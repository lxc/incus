package events

import (
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/lxc/incus/v6/shared/api"
)

// LoggingServer controls what server to use for messages coming from the logger.
var LoggingServer *Server

// Handler describes an event handler.
type Handler struct {
}

// NewEventHandler creates and returns a new event handler.
func NewEventHandler() logrus.Hook {
	return &Handler{}
}

// Fire sends a new logging event.
func (h Handler) Fire(entry *logrus.Entry) error {
	if LoggingServer == nil {
		return nil
	}

	return LoggingServer.Send("", api.EventTypeLogging, api.EventLogging{
		Message: entry.Message,
		Level:   entry.Level.String(),
		Context: logContextMap(entry.Data),
	})
}

// Levels returns the list of supported log levels.
func (h Handler) Levels() []logrus.Level {
	return logrus.AllLevels
}

func logContextMap(ctx logrus.Fields) map[string]string {
	ctxMap := map[string]string{}

	for k, v := range ctx {
		ctxMap[k] = fmt.Sprintf("%v", v)
	}

	return ctxMap
}
