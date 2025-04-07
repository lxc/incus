package logging

import (
	"encoding/json"

	"github.com/sirupsen/logrus"

	clusterConfig "github.com/lxc/incus/v6/internal/server/cluster/config"
	"github.com/lxc/incus/v6/shared/api"
)

// Logger is an interface that must be implemented by all loggers.
type Logger interface {
	HandleEvent(event api.Event)
	Start() error
	Stop()
	Validate() error
}

// common embeds shared configuration fields for all logger types.
type common struct {
	lifecycleProjects []string
	lifecycleTypes    []string
	loggingLevel      string
	name              string
	types             []string
}

// newCommonLogger instantiates a new common logger.
func newCommonLogger(name string, cfg *clusterConfig.Config) common {
	lifecycleProjects, lifecycleTypes, loggingLevel, types := cfg.LoggingCommonConfig(name)

	return common{
		loggingLevel:      loggingLevel,
		lifecycleProjects: sliceFromString(lifecycleProjects),
		lifecycleTypes:    sliceFromString(lifecycleTypes),
		name:              name,
		types:             sliceFromString(types),
	}
}

// processEvent verifies whether the event should be processed for the specific logger.
func (c *common) processEvent(event api.Event) bool {
	switch event.Type {
	case api.EventTypeLifecycle:
		if !contains(c.types, "lifecycle") {
			return false
		}

		lifecycleEvent := api.EventLifecycle{}

		err := json.Unmarshal(event.Metadata, &lifecycleEvent)
		if err != nil {
			return false
		}

		if lifecycleEvent.Project != "" && len(c.lifecycleProjects) > 0 {
			if !contains(c.lifecycleProjects, lifecycleEvent.Project) {
				return false
			}
		}

		if len(c.lifecycleTypes) > 0 && !hasAnyPrefix(c.lifecycleTypes, lifecycleEvent.Action) {
			return false
		}

		return true
	case api.EventTypeLogging, api.EventTypeNetworkACL:
		if !contains(c.types, "logging") && event.Type == api.EventTypeLogging {
			return false
		}

		if !contains(c.types, "network-acl") && event.Type == api.EventTypeNetworkACL {
			return false
		}

		logEvent := api.EventLogging{}

		err := json.Unmarshal(event.Metadata, &logEvent)
		if err != nil {
			return false
		}

		// The errors can be ignored as the values are validated elsewhere.
		l1, _ := logrus.ParseLevel(logEvent.Level)
		l2, _ := logrus.ParseLevel(c.loggingLevel)

		// Only consider log messages with a certain log level.
		if l2 < l1 {
			return false
		}

		return true
	default:
		return false
	}
}
