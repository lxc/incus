//go:build !windows

package main

import (
	"os"
	"os/signal"

	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
)

func (c *cmdExec) getTERM() (string, bool) {
	return os.LookupEnv("TERM")
}

func (c *cmdExec) controlSocketHandler(control *websocket.Conn) {
	ch := make(chan os.Signal, 10)
	signal.Notify(ch,
		unix.SIGWINCH,
		unix.SIGTERM,
		unix.SIGHUP,
		unix.SIGINT,
		unix.SIGQUIT,
		unix.SIGABRT,
		unix.SIGTSTP,
		unix.SIGTTIN,
		unix.SIGTTOU,
		unix.SIGUSR1,
		unix.SIGUSR2,
		unix.SIGSEGV,
		unix.SIGCONT)

	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	defer func() { _ = control.WriteMessage(websocket.CloseMessage, closeMsg) }()

	for {
		sig := <-ch

		switch sig {
		case unix.SIGWINCH:
			if !c.interactive {
				// Don't send SIGWINCH to non-interactive, this can lead to console corruption/crashes.
				continue
			}

			logger.Debugf("Received '%s signal', updating window geometry.", sig)
			err := c.sendTermSize(control)
			if err != nil {
				logger.Debugf("error setting term size %s", err)
				return
			}

		case unix.SIGTERM:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGTERM)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGTERM)
				return
			}

		case unix.SIGHUP:
			file, err := os.OpenFile("/dev/tty", os.O_RDONLY|unix.O_NOCTTY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0666)
			if err == nil {
				_ = file.Close()
				err = c.forwardSignal(control, unix.SIGHUP)
			} else {
				err = c.forwardSignal(control, unix.SIGTERM)
				sig = unix.SIGTERM
			}

			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", sig)
				return
			}

		case unix.SIGINT:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGINT)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGINT)
				return
			}

		case unix.SIGQUIT:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGQUIT)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGQUIT)
				return
			}

		case unix.SIGABRT:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGABRT)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGABRT)
				return
			}

		case unix.SIGTSTP:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGTSTP)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGTSTP)
				return
			}

		case unix.SIGTTIN:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGTTIN)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGTTIN)
				return
			}

		case unix.SIGTTOU:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGTTOU)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGTTOU)
				return
			}

		case unix.SIGUSR1:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGUSR1)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGUSR1)
				return
			}

		case unix.SIGUSR2:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGUSR2)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGUSR2)
				return
			}

		case unix.SIGSEGV:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGSEGV)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGSEGV)
				return
			}

		case unix.SIGCONT:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGCONT)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGCONT)
				return
			}
		}
	}
}

func (c *cmdExec) forwardSignal(control *websocket.Conn, sig unix.Signal) error {
	logger.Debugf("Forwarding signal: %s", sig)

	msg := api.InstanceExecControl{}
	msg.Command = "signal"
	msg.Signal = int(sig)

	return control.WriteJSON(msg)
}
