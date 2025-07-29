package config

import (
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/lxc/incus/v6/shared/validate"
)

// IsLoggingConfig reports whether the config key is for a logging configuration.
func IsLoggingConfig(key string) bool {
	return strings.HasPrefix(key, "logging.")
}

// GetLoggingRuleForKey returns the rule for the specified logging config key.
func GetLoggingRuleForKey(key string) (Key, error) {
	fields := strings.Split(key, ".")
	if len(fields) < 3 {
		return Key{}, fmt.Errorf("%s is not a valid logging config key", key)
	}

	loggingKey := strings.Join(fields[2:], ".")

	switch loggingKey {
	case "target.address":
		// gendoc:generate(entity=server, group=logging, key=logging.NAME.target.address)
		// Specify the protocol, name or IP and port. For example `tcp://syslog01.int.example.net:514`.
		// ---
		//  type: string
		//  scope: global
		//  shortdesc: Address of the logger
		return Key{}, nil
	case "target.username":
		// gendoc:generate(entity=server, group=logging, key=logging.NAME.target.username)
		//
		// ---
		//  type: string
		//  scope: global
		//  shortdesc: User name used for authentication
		return Key{}, nil
	case "target.password":
		// gendoc:generate(entity=server, group=logging, key=logging.NAME.target.password)
		//
		// ---
		//  type: string
		//  scope: global
		//  shortdesc: Password used for authentication
		return Key{}, nil
	case "target.ca_cert":
		// gendoc:generate(entity=server, group=logging, key=logging.NAME.target.ca_cert)
		//
		// ---
		//  type: string
		//  scope: global
		//  shortdesc: CA certificate for the server
		return Key{}, nil
	case "target.instance":
		// gendoc:generate(entity=server, group=logging, key=logging.NAME.target.instance)
		// This allows replacing the default instance value (server host name) by a more relevant value like a cluster identifier.
		// ---
		//  type: string
		//  scope: global
		//  defaultdesc: Local server host name or cluster member name
		//  shortdesc: Name to use as the instance field in Loki events.
		return Key{}, nil
	case "target.labels":
		// gendoc:generate(entity=server, group=logging, key=logging.NAME.target.labels)
		// Specify a comma-separated list of values that should be used as labels for a Loki log entry.
		// ---
		//  type: string
		//  scope: global
		//  shortdesc: Labels for a Loki log entry
		return Key{}, nil
	case "target.facility":
		// gendoc:generate(entity=server, group=logging, key=logging.NAME.target.facility)
		//
		// ---
		//  type: string
		//  scope: global
		//  shortdesc: The syslog facility defines the category of the log message
		return Key{Default: "daemon"}, nil
	case "target.type":
		// gendoc:generate(entity=server, group=logging, key=logging.NAME.target.type)
		//
		// ---
		//  type: string
		//  scope: global
		//  shortdesc: The type of the logger. One of `loki`, `syslog` or `webhook`.
		return Key{Validator: validate.Optional(validate.IsListOf(validate.IsOneOf("syslog", "loki", "webhook")))}, nil
	case "target.retry":
		// gendoc:generate(entity=server, group=logging, key=logging.NAME.target.retry)
		//
		// ---
		//  type: integer
		//  scope: global
		//  shortdesc: number of delivery retries, default 3
		return Key{Validator: validate.Optional(), Default: "3"}, nil
	case "types":
		// gendoc:generate(entity=server, group=logging, key=logging.NAME.types)
		// Specify a comma-separated list of events to send to the logger.
		// The events can be any combination of `lifecycle`, `logging`, and `network-acl`.
		// ---
		//  type: string
		//  scope: global
		//  defaultdesc: `lifecycle,logging`
		//  shortdesc: Events to send to the logger
		return Key{Validator: validate.Optional(validate.IsListOf(validate.IsOneOf("lifecycle", "logging", "network-acl"))), Default: "lifecycle,logging"}, nil
	case "logging.level":
		// gendoc:generate(entity=server, group=logging, key=logging.NAME.logging.level)
		//
		// ---
		//  type: string
		//  scope: global
		//  defaultdesc: `info`
		//  shortdesc: Minimum log level to send to the logger
		return Key{Validator: LogLevelValidator, Default: logrus.InfoLevel.String()}, nil
	case "lifecycle.types":
		// gendoc:generate(entity=server, group=logging, key=logging.NAME.lifecycle.types)
		//
		// ---
		//  type: string
		//  scope: global
		//  shortdesc: E.g., `instance`, comma separate, empty means all
		return Key{Validator: validate.Optional(validate.IsAny)}, nil
	case "lifecycle.projects":
		// gendoc:generate(entity=server, group=logging, key=logging.NAME.lifecycle.projects)
		//
		// ---
		//  type: string
		//  scope: global
		//  shortdesc: Comma separate list of projects, empty means all
		return Key{Validator: validate.Optional(validate.IsAny)}, nil
	}

	return Key{}, fmt.Errorf("%s is not a valid logging config key", key)
}
