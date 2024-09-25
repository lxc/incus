package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/internal/eagain"
)

type cmdNetcat struct {
	global *cmdGlobal
}

func (c *cmdNetcat) Command() *cobra.Command {
	cmd := &cobra.Command{}

	cmd.Use = "netcat <address>"
	cmd.Short = "Sends stdin data to a unix socket"
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdNetcat) Run(cmd *cobra.Command, args []string) error {
	// Help and usage
	if len(args) == 0 {
		_ = cmd.Help()
		return nil
	}

	// Handle mandatory arguments
	if len(args) != 1 {
		_ = cmd.Help()
		return fmt.Errorf("Missing required argument")
	}

	// Connect to the provided address
	uAddr, err := net.ResolveUnixAddr("unix", args[0])
	if err != nil {
		return err
	}

	conn, err := net.DialUnix("unix", nil, uAddr)
	if err != nil {
		return err
	}

	// We'll wait until we're done reading from the socket
	wg := sync.WaitGroup{}
	wg.Add(1)

	go func() {
		_, err = io.Copy(eagain.Writer{Writer: os.Stdout}, eagain.Reader{Reader: conn})
		_ = conn.Close()
		wg.Done()
	}()

	go func() {
		_, _ = io.Copy(eagain.Writer{Writer: conn}, eagain.Reader{Reader: os.Stdin})
	}()

	// Wait
	wg.Wait()

	return err
}
