package cliconfig

import (
	"fmt"
	"io"
	"os"

	"github.com/lxc/incus/shared"
)

// HasClientCertificate will return true if a client certificate has already been generated.
func (c *Config) HasClientCertificate() bool {
	certf := c.ConfigPath("client.crt")
	keyf := c.ConfigPath("client.key")
	if !shared.PathExists(certf) || !shared.PathExists(keyf) {
		return false
	}

	return true
}

// GenerateClientCertificate will generate the needed client.crt and client.key if needed.
func (c *Config) GenerateClientCertificate() error {
	if c.HasClientCertificate() {
		return nil
	}

	certf := c.ConfigPath("client.crt")
	keyf := c.ConfigPath("client.key")

	return shared.FindOrGenCert(certf, keyf, true, false)
}

// CopyGlobalCert will copy global (system-wide) certificate to the user config path.
func (c *Config) CopyGlobalCert(src string, dst string) error {
	oldPath := c.GlobalConfigPath("servercerts", fmt.Sprintf("%s.crt", src))
	newPath := c.ConfigPath("servercerts", fmt.Sprintf("%s.crt", dst))
	sourceFile, err := os.Open(oldPath)
	if err != nil {
		return err
	}

	defer func() { _ = sourceFile.Close() }()

	// Create new file
	newFile, err := os.Create(newPath)
	if err != nil {
		return err
	}

	defer func() { _ = newFile.Close() }()

	_, err = io.Copy(newFile, sourceFile)
	if err != nil {
		return err
	}

	return newFile.Close()
}
