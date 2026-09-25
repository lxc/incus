package cluster

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	cowsqldb "github.com/cowsql/go-cowsql/cluster/db"
	cowsqlstate "github.com/cowsql/go-cowsql/cluster/state"
	cowsqltls "github.com/cowsql/go-cowsql/cluster/tls"

	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/tls"
	"github.com/lxc/incus/v7/shared/util"
)

type stateWrapper struct {
	s func() *state.State
}

// NewClusterCertificate implements [state.State].
func (s *stateWrapper) NewClusterCertificate() (cowsqltls.CertInfo, error) {
	certDir := s.s().OS.VarDir

	for _, suffix := range []string{"ca", "crt", "key"} {
		certPath := filepath.Join(certDir, "cluster."+suffix)
		_, err := os.Stat(certPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("Failed to check for stale certificate path %q: %w", certPath, err)
		}

		if err != nil {
			continue
		}

		err = os.Remove(certPath)
		if err != nil {
			return nil, fmt.Errorf("Failed to remove stale certificate path %q: %w", certPath, err)
		}
	}

	// The cluster CA certificate is a symlink against the regular server CA certificate.
	if util.PathExists(filepath.Join(certDir, "server.ca")) {
		err := os.Symlink("server.ca", filepath.Join(certDir, "cluster.ca"))
		if err != nil {
			return nil, fmt.Errorf("Failed to symlink server CA cert to cluster CA cert: %w", err)
		}
	}

	certInfo, err := tls.KeyPairAndCA(certDir, "cluster", tls.CertServer, true)
	if err != nil {
		return nil, err
	}

	localState := s.s()
	if localState.Endpoints != nil {
		localState.Endpoints.NetworkUpdateCert(certInfo)
	}

	return certInfo, nil
}

// SetClusterCertificate implements [state.State].
func (s *stateWrapper) SetClusterCertificate(i cowsqltls.CertInfo) (cowsqltls.CertInfo, error) {
	var chain []byte
	for _, cert := range i.KeyPair().Certificate {
		chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})...)
	}

	certDir := s.s().OS.VarDir
	err := os.WriteFile(filepath.Join(certDir, "cluster.crt"), chain, 0o644)
	if err != nil {
		return nil, err
	}

	err = os.WriteFile(filepath.Join(certDir, "cluster.key"), i.PrivateKey(), 0o600)
	if err != nil {
		return nil, err
	}

	certInfo, err := tls.KeyPairAndCA(certDir, "cluster", tls.CertServer, true)
	if err != nil {
		return nil, err
	}

	localState := s.s()
	if localState.Endpoints != nil {
		localState.Endpoints.NetworkUpdateCert(certInfo)
	}

	return certInfo, nil
}

// ClusterAddress implements [state.State].
func (s *stateWrapper) ClusterAddress() string {
	localState := s.s()
	if localState.LocalConfig != nil {
		return localState.LocalConfig.ClusterAddress()
	}

	return ""
}

// HasListener implements [state.State].
func (s *stateWrapper) HasListener(_ context.Context) error {
	if s.s().Endpoints != nil {
		return nil
	}

	return errors.New("Network connectivity has not been set up")
}

// UpdateAuthenticator implements [state.State].
func (s *stateWrapper) UpdateAuthenticator(ctx context.Context) error {
	s.s().UpdateCertificateCache()
	return nil
}

// OnHeartbeatNotification implements [state.State].
func (s *stateWrapper) OnHeartbeatNotification(members map[int64]cowsqldb.HeartbeatMember) {
	localState := s.s()

	EventsUpdateListeners(localState, members, localState.Events.Inject)
}

func cowsqlState(f func() *state.State) cowsqlstate.State {
	return &stateWrapper{s: f}
}
