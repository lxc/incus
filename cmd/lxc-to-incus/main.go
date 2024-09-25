package main

/*
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <sched.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

__attribute__((constructor)) void init(void) {
	int ret;
	int mntns_fd;

	if (getenv("SNAP") == NULL)
		return;

	mntns_fd = open("/proc/1/ns/mnt", O_RDONLY | O_CLOEXEC);
	if (ret < 0) {
		fprintf(stderr, "Failed open mntns: %s\n", strerror(errno));
		_exit(EXIT_FAILURE);
	}

	ret = setns(mntns_fd, CLONE_NEWNS);
	close(mntns_fd);
	if (ret < 0) {
		fprintf(stderr, "Failed setns to outside mount namespace: %s\n", strerror(errno));
		_exit(EXIT_FAILURE);
	}

	ret = chdir("/");
	if (ret < 0) {
		fprintf(stderr, "Failed chdir /: %s\n", strerror(errno));
		_exit(EXIT_FAILURE);
	}

	// We're done, jump back to Go
}
*/
import "C"

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/internal/version"
)

type cmdGlobal struct {
	flagVersion bool
	flagHelp    bool
}

func main() {
	// migrate command (main)
	migrateCmd := cmdMigrate{}
	app := migrateCmd.Command()
	app.SilenceUsage = true
	app.CompletionOptions = cobra.CompletionOptions{DisableDefaultCmd: true}

	// Workaround for main command
	app.Args = cobra.ArbitraryArgs

	// Global flags
	globalCmd := cmdGlobal{}
	migrateCmd.global = &globalCmd
	app.PersistentFlags().BoolVar(&globalCmd.flagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&globalCmd.flagHelp, "help", "h", false, "Print help")

	// Version handling
	app.SetVersionTemplate("{{.Version}}\n")
	app.Version = version.Version

	// netcat sub-command
	netcatCmd := cmdNetcat{global: &globalCmd}
	app.AddCommand(netcatCmd.Command())

	// Run the main command and handle errors
	err := app.Execute()
	if err != nil {
		os.Exit(1)
	}
}
