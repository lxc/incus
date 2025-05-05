package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v6/client"
	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/termios"
	"github.com/lxc/incus/v6/shared/util"
)

type cmdConsole struct {
	global *cmdGlobal

	flagForce   bool
	flagShowLog bool
	flagType    string
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdConsole) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("console", i18n.G("[<remote>:]<instance>"))
	cmd.Short = i18n.G("Attach to instance consoles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Attach to instance consoles

This command allows you to interact with the boot console of an instance
as well as retrieve past log entries from it.`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Forces a connection to the console, even if there is already an active session"))
	cmd.Flags().BoolVar(&c.flagShowLog, "show-log", false, i18n.G("Retrieve the instance's console log"))
	cmd.Flags().StringVarP(&c.flagType, "type", "t", "console", i18n.G("Type of connection to establish: 'console' for serial console, 'vga' for SPICE graphical output")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpInstances(toComplete)
	}

	return cmd
}

func (c *cmdConsole) sendTermSize(control *websocket.Conn) error {
	width, height, err := termios.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return err
	}

	logger.Debugf("Window size is now: %dx%d", width, height)

	msg := api.InstanceExecControl{}
	msg.Command = "window-resize"
	msg.Args = make(map[string]string)
	msg.Args["width"] = strconv.Itoa(width)
	msg.Args["height"] = strconv.Itoa(height)

	return control.WriteJSON(msg)
}

type readWriteCloser struct {
	io.Reader
	io.WriteCloser
}

type stdinMirror struct {
	r                 io.Reader
	consoleDisconnect chan struct{}
	foundEscape       *bool
}

// The pty has been switched to raw mode so we will only ever read a single
// byte. The buffer size is therefore uninteresting to us.
func (er stdinMirror) Read(p []byte) (int, error) {
	n, err := er.r.Read(p)

	v := rune(p[0])
	if v == '\u0001' && !*er.foundEscape {
		*er.foundEscape = true
		return 0, err
	}

	if v == 'q' && *er.foundEscape {
		close(er.consoleDisconnect)
		return 0, err
	}

	*er.foundEscape = false
	return n, err
}

// Run runs the actual command logic.
func (c *cmdConsole) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Validate flags.
	if !slices.Contains([]string{"console", "vga"}, c.flagType) {
		return fmt.Errorf(i18n.G("Unknown output type %q"), c.flagType)
	}

	// Connect to the daemon.
	remote, name, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	// Fetch instance config to apply console defaults
	instance, _, err := d.GetInstance(name)
	if err != nil {
		return err
	}

	cfg := instance.ExpandedConfig
	if c.flagType == "" {
		c.flagType = cfg["console.type"]
		if c.flagType == "" {
			c.flagType = "console"
		}
	}

	return c.console(d, name)
}

func (c *cmdConsole) console(d incus.InstanceServer, name string) error {
	// Show the current log if requested.
	if c.flagShowLog {
		if c.flagType != "console" {
			return errors.New(i18n.G("The --show-log flag is only supported for by 'console' output type"))
		}

		console := &incus.InstanceConsoleLogArgs{}
		log, err := d.GetInstanceConsoleLog(name, console)
		if err != nil {
			return err
		}

		content, err := io.ReadAll(log)
		if err != nil {
			return err
		}

		fmt.Println(string(content))
		return nil
	}

	// Handle running consoles.
	if c.flagType == "" {
		c.flagType = "console"
	}

	switch c.flagType {
	case "console":
		return c.text(d, name)
	case "vga":
		return c.vga(d, name)
	}

	return fmt.Errorf(i18n.G("Unknown console type %q"), c.flagType)
}

func (c *cmdConsole) text(d incus.InstanceServer, name string) error {
	// Configure the terminal
	cfd := int(os.Stdin.Fd())

	oldTTYstate, err := termios.MakeRaw(cfd)
	if err != nil {
		return err
	}

	defer func() { _ = termios.Restore(cfd, oldTTYstate) }()

	handler := c.controlSocketHandler

	var width, height int
	width, height, err = termios.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}

	// Prepare the remote console
	req := api.InstanceConsolePost{
		Width:  width,
		Height: height,
		Type:   "console",
		Force:  c.flagForce,
	}

	consoleDisconnect := make(chan bool)
	manualDisconnect := make(chan struct{})

	sendDisconnect := make(chan struct{})
	defer close(sendDisconnect)

	consoleArgs := incus.InstanceConsoleArgs{
		Terminal: &readWriteCloser{stdinMirror{
			os.Stdin,
			manualDisconnect, new(bool),
		}, os.Stdout},
		Control:           handler,
		ConsoleDisconnect: consoleDisconnect,
	}

	go func() {
		select {
		case <-sendDisconnect:
		case <-manualDisconnect:
		}

		close(consoleDisconnect)

		// Make sure we leave the user back to a clean prompt.
		fmt.Printf("\r\n")
	}()

	// Attach to the instance console
	op, err := d.ConsoleInstance(name, req, &consoleArgs)
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G("To detach from the console, press: <ctrl>+a q") + "\n\r")

	// Wait for the operation to complete
	err = op.Wait()
	if err != nil {
		return err
	}

	return nil
}

func (c *cmdConsole) vga(d incus.InstanceServer, name string) error {
	var err error
	conf := c.global.conf

	// We currently use the control websocket just to abort in case of errors.
	controlDone := make(chan struct{}, 1)
	handler := func(control *websocket.Conn) {
		<-controlDone
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		_ = control.WriteMessage(websocket.CloseMessage, closeMsg)
	}

	// Prepare the remote console.
	req := api.InstanceConsolePost{
		Type:  "vga",
		Force: c.flagForce,
	}

	chDisconnect := make(chan bool)
	chViewer := make(chan struct{})

	consoleArgs := incus.InstanceConsoleArgs{
		Control:           handler,
		ConsoleDisconnect: chDisconnect,
	}

	// Setup local socket.
	var socket string
	var listener net.Listener
	if runtime.GOOS != "windows" {
		// Create a temporary unix socket mirroring the instance's spice socket.
		if !util.PathExists(conf.ConfigPath("sockets")) {
			err := os.MkdirAll(conf.ConfigPath("sockets"), 0o700)
			if err != nil {
				return err
			}
		}

		// Generate a random file name.
		path, err := os.CreateTemp(conf.ConfigPath("sockets"), "*.spice")
		if err != nil {
			return err
		}

		_ = path.Close()

		err = os.Remove(path.Name())
		if err != nil {
			return err
		}

		// Listen on the socket.
		listener, err = net.Listen("unix", path.Name())
		if err != nil {
			return err
		}

		defer func() { _ = os.Remove(path.Name()) }()

		socket = fmt.Sprintf("spice+unix://%s", path.Name())
	} else {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}

		addr, ok := listener.Addr().(*net.TCPAddr)
		if !ok {
			return errors.New("Bad TCP listener")
		}

		socket = fmt.Sprintf("spice://127.0.0.1:%d", addr.Port)
	}

	// Clean everything up when the viewer is done.
	go func() {
		<-chViewer
		_ = listener.Close()
		close(chDisconnect)
	}()

	// Spawn the remote console.
	op, connect, err := d.ConsoleInstanceDynamic(name, req, &consoleArgs)
	if err != nil {
		close(chViewer)
		return err
	}

	// Handle connections to the socket.
	wgConnections := sync.WaitGroup{}
	chConnected := make(chan struct{})
	go func() {
		hasConnected := false

		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			if !hasConnected {
				hasConnected = true
				close(chConnected)
			}

			wgConnections.Add(1)

			go func(conn io.ReadWriteCloser) {
				defer wgConnections.Done()

				err = connect(conn)
				if err != nil {
					return
				}
			}(conn)
		}
	}()

	
	consoleExecutable := cfg["console.executable"]
	consoleArgs := cfg["console.args"]

	var cmd *exec.Cmd
	if consoleExecutable != "" {
		// Split custom args (basic split; consider shellwords lib for full parsing)
		args := []string{}
		if consoleArgs != "" {
			args = append(args, util.SplitNArgs(consoleArgs, -1)...)
		}
		args = append(args, socket)
		cmd = exec.Command(consoleExecutable, args...)
	} else {
		remoteViewer := c.findCommand("remote-viewer")
		spicy := c.findCommand("spicy")

		if remoteViewer != "" {
			cmd = exec.Command(remoteViewer, socket)
		} else if spicy != "" {
			cmd = exec.Command(spicy, fmt.Sprintf("--uri=%s", socket))
		}
	}


	// Wait for the operation to complete.
	err = op.Wait()
	if err != nil {
		return err
	}

	return nil
}
