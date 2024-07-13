package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"

	"github.com/lxc/incus/v6/client"
	internalInstance "github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/internal/revert"
	"github.com/lxc/incus/v6/internal/server/acme"
	"github.com/lxc/incus/v6/internal/server/auth"
	"github.com/lxc/incus/v6/internal/server/certificate"
	"github.com/lxc/incus/v6/internal/server/cluster"
	clusterConfig "github.com/lxc/incus/v6/internal/server/cluster/config"
	clusterRequest "github.com/lxc/incus/v6/internal/server/cluster/request"
	"github.com/lxc/incus/v6/internal/server/db"
	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/operationtype"
	"github.com/lxc/incus/v6/internal/server/db/warningtype"
	"github.com/lxc/incus/v6/internal/server/instance"
	instanceDrivers "github.com/lxc/incus/v6/internal/server/instance/drivers"
	"github.com/lxc/incus/v6/internal/server/lifecycle"
	"github.com/lxc/incus/v6/internal/server/node"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/internal/server/scriptlet"
	"github.com/lxc/incus/v6/internal/server/state"
	storagePools "github.com/lxc/incus/v6/internal/server/storage"
	"github.com/lxc/incus/v6/internal/server/task"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/internal/server/warnings"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	apiScriptlet "github.com/lxc/incus/v6/shared/api/scriptlet"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/osarch"
	localtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/validate"
)

type evacuateStopFunc func(inst instance.Instance, action string) error
type evacuateMigrateFunc func(ctx context.Context, s *state.State, r *http.Request, inst instance.Instance, sourceMemberInfo *db.NodeInfo, targetMemberInfo *db.NodeInfo, live bool, startInstance bool, metadata map[string]any, op *operations.Operation) error

type evacuateOpts struct {
	s               *state.State
	gateway         *cluster.Gateway
	r               *http.Request
	instances       []instance.Instance
	mode            string
	srcMemberName   string
	stopInstance    evacuateStopFunc
	migrateInstance evacuateMigrateFunc
	op              *operations.Operation
}

var targetGroupPrefix = "@"

var clusterCmd = APIEndpoint{
	Path: "cluster",

	Get: APIEndpointAction{Handler: clusterGet, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanView)},
	Put: APIEndpointAction{Handler: clusterPut, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var clusterNodesCmd = APIEndpoint{
	Path: "cluster/members",

	Get:  APIEndpointAction{Handler: clusterNodesGet, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanView)},
	Post: APIEndpointAction{Handler: clusterNodesPost, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var clusterNodeCmd = APIEndpoint{
	Path: "cluster/members/{name}",

	Delete: APIEndpointAction{Handler: clusterNodeDelete, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
	Get:    APIEndpointAction{Handler: clusterNodeGet, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanView)},
	Patch:  APIEndpointAction{Handler: clusterNodePatch, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
	Put:    APIEndpointAction{Handler: clusterNodePut, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
	Post:   APIEndpointAction{Handler: clusterNodePost, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var clusterNodeStateCmd = APIEndpoint{
	Path: "cluster/members/{name}/state",

	Get:  APIEndpointAction{Handler: clusterNodeStateGet, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanView)},
	Post: APIEndpointAction{Handler: clusterNodeStatePost, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var clusterCertificateCmd = APIEndpoint{
	Path: "cluster/certificate",

	Put: APIEndpointAction{Handler: clusterCertificatePut, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var clusterGroupsCmd = APIEndpoint{
	Path: "cluster/groups",

	Get:  APIEndpointAction{Handler: clusterGroupsGet, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanView)},
	Post: APIEndpointAction{Handler: clusterGroupsPost, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var clusterGroupCmd = APIEndpoint{
	Path: "cluster/groups/{name}",

	Get:    APIEndpointAction{Handler: clusterGroupGet, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanView)},
	Post:   APIEndpointAction{Handler: clusterGroupPost, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
	Put:    APIEndpointAction{Handler: clusterGroupPut, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
	Patch:  APIEndpointAction{Handler: clusterGroupPatch, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
	Delete: APIEndpointAction{Handler: clusterGroupDelete, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var internalClusterAcceptCmd = APIEndpoint{
	Path: "cluster/accept",

	Post: APIEndpointAction{Handler: internalClusterPostAccept, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var internalClusterRebalanceCmd = APIEndpoint{
	Path: "cluster/rebalance",

	Post: APIEndpointAction{Handler: internalClusterPostRebalance, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var internalClusterAssignCmd = APIEndpoint{
	Path: "cluster/assign",

	Post: APIEndpointAction{Handler: internalClusterPostAssign, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var internalClusterHandoverCmd = APIEndpoint{
	Path: "cluster/handover",

	Post: APIEndpointAction{Handler: internalClusterPostHandover, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var internalClusterRaftNodeCmd = APIEndpoint{
	Path: "cluster/raft-node/{address}",

	Delete: APIEndpointAction{Handler: internalClusterRaftNodeDelete, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var internalClusterHealCmd = APIEndpoint{
	Path: "cluster/heal/{name}",

	Post: APIEndpointAction{Handler: internalClusterHeal, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

// swagger:operation GET /1.0/cluster cluster cluster_get
//
//	Get the cluster configuration
//
//	Gets the current cluster configuration.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Cluster configuration
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/Cluster"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()
	serverName := s.ServerName

	// If the name is set to the hard-coded default node name, then
	// clustering is not enabled.
	if serverName == "none" {
		serverName = ""
	}

	memberConfig, err := clusterGetMemberConfig(r.Context(), s.DB.Cluster)
	if err != nil {
		return response.SmartError(err)
	}

	// Sort the member config.
	sort.Slice(memberConfig, func(i, j int) bool {
		left := memberConfig[i]
		right := memberConfig[j]

		if left.Entity != right.Entity {
			return left.Entity < right.Entity
		}

		if left.Name != right.Name {
			return left.Name < right.Name
		}

		if left.Key != right.Key {
			return left.Key < right.Key
		}

		return left.Description < right.Description
	})

	cluster := api.Cluster{
		ServerName:   serverName,
		Enabled:      serverName != "",
		MemberConfig: memberConfig,
	}

	return response.SyncResponseETag(true, cluster, cluster)
}

// Fetch information about all node-specific configuration keys set on the
// storage pools and networks of this cluster.
func clusterGetMemberConfig(ctx context.Context, clusterDB *db.Cluster) ([]api.ClusterMemberConfigKey, error) {
	var pools map[string]map[string]string
	var networks map[string]map[string]string

	keys := []api.ClusterMemberConfigKey{}

	err := clusterDB.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		pools, err = tx.GetStoragePoolsLocalConfig(ctx)
		if err != nil {
			return fmt.Errorf("Failed to fetch storage pools configuration: %w", err)
		}

		networks, err = tx.GetNetworksLocalConfig(ctx)
		if err != nil {
			return fmt.Errorf("Failed to fetch networks configuration: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	for pool, config := range pools {
		for key := range config {
			if strings.HasPrefix(key, internalInstance.ConfigVolatilePrefix) {
				continue
			}

			key := api.ClusterMemberConfigKey{
				Entity:      "storage-pool",
				Name:        pool,
				Key:         key,
				Description: fmt.Sprintf("\"%s\" property for storage pool \"%s\"", key, pool),
			}

			keys = append(keys, key)
		}
	}

	for network, config := range networks {
		for key := range config {
			if strings.HasPrefix(key, internalInstance.ConfigVolatilePrefix) {
				continue
			}

			key := api.ClusterMemberConfigKey{
				Entity:      "network",
				Name:        network,
				Key:         key,
				Description: fmt.Sprintf("\"%s\" property for network \"%s\"", key, network),
			}

			keys = append(keys, key)
		}
	}

	return keys, nil
}

// Depending on the parameters passed and on local state this endpoint will
// either:
//
// - bootstrap a new cluster (if this node is not clustered yet)
// - request to join an existing cluster
// - disable clustering on a node
//
// The client is required to be trusted.

// swagger:operation PUT /1.0/cluster cluster cluster_put
//
//	Update the cluster configuration
//
//	Updates the entire cluster configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster
//	    description: Cluster configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterPut(d *Daemon, r *http.Request) response.Response {
	req := api.ClusterPut{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.ServerName == "" && req.Enabled {
		return response.BadRequest(fmt.Errorf("ServerName is required when enabling clustering"))
	}

	if req.ServerName != "" && !req.Enabled {
		return response.BadRequest(fmt.Errorf("ServerName must be empty when disabling clustering"))
	}

	if req.ServerName != "" && strings.HasPrefix(req.ServerName, targetGroupPrefix) {
		return response.BadRequest(fmt.Errorf("ServerName may not start with %q", targetGroupPrefix))
	}

	// Disable clustering.
	if !req.Enabled {
		return clusterPutDisable(d, r, req)
	}

	// Depending on the provided parameters we either bootstrap a brand new
	// cluster with this node as first node, or perform a request to join a
	// given cluster.
	if req.ClusterAddress == "" {
		return clusterPutBootstrap(d, r, req)
	}

	return clusterPutJoin(d, r, req)
}

func clusterPutBootstrap(d *Daemon, r *http.Request, req api.ClusterPut) response.Response {
	s := d.State()

	logger.Info("Bootstrapping cluster", logger.Ctx{"serverName": req.ServerName})

	run := func(op *operations.Operation) error {
		// Update server name.
		d.globalConfigMu.Lock()
		d.serverName = req.ServerName
		d.serverClustered = true
		d.globalConfigMu.Unlock()

		d.events.SetLocalLocation(d.serverName)

		// Refresh the state.
		s = d.State()

		// Start clustering tasks
		d.startClusterTasks()

		err := cluster.Bootstrap(s, d.gateway, req.ServerName)
		if err != nil {
			d.stopClusterTasks()
			return err
		}

		// Restart the networks.
		err = networkStartup(s)
		if err != nil {
			return err
		}

		s.Events.SendLifecycle(request.ProjectParam(r), lifecycle.ClusterEnabled.Event(req.ServerName, op.Requestor(), nil))

		return nil
	}

	resources := map[string][]api.URL{}
	resources["cluster"] = []api.URL{}

	// If there's no cluster.https_address set, but core.https_address is,
	// let's default to it.
	var err error
	var config *node.Config
	err = s.DB.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
		config, err = node.ConfigLoad(ctx, tx)
		if err != nil {
			return fmt.Errorf("Failed to fetch member configuration: %w", err)
		}

		localClusterAddress := config.ClusterAddress()
		if localClusterAddress != "" {
			return nil
		}

		localHTTPSAddress := config.HTTPSAddress()

		if internalUtil.IsWildCardAddress(localHTTPSAddress) {
			return fmt.Errorf("Cannot use wildcard core.https_address %q for cluster.https_address. Please specify a new cluster.https_address or core.https_address", localClusterAddress)
		}

		_, err = config.Patch(map[string]string{
			"cluster.https_address": localHTTPSAddress,
		})
		if err != nil {
			return fmt.Errorf("Copy core.https_address to cluster.https_address: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Update local config cache.
	d.globalConfigMu.Lock()
	d.localConfig = config
	d.globalConfigMu.Unlock()

	op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.ClusterBootstrap, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	// Add the cluster flag from the agent
	version.UserAgentFeatures([]string{"cluster"})

	return operations.OperationResponse(op)
}

func clusterPutJoin(d *Daemon, r *http.Request, req api.ClusterPut) response.Response {
	s := d.State()

	logger.Info("Joining cluster", logger.Ctx{"serverName": req.ServerName})

	// Make sure basic pre-conditions are met.
	if len(req.ClusterCertificate) == 0 {
		return response.BadRequest(fmt.Errorf("No target cluster member certificate provided"))
	}

	if s.ServerClustered {
		return response.BadRequest(fmt.Errorf("This server is already clustered"))
	}

	// The old pre 'clustering_join' join API approach is no longer supported.
	if req.ServerAddress == "" {
		return response.BadRequest(fmt.Errorf("No server address provided for this member"))
	}

	localHTTPSAddress := s.LocalConfig.HTTPSAddress()

	var config *node.Config

	if localHTTPSAddress == "" {
		// As the user always provides a server address, but no networking
		// was setup on this node, let's do the job and open the
		// port. We'll use the same address both for the REST API and
		// for clustering.

		// First try to listen to the provided address. If we fail, we
		// won't actually update the database config.
		err := s.Endpoints.NetworkUpdateAddress(req.ServerAddress)
		if err != nil {
			return response.SmartError(err)
		}

		err = s.DB.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
			config, err = node.ConfigLoad(ctx, tx)
			if err != nil {
				return fmt.Errorf("Failed to load cluster config: %w", err)
			}

			_, err = config.Patch(map[string]string{
				"core.https_address":    req.ServerAddress,
				"cluster.https_address": req.ServerAddress,
			})
			return err
		})
		if err != nil {
			return response.SmartError(err)
		}

		localHTTPSAddress = req.ServerAddress
	} else {
		// The user has previously set core.https_address and
		// is now providing a cluster address as well. If they
		// differ we need to listen to it.
		if !internalUtil.IsAddressCovered(req.ServerAddress, localHTTPSAddress) {
			err := s.Endpoints.ClusterUpdateAddress(req.ServerAddress)
			if err != nil {
				return response.SmartError(err)
			}

			localHTTPSAddress = req.ServerAddress
		}

		// Update the cluster.https_address config key.
		err := s.DB.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
			var err error

			config, err = node.ConfigLoad(ctx, tx)
			if err != nil {
				return fmt.Errorf("Failed to load cluster config: %w", err)
			}

			_, err = config.Patch(map[string]string{
				"cluster.https_address": localHTTPSAddress,
			})
			return err
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Update local config cache.
	d.globalConfigMu.Lock()
	d.localConfig = config
	d.globalConfigMu.Unlock()

	// Client parameters to connect to the target cluster node.
	serverCert := s.ServerCert()
	args := &incus.ConnectionArgs{
		TLSClientCert: string(serverCert.PublicKey()),
		TLSClientKey:  string(serverCert.PrivateKey()),
		TLSServerCert: string(req.ClusterCertificate),
		UserAgent:     version.UserAgent,
	}

	// Asynchronously join the cluster.
	run := func(op *operations.Operation) error {
		logger.Debug("Running cluster join operation")

		// If the user has provided a join token, setup the trust
		// relationship by adding our own certificate to the cluster.
		if req.ClusterToken != "" {
			err := cluster.SetupTrust(serverCert, req.ServerName, req.ClusterAddress, req.ClusterCertificate, req.ClusterToken)
			if err != nil {
				return fmt.Errorf("Failed to setup cluster trust: %w", err)
			}
		}

		// Now we are in the remote trust store, ensure our name and type are correct to allow the cluster
		// to associate our member name to the server certificate.
		err := cluster.UpdateTrust(serverCert, req.ServerName, req.ClusterAddress, req.ClusterCertificate)
		if err != nil {
			return fmt.Errorf("Failed to update cluster trust: %w", err)
		}

		// Connect to the target cluster node.
		client, err := incus.ConnectIncus(fmt.Sprintf("https://%s", req.ClusterAddress), args)
		if err != nil {
			return err
		}

		// As ServerAddress field is required to be set it means that we're using the new join API
		// introduced with the 'clustering_join' extension.
		// Connect to ourselves to initialize storage pools and networks using the API.
		localClient, err := incus.ConnectIncusUnix(d.os.GetUnixSocket(), &incus.ConnectionArgs{UserAgent: clusterRequest.UserAgentJoiner})
		if err != nil {
			return fmt.Errorf("Failed to connect to local server: %w", err)
		}

		revert := revert.New()
		defer revert.Fail()

		// Update server name.
		oldServerName := d.serverName
		d.globalConfigMu.Lock()
		d.serverName = req.ServerName
		d.serverClustered = true
		d.globalConfigMu.Unlock()
		revert.Add(func() {
			d.globalConfigMu.Lock()
			d.serverName = oldServerName
			d.serverClustered = false
			d.globalConfigMu.Unlock()

			d.events.SetLocalLocation(d.serverName)
		})

		d.events.SetLocalLocation(d.serverName)

		// Create all storage pools and networks.
		err = clusterInitMember(localClient, client, req.MemberConfig)
		if err != nil {
			return fmt.Errorf("Failed to initialize member: %w", err)
		}

		// Get all defined storage pools and networks, so they can be compared to the ones in the cluster.
		pools := []api.StoragePool{}
		networks := []api.InitNetworksProjectPost{}

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			poolNames, err := tx.GetStoragePoolNames(ctx)
			if err != nil && !response.IsNotFoundError(err) {
				return err
			}

			for _, name := range poolNames {
				_, pool, _, err := tx.GetStoragePoolInAnyState(ctx, name)
				if err != nil {
					return err
				}

				pools = append(pools, *pool)
			}

			// Get a list of projects for networks.
			var projects []dbCluster.Project

			projects, err = dbCluster.GetProjects(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed to load projects for networks: %w", err)
			}

			for _, p := range projects {
				networkNames, err := tx.GetNetworks(ctx, p.Name)
				if err != nil && !response.IsNotFoundError(err) {
					return err
				}

				for _, name := range networkNames {
					_, network, _, err := tx.GetNetworkInAnyState(ctx, p.Name, name)
					if err != nil {
						return err
					}

					internalNetwork := api.InitNetworksProjectPost{
						NetworksPost: api.NetworksPost{
							NetworkPut: network.NetworkPut,
							Name:       network.Name,
							Type:       network.Type,
						},
						Project: p.Name,
					}

					networks = append(networks, internalNetwork)
				}
			}

			return nil
		})
		if err != nil {
			return err
		}

		// Now request for this node to be added to the list of cluster nodes.
		info, err := clusterAcceptMember(client, req.ServerName, localHTTPSAddress, cluster.SchemaVersion, version.APIExtensionsCount(), pools, networks)
		if err != nil {
			return fmt.Errorf("Failed request to add member: %w", err)
		}

		// Update our TLS configuration using the returned cluster certificate.
		err = internalUtil.WriteCert(s.OS.VarDir, "cluster", []byte(req.ClusterCertificate), info.PrivateKey, nil)
		if err != nil {
			return fmt.Errorf("Failed to save cluster certificate: %w", err)
		}

		networkCert, err := internalUtil.LoadClusterCert(s.OS.VarDir)
		if err != nil {
			return fmt.Errorf("Failed to parse cluster certificate: %w", err)
		}

		s.Endpoints.NetworkUpdateCert(networkCert)

		// Add trusted certificates of other members to local trust store.
		trustedCerts, err := client.GetCertificates()
		if err != nil {
			return fmt.Errorf("Failed to get trusted certificates: %w", err)
		}

		for _, trustedCert := range trustedCerts {
			if trustedCert.Type == api.CertificateTypeServer {
				dbType, err := certificate.FromAPIType(trustedCert.Type)
				if err != nil {
					return err
				}

				// Store the certificate in the local database.
				dbCert := dbCluster.Certificate{
					Fingerprint: trustedCert.Fingerprint,
					Type:        dbType,
					Name:        trustedCert.Name,
					Certificate: trustedCert.Certificate,
					Restricted:  trustedCert.Restricted,
				}

				logger.Debugf("Adding certificate %q (%s) to local trust store", trustedCert.Name, trustedCert.Fingerprint)

				err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
					id, err := dbCluster.CreateCertificate(ctx, tx.Tx(), dbCert)
					if err != nil {
						return err
					}

					err = dbCluster.UpdateCertificateProjects(ctx, tx.Tx(), int(id), trustedCert.Projects)
					if err != nil {
						return err
					}

					return nil
				})
				if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
					return fmt.Errorf("Failed adding local trusted certificate %q (%s): %w", trustedCert.Name, trustedCert.Fingerprint, err)
				}
			}
		}

		// Update cached trusted certificates (this adds the server certificates we collected above) so that we are able to join.
		// Client and metric type certificates from the cluster we are joining will not be added until later.
		s.UpdateCertificateCache()

		// Update local setup and possibly join the raft dqlite cluster.
		nodes := make([]db.RaftNode, len(info.RaftNodes))
		for i, node := range info.RaftNodes {
			nodes[i].ID = node.ID
			nodes[i].Address = node.Address
			nodes[i].Role = db.RaftRole(node.Role)
		}

		err = cluster.Join(s, d.gateway, networkCert, serverCert, req.ServerName, nodes)
		if err != nil {
			return err
		}

		// Add the new node to the default cluster group.
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			err := tx.AddNodeToClusterGroup(ctx, "default", req.ServerName)
			if err != nil {
				return fmt.Errorf("Failed to add new member to the default cluster group: %w", err)
			}

			return nil
		})
		if err != nil {
			return err
		}

		// Start clustering tasks.
		d.startClusterTasks()
		revert.Add(func() { d.stopClusterTasks() })

		// Load the configuration.
		var nodeConfig *node.Config
		err = s.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
			var err error
			nodeConfig, err = node.ConfigLoad(ctx, tx)
			return err
		})
		if err != nil {
			return err
		}

		// Get the current (updated) config.
		var currentClusterConfig *clusterConfig.Config
		err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			currentClusterConfig, err = clusterConfig.Load(ctx, tx)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return err
		}

		d.globalConfigMu.Lock()
		d.localConfig = nodeConfig
		d.globalConfig = currentClusterConfig
		d.globalConfigMu.Unlock()

		existingConfigDump := currentClusterConfig.Dump()
		changes := make(map[string]string, len(existingConfigDump))
		for k, v := range existingConfigDump {
			changes[k] = v
		}

		err = doApi10UpdateTriggers(d, nil, changes, nodeConfig, currentClusterConfig)
		if err != nil {
			return err
		}

		// Refresh the state.
		s = d.State()

		// Start up networks so any post-join changes can be applied now that we have a Node ID.
		logger.Debug("Starting networks after cluster join")
		err = networkStartup(s)
		if err != nil {
			logger.Errorf("Failed starting networks: %v", err)
		}

		client, err = cluster.Connect(req.ClusterAddress, s.Endpoints.NetworkCert(), serverCert, r, true)
		if err != nil {
			return err
		}

		// Add the cluster flag from the agent
		version.UserAgentFeatures([]string{"cluster"})

		// Notify the leader of successful join, possibly triggering
		// role changes.
		_, _, err = client.RawQuery("POST", "/internal/cluster/rebalance", nil, "")
		if err != nil {
			logger.Warnf("Failed to trigger cluster rebalance: %v", err)
		}

		// Ensure all images are available after this node has joined.
		err = autoSyncImages(s.ShutdownCtx, s)
		if err != nil {
			logger.Warn("Failed to sync images")
		}

		// Update the cert cache again to add client and metric certs to the cache.
		s.UpdateCertificateCache()

		s.Events.SendLifecycle(request.ProjectParam(r), lifecycle.ClusterMemberAdded.Event(req.ServerName, op.Requestor(), nil))

		revert.Success()
		return nil
	}

	resources := map[string][]api.URL{}
	resources["cluster"] = []api.URL{}

	op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.ClusterJoin, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// clusterPutDisableMu is used to prevent the daemon from being replaced/stopped during removal from the
// cluster until such time as the request that initiated the removal has finished. This allows for self removal
// from the cluster when not the leader.
var clusterPutDisableMu sync.Mutex

// Disable clustering on a node.
func clusterPutDisable(d *Daemon, r *http.Request, req api.ClusterPut) response.Response {
	s := d.State()

	logger.Info("Disabling clustering", logger.Ctx{"serverName": req.ServerName})

	// Close the cluster database
	err := s.DB.Cluster.Close()
	if err != nil {
		return response.SmartError(err)
	}

	// Update our TLS configuration using our original certificate.
	for _, suffix := range []string{"crt", "key", "ca"} {
		path := filepath.Join(s.OS.VarDir, "cluster."+suffix)
		if !util.PathExists(path) {
			continue
		}

		err := os.Remove(path)
		if err != nil {
			return response.InternalError(err)
		}
	}

	networkCert, err := internalUtil.LoadCert(s.OS.VarDir)
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed to parse member certificate: %w", err))
	}

	// Reset the cluster database and make it local to this node.
	err = d.gateway.Reset(networkCert)
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(request.ProjectParam(r), lifecycle.ClusterDisabled.Event(req.ServerName, requestor, nil))

	// Stop database cluster connection.
	d.gateway.Kill()

	go func() {
		<-r.Context().Done() // Wait until request has finished.

		// Wait until we can acquire the lock. This way if another request is holding the lock we won't
		// replace/stop the daemon until that request has finished.
		clusterPutDisableMu.Lock()
		defer clusterPutDisableMu.Unlock()

		if d.systemdSocketActivated {
			logger.Info("Exiting daemon following removal from cluster")
			os.Exit(0)
		} else {
			logger.Info("Restarting daemon following removal from cluster")
			err = localUtil.ReplaceDaemon()
			if err != nil {
				logger.Error("Failed restarting daemon", logger.Ctx{"err": err})
			}
		}
	}()

	return response.ManualResponse(func(w http.ResponseWriter) error {
		err := response.EmptySyncResponse.Render(w)
		if err != nil {
			return err
		}

		// Send the response before replacing the daemon process.
		f, ok := w.(http.Flusher)
		if ok {
			f.Flush()
		} else {
			return fmt.Errorf("http.ResponseWriter is not type http.Flusher")
		}

		return nil
	})
}

// clusterInitMember initializes storage pools and networks on this member. We pass two client instances, one
// connected to ourselves (the joining member) and one connected to the target cluster member to join.
func clusterInitMember(d incus.InstanceServer, client incus.InstanceServer, memberConfig []api.ClusterMemberConfigKey) error {
	data := api.InitLocalPreseed{}

	// Fetch all pools currently defined in the cluster.
	pools, err := client.GetStoragePools()
	if err != nil {
		return fmt.Errorf("Failed to fetch information about cluster storage pools: %w", err)
	}

	// Merge the returned storage pools configs with the node-specific
	// configs provided by the user.
	for _, pool := range pools {
		// Skip pending pools.
		if pool.Status == "Pending" {
			continue
		}

		logger.Debugf("Populating init data for storage pool %q", pool.Name)

		post := api.StoragePoolsPost{
			StoragePoolPut: pool.StoragePoolPut,
			Driver:         pool.Driver,
			Name:           pool.Name,
		}

		// Delete config keys that are automatically populated by the daemon.
		delete(post.Config, "volatile.initial_source")
		delete(post.Config, "zfs.pool_name")

		// Apply the node-specific config supplied by the user.
		for _, config := range memberConfig {
			if config.Entity != "storage-pool" {
				continue
			}

			if config.Name != pool.Name {
				continue
			}

			if !slices.Contains(db.NodeSpecificStorageConfig, config.Key) {
				logger.Warnf("Ignoring config key %q for storage pool %q", config.Key, config.Name)
				continue
			}

			post.Config[config.Key] = config.Value
		}

		data.StoragePools = append(data.StoragePools, post)
	}

	projects, err := client.GetProjects()
	if err != nil {
		return fmt.Errorf("Failed to fetch project information about cluster networks: %w", err)
	}

	for _, p := range projects {
		if util.IsFalseOrEmpty(p.Config["features.networks"]) && p.Name != api.ProjectDefaultName {
			// Skip non-default projects that can't have their own networks so we don't try
			// and add the same default project networks twice.
			continue
		}

		// Request that the project be created first before the project specific networks.
		data.Projects = append(data.Projects, api.ProjectsPost{
			Name: p.Name,
			ProjectPut: api.ProjectPut{
				Description: p.Description,
				Config:      p.Config,
			},
		})

		// Fetch all project specific networks currently defined in the cluster for the project.
		networks, err := client.UseProject(p.Name).GetNetworks()
		if err != nil {
			return fmt.Errorf("Failed to fetch network information about cluster networks in project %q: %w", p.Name, err)
		}

		// Merge the returned networks configs with the node-specific configs provided by the user.
		for _, network := range networks {
			// Skip unmanaged or pending networks.
			if !network.Managed || network.Status != api.NetworkStatusCreated {
				continue
			}

			// OVN networks don't need local creation.
			if network.Type == "ovn" {
				continue
			}

			post := api.InitNetworksProjectPost{
				NetworksPost: api.NetworksPost{
					NetworkPut: network.NetworkPut,
					Name:       network.Name,
					Type:       network.Type,
				},
				Project: p.Name,
			}

			// Apply the node-specific config supplied by the user for networks in the default project.
			// At this time project specific networks don't have node specific config options.
			if p.Name == api.ProjectDefaultName {
				for _, config := range memberConfig {
					if config.Entity != "network" {
						continue
					}

					if config.Name != network.Name {
						continue
					}

					if !slices.Contains(db.NodeSpecificNetworkConfig, config.Key) {
						logger.Warnf("Ignoring config key %q for network %q in project %q", config.Key, config.Name, p.Name)
						continue
					}

					post.Config[config.Key] = config.Value
				}
			}

			data.Networks = append(data.Networks, post)
		}
	}

	err = d.ApplyServerPreseed(api.InitPreseed{Server: data})
	if err != nil {
		return fmt.Errorf("Failed to initialize storage pools and networks: %w", err)
	}

	return nil
}

// Perform a request to the /internal/cluster/accept endpoint to check if a new
// node can be accepted into the cluster and obtain joining information such as
// the cluster private certificate.
func clusterAcceptMember(client incus.InstanceServer, name string, address string, schema int, apiExt int, pools []api.StoragePool, networks []api.InitNetworksProjectPost) (*internalClusterPostAcceptResponse, error) {
	architecture, err := osarch.ArchitectureGetLocalID()
	if err != nil {
		return nil, err
	}

	req := internalClusterPostAcceptRequest{
		Name:         name,
		Address:      address,
		Schema:       schema,
		API:          apiExt,
		StoragePools: pools,
		Networks:     networks,
		Architecture: architecture,
	}

	info := &internalClusterPostAcceptResponse{}
	resp, _, err := client.RawQuery("POST", "/internal/cluster/accept", req, "")
	if err != nil {
		return nil, err
	}

	err = resp.MetadataAsStruct(&info)
	if err != nil {
		return nil, err
	}

	return info, nil
}

// swagger:operation GET /1.0/cluster/members cluster cluster_members_get
//
//  Get the cluster members
//
//  Returns a list of cluster members (URLs).
//
//  ---
//  produces:
//    - application/json
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/cluster/members/server01",
//                "/1.0/cluster/members/server02"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/cluster/members?recursion=1 cluster cluster_members_get_recursion1
//
//	Get the cluster members
//
//	Returns a list of cluster members (structs).
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of cluster members
//	          items:
//	            $ref: "#/definitions/ClusterMember"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterNodesGet(d *Daemon, r *http.Request) response.Response {
	recursion := localUtil.IsRecursionRequest(r)
	s := d.State()

	leaderAddress, err := d.gateway.LeaderAddress()
	if err != nil {
		return response.InternalError(err)
	}

	var raftNodes []db.RaftNode
	err = s.DB.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
		raftNodes, err = tx.GetRaftNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading RAFT nodes: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	var members []db.NodeInfo
	var membersInfo []api.ClusterMember
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		failureDomains, err := tx.GetFailureDomainsNames(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading failure domains names: %w", err)
		}

		memberFailureDomains, err := tx.GetNodesFailureDomains(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading member failure domains: %w", err)
		}

		members, err = tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		maxVersion, err := tx.GetNodeMaxVersion(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting max member version: %w", err)
		}

		args := db.NodeInfoArgs{
			LeaderAddress:        leaderAddress,
			FailureDomains:       failureDomains,
			MemberFailureDomains: memberFailureDomains,
			OfflineThreshold:     s.GlobalConfig.OfflineThreshold(),
			MaxMemberVersion:     maxVersion,
			RaftNodes:            raftNodes,
		}

		if recursion {
			membersInfo = make([]api.ClusterMember, 0, len(members))
			for i := range members {
				member, err := members[i].ToAPI(ctx, tx, args)
				if err != nil {
					return err
				}

				membersInfo = append(membersInfo, *member)
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if recursion {
		return response.SyncResponse(true, membersInfo)
	}

	urls := make([]string, 0, len(members))
	for _, member := range members {
		u := api.NewURL().Path(version.APIVersion, "cluster", "members", member.Name)
		urls = append(urls, u.String())
	}

	return response.SyncResponse(true, urls)
}

var clusterNodesPostMu sync.Mutex // Used to prevent races when creating cluster join tokens.

// swagger:operation POST /1.0/cluster/members cluster cluster_members_post
//
//	Request a join token
//
//	Requests a join token to add a cluster member.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster
//	    description: Cluster member add request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterMembersPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterNodesPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	req := api.ClusterMembersPost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if !s.ServerClustered {
		return response.BadRequest(fmt.Errorf("This server is not clustered"))
	}

	expiry, err := internalInstance.GetExpiry(time.Now(), s.GlobalConfig.ClusterJoinTokenExpiry())
	if err != nil {
		return response.BadRequest(err)
	}

	// Get target addresses for existing online members, so that it can be encoded into the join token so that
	// the joining member will not have to specify a joining address during the join process.
	// Use anonymous interface type to align with how the API response will be returned for consistency when
	// retrieving remote operations.
	onlineNodeAddresses := make([]any, 0)

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the nodes.
		members, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		// Filter to online members.
		for _, member := range members {
			if member.State == db.ClusterMemberStateEvacuated || member.IsOffline(s.GlobalConfig.OfflineThreshold()) {
				continue
			}

			onlineNodeAddresses = append(onlineNodeAddresses, member.Address)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if len(onlineNodeAddresses) < 1 {
		return response.InternalError(fmt.Errorf("There are no online cluster members"))
	}

	// Lock to prevent concurrent requests racing the operationsGetByType function and creating duplicates.
	// We have to do this because collecting all of the operations from existing cluster members can take time.
	clusterNodesPostMu.Lock()
	defer clusterNodesPostMu.Unlock()

	// Remove any existing join tokens for the requested cluster member, this way we only ever have one active
	// join token for each potential new member, and it has the most recent active members list for joining.
	// This also ensures any historically unused (but potentially published) join tokens are removed.
	ops, err := operationsGetByType(s, r, api.ProjectDefaultName, operationtype.ClusterJoinToken)
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed getting cluster join token operations: %w", err))
	}

	for _, op := range ops {
		if op.StatusCode != api.Running {
			continue // Tokens are single use, so if cancelled but not deleted yet its not available.
		}

		opServerName, ok := op.Metadata["serverName"]
		if !ok {
			continue
		}

		if opServerName == req.ServerName {
			// Join token operation matches requested server name, so lets cancel it.
			logger.Warn("Cancelling duplicate join token operation", logger.Ctx{"operation": op.ID, "serverName": opServerName})
			err = operationCancel(s, r, api.ProjectDefaultName, op)
			if err != nil {
				return response.InternalError(fmt.Errorf("Failed to cancel operation %q: %w", op.ID, err))
			}
		}
	}

	// Generate join secret for new member. This will be stored inside the join token operation and will be
	// supplied by the joining member (encoded inside the join token) which will allow us to lookup the correct
	// operation in order to validate the requested joining server name is correct and authorised.
	joinSecret, err := internalUtil.RandomHexString(32)
	if err != nil {
		return response.InternalError(err)
	}

	// Generate fingerprint of network certificate so joining member can automatically trust the correct
	// certificate when it is presented during the join process.
	fingerprint, err := localtls.CertFingerprintStr(string(s.Endpoints.NetworkPublicKey()))
	if err != nil {
		return response.InternalError(err)
	}

	meta := map[string]any{
		"serverName":  req.ServerName, // Add server name to allow validation of name during join process.
		"secret":      joinSecret,
		"fingerprint": fingerprint,
		"addresses":   onlineNodeAddresses,
		"expiresAt":   expiry,
	}

	resources := map[string][]api.URL{}
	resources["cluster"] = []api.URL{}

	op, err := operations.OperationCreate(s, api.ProjectDefaultName, operations.OperationClassToken, operationtype.ClusterJoinToken, resources, meta, nil, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	s.Events.SendLifecycle(request.ProjectParam(r), lifecycle.ClusterTokenCreated.Event("members", op.Requestor(), nil))

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/cluster/members/{name} cluster cluster_member_get
//
//	Get the cluster member
//
//	Gets a specific cluster member.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Cluster member
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/ClusterMember"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterNodeGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	leaderAddress, err := d.gateway.LeaderAddress()
	if err != nil {
		return response.InternalError(err)
	}

	var raftNodes []db.RaftNode
	err = s.DB.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
		raftNodes, err = tx.GetRaftNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading RAFT nodes: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	var memberInfo *api.ClusterMember
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		failureDomains, err := tx.GetFailureDomainsNames(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading failure domains names: %w", err)
		}

		memberFailureDomains, err := tx.GetNodesFailureDomains(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading member failure domains: %w", err)
		}

		member, err := tx.GetNodeByName(ctx, name)
		if err != nil {
			return err
		}

		maxVersion, err := tx.GetNodeMaxVersion(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting max member version: %w", err)
		}

		args := db.NodeInfoArgs{
			LeaderAddress:        leaderAddress,
			FailureDomains:       failureDomains,
			MemberFailureDomains: memberFailureDomains,
			OfflineThreshold:     s.GlobalConfig.OfflineThreshold(),
			MaxMemberVersion:     maxVersion,
			RaftNodes:            raftNodes,
		}

		memberInfo, err = member.ToAPI(ctx, tx, args)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, memberInfo, memberInfo.ClusterMemberPut)
}

// swagger:operation PATCH /1.0/cluster/members/{name} cluster cluster_member_patch
//
//	Partially update the cluster member
//
//	Updates a subset of the cluster member configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster
//	    description: Cluster member configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterMemberPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterNodePatch(d *Daemon, r *http.Request) response.Response {
	return updateClusterNode(d.State(), d.gateway, r, true)
}

// swagger:operation PUT /1.0/cluster/members/{name} cluster cluster_member_put
//
//	Update the cluster member
//
//	Updates the entire cluster member configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster
//	    description: Cluster member configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterMemberPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterNodePut(d *Daemon, r *http.Request) response.Response {
	return updateClusterNode(d.State(), d.gateway, r, false)
}

// updateClusterNode is shared between clusterNodePut and clusterNodePatch.
func updateClusterNode(s *state.State, gateway *cluster.Gateway, r *http.Request, isPatch bool) response.Response {
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	leaderAddress, err := gateway.LeaderAddress()
	if err != nil {
		return response.InternalError(err)
	}

	var raftNodes []db.RaftNode
	err = s.DB.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
		raftNodes, err = tx.GetRaftNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading RAFT nodes: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	var member db.NodeInfo
	var memberInfo *api.ClusterMember
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		failureDomains, err := tx.GetFailureDomainsNames(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading failure domains names: %w", err)
		}

		memberFailureDomains, err := tx.GetNodesFailureDomains(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading member failure domains: %w", err)
		}

		member, err = tx.GetNodeByName(ctx, name)
		if err != nil {
			return err
		}

		maxVersion, err := tx.GetNodeMaxVersion(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting max member version: %w", err)
		}

		args := db.NodeInfoArgs{
			LeaderAddress:        leaderAddress,
			FailureDomains:       failureDomains,
			MemberFailureDomains: memberFailureDomains,
			OfflineThreshold:     s.GlobalConfig.OfflineThreshold(),
			MaxMemberVersion:     maxVersion,
			RaftNodes:            raftNodes,
		}

		memberInfo, err = member.ToAPI(ctx, tx, args)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the request is fine
	err = localUtil.EtagCheck(r, memberInfo.ClusterMemberPut)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Parse the request
	req := api.ClusterMemberPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Validate the request
	if slices.Contains(memberInfo.Roles, string(db.ClusterRoleDatabase)) && !slices.Contains(req.Roles, string(db.ClusterRoleDatabase)) {
		return response.BadRequest(fmt.Errorf("The %q role cannot be dropped at this time", db.ClusterRoleDatabase))
	}

	if !slices.Contains(memberInfo.Roles, string(db.ClusterRoleDatabase)) && slices.Contains(req.Roles, string(db.ClusterRoleDatabase)) {
		return response.BadRequest(fmt.Errorf("The %q role cannot be added at this time", db.ClusterRoleDatabase))
	}

	// Nodes must belong to at least one group.
	if len(req.Groups) == 0 {
		return response.BadRequest(fmt.Errorf("Cluster members need to belong to at least one group"))
	}

	// Convert the roles.
	newRoles := make([]db.ClusterRole, 0, len(req.Roles))
	for _, role := range req.Roles {
		newRoles = append(newRoles, db.ClusterRole(role))
	}

	// Update the database
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		nodeInfo, err := tx.GetNodeByName(ctx, name)
		if err != nil {
			return fmt.Errorf("Loading node information: %w", err)
		}

		err = clusterValidateConfig(req.Config)
		if err != nil {
			return err
		}

		if isPatch {
			// Populate request config with current values.
			if req.Config == nil {
				req.Config = nodeInfo.Config
			} else {
				for k, v := range nodeInfo.Config {
					_, ok := req.Config[k]
					if !ok {
						req.Config[k] = v
					}
				}
			}
		}

		// Update node config.
		err = tx.UpdateNodeConfig(ctx, nodeInfo.ID, req.Config)
		if err != nil {
			return fmt.Errorf("Failed to update cluster member config: %w", err)
		}

		// Update the description.
		if req.Description != memberInfo.Description {
			err = tx.SetDescription(nodeInfo.ID, req.Description)
			if err != nil {
				return fmt.Errorf("Update description: %w", err)
			}
		}

		// Update the roles.
		err = tx.UpdateNodeRoles(nodeInfo.ID, newRoles)
		if err != nil {
			return fmt.Errorf("Update roles: %w", err)
		}

		err = tx.UpdateNodeFailureDomain(ctx, nodeInfo.ID, req.FailureDomain)
		if err != nil {
			return fmt.Errorf("Update failure domain: %w", err)
		}

		// Update the cluster groups.
		err = tx.UpdateNodeClusterGroups(ctx, nodeInfo.ID, req.Groups)
		if err != nil {
			return fmt.Errorf("Update cluster groups: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// If cluster roles changed, then distribute the info to all members.
	if s.Endpoints != nil && clusterRolesChanged(member.Roles, newRoles) {
		cluster.NotifyHeartbeat(s, gateway)
	}

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(request.ProjectParam(r), lifecycle.ClusterMemberUpdated.Event(name, requestor, nil))

	return response.EmptySyncResponse
}

// clusterRolesChanged checks whether the non-internal roles have changed between oldRoles and newRoles.
func clusterRolesChanged(oldRoles []db.ClusterRole, newRoles []db.ClusterRole) bool {
	// Build list of external-only roles from the newRoles list (excludes internal roles added by raft).
	newExternalRoles := make([]db.ClusterRole, 0, len(newRoles))
	for _, r := range newRoles {
		// Check list of known external roles.
		for _, externalRole := range db.ClusterRoles {
			if r == externalRole {
				newExternalRoles = append(newExternalRoles, r) // Found external role.
				break
			}
		}
	}

	for _, r := range oldRoles {
		if !cluster.RoleInSlice(r, newExternalRoles) {
			return true
		}
	}

	for _, r := range newExternalRoles {
		if !cluster.RoleInSlice(r, oldRoles) {
			return true
		}
	}

	return false
}

// clusterValidateConfig validates the configuration keys/values for cluster members.
func clusterValidateConfig(config map[string]string) error {
	clusterConfigKeys := map[string]func(value string) error{
		// gendoc:generate(entity=cluster, group=cluster, key=scheduler.instance)
		// Possible values are `all`, `manual`, and `group`. See
		// {ref}`clustering-instance-placement` for more information.
		// ---
		//  type: string
		//  defaultdesc: `all`
		//  shortdesc: Controls how instances are scheduled to run on this member
		"scheduler.instance": validate.Optional(validate.IsOneOf("all", "group", "manual")),
	}

	for k, v := range config {
		// User keys are free for all.

		// gendoc:generate(entity=cluster, group=cluster, key=user.*)
		// User keys can be used in search.
		// ---
		//  type: string
		//  shortdesc: Free form user key/value storage
		if strings.HasPrefix(k, "user.") {
			continue
		}

		validator, ok := clusterConfigKeys[k]
		if !ok {
			return fmt.Errorf("Invalid cluster configuration key %q", k)
		}

		err := validator(v)
		if err != nil {
			return fmt.Errorf("Invalid cluster configuration key %q value", k)
		}
	}

	return nil
}

// swagger:operation POST /1.0/cluster/members/{name} cluster cluster_member_post
//
//	Rename the cluster member
//
//	Renames an existing cluster member.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster
//	    description: Cluster member rename request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterMemberPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterNodePost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	memberName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Forward request.
	resp := forwardedResponseToNode(s, r, memberName)
	if resp != nil {
		return resp
	}

	req := api.ClusterMemberPost{}

	// Parse the request
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.RenameNode(ctx, memberName, req.ServerName)
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Update local server name.
	d.globalConfigMu.Lock()
	d.serverName = req.ServerName
	d.globalConfigMu.Unlock()

	d.events.SetLocalLocation(d.serverName)

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(request.ProjectParam(r), lifecycle.ClusterMemberRenamed.Event(req.ServerName, requestor, logger.Ctx{"old_name": memberName}))

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/cluster/members/{name} cluster cluster_member_delete
//
//	Delete the cluster member
//
//	Removes the member from the cluster.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterNodeDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	force, err := strconv.Atoi(r.FormValue("force"))
	if err != nil {
		force = 0
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Redirect all requests to the leader, which is the one with
	// knowing what nodes are part of the raft cluster.
	localClusterAddress := s.LocalConfig.ClusterAddress()

	leader, err := d.gateway.LeaderAddress()
	if err != nil {
		return response.InternalError(err)
	}

	var localInfo, leaderInfo db.NodeInfo
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		localInfo, err = tx.GetNodeByAddress(ctx, localClusterAddress)
		if err != nil {
			return fmt.Errorf("Failed loading local member info %q: %w", localClusterAddress, err)
		}

		leaderInfo, err = tx.GetNodeByAddress(ctx, leader)
		if err != nil {
			return fmt.Errorf("Failed loading leader member info %q: %w", leader, err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Get information about the cluster.
	var nodes []db.RaftNode
	err = s.DB.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
		var err error
		nodes, err = tx.GetRaftNodes(ctx)
		return err
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Unable to get raft nodes: %w", err))
	}

	if localClusterAddress != leader {
		if localInfo.Name == name {
			// If the member being removed is ourselves and we are not the leader, then lock the
			// clusterPutDisableMu before we forward the request to the leader, so that when the leader
			// goes on to request clusterPutDisable back to ourselves it won't be actioned until we
			// have returned this request back to the original client.
			clusterPutDisableMu.Lock()
			logger.Info("Acquired cluster self removal lock", logger.Ctx{"member": localInfo.Name})

			go func() {
				<-r.Context().Done() // Wait until request is finished.

				logger.Info("Releasing cluster self removal lock", logger.Ctx{"member": localInfo.Name})
				clusterPutDisableMu.Unlock()
			}()
		}

		logger.Debugf("Redirect member delete request to %s", leader)
		client, err := cluster.Connect(leader, s.Endpoints.NetworkCert(), s.ServerCert(), r, false)
		if err != nil {
			return response.SmartError(err)
		}

		err = client.DeleteClusterMember(name, force == 1)
		if err != nil {
			return response.SmartError(err)
		}

		// If we are the only remaining node, wait until promotion to leader,
		// then update cluster certs.
		if name == leaderInfo.Name && len(nodes) == 2 {
			err = d.gateway.WaitLeadership()
			if err != nil {
				return response.SmartError(err)
			}

			s.UpdateCertificateCache()
		}

		return response.ManualResponse(func(w http.ResponseWriter) error {
			err := response.EmptySyncResponse.Render(w)
			if err != nil {
				return err
			}

			// Send the response before replacing the daemon process.
			f, ok := w.(http.Flusher)
			if ok {
				f.Flush()
			} else {
				return fmt.Errorf("http.ResponseWriter is not type http.Flusher")
			}

			return nil
		})
	}

	// Get lock now we are on leader.
	d.clusterMembershipMutex.Lock()
	defer d.clusterMembershipMutex.Unlock()

	// If we are removing the leader of a 2 node cluster, ensure the other node can be a leader.
	if name == leaderInfo.Name && len(nodes) == 2 {
		for i := range nodes {
			if nodes[i].Address != leader && nodes[i].Role != db.RaftVoter {
				// Promote the remaining node.
				nodes[i].Role = db.RaftVoter
				err := changeMemberRole(s, r, nodes[i].Address, nodes)
				if err != nil {
					return response.SmartError(fmt.Errorf("Unable to promote remaining cluster member to leader: %w", err))
				}

				break
			}
		}
	}

	logger.Info("Deleting member from cluster", logger.Ctx{"name": name, "force": force})

	err = autoSyncImages(s.ShutdownCtx, s)
	if err != nil {
		if force == 0 {
			return response.SmartError(fmt.Errorf("Failed to sync images: %w", err))
		}

		// If force is set, only show a warning instead of returning an error.
		logger.Warn("Failed to sync images")
	}

	// First check that the node is clear from containers and images and
	// make it leave the database cluster, if it's part of it.
	address, err := cluster.Leave(s, d.gateway, name, force == 1)
	if err != nil {
		return response.SmartError(err)
	}

	if force != 1 {
		// Try to gracefully delete all networks and storage pools on it.
		// Delete all networks on this node
		client, err := cluster.Connect(address, s.Endpoints.NetworkCert(), s.ServerCert(), r, true)
		if err != nil {
			return response.SmartError(err)
		}

		// Get a list of projects for networks.
		var networkProjectNames []string

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			networkProjectNames, err = dbCluster.GetProjectNames(ctx, tx.Tx())
			return err
		})
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to load projects for networks: %w", err))
		}

		for _, networkProjectName := range networkProjectNames {
			var networks []string

			err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
				networks, err = tx.GetNetworks(ctx, networkProjectName)

				return err
			})
			if err != nil {
				return response.SmartError(err)
			}

			for _, name := range networks {
				err := client.UseProject(networkProjectName).DeleteNetwork(name)
				if err != nil {
					return response.SmartError(err)
				}
			}
		}

		var pools []string

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Delete all the pools on this node
			pools, err = tx.GetStoragePoolNames(ctx)

			return err
		})
		if err != nil && !response.IsNotFoundError(err) {
			return response.SmartError(err)
		}

		for _, name := range pools {
			err := client.DeleteStoragePool(name)
			if err != nil {
				return response.SmartError(err)
			}
		}
	}

	// Remove node from the database
	err = cluster.Purge(s.DB.Cluster, name)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to remove member from database: %w", err))
	}

	err = rebalanceMemberRoles(s, d.gateway, r, nil)
	if err != nil {
		logger.Warnf("Failed to rebalance dqlite nodes: %v", err)
	}

	// If this leader node removed itself, just disable clustering.
	if address == localClusterAddress {
		return clusterPutDisable(d, r, api.ClusterPut{})
	} else if force != 1 {
		// Try to gracefully reset the database on the node.
		client, err := cluster.Connect(address, s.Endpoints.NetworkCert(), s.ServerCert(), r, true)
		if err != nil {
			return response.SmartError(err)
		}

		put := api.ClusterPut{}
		put.Enabled = false
		_, err = client.UpdateCluster(put, "")
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to cleanup the member: %w", err))
		}
	}

	// Refresh the trusted certificate cache now that the member certificate has been removed.
	// We do not need to notify the other members here because the next heartbeat will trigger member change
	// detection and updateCertificateCache is called as part of that.
	s.UpdateCertificateCache()

	// Ensure all images are available after this node has been deleted.
	err = autoSyncImages(s.ShutdownCtx, s)
	if err != nil {
		logger.Warn("Failed to sync images")
	}

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(request.ProjectParam(r), lifecycle.ClusterMemberRemoved.Event(name, requestor, nil))

	return response.EmptySyncResponse
}

// swagger:operation PUT /1.0/cluster/certificate cluster clustering_update_cert
//
//	Update the certificate for the cluster
//
//	Replaces existing cluster certificate and reloads each cluster member.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster
//	    description: Cluster certificate replace request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterCertificatePut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterCertificatePut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	req := api.ClusterCertificatePut{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	certBytes := []byte(req.ClusterCertificate)
	keyBytes := []byte(req.ClusterCertificateKey)

	certBlock, _ := pem.Decode(certBytes)
	if certBlock == nil {
		return response.BadRequest(fmt.Errorf("Certificate must be base64 encoded PEM certificate: %w", err))
	}

	keyBlock, _ := pem.Decode(keyBytes)
	if keyBlock == nil {
		return response.BadRequest(fmt.Errorf("Private key must be base64 encoded PEM key: %w", err))
	}

	err = updateClusterCertificate(r.Context(), s, d.gateway, r, req)
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(request.ProjectParam(r), lifecycle.ClusterCertificateUpdated.Event("certificate", requestor, nil))

	return response.EmptySyncResponse
}

func updateClusterCertificate(ctx context.Context, s *state.State, gateway *cluster.Gateway, r *http.Request, req api.ClusterCertificatePut) error {
	revert := revert.New()
	defer revert.Fail()

	newClusterCertFilename := internalUtil.VarPath(acme.ClusterCertFilename)

	// First node forwards request to all other cluster nodes
	if r == nil || !isClusterNotification(r) {
		var err error

		revert.Add(func() {
			_ = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.UpsertWarningLocalNode(ctx, "", -1, -1, warningtype.UnableToUpdateClusterCertificate, err.Error())
			})
		})

		oldCertBytes, err := os.ReadFile(internalUtil.VarPath("cluster.crt"))
		if err != nil {
			return err
		}

		keyBytes, err := os.ReadFile(internalUtil.VarPath("cluster.key"))
		if err != nil {
			return err
		}

		oldReq := api.ClusterCertificatePut{
			ClusterCertificate:    string(oldCertBytes),
			ClusterCertificateKey: string(keyBytes),
		}

		// Get all members in cluster.
		var members []db.NodeInfo
		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			members, err = tx.GetNodes(ctx)
			if err != nil {
				return fmt.Errorf("Failed getting cluster members: %w", err)
			}

			return nil
		})
		if err != nil {
			return err
		}

		localClusterAddress := s.LocalConfig.ClusterAddress()

		revert.Add(func() {
			// If distributing the new certificate fails, store the certificate. This new file will
			// be considered when running the auto renewal again.
			err := os.WriteFile(newClusterCertFilename, []byte(req.ClusterCertificate), 0600)
			if err != nil {
				logger.Error("Failed storing new certificate", logger.Ctx{"err": err})
			}
		})

		newCertInfo, err := localtls.KeyPairFromRaw([]byte(req.ClusterCertificate), []byte(req.ClusterCertificateKey))
		if err != nil {
			return err
		}

		var client incus.InstanceServer

		for i := range members {
			member := members[i]

			if member.Address == localClusterAddress {
				continue
			}

			client, err = cluster.Connect(member.Address, s.Endpoints.NetworkCert(), s.ServerCert(), r, true)
			if err != nil {
				return err
			}

			err = client.UpdateClusterCertificate(req, "")
			if err != nil {
				return err
			}

			// When reverting the certificate, we need to connect to the cluster members using the
			// new certificate otherwise we'll get a bad certificate error.
			revert.Add(func() {
				client, err := cluster.Connect(member.Address, newCertInfo, s.ServerCert(), r, true)
				if err != nil {
					logger.Error("Failed to connect to cluster member", logger.Ctx{"address": member.Address, "err": err})
					return
				}

				err = client.UpdateClusterCertificate(oldReq, "")
				if err != nil {
					logger.Error("Failed to update cluster certificate on cluster member", logger.Ctx{"address": member.Address, "err": err})
				}
			})
		}
	}

	err := internalUtil.WriteCert(s.OS.VarDir, "cluster", []byte(req.ClusterCertificate), []byte(req.ClusterCertificateKey), nil)
	if err != nil {
		return err
	}

	if util.PathExists(newClusterCertFilename) {
		err := os.Remove(newClusterCertFilename)
		if err != nil {
			return fmt.Errorf("Failed to remove cluster certificate: %w", err)
		}
	}

	// Get the new cluster certificate struct
	cert, err := internalUtil.LoadClusterCert(s.OS.VarDir)
	if err != nil {
		return err
	}

	// Update the certificate on the network endpoint and gateway
	s.Endpoints.NetworkUpdateCert(cert)
	gateway.NetworkUpdateCert(cert)

	// Resolve warning of this type
	_ = warnings.ResolveWarningsByLocalNodeAndType(s.DB.Cluster, warningtype.UnableToUpdateClusterCertificate)

	revert.Success()

	return nil
}

func internalClusterPostAccept(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	req := internalClusterPostAcceptRequest{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	// Redirect all requests to the leader, which is the one with
	// knowning what nodes are part of the raft cluster.
	localClusterAddress := s.LocalConfig.ClusterAddress()

	leader, err := d.gateway.LeaderAddress()
	if err != nil {
		return response.InternalError(err)
	}

	if localClusterAddress != leader {
		logger.Debugf("Redirect member accept request to %s", leader)

		if leader == "" {
			return response.SmartError(fmt.Errorf("Unable to find leader address"))
		}

		url := &url.URL{
			Scheme: "https",
			Path:   "/internal/cluster/accept",
			Host:   leader,
		}

		return response.SyncResponseRedirect(url.String())
	}

	// Get lock now we are on leader.
	d.clusterMembershipMutex.Lock()
	defer d.clusterMembershipMutex.Unlock()

	// Make sure we have all the expected storage pools.
	err = clusterCheckStoragePoolsMatch(r.Context(), s.DB.Cluster, req.StoragePools)
	if err != nil {
		return response.SmartError(err)
	}

	// Make sure we have all the expected networks.
	err = clusterCheckNetworksMatch(r.Context(), s.DB.Cluster, req.Networks)
	if err != nil {
		return response.SmartError(err)
	}

	nodes, err := cluster.Accept(s, d.gateway, req.Name, req.Address, req.Schema, req.API, req.Architecture)
	if err != nil {
		return response.BadRequest(err)
	}

	accepted := internalClusterPostAcceptResponse{
		RaftNodes:  make([]internalRaftNode, len(nodes)),
		PrivateKey: s.Endpoints.NetworkPrivateKey(),
	}

	for i, node := range nodes {
		accepted.RaftNodes[i].ID = node.ID
		accepted.RaftNodes[i].Address = node.Address
		accepted.RaftNodes[i].Role = int(node.Role)
	}

	return response.SyncResponse(true, accepted)
}

// A request for the /internal/cluster/accept endpoint.
type internalClusterPostAcceptRequest struct {
	Name         string                        `json:"name" yaml:"name"`
	Address      string                        `json:"address" yaml:"address"`
	Schema       int                           `json:"schema" yaml:"schema"`
	API          int                           `json:"api" yaml:"api"`
	StoragePools []api.StoragePool             `json:"storage_pools" yaml:"storage_pools"`
	Networks     []api.InitNetworksProjectPost `json:"networks" yaml:"networks"`
	Architecture int                           `json:"architecture" yaml:"architecture"`
}

// A Response for the /internal/cluster/accept endpoint.
type internalClusterPostAcceptResponse struct {
	RaftNodes  []internalRaftNode `json:"raft_nodes" yaml:"raft_nodes"`
	PrivateKey []byte             `json:"private_key" yaml:"private_key"`
}

// Represent a node that is part of the dqlite raft cluster.
type internalRaftNode struct {
	ID      uint64 `json:"id" yaml:"id"`
	Address string `json:"address" yaml:"address"`
	Role    int    `json:"role" yaml:"role"`
	Name    string `json:"name" yaml:"name"`
}

// Used to update the cluster after a database node has been removed, and
// possibly promote another one as database node.
func internalClusterPostRebalance(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Redirect all requests to the leader, which is the one with with
	// up-to-date knowledge of what nodes are part of the raft cluster.
	localClusterAddress := s.LocalConfig.ClusterAddress()

	leader, err := d.gateway.LeaderAddress()
	if err != nil {
		return response.InternalError(err)
	}

	if localClusterAddress != leader {
		logger.Debugf("Redirect cluster rebalance request to %s", leader)
		url := &url.URL{
			Scheme: "https",
			Path:   "/internal/cluster/rebalance",
			Host:   leader,
		}

		return response.SyncResponseRedirect(url.String())
	}

	// Get lock now we are on leader.
	d.clusterMembershipMutex.Lock()
	defer d.clusterMembershipMutex.Unlock()

	err = rebalanceMemberRoles(s, d.gateway, r, nil)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, nil)
}

// Check if there's a dqlite node whose role should be changed, and post a
// change role request if so.
func rebalanceMemberRoles(s *state.State, gateway *cluster.Gateway, r *http.Request, unavailableMembers []string) error {
	if s.ShutdownCtx.Err() != nil {
		return nil
	}

again:
	address, nodes, err := cluster.Rebalance(s, gateway, unavailableMembers)
	if err != nil {
		return err
	}

	if address == "" {
		// Nothing to do.
		return nil
	}

	// Process demotions of offline nodes immediately.
	for _, node := range nodes {
		if node.Address != address || node.Role != db.RaftSpare {
			continue
		}

		if cluster.HasConnectivity(s.Endpoints.NetworkCert(), s.ServerCert(), address) {
			break
		}

		logger.Info("Demoting offline member during rebalance", logger.Ctx{"candidateAddress": node.Address})
		err := gateway.DemoteOfflineNode(node.ID)
		if err != nil {
			return fmt.Errorf("Demote offline node %s: %w", node.Address, err)
		}

		goto again
	}

	// Tell the node to promote itself.
	logger.Info("Promoting member during rebalance", logger.Ctx{"candidateAddress": address})
	err = changeMemberRole(s, r, address, nodes)
	if err != nil {
		return err
	}

	goto again
}

// Check if there are nodes not part of the raft configuration and add them in
// case.
func upgradeNodesWithoutRaftRole(s *state.State, gateway *cluster.Gateway) error {
	if s.ShutdownCtx.Err() != nil {
		return nil
	}

	var members []db.NodeInfo
	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		members, err = tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	return cluster.UpgradeMembersWithoutRole(gateway, members)
}

// Post a change role request to the member with the given address. The nodes
// slice contains details about all members, including the one being changed.
func changeMemberRole(s *state.State, r *http.Request, address string, nodes []db.RaftNode) error {
	post := &internalClusterPostAssignRequest{}
	for _, node := range nodes {
		post.RaftNodes = append(post.RaftNodes, internalRaftNode{
			ID:      node.ID,
			Address: node.Address,
			Role:    int(node.Role),
			Name:    node.Name,
		})
	}

	client, err := cluster.Connect(address, s.Endpoints.NetworkCert(), s.ServerCert(), r, true)
	if err != nil {
		return err
	}

	_, _, err = client.RawQuery("POST", "/internal/cluster/assign", post, "")
	if err != nil {
		return err
	}

	return nil
}

// Try to handover the role of this member to another one.
func handoverMemberRole(s *state.State, gateway *cluster.Gateway) error {
	// If we aren't clustered, there's nothing to do.
	if !s.ServerClustered {
		return nil
	}

	// Figure out our own cluster address.
	localClusterAddress := s.LocalConfig.ClusterAddress()

	post := &internalClusterPostHandoverRequest{
		Address: localClusterAddress,
	}

	logCtx := logger.Ctx{"address": localClusterAddress}

	// Find the cluster leader.
findLeader:
	leader, err := gateway.LeaderAddress()
	if err != nil {
		return err
	}

	if leader == "" {
		return fmt.Errorf("No leader address found")
	}

	if leader == localClusterAddress {
		logger.Info("Transferring leadership", logCtx)
		err := gateway.TransferLeadership()
		if err != nil {
			return fmt.Errorf("Failed to transfer leadership: %w", err)
		}

		goto findLeader
	}

	logger.Info("Handing over cluster member role", logCtx)
	client, err := cluster.Connect(leader, s.Endpoints.NetworkCert(), s.ServerCert(), nil, true)
	if err != nil {
		return fmt.Errorf("Failed handing over cluster member role: %w", err)
	}

	_, _, err = client.RawQuery("POST", "/internal/cluster/handover", post, "")
	if err != nil {
		return err
	}

	return nil
}

// Used to assign a new role to a the local dqlite node.
func internalClusterPostAssign(d *Daemon, r *http.Request) response.Response {
	s := d.State()
	req := internalClusterPostAssignRequest{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if len(req.RaftNodes) == 0 {
		return response.BadRequest(fmt.Errorf("No raft members provided"))
	}

	nodes := make([]db.RaftNode, len(req.RaftNodes))
	for i, node := range req.RaftNodes {
		nodes[i].ID = node.ID
		nodes[i].Address = node.Address
		nodes[i].Role = db.RaftRole(node.Role)
		nodes[i].Name = node.Name
	}

	err = cluster.Assign(s, d.gateway, nodes)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, nil)
}

// A request for the /internal/cluster/assign endpoint.
type internalClusterPostAssignRequest struct {
	RaftNodes []internalRaftNode `json:"raft_nodes" yaml:"raft_nodes"`
}

// Used to to transfer the responsibilities of a member to another one.
func internalClusterPostHandover(d *Daemon, r *http.Request) response.Response {
	s := d.State()
	req := internalClusterPostHandoverRequest{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.Address == "" {
		return response.BadRequest(fmt.Errorf("No id provided"))
	}

	// Redirect all requests to the leader, which is the one with
	// authoritative knowledge of the current raft configuration.
	localClusterAddress := s.LocalConfig.ClusterAddress()

	leader, err := d.gateway.LeaderAddress()
	if err != nil {
		return response.InternalError(err)
	}

	if leader == "" {
		return response.SmartError(fmt.Errorf("No leader address found"))
	}

	if localClusterAddress != leader {
		logger.Debugf("Redirect handover request to %s", leader)
		url := &url.URL{
			Scheme: "https",
			Path:   "/internal/cluster/handover",
			Host:   leader,
		}

		return response.SyncResponseRedirect(url.String())
	}

	// Get lock now we are on leader.
	d.clusterMembershipMutex.Lock()
	defer d.clusterMembershipMutex.Unlock()

	target, nodes, err := cluster.Handover(s, d.gateway, req.Address)
	if err != nil {
		return response.SmartError(err)
	}

	// If there's no other member we can promote, there's nothing we can
	// do, just return.
	if target == "" {
		goto out
	}

	logger.Info("Promoting member during handover", logger.Ctx{"address": localClusterAddress, "losingAddress": req.Address, "candidateAddress": target})
	err = changeMemberRole(s, r, target, nodes)
	if err != nil {
		return response.SmartError(err)
	}

	// Demote the member that is handing over.
	for i, node := range nodes {
		if node.Address == req.Address {
			nodes[i].Role = db.RaftSpare
		}
	}

	logger.Info("Demoting member during handover", logger.Ctx{"address": localClusterAddress, "losingAddress": req.Address})
	err = changeMemberRole(s, r, req.Address, nodes)
	if err != nil {
		return response.SmartError(err)
	}

out:
	return response.SyncResponse(true, nil)
}

// A request for the /internal/cluster/handover endpoint.
type internalClusterPostHandoverRequest struct {
	// Address of the server whose role should be transferred.
	Address string `json:"address" yaml:"address"`
}

func clusterCheckStoragePoolsMatch(ctx context.Context, clusterDB *db.Cluster, reqPools []api.StoragePool) error {
	return clusterDB.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		poolNames, err := tx.GetCreatedStoragePoolNames(ctx)
		if err != nil && !response.IsNotFoundError(err) {
			return err
		}

		for _, name := range poolNames {
			found := false
			for _, reqPool := range reqPools {
				if reqPool.Name != name {
					continue
				}

				found = true

				var pool *api.StoragePool

				_, pool, _, err = tx.GetStoragePoolInAnyState(ctx, name)
				if err != nil {
					return err
				}

				if pool.Driver != reqPool.Driver {
					return fmt.Errorf("Mismatching driver for storage pool %s", name)
				}
				// Exclude the keys which are node-specific.
				exclude := db.NodeSpecificStorageConfig
				err = localUtil.CompareConfigs(pool.Config, reqPool.Config, exclude)
				if err != nil {
					return fmt.Errorf("Mismatching config for storage pool %s: %w", name, err)
				}

				break
			}

			if !found {
				return fmt.Errorf("Missing storage pool %s", name)
			}
		}

		return nil
	})
}

func clusterCheckNetworksMatch(ctx context.Context, clusterDB *db.Cluster, reqNetworks []api.InitNetworksProjectPost) error {
	return clusterDB.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Get a list of projects for networks.
		networkProjectNames, err := dbCluster.GetProjectNames(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed to load projects for networks: %w", err)
		}

		for _, networkProjectName := range networkProjectNames {
			networkNames, err := tx.GetCreatedNetworkNamesByProject(ctx, networkProjectName)
			if err != nil && !response.IsNotFoundError(err) {
				return err
			}

			for _, networkName := range networkNames {
				_, network, _, err := tx.GetNetworkInAnyState(ctx, networkProjectName, networkName)
				if err != nil {
					return err
				}

				// OVN networks don't need local creation.
				if network.Type == "ovn" {
					continue
				}

				// Check that the network is present locally.
				found := false
				for _, reqNetwork := range reqNetworks {
					if reqNetwork.Name != networkName || reqNetwork.Project != networkProjectName {
						continue
					}

					found = true

					if reqNetwork.Type != network.Type {
						return fmt.Errorf("Mismatching type for network %q in project %q", networkName, networkProjectName)
					}

					// Exclude the keys which are node-specific.
					exclude := db.NodeSpecificNetworkConfig
					err = localUtil.CompareConfigs(network.Config, reqNetwork.Config, exclude)
					if err != nil {
						return fmt.Errorf("Mismatching config for network %q in project %q: %w", network.Name, networkProjectName, err)
					}

					break
				}

				if !found {
					return fmt.Errorf("Missing network %q in project %q", networkName, networkProjectName)
				}
			}
		}

		return nil
	})
}

// Used as low-level recovering helper.
func internalClusterRaftNodeDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	address, err := url.PathUnescape(mux.Vars(r)["address"])
	if err != nil {
		return response.SmartError(err)
	}

	err = cluster.RemoveRaftNode(d.gateway, address)
	if err != nil {
		return response.SmartError(err)
	}

	err = rebalanceMemberRoles(s, d.gateway, r, nil)
	if err != nil && !errors.Is(err, cluster.ErrNotLeader) {
		logger.Warn("Could not rebalance cluster member roles after raft member removal", logger.Ctx{"err": err})
	}

	return response.SyncResponse(true, nil)
}

// swagger:operation GET /1.0/cluster/members/{name}/state cluster cluster_member_state_get
//
//	Get state of the cluster member
//
//	Gets state of a specific cluster member.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Cluster member state
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/ClusterMemberState"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterNodeStateGet(d *Daemon, r *http.Request) response.Response {
	memberName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	s := d.State()

	// Forward request.
	resp := forwardedResponseToNode(s, r, memberName)
	if resp != nil {
		return resp
	}

	memberState, err := cluster.MemberState(r.Context(), s, memberName)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, memberState)
}

// swagger:operation POST /1.0/cluster/members/{name}/state cluster cluster_member_state_post
//
//	Evacuate or restore a cluster member
//
//	Evacuates or restores a cluster member.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster
//	    description: Cluster member state
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterMemberStatePost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterNodeStatePost(d *Daemon, r *http.Request) response.Response {
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	s := d.State()

	// Forward request.
	resp := forwardedResponseToNode(s, r, name)
	if resp != nil {
		return resp
	}

	// Parse the request.
	req := api.ClusterMemberStatePost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Validate the overrides.
	if req.Action == "evacuate" && req.Mode != "" {
		// Use the validator from the instance logic.
		validator := internalInstance.InstanceConfigKeysAny["cluster.evacuate"]
		err = validator(req.Mode)
		if err != nil {
			return response.BadRequest(err)
		}
	}

	if req.Action == "evacuate" {
		stopFunc := func(inst instance.Instance, action string) error {
			l := logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})

			if action == "force-stop" {
				// Handle forced shutdown.
				err = inst.Stop(false)
				if err != nil && !errors.Is(err, instanceDrivers.ErrInstanceIsStopped) {
					return fmt.Errorf("Failed to force stop instance %q in project %q: %w", inst.Name(), inst.Project().Name, err)
				}
			} else if action == "stateful-stop" {
				// Handle stateful stop.
				err = inst.Stop(true)
				if err != nil && !errors.Is(err, instanceDrivers.ErrInstanceIsStopped) {
					return fmt.Errorf("Failed to stateful stop instance %q in project %q: %w", inst.Name(), inst.Project().Name, err)
				}
			} else {
				// Get the shutdown timeout for the instance.
				timeout := inst.ExpandedConfig()["boot.host_shutdown_timeout"]
				val, err := strconv.Atoi(timeout)
				if err != nil {
					val = evacuateHostShutdownDefaultTimeout
				}

				// Start with a clean shutdown.
				err = inst.Shutdown(time.Duration(val) * time.Second)
				if err != nil {
					l.Warn("Failed shutting down instance, forcing stop", logger.Ctx{"err": err})

					// Fallback to forced stop.
					err = inst.Stop(false)
					if err != nil && !errors.Is(err, instanceDrivers.ErrInstanceIsStopped) {
						return fmt.Errorf("Failed to stop instance %q in project %q: %w", inst.Name(), inst.Project().Name, err)
					}
				}
			}

			// Mark the instance as RUNNING in volatile so its state can be properly restored.
			err = inst.VolatileSet(map[string]string{"volatile.last_state.power": instance.PowerStateRunning})
			if err != nil {
				l.Warn("Failed to set instance state to RUNNING", logger.Ctx{"err": err})
			}

			return nil
		}

		migrateFunc := func(ctx context.Context, s *state.State, r *http.Request, inst instance.Instance, sourceMemberInfo *db.NodeInfo, targetMemberInfo *db.NodeInfo, live bool, startInstance bool, metadata map[string]any, op *operations.Operation) error {
			// Migrate the instance.
			req := api.InstancePost{
				Migration: true,
				Live:      live,
			}

			err := migrateInstance(ctx, s, inst, req, sourceMemberInfo, targetMemberInfo, op)
			if err != nil {
				return fmt.Errorf("Failed to migrate instance %q in project %q: %w", inst.Name(), inst.Project().Name, err)
			}

			if !startInstance || live {
				return nil
			}

			// Start it back up on target.
			dest, err := cluster.Connect(targetMemberInfo.Address, s.Endpoints.NetworkCert(), s.ServerCert(), r, true)
			if err != nil {
				return fmt.Errorf("Failed to connect to destination %q for instance %q in project %q: %w", targetMemberInfo.Address, inst.Name(), inst.Project().Name, err)
			}

			dest = dest.UseProject(inst.Project().Name)

			if metadata != nil && op != nil {
				metadata["evacuation_progress"] = fmt.Sprintf("Starting %q in project %q", inst.Name(), inst.Project().Name)
				_ = op.UpdateMetadata(metadata)
			}

			startOp, err := dest.UpdateInstanceState(inst.Name(), api.InstanceStatePut{Action: "start"}, "")
			if err != nil {
				return err
			}

			err = startOp.Wait()
			if err != nil {
				return err
			}

			return nil
		}

		return evacuateClusterMember(s, d.gateway, r, req.Mode, stopFunc, migrateFunc)
	} else if req.Action == "restore" {
		return restoreClusterMember(d, r)
	}

	return response.BadRequest(fmt.Errorf("Unknown action %q", req.Action))
}

func internalClusterHeal(d *Daemon, r *http.Request) response.Response {
	migrateFunc := func(ctx context.Context, s *state.State, r *http.Request, inst instance.Instance, sourceMemberInfo *db.NodeInfo, targetMemberInfo *db.NodeInfo, live bool, startInstance bool, metadata map[string]any, op *operations.Operation) error {
		// This returns an error if the instance's storage pool is local.
		// Since we only care about remote backed instances, this can be ignored and return nil instead.
		poolName, err := inst.StoragePool()
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusNotFound) {
				return nil // We only care about remote backed instances.
			}

			return err
		}

		pool, err := storagePools.LoadByName(s, poolName)
		if err != nil {
			return err
		}

		// Nothing to do as the instance's pool is on remote storage.
		if !pool.Driver().Info().Remote {
			return nil
		}

		// Migrate the instance.
		req := api.InstancePost{
			Migration: true,
		}

		dest, err := cluster.Connect(targetMemberInfo.Address, s.Endpoints.NetworkCert(), s.ServerCert(), nil, true)
		if err != nil {
			return err
		}

		dest = dest.UseProject(inst.Project().Name)
		dest = dest.UseTarget(targetMemberInfo.Name)

		migrateOp, err := dest.MigrateInstance(inst.Name(), req)
		if err != nil {
			return err
		}

		err = migrateOp.Wait()
		if err != nil {
			return err
		}

		if !startInstance || live {
			return nil
		}

		// Start it back up on target.
		startOp, err := dest.UpdateInstanceState(inst.Name(), api.InstanceStatePut{Action: "start"}, "")
		if err != nil {
			return err
		}

		err = startOp.Wait()
		if err != nil {
			return err
		}

		return nil
	}

	return evacuateClusterMember(d.State(), d.gateway, r, "migrate", nil, migrateFunc)
}

func evacuateClusterSetState(s *state.State, name string, state int) error {
	return s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the node.
		node, err := tx.GetNodeByName(ctx, name)
		if err != nil {
			return fmt.Errorf("Failed to get cluster member by name: %w", err)
		}

		if node.State == db.ClusterMemberStatePending {
			return fmt.Errorf("Cannot evacuate or restore a pending cluster member")
		}

		// Do nothing if the node is already in expected state.
		if node.State == state {
			if state == db.ClusterMemberStateEvacuated {
				return fmt.Errorf("Cluster member is already evacuated")
			} else if state == db.ClusterMemberStateCreated {
				return fmt.Errorf("Cluster member is already restored")
			}

			return fmt.Errorf("Cluster member is already in requested state")
		}

		// Set node status to requested value.
		err = tx.UpdateNodeStatus(node.ID, state)
		if err != nil {
			return fmt.Errorf("Failed to update cluster member status: %w", err)
		}

		return nil
	})
}

// evacuateHostShutdownDefaultTimeout default timeout (in seconds) for waiting for clean shutdown to complete.
const evacuateHostShutdownDefaultTimeout = 30

func evacuateClusterMember(s *state.State, gateway *cluster.Gateway, r *http.Request, mode string, stopInstance evacuateStopFunc, migrateInstance evacuateMigrateFunc) response.Response {
	nodeName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// The instances are retrieved in a separate transaction, after the node is in EVACUATED state.
	var dbInstances []dbCluster.Instance
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// If evacuating, consider only the instances on the node which needs to be evacuated.
		dbInstances, err = dbCluster.GetInstances(ctx, tx.Tx(), dbCluster.InstanceFilter{Node: &nodeName})
		if err != nil {
			return fmt.Errorf("Failed to get instances: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	instances := make([]instance.Instance, len(dbInstances))

	for i, dbInst := range dbInstances {
		inst, err := instance.LoadByProjectAndName(s, dbInst.Project, dbInst.Name)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to load instance: %w", err))
		}

		instances[i] = inst
	}

	run := func(op *operations.Operation) error {
		// Setup a reverter.
		revert := revert.New()
		defer revert.Fail()

		// Set node status to EVACUATED.
		err := evacuateClusterSetState(s, nodeName, db.ClusterMemberStateEvacuated)
		if err != nil {
			return err
		}

		// Ensure node is put into its previous state if anything fails.
		revert.Add(func() {
			_ = evacuateClusterSetState(s, nodeName, db.ClusterMemberStateCreated)
		})

		opts := evacuateOpts{
			s:               s,
			gateway:         gateway,
			r:               r,
			instances:       instances,
			mode:            mode,
			srcMemberName:   nodeName,
			stopInstance:    stopInstance,
			migrateInstance: migrateInstance,
			op:              op,
		}

		err = evacuateInstances(context.Background(), opts)
		if err != nil {
			return err
		}

		// Stop networks after evacuation.
		networkShutdown(s)

		revert.Success()
		return nil
	}

	op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.ClusterMemberEvacuate, nil, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func evacuateInstances(ctx context.Context, opts evacuateOpts) error {
	if opts.migrateInstance == nil {
		return fmt.Errorf("Missing migration callback function")
	}

	metadata := make(map[string]any)

	for _, inst := range opts.instances {
		instProject := inst.Project()
		l := logger.AddContext(logger.Ctx{"project": instProject.Name, "instance": inst.Name()})

		// Check if migratable.
		action := inst.CanMigrate()

		// Apply overrides.
		if opts.mode != "" && opts.mode != "auto" {
			action = opts.mode
		}

		// Stop the instance if needed.
		isRunning := inst.IsRunning()
		if action != "live-migrate" {
			if opts.stopInstance != nil && isRunning {
				metadata["evacuation_progress"] = fmt.Sprintf("Stopping %q in project %q", inst.Name(), instProject.Name)
				_ = opts.op.UpdateMetadata(metadata)

				err := opts.stopInstance(inst, action)
				if err != nil {
					return err
				}
			}

			if action != "migrate" {
				// Done with this instance.
				continue
			}
		}

		// Find a new location for the instance.
		sourceMemberInfo, targetMemberInfo, err := evacuateClusterSelectTarget(ctx, opts.s, opts.gateway, inst)
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusNotFound) {
				// Skip migration if no target is available.
				l.Warn("No migration target available for instance")
				continue
			}
		}

		// Start migrating the instance.
		metadata["evacuation_progress"] = fmt.Sprintf("Migrating %q in project %q to %q", inst.Name(), instProject.Name, targetMemberInfo.Name)
		_ = opts.op.UpdateMetadata(metadata)

		// Set origin server (but skip if already set as that suggests more than one server being evacuated).
		if inst.LocalConfig()["volatile.evacuate.origin"] == "" {
			_ = inst.VolatileSet(map[string]string{"volatile.evacuate.origin": opts.srcMemberName})
		}

		start := isRunning || instanceShouldAutoStart(inst)
		err = opts.migrateInstance(ctx, opts.s, opts.r, inst, sourceMemberInfo, targetMemberInfo, action == "live-migrate", start, metadata, opts.op)
		if err != nil {
			return err
		}
	}

	return nil
}

func restoreClusterMember(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	originName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// List the instances.
	var dbInstances []dbCluster.Instance
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbInstances, err = dbCluster.GetInstances(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed to get instances: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	instances := make([]instance.Instance, 0)
	localInstances := make([]instance.Instance, 0)

	for _, dbInst := range dbInstances {
		inst, err := instance.LoadByProjectAndName(s, dbInst.Project, dbInst.Name)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to load instance: %w", err))
		}

		if dbInst.Node == originName {
			localInstances = append(localInstances, inst)
			continue
		}

		// Only consider instances where volatile.evacuate.origin is set to the node which needs to be restored.
		val, ok := inst.LocalConfig()["volatile.evacuate.origin"]
		if !ok || val != originName {
			continue
		}

		instances = append(instances, inst)
	}

	run := func(op *operations.Operation) error {
		// Setup a reverter.
		revert := revert.New()
		defer revert.Fail()

		// Set node status to CREATED.
		err := evacuateClusterSetState(s, originName, db.ClusterMemberStateCreated)
		if err != nil {
			return err
		}

		// Ensure node is put into its previous state if anything fails.
		revert.Add(func() {
			_ = evacuateClusterSetState(s, originName, db.ClusterMemberStateEvacuated)
		})

		var source incus.InstanceServer
		var sourceNode db.NodeInfo

		metadata := make(map[string]any)

		// Restart the networks.
		err = networkStartup(d.State())
		if err != nil {
			return err
		}

		// Restart the local instances.
		for _, inst := range localInstances {
			// Don't start instances which were stopped by the user.
			if inst.LocalConfig()["volatile.last_state.power"] != instance.PowerStateRunning {
				continue
			}

			// Don't attempt to start instances which are already running.
			if inst.IsRunning() {
				continue
			}

			// Start the instance.
			metadata["evacuation_progress"] = fmt.Sprintf("Starting %q in project %q", inst.Name(), inst.Project().Name)
			_ = op.UpdateMetadata(metadata)

			// If configured for stateful stop, try restoring its state.
			action := inst.CanMigrate()
			if action == "stateful-stop" {
				err = inst.Start(true)
			} else {
				err = inst.Start(false)
			}

			if err != nil {
				return fmt.Errorf("Failed to start instance %q: %w", inst.Name(), err)
			}
		}

		// Migrate back the remote instances.
		for _, inst := range instances {
			l := logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})

			// Check the action.
			live := inst.CanMigrate() == "live-migrate"

			metadata["evacuation_progress"] = fmt.Sprintf("Migrating %q in project %q from %q", inst.Name(), inst.Project().Name, inst.Location())
			_ = op.UpdateMetadata(metadata)

			err = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				sourceNode, err = tx.GetNodeByName(ctx, inst.Location())
				if err != nil {
					return fmt.Errorf("Failed to get node %q: %w", inst.Location(), err)
				}

				return nil
			})
			if err != nil {
				return fmt.Errorf("Failed to get node: %w", err)
			}

			source, err = cluster.Connect(sourceNode.Address, s.Endpoints.NetworkCert(), s.ServerCert(), r, true)
			if err != nil {
				return fmt.Errorf("Failed to connect to source: %w", err)
			}

			source = source.UseProject(inst.Project().Name)

			apiInst, _, err := source.GetInstance(inst.Name())
			if err != nil {
				return fmt.Errorf("Failed to get instance %q: %w", inst.Name(), err)
			}

			isRunning := apiInst.StatusCode == api.Running
			if isRunning && !live {
				metadata["evacuation_progress"] = fmt.Sprintf("Stopping %q in project %q", inst.Name(), inst.Project().Name)
				_ = op.UpdateMetadata(metadata)

				timeout := inst.ExpandedConfig()["boot.host_shutdown_timeout"]
				val, err := strconv.Atoi(timeout)
				if err != nil {
					val = evacuateHostShutdownDefaultTimeout
				}

				// Attempt a clean stop.
				stopOp, err := source.UpdateInstanceState(inst.Name(), api.InstanceStatePut{Action: "stop", Force: false, Timeout: val}, "")
				if err != nil {
					return fmt.Errorf("Failed to stop instance %q: %w", inst.Name(), err)
				}

				// Wait for the stop operation to complete or timeout.
				err = stopOp.Wait()
				if err != nil {
					l.Warn("Failed shutting down instance, forcing stop", logger.Ctx{"err": err})

					// On failure, attempt a forceful stop.
					stopOp, err = source.UpdateInstanceState(inst.Name(), api.InstanceStatePut{Action: "stop", Force: true}, "")
					if err != nil {
						// If this fails too, fail the whole operation.
						return fmt.Errorf("Failed to stop instance %q: %w", inst.Name(), err)
					}

					// Wait for the forceful stop to complete.
					err = stopOp.Wait()
					if err != nil && !strings.Contains(err.Error(), "The instance is already stopped") {
						return fmt.Errorf("Failed to stop instance %q: %w", inst.Name(), err)
					}
				}
			}

			req := api.InstancePost{
				Name:      inst.Name(),
				Migration: true,
				Live:      live,
			}

			source = source.UseTarget(originName)

			migrationOp, err := source.MigrateInstance(inst.Name(), req)
			if err != nil {
				return fmt.Errorf("Migration API failure: %w", err)
			}

			err = migrationOp.Wait()
			if err != nil {
				return fmt.Errorf("Failed to wait for migration to finish: %w", err)
			}

			// Reload the instance after migration.
			inst, err := instance.LoadByProjectAndName(s, inst.Project().Name, inst.Name())
			if err != nil {
				return fmt.Errorf("Failed to load instance: %w", err)
			}

			config := inst.LocalConfig()
			delete(config, "volatile.evacuate.origin")

			args := db.InstanceArgs{
				Architecture: inst.Architecture(),
				Config:       config,
				Description:  inst.Description(),
				Devices:      inst.LocalDevices(),
				Ephemeral:    inst.IsEphemeral(),
				Profiles:     inst.Profiles(),
				Project:      inst.Project().Name,
				ExpiryDate:   inst.ExpiryDate(),
			}

			err = inst.Update(args, false)
			if err != nil {
				return fmt.Errorf("Failed to update instance %q: %w", inst.Name(), err)
			}

			if !isRunning || live {
				continue
			}

			metadata["evacuation_progress"] = fmt.Sprintf("Starting %q in project %q", inst.Name(), inst.Project().Name)
			_ = op.UpdateMetadata(metadata)

			err = inst.Start(false)
			if err != nil {
				return fmt.Errorf("Failed to start instance %q: %w", inst.Name(), err)
			}
		}

		revert.Success()
		return nil
	}

	op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.ClusterMemberRestore, nil, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation POST /1.0/cluster/groups cluster cluster_groups_post
//
//	Create a cluster group.
//
//	Creates a new cluster group.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster
//	    description: Cluster group to create
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterGroupsPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterGroupsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	if !s.ServerClustered {
		return response.BadRequest(fmt.Errorf("This server is not clustered"))
	}

	req := api.ClusterGroupsPost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	err = clusterGroupValidateName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		obj := dbCluster.ClusterGroup{
			Name:        req.Name,
			Description: req.Description,
			Nodes:       req.Members,
		}

		groupID, err := dbCluster.CreateClusterGroup(ctx, tx.Tx(), obj)
		if err != nil {
			return err
		}

		for _, node := range obj.Nodes {
			_, err = dbCluster.CreateNodeClusterGroup(ctx, tx.Tx(), dbCluster.NodeClusterGroup{GroupID: int(groupID), Node: node})
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	lc := lifecycle.ClusterGroupCreated.Event(req.Name, requestor, nil)
	s.Events.SendLifecycle(api.ProjectDefaultName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation GET /1.0/cluster/groups cluster-groups cluster_groups_get
//
//  Get the cluster groups
//
//  Returns a list of cluster groups (URLs).
//
//  ---
//  produces:
//    - application/json
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/cluster/groups/server01",
//                "/1.0/cluster/groups/server02"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/cluster/groups?recursion=1 cluster-groups cluster_groups_get_recursion1
//
//	Get the cluster groups
//
//	Returns a list of cluster groups (structs).
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of cluster groups
//	          items:
//	            $ref: "#/definitions/ClusterGroup"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterGroupsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	if !s.ServerClustered {
		return response.BadRequest(fmt.Errorf("This server is not clustered"))
	}

	recursion := localUtil.IsRecursionRequest(r)

	var result any

	err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		if recursion {
			clusterGroups, err := dbCluster.GetClusterGroups(ctx, tx.Tx())
			if err != nil {
				return err
			}

			for i := range clusterGroups {
				nodeClusterGroups, err := dbCluster.GetNodeClusterGroups(ctx, tx.Tx(), dbCluster.NodeClusterGroupFilter{GroupID: &clusterGroups[i].ID})
				if err != nil {
					return err
				}

				clusterGroups[i].Nodes = make([]string, 0, len(nodeClusterGroups))
				for _, node := range nodeClusterGroups {
					clusterGroups[i].Nodes = append(clusterGroups[i].Nodes, node.Node)
				}
			}

			apiClusterGroups := make([]*api.ClusterGroup, len(clusterGroups))
			for i, clusterGroup := range clusterGroups {
				members, err := tx.GetClusterGroupNodes(ctx, clusterGroup.Name)
				if err != nil {
					return err
				}

				apiClusterGroups[i] = db.ClusterGroupToAPI(&clusterGroup, members)
			}

			result = apiClusterGroups
		} else {
			result, err = tx.GetClusterGroupURIs(ctx, dbCluster.ClusterGroupFilter{})
		}

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, result)
}

// swagger:operation GET /1.0/cluster/groups/{name} cluster-groups cluster_group_get
//
//	Get the cluster group
//
//	Gets a specific cluster group.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Cluster group
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/ClusterGroup"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterGroupGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if !s.ServerClustered {
		return response.BadRequest(fmt.Errorf("This server is not clustered"))
	}

	var group *dbCluster.ClusterGroup

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the cluster group.
		group, err = dbCluster.GetClusterGroup(ctx, tx.Tx(), name)
		if err != nil {
			return err
		}

		nodeClusterGroups, err := dbCluster.GetNodeClusterGroups(ctx, tx.Tx(), dbCluster.NodeClusterGroupFilter{GroupID: &group.ID})
		if err != nil {
			return err
		}

		group.Nodes = make([]string, 0, len(nodeClusterGroups))
		for _, node := range nodeClusterGroups {
			group.Nodes = append(group.Nodes, node.Node)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	apiGroup, err := group.ToAPI()
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponseETag(true, apiGroup, apiGroup.ClusterGroupPut)
}

// swagger:operation POST /1.0/cluster/groups/{name} cluster-groups cluster_group_post
//
//	Rename the cluster group
//
//	Renames an existing cluster group.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: name
//	    description: Cluster group rename request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterGroupPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterGroupPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if name == "default" {
		return response.Forbidden(errors.New(`The "default" group cannot be renamed`))
	}

	if !s.ServerClustered {
		return response.BadRequest(fmt.Errorf("This server is not clustered"))
	}

	req := api.ClusterGroupPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	err = clusterGroupValidateName(name)
	if err != nil {
		return response.BadRequest(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check that the name isn't already in use.
		_, err = dbCluster.GetClusterGroup(ctx, tx.Tx(), req.Name)
		if err == nil {
			return fmt.Errorf("Name %q already in use", req.Name)
		}

		// Rename the cluster group.
		err = dbCluster.RenameClusterGroup(ctx, tx.Tx(), name, req.Name)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	lc := lifecycle.ClusterGroupRenamed.Event(req.Name, requestor, logger.Ctx{"old_name": name})
	s.Events.SendLifecycle(api.ProjectDefaultName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation PUT /1.0/cluster/groups/{name} cluster-groups cluster_group_put
//
//	Update the cluster group
//
//	Updates the entire cluster group configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster group
//	    description: cluster group configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterGroupPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterGroupPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if !s.ServerClustered {
		return response.BadRequest(fmt.Errorf("This server is not clustered"))
	}

	req := api.ClusterGroupPut{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		group, err := dbCluster.GetClusterGroup(ctx, tx.Tx(), name)
		if err != nil {
			return err
		}

		obj := dbCluster.ClusterGroup{
			Name:        group.Name,
			Description: req.Description,
		}

		err = dbCluster.UpdateClusterGroup(ctx, tx.Tx(), name, obj)
		if err != nil {
			return err
		}

		members, err := tx.GetClusterGroupNodes(ctx, name)
		if err != nil {
			return err
		}

		// skipMembers is a list of members which already belong to the group.
		skipMembers := []string{}

		for _, oldMember := range members {
			if !slices.Contains(req.Members, oldMember) {
				// Get all cluster groups this member belongs to.
				groups, err := tx.GetClusterGroupsWithNode(ctx, oldMember)
				if err != nil {
					return err
				}

				// Note that members who only belong to this group will not be removed from it.
				// That is because each member needs to belong to at least one group.
				if len(groups) > 1 {
					// Remove member from this group as it belongs to at least one other group.
					err = tx.RemoveNodeFromClusterGroup(ctx, name, oldMember)
					if err != nil {
						return err
					}
				}
			} else {
				skipMembers = append(skipMembers, oldMember)
			}
		}

		for _, member := range req.Members {
			// Skip these members as they already belong to this group.
			if slices.Contains(skipMembers, member) {
				continue
			}

			// Add new members to the group.
			err = tx.AddNodeToClusterGroup(ctx, name, member)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ClusterGroupUpdated.Event(name, requestor, logger.Ctx{"description": req.Description, "members": req.Members}))

	return response.EmptySyncResponse
}

// swagger:operation PATCH /1.0/cluster/groups/{name} cluster-groups cluster_group_patch
//
//	Update the cluster group
//
//	Updates the cluster group configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster group
//	    description: cluster group configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterGroupPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterGroupPatch(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if !s.ServerClustered {
		return response.BadRequest(fmt.Errorf("This server is not clustered"))
	}

	var clusterGroup *api.ClusterGroup
	var dbClusterGroup *dbCluster.ClusterGroup

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbClusterGroup, err = dbCluster.GetClusterGroup(ctx, tx.Tx(), name)
		if err != nil {
			return err
		}

		nodeClusterGroups, err := dbCluster.GetNodeClusterGroups(ctx, tx.Tx(), dbCluster.NodeClusterGroupFilter{GroupID: &dbClusterGroup.ID})
		if err != nil {
			return err
		}

		dbClusterGroup.Nodes = make([]string, 0, len(nodeClusterGroups))
		for _, node := range nodeClusterGroups {
			dbClusterGroup.Nodes = append(dbClusterGroup.Nodes, node.Node)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	clusterGroup, err = dbClusterGroup.ToAPI()
	if err != nil {
		return response.SmartError(err)
	}

	req := clusterGroup.Writable()

	// Validate the ETag.
	etag := []any{clusterGroup.Description, clusterGroup.Members}
	err = localUtil.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Members == nil {
		req.Members = clusterGroup.Members
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		obj := dbCluster.ClusterGroup{
			Name:        dbClusterGroup.Name,
			Description: req.Description,
		}

		err = dbCluster.UpdateClusterGroup(ctx, tx.Tx(), name, obj)
		if err != nil {
			return err
		}

		groupID, err := dbCluster.GetClusterGroupID(ctx, tx.Tx(), obj.Name)
		if err != nil {
			return err
		}

		err = dbCluster.DeleteNodeClusterGroup(ctx, tx.Tx(), int(groupID))
		if err != nil {
			return err
		}

		for _, node := range obj.Nodes {
			_, err = dbCluster.CreateNodeClusterGroup(ctx, tx.Tx(), dbCluster.NodeClusterGroup{GroupID: int(groupID), Node: node})
			if err != nil {
				return err
			}
		}

		members, err := tx.GetClusterGroupNodes(ctx, name)
		if err != nil {
			return err
		}

		// skipMembers is a list of members which already belong to the group.
		skipMembers := []string{}

		for _, oldMember := range members {
			if !slices.Contains(req.Members, oldMember) {
				// Get all cluster groups this member belongs to.
				groups, err := tx.GetClusterGroupsWithNode(ctx, oldMember)
				if err != nil {
					return err
				}

				// Cluster member cannot be removed from the group as it doesn't belong to any other.
				if len(groups) == 1 {
					return fmt.Errorf("Cannot remove %s from group as member needs to belong to at least one group", oldMember)
				}

				// Remove member from this group as it belongs to at least one other group.
				err = tx.RemoveNodeFromClusterGroup(ctx, name, oldMember)
				if err != nil {
					return err
				}
			} else {
				skipMembers = append(skipMembers, oldMember)
			}
		}

		for _, member := range req.Members {
			// Skip these members as they already belong to this group.
			if slices.Contains(skipMembers, member) {
				continue
			}

			// Add new members to the group.
			err = tx.AddNodeToClusterGroup(ctx, name, member)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ClusterGroupUpdated.Event(name, requestor, logger.Ctx{"description": req.Description, "members": req.Members}))

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/cluster/groups/{name} cluster-groups cluster_group_delete
//
//	Delete the cluster group.
//
//	Removes the cluster group.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterGroupDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Quick checks.
	if name == "default" {
		return response.Forbidden(fmt.Errorf("The 'default' cluster group cannot be deleted"))
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		members, err := tx.GetClusterGroupNodes(ctx, name)
		if err != nil {
			return err
		}

		if len(members) > 0 {
			return fmt.Errorf("Only empty cluster groups can be removed")
		}

		return dbCluster.DeleteClusterGroup(ctx, tx.Tx(), name)
	})

	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(name, lifecycle.ClusterGroupDeleted.Event(name, requestor, nil))

	return response.EmptySyncResponse
}

func clusterGroupValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("No name provided")
	}

	if strings.Contains(name, "/") {
		return fmt.Errorf("Cluster group names may not contain slashes")
	}

	if strings.Contains(name, " ") {
		return fmt.Errorf("Cluster group names may not contain spaces")
	}

	if strings.Contains(name, "_") {
		return fmt.Errorf("Cluster group names may not contain underscores")
	}

	if strings.Contains(name, "'") || strings.Contains(name, `"`) {
		return fmt.Errorf("Cluster group names may not contain quotes")
	}

	if name == "*" {
		return fmt.Errorf("Reserved cluster group name")
	}

	if slices.Contains([]string{".", ".."}, name) {
		return fmt.Errorf("Invalid cluster group name %q", name)
	}

	return nil
}

func evacuateClusterSelectTarget(ctx context.Context, s *state.State, gateway *cluster.Gateway, inst instance.Instance) (*db.NodeInfo, *db.NodeInfo, error) {
	var sourceMemberInfo *db.NodeInfo
	var targetMemberInfo *db.NodeInfo

	// Get candidate cluster members to move instances to.
	var candidateMembers []db.NodeInfo
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the source member info.
		srcMember, err := tx.GetNodeByName(ctx, inst.Location())
		if err != nil {
			return fmt.Errorf("Failed getting current cluster member of instance %q", inst.Name())
		}

		sourceMemberInfo = &srcMember

		allMembers, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		// Filter candidates by group if needed.
		group := inst.LocalConfig()["volatile.cluster.group"]
		if group != "" {
			newMembers := make([]db.NodeInfo, 0, len(allMembers))
			for _, member := range allMembers {
				if !slices.Contains(member.Groups, group) {
					continue
				}

				newMembers = append(newMembers, member)
			}

			allMembers = newMembers
		}

		// Filter offline servers.
		candidateMembers, err = tx.GetCandidateMembers(ctx, allMembers, []int{inst.Architecture()}, "", nil, s.GlobalConfig.OfflineThreshold())
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// Run instance placement scriptlet if enabled.
	if s.GlobalConfig.InstancesPlacementScriptlet() != "" {
		leaderAddress, err := gateway.LeaderAddress()
		if err != nil {
			return nil, nil, err
		}

		// Copy request so we don't modify it when expanding the config.
		reqExpanded := apiScriptlet.InstancePlacement{
			InstancesPost: api.InstancesPost{
				Name: inst.Name(),
				Type: api.InstanceType(inst.Type().String()),
				InstancePut: api.InstancePut{
					Config:  inst.ExpandedConfig(),
					Devices: inst.ExpandedDevices().CloneNative(),
				},
			},
			Project: inst.Project().Name,
			Reason:  apiScriptlet.InstancePlacementReasonEvacuation,
		}

		reqExpanded.Architecture, err = osarch.ArchitectureName(inst.Architecture())
		if err != nil {
			return nil, nil, fmt.Errorf("Failed getting architecture for instance %q in project %q: %w", inst.Name(), inst.Project().Name, err)
		}

		for _, p := range inst.Profiles() {
			reqExpanded.Profiles = append(reqExpanded.Profiles, p.Name)
		}

		ctx, cancel := context.WithTimeout(ctx, time.Second*5)
		targetMemberInfo, err = scriptlet.InstancePlacementRun(ctx, logger.Log, s, &reqExpanded, candidateMembers, leaderAddress)
		if err != nil {
			cancel()
			return nil, nil, fmt.Errorf("Failed instance placement scriptlet for instance %q in project %q: %w", inst.Name(), inst.Project().Name, err)
		}

		cancel()
	}

	// If target member not specified yet, then find the least loaded cluster member which
	// supports the instance's architecture.
	if targetMemberInfo == nil {
		var err error

		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			targetMemberInfo, err = tx.GetNodeWithLeastInstances(ctx, candidateMembers)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}

	return sourceMemberInfo, targetMemberInfo, nil
}

func autoHealClusterTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := d.State()
		healingThreshold := s.GlobalConfig.ClusterHealingThreshold()
		if healingThreshold == 0 {
			return // Skip healing if it's disabled.
		}

		leader, err := d.gateway.LeaderAddress()
		if err != nil {
			if errors.Is(err, cluster.ErrNodeIsNotClustered) {
				return // Skip healing if not clustered.
			}

			logger.Error("Failed to get leader cluster member address", logger.Ctx{"err": err})
			return
		}

		if s.LocalConfig.ClusterAddress() != leader {
			return // Skip healing if not cluster leader.
		}

		var offlineMembers []db.NodeInfo
		{
			var members []db.NodeInfo
			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				members, err = tx.GetNodes(ctx)
				if err != nil {
					return fmt.Errorf("Failed getting cluster members: %w", err)
				}

				return nil
			})
			if err != nil {
				logger.Error("Failed healing cluster instances", logger.Ctx{"err": err})
				return
			}

			for _, member := range members {
				// Ignore members which have been evacuated, and those which haven't exceeded the
				// healing offline trigger threshold.
				if member.State == db.ClusterMemberStateEvacuated || !member.IsOffline(healingThreshold) {
					continue
				}

				offlineMembers = append(offlineMembers, member)
			}
		}

		if len(offlineMembers) == 0 {
			return // Skip healing if there are no cluster members to evacuate.
		}

		opRun := func(op *operations.Operation) error {
			err := autoHealCluster(ctx, s, offlineMembers)
			if err != nil {
				logger.Error("Failed healing cluster instances", logger.Ctx{"err": err})
				return err
			}

			return nil
		}

		op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.ClusterHeal, nil, nil, opRun, nil, nil, nil)
		if err != nil {
			logger.Error("Failed creating cluster instances heal operation", logger.Ctx{"err": err})
			return
		}

		err = op.Start()
		if err != nil {
			logger.Error("Failed starting cluster instances heal operation", logger.Ctx{"err": err})
			return
		}

		err = op.Wait(ctx)
		if err != nil {
			logger.Error("Failed healing cluster instances", logger.Ctx{"err": err})
			return
		}
	}

	return f, task.Every(time.Minute)
}

func autoHealCluster(ctx context.Context, s *state.State, offlineMembers []db.NodeInfo) error {
	logger.Info("Healing cluster instances")

	dest, err := cluster.Connect(s.LocalConfig.ClusterAddress(), s.Endpoints.NetworkCert(), s.ServerCert(), nil, true)
	if err != nil {
		return err
	}

	for _, member := range offlineMembers {
		logger.Info("Healing cluster member instances", logger.Ctx{"member": member.Name})
		_, _, err = dest.RawQuery("POST", fmt.Sprintf("/internal/cluster/heal/%s", member.Name), nil, "")
		if err != nil {
			return fmt.Errorf("Failed evacuating cluster member %q: %w", member.Name, err)
		}
	}

	logger.Info("Done healing cluster instances")

	return nil
}
