package main

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/go-macaroon-bakery/macaroon-bakery/v3/httpbakery"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/httpbakery/form"
	cookiejar "github.com/juju/persistent-cookiejar"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
	schemaform "gopkg.in/juju/environschema.v1/form"

	"github.com/cyphar/incus/client"
	"github.com/cyphar/incus/incus/migration"
	"github.com/cyphar/incus/shared"
	"github.com/cyphar/incus/shared/api"
	cli "github.com/cyphar/incus/shared/cmd"
	"github.com/cyphar/incus/shared/version"
	"github.com/cyphar/incus/shared/ws"
)

func transferRootfs(ctx context.Context, dst incus.InstanceServer, op incus.Operation, rootfs string, rsyncArgs string, instanceType api.InstanceType) error {
	opAPI := op.Get()

	// Connect to the websockets
	wsControl, err := op.GetWebsocket(opAPI.Metadata[api.SecretNameControl].(string))
	if err != nil {
		return err
	}

	abort := func(err error) error {
		protoSendError(wsControl, err)
		return err
	}

	wsFs, err := op.GetWebsocket(opAPI.Metadata[api.SecretNameFilesystem].(string))
	if err != nil {
		return abort(err)
	}

	// Setup control struct
	var fs migration.MigrationFSType
	var rsyncHasFeature bool

	if instanceType == api.InstanceTypeVM {
		fs = migration.MigrationFSType_BLOCK_AND_RSYNC
		rsyncHasFeature = false
	} else {
		fs = migration.MigrationFSType_RSYNC
		rsyncHasFeature = true
	}

	offerHeader := migration.MigrationHeader{
		RsyncFeatures: &migration.RsyncFeatures{
			Xattrs:   &rsyncHasFeature,
			Delete:   &rsyncHasFeature,
			Compress: &rsyncHasFeature,
		},
		Fs: &fs,
	}

	if instanceType == api.InstanceTypeVM {
		stat, err := os.Stat(filepath.Join(rootfs, "root.img"))
		if err != nil {
			return abort(err)
		}

		size := stat.Size()
		offerHeader.VolumeSize = &size
		rootfs = shared.AddSlash(rootfs)
	}

	err = migration.ProtoSend(wsControl, &offerHeader)
	if err != nil {
		return abort(err)
	}

	var respHeader migration.MigrationHeader
	err = migration.ProtoRecv(wsControl, &respHeader)
	if err != nil {
		return abort(err)
	}

	rsyncFeaturesOffered := offerHeader.GetRsyncFeaturesSlice()
	rsyncFeaturesResponse := respHeader.GetRsyncFeaturesSlice()

	if !reflect.DeepEqual(rsyncFeaturesOffered, rsyncFeaturesResponse) {
		return abort(fmt.Errorf("Offered rsync features (%v) differ from those in the migration response (%v)", rsyncFeaturesOffered, rsyncFeaturesResponse))
	}

	// Send the filesystem
	err = rsyncSend(ctx, wsFs, rootfs, rsyncArgs, instanceType)
	if err != nil {
		return abort(fmt.Errorf("Failed sending filesystem volume: %w", err))
	}

	// Send block volume
	if instanceType == api.InstanceTypeVM {
		f, err := os.Open(filepath.Join(rootfs, "root.img"))
		if err != nil {
			return abort(err)
		}

		defer func() { _ = f.Close() }()

		conn := ws.NewWrapper(wsFs)

		go func() {
			<-ctx.Done()
			_ = conn.Close()
			_ = f.Close()
		}()

		_, err = io.Copy(conn, f)
		if err != nil {
			return abort(fmt.Errorf("Failed sending block volume: %w", err))
		}

		err = conn.Close()
		if err != nil {
			return abort(err)
		}
	}

	// Check the result
	msg := migration.MigrationControl{}
	err = migration.ProtoRecv(wsControl, &msg)
	if err != nil {
		_ = wsControl.Close()
		return err
	}

	if !msg.GetSuccess() {
		return fmt.Errorf(msg.GetMessage())
	}

	return nil
}

func connectTarget(url string, certPath string, keyPath string, authType string, token string) (incus.InstanceServer, string, error) {
	args := incus.ConnectionArgs{
		AuthType: authType,
	}

	clientFingerprint := ""

	if authType == "tls" {
		var clientCrt []byte
		var clientKey []byte

		// Generate a new client certificate for this
		if certPath == "" || keyPath == "" {
			var err error

			clientCrt, clientKey, err = shared.GenerateMemCert(true, false)
			if err != nil {
				return nil, "", err
			}

			clientFingerprint, err = shared.CertFingerprintStr(string(clientCrt))
			if err != nil {
				return nil, "", err
			}

			// When using certificate add tokens, there's no need to show the temporary certificate.
			if token == "" {
				fmt.Printf("\nYour temporary certificate is:\n%s\n", string(clientCrt))
			}
		} else {
			var err error

			clientCrt, err = os.ReadFile(certPath)
			if err != nil {
				return nil, "", fmt.Errorf("Failed to read client certificate: %w", err)
			}

			clientKey, err = os.ReadFile(keyPath)
			if err != nil {
				return nil, "", fmt.Errorf("Failed to read client key: %w", err)
			}
		}

		args.TLSClientCert = string(clientCrt)
		args.TLSClientKey = string(clientKey)
	} else if authType == "candid" {
		args.AuthInteractor = []httpbakery.Interactor{
			form.Interactor{Filler: schemaform.IOFiller{}},
			httpbakery.WebBrowserInteractor{
				OpenWebBrowser: httpbakery.OpenWebBrowser,
			},
		}

		f, err := os.CreateTemp("", "lxd-migrate_")
		if err != nil {
			return nil, "", err
		}

		_ = f.Close()

		jar, err := cookiejar.New(
			&cookiejar.Options{
				Filename: f.Name(),
			})
		if err != nil {
			return nil, "", err
		}

		args.CookieJar = jar
	}

	// Attempt to connect using the system CA
	args.UserAgent = fmt.Sprintf("LXC-MIGRATE %s", version.Version)
	c, err := incus.ConnectLXD(url, &args)

	var certificate *x509.Certificate
	if err != nil {
		// Failed to connect using the system CA, so retrieve the remote certificate
		certificate, err = shared.GetRemoteCertificate(url, args.UserAgent)
		if err != nil {
			return nil, "", err
		}
	}

	// Handle certificate prompt
	if certificate != nil {
		serverCrt := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
		args.TLSServerCert = string(serverCrt)

		// Setup a new connection, this time with the remote certificate
		c, err = incus.ConnectLXD(url, &args)
		if err != nil {
			return nil, "", err
		}
	}

	if authType == "candid" {
		c.RequireAuthenticated(false)
	}

	// Get server information
	srv, _, err := c.GetServer()
	if err != nil {
		return nil, "", err
	}

	// Check if our cert is already trusted
	if srv.Auth == "trusted" {
		fmt.Printf("\nRemote LXD server:\n  Hostname: %s\n  Version: %s\n\n", srv.Environment.ServerName, srv.Environment.ServerVersion)
		return c, "", nil
	}

	if authType == "tls" {
		if token != "" {
			req := api.CertificatesPost{
				Password: token,
			}

			err = c.CreateCertificate(req)
			if err != nil {
				return nil, "", fmt.Errorf("Failed to create certificate: %w", err)
			}
		} else {
			fmt.Println("It is recommended to have this certificate be manually added to LXD through `lxc config trust add` on the target server.\nAlternatively you could use a pre-defined trust password to add it remotely (use of a trust password can be a security issue).")

			fmt.Println("")

			useTrustPassword, err := cli.AskBool("Would you like to use a trust password? [default=no]: ", "no")
			if err != nil {
				return nil, "", err
			}

			if useTrustPassword {
				// Prompt for trust password
				fmt.Print("Trust password: ")
				pwd, err := term.ReadPassword(0)
				if err != nil {
					return nil, "", err
				}

				fmt.Println("")

				// Add client certificate to trust store
				req := api.CertificatesPost{
					Password: string(pwd),
				}

				req.Type = api.CertificateTypeClient

				err = c.CreateCertificate(req)
				if err != nil {
					return nil, "", err
				}
			} else {
				fmt.Print("Press ENTER after the certificate was added to the remote server: ")
				_, err = bufio.NewReader(os.Stdin).ReadString('\n')
				if err != nil {
					return nil, "", err
				}
			}
		}
	} else {
		c.RequireAuthenticated(true)
	}

	// Get full server information
	srv, _, err = c.GetServer()
	if err != nil {
		if clientFingerprint != "" {
			_ = c.DeleteCertificate(clientFingerprint)
		}

		return nil, "", err
	}

	if srv.Auth == "untrusted" {
		return nil, "", fmt.Errorf("Server doesn't trust us after authentication")
	}

	fmt.Printf("\nRemote LXD server:\n  Hostname: %s\n  Version: %s\n\n", srv.Environment.ServerName, srv.Environment.ServerVersion)

	return c, clientFingerprint, nil
}

func setupSource(path string, mounts []string) error {
	prefix := "/"
	if len(mounts) > 0 {
		prefix = mounts[0]
	}

	// Mount everything
	for _, mount := range mounts {
		target := fmt.Sprintf("%s/%s", path, strings.TrimPrefix(mount, prefix))

		// Mount the path
		err := unix.Mount(mount, target, "none", unix.MS_BIND, "")
		if err != nil {
			return fmt.Errorf("Failed to mount %s: %w", mount, err)
		}

		// Make it read-only
		err = unix.Mount("", target, "none", unix.MS_BIND|unix.MS_RDONLY|unix.MS_REMOUNT, "")
		if err != nil {
			return fmt.Errorf("Failed to make %s read-only: %w", mount, err)
		}
	}

	return nil
}

func parseURL(URL string) (string, error) {
	u, err := url.Parse(URL)
	if err != nil {
		return "", err
	}

	// Create a URL with scheme and hostname since it wasn't provided
	if u.Scheme == "" && u.Host == "" && u.Path != "" {
		u, err = url.Parse(fmt.Sprintf("https://%s", u.Path))
		if err != nil {
			return "", err
		}
	}

	// If no port was provided, use default port
	if u.Port() == "" {
		u.Host = fmt.Sprintf("%s:%d", u.Hostname(), shared.HTTPSDefaultPort)
	}

	return u.String(), nil
}
