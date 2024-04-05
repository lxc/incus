package syslog

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/internal/revert"
	"github.com/lxc/incus/v6/internal/server/events"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/util"
)

// Listen starts the log monitor.
func Listen(ctx context.Context, eventServer *events.Server) error {
	var listenConfig net.ListenConfig

	sockFile := internalUtil.VarPath("syslog.socket")

	if util.PathExists(sockFile) {
		err := os.Remove(sockFile)
		if err != nil {
			return fmt.Errorf("Failed deleting stale syslog.socket: %w", err)
		}
	}

	conn, err := listenConfig.ListenPacket(ctx, "unixgram", sockFile)
	if err != nil {
		return fmt.Errorf("Failed listening on syslog socket: %w", err)
	}

	revert := revert.New()
	defer revert.Fail()

	revert.Add(func() {
		_ = conn.Close()
		_ = os.Remove(sockFile)
	})

	// Get max size
	var maxBufSize int

	uc, ok := conn.(*net.UnixConn)
	if ok {
		f, err := uc.File()
		if err != nil {
			return fmt.Errorf("Failed getting underlying os.File: %w", err)
		}

		maxBufSize, err = unix.GetsockoptInt(int(f.Fd()), unix.SOL_SOCKET, unix.SO_RCVBUF)
		if err != nil {
			_ = f.Close()
			return fmt.Errorf("Failed getting SO_RCVBUF: %w", err)
		}

		// This makes the fd non-blocking so that conn.Close() won't block.
		// See https://github.com/golang/go/issues/29277#issuecomment-447922481
		err = unix.SetNonblock(int(f.Fd()), true)
		if err != nil {
			_ = f.Close()
			return fmt.Errorf("Failed setting non-block: %w", err)
		}

		_ = f.Close()
	}

	// This goroutine waits for the context to be cancelled and then closes the connection causing `ReadFrom` to return an error and exit the goroutine below.
	go func() {
		<-ctx.Done()
		_ = conn.Close()
		_ = os.Remove(sockFile)
	}()

	// This goroutine is used for reading packets, and processing the log message. `ReadFrom` will block until it either receives data, or an error occurs. If the connection is closed, `ReadFrom` will return an error, and the goroutine will terminate.
	go func() {
		buf := make([]byte, maxBufSize)

		// This maps OVN log level names to logrus log levels.
		logMap := map[string]logrus.Level{
			"dbg":  logrus.DebugLevel,
			"info": logrus.InfoLevel,
			"warn": logrus.WarnLevel,
			"err":  logrus.ErrorLevel,
			"emer": logrus.ErrorLevel,
		}

		for {
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}

			// Acceptable formats:
			// - <29> ovs|00017|rconn|INFO|unix:/var/run/openvswitch/br-int.mgmt: connected"
			// - <29> ovs|ovn-controller|00017|rconn|INFO|unix:/var/run/openvswitch/br-int.mgmt: connected"
			// The first field can be ignored as that information is relevant to syslogd.
			fields := strings.SplitN(string(buf[:n]), "|", 6)

			if len(fields) < 5 {
				continue
			}

			applicationName := ""

			if len(fields) == 6 {
				applicationName = fields[1]
			}

			sequenceNumber := fields[len(fields)-4]
			moduleName := fields[len(fields)-3]
			logLevel := strings.ToLower(fields[len(fields)-2])
			message := fields[len(fields)-1]

			if !strings.HasPrefix(moduleName, "acl_log") {
				continue
			}

			event := api.EventLogging{
				Level:   logMap[logLevel].String(),
				Message: message,
				Context: map[string]string{
					"sequence": sequenceNumber,
				},
			}

			if applicationName != "" {
				event.Context["application"] = applicationName
			}

			err = eventServer.Send("", api.EventTypeNetworkACL, event)
			if err != nil {
				continue
			}
		}
	}()

	revert.Success()

	return nil
}
