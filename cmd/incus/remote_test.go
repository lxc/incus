package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/lxc/incus/v7/shared/cliconfig"
)

func TestRemoteGenerateCertificate(t *testing.T) {
	for _, name := range []string{"", "test"} {
		t.Run(name, func(t *testing.T) {
			conf := &cliconfig.Config{ConfigDir: t.TempDir(), Remotes: map[string]cliconfig.Remote{}}
			c := cmdRemoteGenerateCertificate{global: &cmdGlobal{conf: conf, flagQuiet: true}}
			cmd := c.command()
			args := []string{}
			certPath := conf.ConfigPath("client.crt")
			keyPath := conf.ConfigPath("client.key")
			if name != "" {
				args = append(args, name)
				certPath = conf.ConfigPath("clientcerts", name+".crt")
				keyPath = conf.ConfigPath("clientcerts", name+".key")
			}

			err := c.run(cmd, args)
			require.NoError(t, err)
			assert.Empty(t, conf.Remotes)

			cert, err := os.ReadFile(certPath)
			require.NoError(t, err)
			key, err := os.ReadFile(keyPath)
			require.NoError(t, err)
			pair, err := tls.X509KeyPair(cert, key)
			require.NoError(t, err)
			require.IsType(t, &ecdsa.PrivateKey{}, pair.PrivateKey)
			assert.Equal(t, elliptic.P384(), pair.PrivateKey.(*ecdsa.PrivateKey).Curve)

			if runtime.GOOS != "windows" {
				info, err := os.Stat(keyPath)
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
			}

			err = c.run(cmd, args)
			assert.EqualError(t, err, "A client certificate is already present")
			unchanged, err := os.ReadFile(keyPath)
			require.NoError(t, err)
			assert.Equal(t, key, unchanged)

			if name != "" {
				assert.NoFileExists(t, conf.ConfigPath("client.key"))
			}
		})
	}
}

func TestRemoteGenerateEncryptedCertificate(t *testing.T) {
	conf := &cliconfig.Config{
		ConfigDir: t.TempDir(),
		Remotes:   map[string]cliconfig.Remote{"test": {}},
		PromptPassword: func(_ string) (string, error) {
			return "test-password", nil
		},
	}

	c := cmdRemoteGenerateCertificate{global: &cmdGlobal{conf: conf}}
	certPath := conf.ConfigPath("clientcerts", "test.crt")
	keyPath := conf.ConfigPath("clientcerts", "test.key")
	err := c.generateCertificate(certPath, keyPath, "test-password")
	require.NoError(t, err)

	content, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	block, _ := pem.Decode(content)
	require.NotNil(t, block)
	assert.Equal(t, "OPENSSH PRIVATE KEY", block.Type)

	var header struct {
		Cipher  string
		KDF     string
		Options string
		Keys    uint32
		Rest    []byte `ssh:"rest"`
	}

	err = ssh.Unmarshal(block.Bytes[len("openssh-key-v1\x00"):], &header)
	require.NoError(t, err)
	assert.Equal(t, "aes256-ctr", header.Cipher)
	assert.Equal(t, "bcrypt", header.KDF)
	var options struct {
		Salt   string
		Rounds uint32
	}

	err = ssh.Unmarshal([]byte(header.Options), &options)
	require.NoError(t, err)
	assert.Equal(t, uint32(16), options.Rounds)

	cert, key, _, err := conf.GetClientCertificate("test")
	require.NoError(t, err)
	_, err = tls.X509KeyPair([]byte(cert), []byte(key))
	require.NoError(t, err)
}

func TestRemoteGenerateCertificateRefusesExistingFiles(t *testing.T) {
	for _, extension := range []string{"crt", "key"} {
		t.Run(extension, func(t *testing.T) {
			conf := &cliconfig.Config{ConfigDir: t.TempDir()}
			path := conf.ConfigPath("clientcerts", "test."+extension)
			err := os.MkdirAll(filepath.Dir(path), 0o750)
			require.NoError(t, err)
			err = os.WriteFile(path, []byte("existing credentials"), 0o600)
			require.NoError(t, err)
			c := cmdRemoteGenerateCertificate{global: &cmdGlobal{conf: conf, flagQuiet: true}}
			err = c.run(c.command(), []string{"test"})
			assert.EqualError(t, err, "A client certificate is already present")
			content, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, "existing credentials", string(content))
			files, err := os.ReadDir(filepath.Dir(path))
			require.NoError(t, err)
			assert.Len(t, files, 1)
		})
	}
}

func TestRemoteGenerateCertificateInvalidRemote(t *testing.T) {
	for _, name := range []string{"test", "test:", "test=other", "../other", "test" + string('\\') + "other"} {
		t.Run(name, func(t *testing.T) {
			conf := &cliconfig.Config{ConfigDir: t.TempDir(), Remotes: map[string]cliconfig.Remote{"test": {}}}
			c := cmdRemoteGenerateCertificate{global: &cmdGlobal{conf: conf, flagQuiet: true}}
			err := c.run(c.command(), []string{name})
			assert.Error(t, err)
			assert.NoDirExists(t, conf.ConfigPath("clientcerts"))
		})
	}
}

func TestRemoteGenerateCertificateWriteFailure(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "client.crt")
	keyPath := filepath.Join(dir, "client.key")
	err := os.WriteFile(certPath, []byte("existing certificate"), 0o644)
	require.NoError(t, err)
	c := cmdRemoteGenerateCertificate{}
	err = c.generateCertificate(certPath, keyPath, "")
	assert.Error(t, err)
	assert.NoFileExists(t, keyPath)
	content, err := os.ReadFile(certPath)
	require.NoError(t, err)
	assert.Equal(t, "existing certificate", string(content))
}
