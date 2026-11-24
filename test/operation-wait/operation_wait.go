package main

/*
 * Starts "sleep 10" in an instance with SkipGetEvents: true and output how waiting for it ended.
 */

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
)

func main() {
	if len(os.Args) != 5 {
		fmt.Printf("Usage: %s <unix-socket> <instance> <deadline|cancel|client-cancel|none> <seconds>\n", os.Args[0])
		os.Exit(1)
	}

	seconds, err := strconv.Atoi(os.Args[4])
	if err != nil {
		fmt.Printf("Invalid seconds: %v\n", err)
		os.Exit(1)
	}

	duration := time.Duration(seconds) * time.Second

	clientCtx, clientCancel := context.WithCancel(context.Background())
	defer clientCancel()

	client, err := incus.ConnectIncusUnixWithContext(clientCtx, os.Args[1], &incus.ConnectionArgs{SkipGetEvents: true})
	if err != nil {
		fmt.Printf("Failed to connect: %v\n", err)
		os.Exit(1)
	}

	op, err := client.ExecInstance(os.Args[2], api.InstanceExecPost{Command: []string{"sleep", "10"}}, nil)
	if err != nil {
		fmt.Printf("Failed to exec: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	switch os.Args[3] {
	case "deadline":
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, duration)
		defer cancel()
	case "cancel":
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		time.AfterFunc(duration, cancel)
	case "client-cancel":
		time.AfterFunc(duration, clientCancel)
	}

	fmt.Println(op.WaitContext(ctx))
}
