package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"sort"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/archive"
	"github.com/lxc/incus/v6/shared/ask"
	localtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/lxc/incus/v6/shared/util"
)

// Migrator defines the methods required to perform a migration.
type Migrator interface {
	gatherInfo() error
	migrate() error
	renderObject() error
}

// Migration is a base representation of a migration, which can be extended by more specific structs.
type Migration struct {
	asker         ask.Asker
	ctx           context.Context
	migrationType MigrationType
	mounts        []string
	pool          string
	project       string
	server        incus.InstanceServer
	sourceFormat  string
	sourcePath    string
	target        string
}

func (m *Migration) runMigration(migrationHandler func(path string) error) error {
	m.mounts = append(m.mounts, m.sourcePath)

	// Get and sort the mounts
	sort.Strings(m.mounts)

	// Create the mount namespace and ensure we're not moved around
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Unshare a new mntns so our mounts don't leak
	err := unix.Unshare(unix.CLONE_NEWNS)
	if err != nil {
		return fmt.Errorf("Failed to unshare mount namespace: %w", err)
	}

	// Prevent mount propagation back to initial namespace
	err = unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, "")
	if err != nil {
		return fmt.Errorf("Failed to disable mount propagation: %w", err)
	}

	// Create the temporary directory to be used for the mounts
	path, err := os.MkdirTemp("", "incus-migrate_mount_")
	if err != nil {
		return err
	}

	// Automatically clean-up the temporary path on exit
	defer func(path string) {
		// Unmount the path if it's a mountpoint.
		_ = unix.Unmount(path, unix.MNT_DETACH)
		_ = unix.Unmount(filepath.Join(path, "root.img"), unix.MNT_DETACH)

		// Cleanup VM image files.
		_ = os.Remove(filepath.Join(path, "converted-raw-image.img"))
		_ = os.Remove(filepath.Join(path, "root.img"))

		// Remove the directory itself.
		_ = os.Remove(path)
	}(path)

	var fullPath string

	if m.migrationType == MigrationTypeContainer || m.migrationType == MigrationTypeVolumeFilesystem {
		// Create the rootfs directory
		fullPath = fmt.Sprintf("%s/rootfs", path)

		err = os.Mkdir(fullPath, 0o755)
		if err != nil {
			return err
		}

		// Setup the source (mounts)
		err = setupSource(fullPath, m.mounts)
		if err != nil {
			return fmt.Errorf("Failed to setup the source: %w", err)
		}
	} else {
		_, ext, convCmd, _ := archive.DetectCompression(m.sourcePath)
		if ext == ".qcow2" || ext == ".vmdk" {
			// COnfirm the command is available.
			_, err := exec.LookPath(convCmd[0])
			if err != nil {
				return fmt.Errorf("Unable to find required command %q", convCmd[0])
			}

			destImg := filepath.Join(path, "converted-raw-image.img")

			cmd := []string{
				"nice", "-n19", // Run with low priority to reduce CPU impact on other processes.
			}

			cmd = append(cmd, convCmd...)
			cmd = append(cmd, "-p", "-t", "writeback")

			// Check for Direct I/O support.
			from, err := os.OpenFile(m.sourcePath, unix.O_DIRECT|unix.O_RDONLY, 0)
			if err == nil {
				cmd = append(cmd, "-T", "none")
				_ = from.Close()
			}

			to, err := os.OpenFile(destImg, unix.O_DIRECT|unix.O_RDONLY, 0)
			if err == nil {
				cmd = append(cmd, "-t", "none")
				_ = to.Close()
			}

			cmd = append(cmd, m.sourcePath, destImg)

			fmt.Printf("Converting image %q to raw format before importing\n", m.sourcePath)

			c := exec.Command(cmd[0], cmd[1:]...)
			err = c.Run()
			if err != nil {
				return fmt.Errorf("Failed to convert image %q for importing: %w", m.sourcePath, err)
			}

			m.sourcePath = destImg
		}

		fullPath = path
		target := filepath.Join(path, "root.img")

		err = os.WriteFile(target, nil, 0o644)
		if err != nil {
			return fmt.Errorf("Failed to create %q: %w", target, err)
		}

		// Mount the path
		err = unix.Mount(m.sourcePath, target, "none", unix.MS_BIND, "")
		if err != nil {
			return fmt.Errorf("Failed to mount %s: %w", m.sourcePath, err)
		}

		// Make it read-only
		err = unix.Mount("", target, "none", unix.MS_BIND|unix.MS_RDONLY|unix.MS_REMOUNT, "")
		if err != nil {
			return fmt.Errorf("Failed to make %s read-only: %w", m.sourcePath, err)
		}
	}

	return migrationHandler(fullPath)
}

func (m *Migration) setSourceFormat() error {
	if m.sourcePath == "" {
		return errors.New("Missing source path")
	}

	if m.migrationType == "" {
		return errors.New("Missing migration type")
	}

	// When migrating a disk, report the detected source format
	if m.migrationType == MigrationTypeVM || m.migrationType == MigrationTypeVolumeBlock {
		if linux.IsBlockdevPath(m.sourcePath) {
			m.sourceFormat = "Block device"
		} else if _, ext, _, _ := archive.DetectCompression(m.sourcePath); ext == ".qcow2" {
			m.sourceFormat = "qcow2"
		} else if _, ext, _, _ := archive.DetectCompression(m.sourcePath); ext == ".vmdk" {
			m.sourceFormat = "vmdk"
		} else {
			// If the input isn't a block device or qcow2/vmdk image, assume it's raw.
			// Positively identifying a raw image depends on parsing MBR/GPT partition tables.
			m.sourceFormat = "raw"
		}
	}

	return nil
}

func (m *Migration) askTarget() error {
	if !m.server.IsClustered() {
		return nil
	}

	ok, err := m.asker.AskBool("Would you like to target a specific server or group in the cluster? [default=no]: ", "no")
	if err != nil {
		return err
	}

	if !ok {
		return nil
	}

	clusterTarget, err := m.asker.AskString("Target name: ", "", nil)
	if err != nil {
		return err
	}

	m.target = clusterTarget

	return nil
}

func (m *Migration) askSourcePath(question string) error {
	var err error

	m.sourcePath, err = m.asker.AskString(question, "", func(s string) error {
		if !util.PathExists(s) {
			return errors.New("Path does not exist")
		}

		_, err := os.Stat(s)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

func (m *Migration) askProject(question string) error {
	projectNames, err := m.server.GetProjectNames()
	if err != nil {
		return err
	}

	if len(projectNames) > 1 {
		project, err := m.asker.AskChoice(question, projectNames, api.ProjectDefaultName)
		if err != nil {
			return err
		}

		m.project = project
		return nil
	}

	m.project = api.ProjectDefaultName
	return nil
}

type cmdMigrate struct {
	global *cmdGlobal

	flagRsyncArgs string
}

func (c *cmdMigrate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "incus-migrate"
	cmd.Short = "Physical to instance migration tool"
	cmd.Long = `Description:
  Physical to instance migration tool

  This tool lets you turn any Linux filesystem (including your current one)
  into an instance on a remote host.

  It will setup a clean mount tree made of the root filesystem and any
  additional mount you list, then transfer this through the migration
  API to create a new instance from it.

  The same set of options as ` + "`incus launch`" + ` are also supported.
`
	cmd.RunE = c.run
	cmd.Flags().StringVar(&c.flagRsyncArgs, "rsync-args", "", "Extra arguments to pass to rsync (for file transfers)"+"``")

	return cmd
}

func (c *cmdMigrate) askServer() (incus.InstanceServer, string, error) {
	// Detect local server.
	local, err := c.connectLocal()
	if err == nil {
		useLocal, err := c.global.asker.AskBool("The local Incus server is the target [default=yes]: ", "yes")
		if err != nil {
			return nil, "", err
		}

		if useLocal {
			return local, "", nil
		}
	}

	// Server address
	serverURL, err := c.global.asker.AskString("Please provide Incus server URL: ", "", nil)
	if err != nil {
		return nil, "", err
	}

	serverURL, err = parseURL(serverURL)
	if err != nil {
		return nil, "", err
	}

	args := incus.ConnectionArgs{
		UserAgent: fmt.Sprintf("LXC-MIGRATE %s", version.Version),
	}

	// Attempt to connect
	server, err := incus.ConnectIncus(serverURL, &args)
	if err != nil {
		// Failed to connect using the system CA, so retrieve the remote certificate.
		certificate, err := localtls.GetRemoteCertificate(serverURL, args.UserAgent)
		if err != nil {
			return nil, "", fmt.Errorf("Failed to get remote certificate: %w", err)
		}

		digest := localtls.CertFingerprint(certificate)

		fmt.Println("Certificate fingerprint:", digest)
		fmt.Print("ok (y/n)? ")

		buf := bufio.NewReader(os.Stdin)
		line, _, err := buf.ReadLine()
		if err != nil {
			return nil, "", err
		}

		if len(line) < 1 || line[0] != 'y' && line[0] != 'Y' {
			return nil, "", errors.New("Server certificate rejected by user")
		}

		args.InsecureSkipVerify = true
		server, err = incus.ConnectIncus(serverURL, &args)
		if err != nil {
			return nil, "", fmt.Errorf("Failed to connect to server: %w", err)
		}
	}

	apiServer, _, err := server.GetServer()
	if err != nil {
		return nil, "", fmt.Errorf("Failed to get server: %w", err)
	}

	fmt.Println("")

	type AuthMethod int

	const (
		authMethodTLSCertificate AuthMethod = iota
		authMethodTLSTemporaryCertificate
		authMethodTLSCertificateToken
	)

	// TLS is always available
	var availableAuthMethods []AuthMethod
	var authMethod AuthMethod

	i := 1

	if slices.Contains(apiServer.AuthMethods, api.AuthenticationMethodTLS) {
		fmt.Printf("%d) Use a certificate token\n", i)
		availableAuthMethods = append(availableAuthMethods, authMethodTLSCertificateToken)
		i++
		fmt.Printf("%d) Use an existing TLS authentication certificate\n", i)
		availableAuthMethods = append(availableAuthMethods, authMethodTLSCertificate)
		i++
		fmt.Printf("%d) Generate a temporary TLS authentication certificate\n", i)
		availableAuthMethods = append(availableAuthMethods, authMethodTLSTemporaryCertificate)
	}

	if len(apiServer.AuthMethods) > 1 || slices.Contains(apiServer.AuthMethods, api.AuthenticationMethodTLS) {
		authMethodInt, err := c.global.asker.AskInt("Please pick an authentication mechanism above: ", 1, int64(i), "", nil)
		if err != nil {
			return nil, "", err
		}

		authMethod = availableAuthMethods[authMethodInt-1]
	}

	var certPath string
	var keyPath string
	var token string

	switch authMethod {
	case authMethodTLSCertificate:
		certPath, err = c.global.asker.AskString("Please provide the certificate path: ", "", func(path string) error {
			if !util.PathExists(path) {
				return errors.New("File does not exist")
			}

			return nil
		})
		if err != nil {
			return nil, "", err
		}

		keyPath, err = c.global.asker.AskString("Please provide the keyfile path: ", "", func(path string) error {
			if !util.PathExists(path) {
				return errors.New("File does not exist")
			}

			return nil
		})
		if err != nil {
			return nil, "", err
		}

	case authMethodTLSCertificateToken:
		token, err = c.global.asker.AskString("Please provide the certificate token: ", "", func(token string) error {
			_, err := localtls.CertificateTokenDecode(token)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, "", err
		}

	case authMethodTLSTemporaryCertificate:
		// Intentionally ignored
	}

	var authType string

	switch authMethod {
	case authMethodTLSCertificate, authMethodTLSTemporaryCertificate, authMethodTLSCertificateToken:
		authType = api.AuthenticationMethodTLS
	}

	return c.connectTarget(serverURL, certPath, keyPath, authType, token)
}

func (c *cmdMigrate) run(_ *cobra.Command, _ []string) error {
	// Quick checks.
	if os.Geteuid() != 0 {
		return errors.New("This tool must be run as root")
	}

	_, err := exec.LookPath("rsync")
	if err != nil {
		return errors.New("Unable to find required command \"rsync\"")
	}

	// Server
	server, clientFingerprint, err := c.askServer()
	if err != nil {
		return err
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		<-sigChan

		if clientFingerprint != "" {
			_ = server.DeleteCertificate(clientFingerprint)
		}

		cancel()

		// The following nolint directive ignores the "deep-exit" rule of the revive linter.
		// We should be exiting cleanly by passing the above context into each invoked method and checking for
		// cancellation. Unfortunately our client methods do not accept a context argument.
		os.Exit(1) //nolint:revive
	}()

	if clientFingerprint != "" {
		defer func() { _ = server.DeleteCertificate(clientFingerprint) }()
	}

	// Provide migration type
	creationType, err := c.global.asker.AskInt(`
What would you like to create?
1) Container
2) Virtual Machine
3) Virtual Machine (from .ova)
4) Custom Volume

Please enter the number of your choice: `, 1, 4, "", nil)
	if err != nil {
		return err
	}

	var migrator Migrator
	switch creationType {
	case 1:
		migrator = NewInstanceMigration(ctx, server, c.global.asker, c.flagRsyncArgs, MigrationTypeContainer)
	case 2:
		migrator = NewInstanceMigration(ctx, server, c.global.asker, c.flagRsyncArgs, MigrationTypeVM)
	case 3:
		migrator = NewOVAMigration(ctx, server, c.global.asker, c.flagRsyncArgs)
	case 4:
		migrator = NewVolumeMigration(ctx, server, c.global.asker, c.flagRsyncArgs)
	}

	err = migrator.gatherInfo()
	if err != nil {
		return err
	}

	err = migrator.renderObject()
	if err != nil {
		return err
	}

	return migrator.migrate()
}
