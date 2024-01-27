package cluster

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/lxc/incus/client"
	clusterRequest "github.com/lxc/incus/internal/server/cluster/request"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/request"
	"github.com/lxc/incus/internal/server/state"
	storagePools "github.com/lxc/incus/internal/server/storage"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/proxy"
	localtls "github.com/lxc/incus/shared/tls"
)

// Connect is a convenience around incus.ConnectIncus that configures the client
// with the correct parameters for node-to-node communication.
//
// If 'notify' switch is true, then the user agent will be set to the special
// to the UserAgentNotifier value, which can be used in some cases to distinguish
// between a regular client request and an internal cluster request.
func Connect(address string, networkCert *localtls.CertInfo, serverCert *localtls.CertInfo, r *http.Request, notify bool) (incus.InstanceServer, error) {
	// Wait for a connection to the events API first for non-notify connections.
	if !notify {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(10)*time.Second)
		defer cancel()
		err := EventListenerWait(ctx, address)
		if err != nil {
			return nil, fmt.Errorf("Missing event connection with target cluster member")
		}
	}

	args := &incus.ConnectionArgs{
		TLSServerCert: string(networkCert.PublicKey()),
		TLSClientCert: string(serverCert.PublicKey()),
		TLSClientKey:  string(serverCert.PrivateKey()),
		SkipGetServer: true,
		UserAgent:     version.UserAgent,
	}

	if notify {
		args.UserAgent = clusterRequest.UserAgentNotifier
	}

	if r != nil {
		proxy := func(req *http.Request) (*url.URL, error) {
			ctx := r.Context()

			val, ok := ctx.Value(request.CtxUsername).(string)
			if ok {
				req.Header.Add(request.HeaderForwardedUsername, val)
			}

			val, ok = ctx.Value(request.CtxProtocol).(string)
			if ok {
				req.Header.Add(request.HeaderForwardedProtocol, val)
			}

			req.Header.Add(request.HeaderForwardedAddress, r.RemoteAddr)

			return proxy.FromEnvironment(req)
		}

		args.Proxy = proxy
	}

	url := fmt.Sprintf("https://%s", address)
	return incus.ConnectIncus(url, args)
}

// ConnectIfInstanceIsRemote figures out the address of the cluster member which is running the instance with the
// given name in the specified project. If it's not the local member will connect to it and return the connected
// client (configured with the specified project), otherwise it will just return nil.
func ConnectIfInstanceIsRemote(s *state.State, projectName string, instName string, r *http.Request, instanceType instancetype.Type) (incus.InstanceServer, error) {
	// No need to connect if not clustered.
	if !s.ServerClustered {
		return nil, nil
	}

	var address string // Cluster member address.
	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		address, err = tx.GetNodeAddressOfInstance(ctx, projectName, instName, instanceType)
		return err
	})
	if err != nil {
		return nil, err
	}

	if address == "" {
		return nil, nil // The instance is running on this local member, no need to connect.
	}

	client, err := Connect(address, s.Endpoints.NetworkCert(), s.ServerCert(), r, false)
	if err != nil {
		return nil, err
	}

	client = client.UseProject(projectName)

	return client, nil
}

// ConnectIfVolumeIsRemote figures out the address of the cluster member on which the volume with the given name is
// defined. If it's not the local cluster member it will connect to it and return the connected client, otherwise
// it just returns nil. If there is more than one cluster member with a matching volume name, an error is returned.
func ConnectIfVolumeIsRemote(s *state.State, poolName string, projectName string, volumeName string, volumeType int, networkCert *localtls.CertInfo, serverCert *localtls.CertInfo, r *http.Request) (incus.InstanceServer, error) {
	localNodeID := s.DB.Cluster.GetNodeID()
	var err error
	var nodes []db.NodeInfo
	var poolID int64
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		poolID, err = tx.GetStoragePoolID(ctx, poolName)
		if err != nil {
			return err
		}

		nodes, err = tx.GetStorageVolumeNodes(ctx, poolID, projectName, volumeName, volumeType)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil && err != db.ErrNoClusterMember {
		return nil, err
	}

	// If volume uses a remote storage driver and so has no explicit cluster member, then we need to check
	// whether it is exclusively attached to remote instance, and if so then we need to forward the request to
	// the node whereit is currently used. This avoids conflicting with another member when using it locally.
	if err == db.ErrNoClusterMember {
		// GetStoragePoolVolume returns a volume with an empty Location field for remote drivers.
		var dbVolume *db.StorageVolume
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			dbVolume, err = tx.GetStoragePoolVolume(ctx, poolID, projectName, volumeType, volumeName, true)
			return err
		})
		if err != nil {
			return nil, err
		}

		remoteInstance, err := storagePools.VolumeUsedByExclusiveRemoteInstancesWithProfiles(s, poolName, projectName, &dbVolume.StorageVolume)
		if err != nil {
			return nil, fmt.Errorf("Failed checking if volume %q is available: %w", volumeName, err)
		}

		if remoteInstance != nil {
			var instNode db.NodeInfo
			err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				instNode, err = tx.GetNodeByName(ctx, remoteInstance.Node)
				return err
			})
			if err != nil {
				return nil, fmt.Errorf("Failed getting cluster member info for %q: %w", remoteInstance.Node, err)
			}

			// Replace node list with instance's cluster member node (which might be local member).
			nodes = []db.NodeInfo{instNode}
		} else {
			// Volume isn't exclusively attached to an instance. Use local cluster member.
			return nil, nil
		}
	}

	nodeCount := len(nodes)
	if nodeCount > 1 {
		return nil, fmt.Errorf("More than one cluster member has a volume named %q. Please target a specific member", volumeName)
	} else if nodeCount < 1 {
		// Should never get here.
		return nil, fmt.Errorf("Volume %q has empty cluster member list", volumeName)
	}

	node := nodes[0]
	if node.ID == localNodeID {
		// Use local cluster member if volume belongs to this local member.
		return nil, nil
	}

	// Connect to remote cluster member.
	return Connect(node.Address, networkCert, serverCert, r, false)
}

// SetupTrust is a convenience around InstanceServer.CreateCertificate that adds the given server certificate to
// the trusted pool of the cluster at the given address, using the given token. The certificate is added as
// type CertificateTypeServer to allow intra-member communication. If a certificate with the same fingerprint
// already exists with a different name or type, then no error is returned.
func SetupTrust(serverCert *localtls.CertInfo, serverName string, targetAddress string, targetCert string, targetToken string) error {
	// Connect to the target cluster node.
	args := &incus.ConnectionArgs{
		TLSServerCert: targetCert,
		UserAgent:     version.UserAgent,
	}

	target, err := incus.ConnectIncus(fmt.Sprintf("https://%s", targetAddress), args)
	if err != nil {
		return fmt.Errorf("Failed to connect to target cluster node %q: %w", targetAddress, err)
	}

	cert, err := localtls.GenerateTrustCertificate(serverCert, serverName)
	if err != nil {
		return fmt.Errorf("Failed generating trust certificate: %w", err)
	}

	post := api.CertificatesPost{
		CertificatePut: cert.CertificatePut,
		TrustToken:     targetToken,
	}

	err = target.CreateCertificate(post)
	if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
		return fmt.Errorf("Failed to add server cert to cluster: %w", err)
	}

	return nil
}

// UpdateTrust ensures that the supplied certificate is stored in the target trust store with the correct name
// and type to ensure correct cluster operation. Should be called after SetupTrust. If a certificate with the same
// fingerprint is already in the trust store, but is of the wrong type or name then the existing certificate is
// updated to the correct type and name. If the existing certificate is the correct type but the wrong name then an
// error is returned. And if the existing certificate is the correct type and name then nothing more is done.
func UpdateTrust(serverCert *localtls.CertInfo, serverName string, targetAddress string, targetCert string) error {
	// Connect to the target cluster node.
	args := &incus.ConnectionArgs{
		TLSClientCert: string(serverCert.PublicKey()),
		TLSClientKey:  string(serverCert.PrivateKey()),
		TLSServerCert: targetCert,
		UserAgent:     version.UserAgent,
	}

	target, err := incus.ConnectIncus(fmt.Sprintf("https://%s", targetAddress), args)
	if err != nil {
		return fmt.Errorf("Failed to connect to target cluster node %q: %w", targetAddress, err)
	}

	cert, err := localtls.GenerateTrustCertificate(serverCert, serverName)
	if err != nil {
		return fmt.Errorf("Failed generating trust certificate: %w", err)
	}

	existingCert, _, err := target.GetCertificate(cert.Fingerprint)
	if err != nil {
		return fmt.Errorf("Failed getting existing certificate: %w", err)
	}

	if existingCert.Name != serverName && existingCert.Type == api.CertificateTypeServer {
		// Don't alter an existing server certificate that has our fingerprint but not our name.
		// Something is wrong as this shouldn't happen.
		return fmt.Errorf("Existing server certificate with different name %q already in trust store", existingCert.Name)
	} else if existingCert.Name != serverName && existingCert.Type != api.CertificateTypeServer {
		// Ensure that if a client certificate already exists that matches our fingerprint, that it
		// has the correct name and type for cluster operation, to allow us to associate member
		// server names to certificate names.
		err = target.UpdateCertificate(cert.Fingerprint, cert.CertificatePut, "")
		if err != nil {
			return fmt.Errorf("Failed updating certificate name and type in trust store: %w", err)
		}
	}

	return nil
}

// HasConnectivity probes the member with the given address for connectivity.
func HasConnectivity(networkCert *localtls.CertInfo, serverCert *localtls.CertInfo, address string) bool {
	config, err := tlsClientConfig(networkCert, serverCert)
	if err != nil {
		return false
	}

	var conn net.Conn
	dialer := &net.Dialer{Timeout: time.Second}
	conn, err = tls.DialWithDialer(dialer, "tcp", address, config)
	if err == nil {
		_ = conn.Close()
		return true
	}

	return false
}
