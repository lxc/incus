package endpoints

import (
	"bytes"
	"net"
	"regexp"

	"github.com/lxc/incus/v6/shared/logger"
)

type networkServerErrorLogWriter struct {
	proxies []net.IP
}

// Regex for the log we want to ignore.
var unwantedLogRegex = regexp.MustCompile(`^http: TLS handshake error from ([^\[:]+?|\[([^\]]+?)\]):[0-9]+: .+: connection reset by peer$`)

func (d networkServerErrorLogWriter) Write(p []byte) (int, error) {
	strippedLog := d.stripLog(p)
	if strippedLog == "" {
		return 0, nil
	}

	logger.Info(strippedLog)
	return len(p), nil
}

func (d networkServerErrorLogWriter) stripLog(p []byte) string {
	// Strip the beginning of the log until we reach "http:".
	for len(p) > 5 && string(p[0:5]) != "http:" {
		p = bytes.TrimLeftFunc(p, func(r rune) bool {
			return r != 'h'
		})
	}

	// Strip the newline from the end.
	p = bytes.TrimRightFunc(p, func(r rune) bool {
		return r == '\n'
	})

	// Get the source IP address.
	match := unwantedLogRegex.FindSubmatch(p)
	var sourceIP string
	if match != nil {
		if match[2] != nil {
			// Inner match omits parentheses of ipv6 address.
			sourceIP = string(match[2])
		} else if match[1] != nil {
			sourceIP = string(match[1])
		}
	}

	// Discard the log if the source is in our list of trusted proxies.
	if sourceIP != "" {
		for _, ip := range d.proxies {
			if ip.String() == sourceIP {
				return ""
			}
		}
	}

	return string(p)
}
