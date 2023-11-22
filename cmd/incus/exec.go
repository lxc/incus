package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"

	"github.com/lxc/incus/client"
	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/i18n"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/termios"
)

type cmdExec struct {
	global *cmdGlobal

	flagMode                string
	flagEnvironment         []string
	flagForceInteractive    bool
	flagForceNonInteractive bool
	flagDisableStdin        bool
	flagUser                uint32
	flagGroup               uint32
	flagCwd                 string

	interactive bool
}

func (c *cmdExec) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("exec", i18n.G("[<remote>:]<instance> [flags] [--] <command line>"))
	cmd.Short = i18n.G("Execute commands in instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Execute commands in instances

The command is executed directly using exec, so there is no shell and
shell patterns (variables, file redirects, ...) won't be understood.
If you need a shell environment you need to execute the shell
executable, passing the shell commands as arguments, for example:

  incus exec <instance> -- sh -c "cd /tmp && pwd"

Mode defaults to non-interactive, interactive mode is selected if both stdin AND stdout are terminals (stderr is ignored).`))

	cmd.RunE = c.Run
	cmd.Flags().StringArrayVar(&c.flagEnvironment, "env", nil, i18n.G("Environment variable to set (e.g. HOME=/home/foo)")+"``")
	cmd.Flags().StringVar(&c.flagMode, "mode", "auto", i18n.G("Override the terminal mode (auto, interactive or non-interactive)")+"``")
	cmd.Flags().BoolVarP(&c.flagForceInteractive, "force-interactive", "t", false, i18n.G("Force pseudo-terminal allocation"))
	cmd.Flags().BoolVarP(&c.flagForceNonInteractive, "force-noninteractive", "T", false, i18n.G("Disable pseudo-terminal allocation"))
	cmd.Flags().BoolVarP(&c.flagDisableStdin, "disable-stdin", "n", false, i18n.G("Disable stdin (reads from /dev/null)"))
	cmd.Flags().Uint32Var(&c.flagUser, "user", 0, i18n.G("User ID to run the command as (default 0)")+"``")
	cmd.Flags().Uint32Var(&c.flagGroup, "group", 0, i18n.G("Group ID to run the command as (default 0)")+"``")
	cmd.Flags().StringVar(&c.flagCwd, "cwd", "", i18n.G("Directory to run the command in (default /root)")+"``")

	return cmd
}

func (c *cmdExec) sendTermSize(control *websocket.Conn) error {
	width, height, err := termios.GetSize(getStdoutFd())
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

func (c *cmdExec) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	if c.flagForceInteractive && c.flagForceNonInteractive {
		return fmt.Errorf(i18n.G("You can't pass -t and -T at the same time"))
	}

	if c.flagMode != "auto" && (c.flagForceInteractive || c.flagForceNonInteractive) {
		return fmt.Errorf(i18n.G("You can't pass -t or -T at the same time as --mode"))
	}

	// Connect to the daemon
	remote, name, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	// Set the environment
	env := map[string]string{}
	myTerm, ok := c.getTERM()
	if ok {
		env["TERM"] = myTerm
	}

	for _, arg := range c.flagEnvironment {
		pieces := strings.SplitN(arg, "=", 2)
		value := ""
		if len(pieces) > 1 {
			value = pieces[1]
		}

		env[pieces[0]] = value
	}

	// Configure the terminal
	stdinFd := getStdinFd()
	stdoutFd := getStdoutFd()

	stdinTerminal := termios.IsTerminal(stdinFd)
	stdoutTerminal := termios.IsTerminal(stdoutFd)

	// Determine interaction mode
	if c.flagDisableStdin {
		c.interactive = false
	} else if c.flagMode == "interactive" || c.flagForceInteractive {
		c.interactive = true
	} else if c.flagMode == "non-interactive" || c.flagForceNonInteractive {
		c.interactive = false
	} else {
		c.interactive = stdinTerminal && stdoutTerminal
	}

	// Record terminal state
	var oldttystate *termios.State
	if c.interactive && stdinTerminal {
		oldttystate, err = termios.MakeRaw(stdinFd)
		if err != nil {
			return err
		}

		defer func() { _ = termios.Restore(stdinFd, oldttystate) }()
	}

	// Setup interactive console handler
	handler := c.controlSocketHandler

	// Grab current terminal dimensions
	var width, height int
	if stdoutTerminal {
		width, height, err = termios.GetSize(getStdoutFd())
		if err != nil {
			return err
		}
	}

	var stdin io.Reader
	stdin = os.Stdin
	if c.flagDisableStdin {
		stdin = bytes.NewReader(nil)
	}

	stdout := getStdout()

	// Prepare the command
	req := api.InstanceExecPost{
		Command:     args[1:],
		WaitForWS:   true,
		Interactive: c.interactive,
		Environment: env,
		Width:       width,
		Height:      height,
		User:        c.flagUser,
		Group:       c.flagGroup,
		Cwd:         c.flagCwd,
	}

	execArgs := incus.InstanceExecArgs{
		Stdin:    stdin,
		Stdout:   stdout,
		Stderr:   os.Stderr,
		Control:  handler,
		DataDone: make(chan bool),
	}

	// Run the command in the instance
	op, err := d.ExecInstance(name, req, &execArgs)
	if err != nil {
		return err
	}

	// Wait for the operation to complete
	err = op.Wait()
	opAPI := op.Get()
	if opAPI.Metadata != nil {
		exitStatusRaw, ok := opAPI.Metadata["return"].(float64)
		if ok {
			c.global.ret = int(exitStatusRaw)
		}
	}

	if err != nil {
		return err
	}

	// Wait for any remaining I/O to be flushed
	<-execArgs.DataDone

	return nil
}
