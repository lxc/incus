// Package incus implements a client for the Incus API
//
// # Overview
//
// This package lets you connect to Incus daemons or SimpleStream image
// servers over a Unix socket or HTTPs. You can then interact with those
// remote servers, creating instances, images, moving them around, ...
//
// The following examples make use of several imports:
//
//	import (
//		"github.com/lxc/incus/client"
//		"github.com/lxc/incus/shared/api"
//		"github.com/lxc/incus/shared/termios"
//	)
//
// # Example - instance creation
//
// This creates a container on a local Incus daemon and then starts it.
//
//	// Connect to Incus over the Unix socket
//	c, err := incus.ConnectIncusUnix("", nil)
//	if err != nil {
//	  return err
//	}
//
//	// Instance creation request
//	name := "my-container"
//	req := api.InstancesPost{
//	  Name: name,
//	  Source: api.InstanceSource{
//	    Type:  "image",
//	    Alias: "my-image", # e.g. alpine/3.20
//	    Server: "https://images.linuxcontainers.org",
//	    Protocol: "simplestreams",
//	  },
//	  Type: "container"
//	}
//
//	// Get Incus to create the instance (background operation)
//	op, err := c.CreateInstance(req)
//	if err != nil {
//	  return err
//	}
//
//	// Wait for the operation to complete
//	err = op.Wait()
//	if err != nil {
//	  return err
//	}
//
//	// Get Incus to start the instance (background operation)
//	reqState := api.InstanceStatePut{
//	  Action: "start",
//	  Timeout: -1,
//	}
//
//	op, err = c.UpdateInstanceState(name, reqState, "")
//	if err != nil {
//	  return err
//	}
//
//	// Wait for the operation to complete
//	err = op.Wait()
//	if err != nil {
//	  return err
//	}
//
// # Example - command execution
//
// This executes an interactive bash terminal
//
//	// Connect to Incus over the Unix socket
//	c, err := incus.ConnectIncusUnix("", nil)
//	if err != nil {
//	  return err
//	}
//
//	// Setup the exec request
//	req := api.InstanceExecPost{
//	  Command: []string{"bash"},
//	  WaitForWS: true,
//	  Interactive: true,
//	  Width: 80,
//	  Height: 15,
//	}
//
//	// Setup the exec arguments (fds)
//	args := incus.InstanceExecArgs{
//	  Stdin: os.Stdin,
//	  Stdout: os.Stdout,
//	  Stderr: os.Stderr,
//	}
//
//	// Setup the terminal (set to raw mode)
//	if req.Interactive {
//	  cfd := int(syscall.Stdin)
//	  oldttystate, err := termios.MakeRaw(cfd)
//	  if err != nil {
//	    return err
//	  }
//
//	  defer termios.Restore(cfd, oldttystate)
//	}
//
//	// Get the current state
//	op, err := c.ExecInstance(name, req, &args)
//	if err != nil {
//	  return err
//	}
//
//	// Wait for it to complete
//	err = op.Wait()
//	if err != nil {
//	  return err
//	}
//
// # Example - image copy
//
// This copies an image from a simplestreams server to a local Incus daemon
//
//	// Connect to Incus over the Unix socket
//	c, err := incus.ConnectIncusUnix("", nil)
//	if err != nil {
//	  return err
//	}
//
//	// Connect to the remote SimpleStreams server
//	d, err = incus.ConnectSimpleStreams("https://images.linuxcontainers.org", nil)
//	if err != nil {
//	  return err
//	}
//
//	// Resolve the alias
//	alias, _, err := d.GetImageAlias("centos/7")
//	if err != nil {
//	  return err
//	}
//
//	// Get the image information
//	image, _, err := d.GetImage(alias.Target)
//	if err != nil {
//	  return err
//	}
//
//	// Ask Incus to copy the image from the remote server
//	op, err := d.CopyImage(*image, c, nil)
//	if err != nil {
//	  return err
//	}
//
//	// And wait for it to finish
//	err = op.Wait()
//	if err != nil {
//	  return err
//	}
package incus
