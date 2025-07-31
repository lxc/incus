package cliconfig

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/ssh"

	localtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/lxc/incus/v6/shared/util"
)

// HasClientCertificate will return true if a client certificate has already been generated.
func (c *Config) HasClientCertificate() bool {
	certf := c.ConfigPath("client.crt")
	keyf := c.ConfigPath("client.key")
	if !util.PathExists(certf) || !util.PathExists(keyf) {
		return false
	}

	return true
}

// HasRemoteClientCertificate will return true if a remote-specific client certificate is present.
func (c *Config) HasRemoteClientCertificate(name string) bool {
	certf := c.ConfigPath("clientcerts", fmt.Sprintf("%s.crt", name))
	keyf := c.ConfigPath("clientcerts", fmt.Sprintf("%s.key", name))
	if !util.PathExists(certf) || !util.PathExists(keyf) {
		return false
	}

	return true
}

// GetClientCertificate returns the client certificate, key and CA (with optional remote name).
func (c *Config) GetClientCertificate(name string) (string, string, string, error) {
	// Values.
	var tlsClientCert string
	var tlsClientKey string
	var tlsClientCA string

	// Certificate paths.
	var pathClientCertificate string
	var pathClientKey string
	var pathClientCA string

	if c.HasRemoteClientCertificate(name) {
		pathClientCertificate = c.ConfigPath("clientcerts", fmt.Sprintf("%s.crt", name))
		pathClientKey = c.ConfigPath("clientcerts", fmt.Sprintf("%s.key", name))
		pathClientCA = c.ConfigPath("clientcerts", fmt.Sprintf("%s.ca", name))
	} else {
		pathClientCertificate = c.ConfigPath("client.crt")
		pathClientKey = c.ConfigPath("client.key")
		pathClientCA = c.ConfigPath("client.ca")
	}

	if util.PathExists(pathClientCertificate) {
		content, err := os.ReadFile(pathClientCertificate)
		if err != nil {
			return "", "", "", err
		}

		tlsClientCert = string(content)
	}

	// Client CA
	if util.PathExists(pathClientCA) {
		content, err := os.ReadFile(pathClientCA)
		if err != nil {
			return "", "", "", err
		}

		tlsClientCA = string(content)
	}

	// Client key
	if util.PathExists(pathClientKey) {
		content, err := os.ReadFile(pathClientKey)
		if err != nil {
			return "", "", "", err
		}

		pemKey, _ := pem.Decode(content)

		// Golang has deprecated all methods relating to PEM encryption due to a vulnerability.
		// However, the weakness does not make PEM unsafe for our purposes as it pertains to password protection on the
		// key file (client.key is only readable to the user in any case), so we'll ignore deprecation.
		isEncrypted := x509.IsEncryptedPEMBlock(pemKey) //nolint:staticcheck
		isSSH := pemKey.Type == "OPENSSH PRIVATE KEY"
		if isEncrypted || isSSH {
			if c.PromptPassword == nil {
				return "", "", "", errors.New("Private key is password protected and no helper was configured")
			}

			password, err := c.PromptPassword(pathClientKey)
			if err != nil {
				return "", "", "", err
			}

			if isSSH {
				sshKey, err := ssh.ParseRawPrivateKeyWithPassphrase(content, []byte(password))
				if err != nil {
					return "", "", "", err
				}

				ecdsaKey, okEcdsa := (sshKey).(*ecdsa.PrivateKey)
				rsaKey, okRsa := (sshKey).(*rsa.PrivateKey)
				if okEcdsa {
					derKey, err := x509.MarshalECPrivateKey(ecdsaKey)
					if err != nil {
						return "", "", "", err
					}

					content = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: derKey})
				} else if okRsa {
					derKey := x509.MarshalPKCS1PrivateKey(rsaKey)
					content = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: derKey})
				} else {
					return "", "", "", fmt.Errorf("Unsupported key type: %T", sshKey)
				}
			} else {
				derKey, err := x509.DecryptPEMBlock(pemKey, []byte(password)) //nolint:staticcheck
				if err != nil {
					return "", "", "", err
				}

				content = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: derKey})
			}
		}

		tlsClientKey = string(content)
	}

	return tlsClientCert, tlsClientKey, tlsClientCA, nil
}

// GenerateClientCertificate will generate the needed client.crt and client.key if needed.
func (c *Config) GenerateClientCertificate() error {
	if c.HasClientCertificate() {
		return nil
	}

	certf := c.ConfigPath("client.crt")
	keyf := c.ConfigPath("client.key")

	return localtls.FindOrGenCert(certf, keyf, true, false)
}

// CopyGlobalCert will copy global (system-wide) certificates to the user config path.
func (c *Config) CopyGlobalCert(src string, dst string) error {
	copyFile := func(oldPath string, newPath string, mode os.FileMode) error {
		sourceFile, err := os.Open(oldPath)
		if err != nil {
			return err
		}

		defer sourceFile.Close()

		// Get the mode from the source file if not specified.
		if mode == 0 {
			fInfo, err := sourceFile.Stat()
			if err != nil {
				return err
			}

			mode = fInfo.Mode()
		}

		// Create new file.
		newFile, err := os.Create(newPath)
		if err != nil {
			return err
		}

		defer newFile.Close()

		// Apply the file mode.
		err = newFile.Chmod(mode)
		if err != nil {
			return err
		}

		// Copy the content.
		_, err = io.Copy(newFile, sourceFile)
		if err != nil {
			return err
		}

		return nil
	}

	// Server certificate.
	oldPath := c.GlobalConfigPath("servercerts", fmt.Sprintf("%s.crt", src))
	if util.PathExists(oldPath) {
		newPath := c.ConfigPath("servercerts", fmt.Sprintf("%s.crt", dst))

		err := copyFile(oldPath, newPath, 0)
		if err != nil {
			return err
		}
	}

	// Client certificate.
	oldPath = c.GlobalConfigPath("clientcerts", fmt.Sprintf("%s.crt", src))
	if util.PathExists(oldPath) {
		newPath := c.ConfigPath("clientcerts", fmt.Sprintf("%s.crt", dst))

		err := copyFile(oldPath, newPath, 0)
		if err != nil {
			return err
		}
	}

	// Client key.
	oldPath = c.GlobalConfigPath("clientcerts", fmt.Sprintf("%s.key", src))
	if util.PathExists(oldPath) {
		newPath := c.ConfigPath("clientcerts", fmt.Sprintf("%s.key", dst))

		err := copyFile(oldPath, newPath, 0o600)
		if err != nil {
			return err
		}
	}

	// Client CA.
	oldPath = c.GlobalConfigPath("clientcerts", fmt.Sprintf("%s.ca", src))
	if util.PathExists(oldPath) {
		newPath := c.ConfigPath("clientcerts", fmt.Sprintf("%s.ca", dst))

		err := copyFile(oldPath, newPath, 0)
		if err != nil {
			return err
		}
	}

	return nil
}
