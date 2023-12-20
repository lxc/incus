package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/auth"
	"github.com/lxc/incus/internal/server/cluster"
	clusterRequest "github.com/lxc/incus/internal/server/cluster/request"
	"github.com/lxc/incus/internal/server/db"
	dbCluster "github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/db/warningtype"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/lifecycle"
	"github.com/lxc/incus/internal/server/network"
	"github.com/lxc/incus/internal/server/network/ovs"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/request"
	"github.com/lxc/incus/internal/server/resources"
	"github.com/lxc/incus/internal/server/response"
	"github.com/lxc/incus/internal/server/state"
	localUtil "github.com/lxc/incus/internal/server/util"
	"github.com/lxc/incus/internal/server/warnings"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
)

// Lock to prevent concurent networks creation.
var networkCreateLock sync.Mutex

var networksCmd = APIEndpoint{
	Path: "networks",

	Get:  APIEndpointAction{Handler: networksGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: networksPost, AccessHandler: allowPermission(auth.ObjectTypeProject, auth.EntitlementCanCreateNetworks)},
}

var networkCmd = APIEndpoint{
	Path: "networks/{networkName}",

	Delete: APIEndpointAction{Handler: networkDelete, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanEdit, "networkName")},
	Get:    APIEndpointAction{Handler: networkGet, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanView, "networkName")},
	Patch:  APIEndpointAction{Handler: networkPatch, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanEdit, "networkName")},
	Post:   APIEndpointAction{Handler: networkPost, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanEdit, "networkName")},
	Put:    APIEndpointAction{Handler: networkPut, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanEdit, "networkName")},
}

var networkLeasesCmd = APIEndpoint{
	Path: "networks/{networkName}/leases",

	Get: APIEndpointAction{Handler: networkLeasesGet, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanView, "networkName")},
}

var networkStateCmd = APIEndpoint{
	Path: "networks/{networkName}/state",

	Get: APIEndpointAction{Handler: networkStateGet, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanView, "networkName")},
}

// API endpoints

// swagger:operation GET /1.0/networks networks networks_get
//
//  Get the networks
//
//  Returns a list of networks (URLs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
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
//                "/1.0/networks/mybr0",
//                "/1.0/networks/mybr1"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/networks?recursion=1 networks networks_get_recursion1
//
//	Get the networks
//
//	Returns a list of networks (structs).
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
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
//	          description: List of networks
//	          items:
//	            $ref: "#/definitions/Network"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networksGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	recursion := localUtil.IsRecursionRequest(r)

	// Get list of managed networks (that may or may not have network interfaces on the host).
	networkNames, err := s.DB.Cluster.GetNetworks(projectName)
	if err != nil {
		return response.InternalError(err)
	}

	// Get list of actual network interfaces on the host as well if the effective project is Default.
	if projectName == api.ProjectDefaultName {
		ifaces, err := net.Interfaces()
		if err != nil {
			return response.InternalError(err)
		}

		for _, iface := range ifaces {
			// Ignore veth pairs (for performance reasons).
			if strings.HasPrefix(iface.Name, "veth") {
				continue
			}

			// Append to the list of networks if a managed network of same name doesn't exist.
			if !util.ValueInSlice(iface.Name, networkNames) {
				networkNames = append(networkNames, iface.Name)
			}
		}
	}

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanView, auth.ObjectTypeNetwork)
	if err != nil {
		return response.InternalError(err)
	}

	resultString := []string{}
	resultMap := []api.Network{}
	for _, networkName := range networkNames {
		if !userHasPermission(auth.ObjectNetwork(projectName, networkName)) {
			continue
		}

		if !recursion {
			resultString = append(resultString, fmt.Sprintf("/%s/networks/%s", version.APIVersion, networkName))
		} else {
			net, err := doNetworkGet(s, r, s.ServerClustered, projectName, reqProject.Config, networkName)
			if err != nil {
				continue
			}

			resultMap = append(resultMap, net)
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

// swagger:operation POST /1.0/networks networks networks_post
//
//	Add a network
//
//	Creates a new network.
//	When clustered, most network types require individual POST for each cluster member prior to a global POST.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: server01
//	  - in: body
//	    name: network
//	    description: Network
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworksPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networksPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	networkCreateLock.Lock()
	defer networkCreateLock.Unlock()

	req := api.NetworksPost{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(reqProject.Config, req.Name, true) {
		return response.SmartError(api.StatusErrorf(http.StatusForbidden, "Network not allowed in project"))
	}

	if req.Type == "" {
		if projectName != api.ProjectDefaultName {
			req.Type = "ovn" // Only OVN networks are allowed inside network enabled projects.
		} else {
			req.Type = "bridge" // Default to bridge for non-network enabled projects.
		}
	}

	if req.Config == nil {
		req.Config = map[string]string{}
	}

	netType, err := network.LoadByType(req.Type)
	if err != nil {
		return response.BadRequest(err)
	}

	err = netType.ValidateName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	netTypeInfo := netType.Info()
	if projectName != api.ProjectDefaultName && !netTypeInfo.Projects {
		return response.BadRequest(fmt.Errorf("Network type does not support non-default projects"))
	}

	// Check if project has limits.network and if so check we are allowed to create another network.
	if projectName != api.ProjectDefaultName && reqProject.Config != nil && reqProject.Config["limits.networks"] != "" {
		networksLimit, err := strconv.Atoi(reqProject.Config["limits.networks"])
		if err != nil {
			return response.InternalError(fmt.Errorf("Invalid project limits.network value: %w", err))
		}

		networks, err := s.DB.Cluster.GetNetworks(projectName)
		if err != nil {
			return response.InternalError(fmt.Errorf("Failed loading project's networks for limits check: %w", err))
		}

		// Only check network limits if the new network name doesn't exist already in networks list.
		// If it does then this create request will either be for adding a target node to an existing
		// pending network or it will fail anyway as it is a duplicate.
		if !util.ValueInSlice(req.Name, networks) && len(networks) >= networksLimit {
			return response.BadRequest(fmt.Errorf("Networks limit has been reached for project"))
		}
	}

	u := api.NewURL().Path(version.APIVersion, "networks", req.Name).Project(projectName)

	resp := response.SyncResponseLocation(true, nil, u.String())

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	if isClusterNotification(r) {
		n, err := network.LoadByName(s, projectName, req.Name)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
		}

		// This is an internal request which triggers the actual creation of the network across all nodes
		// after they have been previously defined.
		err = doNetworksCreate(s, n, clientType)
		if err != nil {
			return response.SmartError(err)
		}

		return resp
	}

	targetNode := request.QueryParam(r, "target")
	if targetNode != "" {
		if !netTypeInfo.NodeSpecificConfig {
			return response.BadRequest(fmt.Errorf("Network type %q does not support member specific config", netType.Type()))
		}

		// A targetNode was specified, let's just define the node's network without actually creating it.
		// Check that only NodeSpecificNetworkConfig keys are specified.
		for key := range req.Config {
			if !util.ValueInSlice(key, db.NodeSpecificNetworkConfig) {
				return response.BadRequest(fmt.Errorf("Config key %q may not be used as member-specific key", key))
			}
		}

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.CreatePendingNetwork(ctx, targetNode, projectName, req.Name, netType.DBType(), req.Config)
		})
		if err != nil {
			if err == db.ErrAlreadyDefined {
				return response.BadRequest(fmt.Errorf("The network is already defined on member %q", targetNode))
			}

			return response.SmartError(err)
		}

		return resp
	}

	// Load existing network if exists, if not don't fail.
	_, netInfo, _, err := s.DB.Cluster.GetNetworkInAnyState(projectName, req.Name)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return response.InternalError(err)
	}

	// Check if we're clustered.
	count, err := cluster.Count(s)
	if err != nil {
		return response.SmartError(err)
	}

	// No targetNode was specified and we're clustered or there is an existing partially created single node
	// network, either way finalize the config in the db and actually create the network on all cluster nodes.
	if count > 1 || (netInfo != nil && netInfo.Status != api.NetworkStatusCreated) {
		// Simulate adding pending node network config when the driver doesn't support per-node config.
		if !netTypeInfo.NodeSpecificConfig && clientType != clusterRequest.ClientTypeJoiner {
			// Create pending entry for each node.
			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				members, err := tx.GetNodes(ctx)
				if err != nil {
					return fmt.Errorf("Failed getting cluster members: %w", err)
				}

				for _, member := range members {
					// Don't pass in any config, as these nodes don't have any node-specific
					// config and we don't want to create duplicate global config.
					err = tx.CreatePendingNetwork(ctx, member.Name, projectName, req.Name, netType.DBType(), nil)
					if err != nil && !errors.Is(err, db.ErrAlreadyDefined) {
						return fmt.Errorf("Failed creating pending network for member %q: %w", member.Name, err)
					}
				}

				return nil
			})
			if err != nil {
				return response.SmartError(err)
			}
		}

		err = networksPostCluster(s, projectName, netInfo, req, clientType, netType)
		if err != nil {
			return response.SmartError(err)
		}

		return resp
	}

	// Non-clustered network creation.
	if netInfo != nil {
		return response.BadRequest(fmt.Errorf("The network already exists"))
	}

	revert := revert.New()
	defer revert.Fail()

	// Populate default config.
	err = netType.FillConfig(req.Config)
	if err != nil {
		return response.SmartError(err)
	}

	// Create the database entry.
	_, err = s.DB.Cluster.CreateNetwork(projectName, req.Name, req.Description, netType.DBType(), req.Config)
	if err != nil {
		return response.SmartError(fmt.Errorf("Error inserting %q into database: %w", req.Name, err))
	}

	revert.Add(func() { _ = s.DB.Cluster.DeleteNetwork(projectName, req.Name) })

	n, err := network.LoadByName(s, projectName, req.Name)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	err = doNetworksCreate(s, n, clientType)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.Authorizer.AddNetwork(r.Context(), projectName, req.Name)
	if err != nil {
		logger.Error("Failed to add network to authorizer", logger.Ctx{"name": req.Name, "project": projectName, "error": err})
	}

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(projectName, lifecycle.NetworkCreated.Event(n, requestor, nil))

	revert.Success()
	return resp
}

// networkPartiallyCreated returns true of supplied network has properties that indicate it has had previous
// create attempts run on it but failed on one or more nodes.
func networkPartiallyCreated(netInfo *api.Network) bool {
	// If the network status is NetworkStatusErrored, this means create has been run in the past and has
	// failed on one or more nodes. Hence it is partially created.
	if netInfo.Status == api.NetworkStatusErrored {
		return true
	}

	// If the network has global config keys, then it has previously been created by having its global config
	// inserted, and this means it is partialled created.
	for key := range netInfo.Config {
		if !util.ValueInSlice(key, db.NodeSpecificNetworkConfig) {
			return true
		}
	}

	return false
}

// networksPostCluster checks that there is a pending network in the database and then attempts to setup the
// network on each node. If all nodes are successfully setup then the network's state is set to created.
// Accepts an optional existing network record, which will exist when performing subsequent re-create attempts.
func networksPostCluster(s *state.State, projectName string, netInfo *api.Network, req api.NetworksPost, clientType clusterRequest.ClientType, netType network.Type) error {
	// Check that no node-specific config key has been supplied in request.
	for key := range req.Config {
		if util.ValueInSlice(key, db.NodeSpecificNetworkConfig) {
			return fmt.Errorf("Config key %q is cluster member specific", key)
		}
	}

	// If network already exists, perform quick checks.
	if netInfo != nil {
		// Check network isn't already created.
		if netInfo.Status == api.NetworkStatusCreated {
			return fmt.Errorf("The network is already created")
		}

		// Check the requested network type matches the type created when adding the local member config.
		if req.Type != netInfo.Type {
			return fmt.Errorf("Requested network type %q doesn't match type in existing database record %q", req.Type, netInfo.Type)
		}
	}

	// Check that the network is properly defined, get the node-specific configs and merge with global config.
	var nodeConfigs map[string]map[string]string
	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check if any global config exists already, if so we should not create global config again.
		if netInfo != nil && networkPartiallyCreated(netInfo) {
			if len(req.Config) > 0 {
				return fmt.Errorf("Network already partially created. Please do not specify any global config when re-running create")
			}

			logger.Debug("Skipping global network create as global config already partially created", logger.Ctx{"project": projectName, "network": req.Name})
			return nil
		}

		// Fetch the network ID.
		networkID, err := tx.GetNetworkID(ctx, projectName, req.Name)
		if err != nil {
			return err
		}

		// Fetch the node-specific configs and check the network is defined for all nodes.
		nodeConfigs, err = tx.NetworkNodeConfigs(ctx, networkID)
		if err != nil {
			return err
		}

		// Add default values if we are inserting global config for first time.
		err = netType.FillConfig(req.Config)
		if err != nil {
			return err
		}

		// Insert the global config keys.
		err = tx.CreateNetworkConfig(networkID, 0, req.Config)
		if err != nil {
			return err
		}

		// Assume failure unless we succeed later on.
		return tx.NetworkErrored(projectName, req.Name)
	})
	if err != nil {
		if response.IsNotFoundError(err) {
			return fmt.Errorf("Network not pending on any node (use --target <node> first)")
		}

		return err
	}

	// Create notifier for other nodes to create the network.
	notifier, err := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAll)
	if err != nil {
		return err
	}

	// Load the network from the database for the local member.
	n, err := network.LoadByName(s, projectName, req.Name)
	if err != nil {
		return fmt.Errorf("Failed loading network: %w", err)
	}

	netConfig := n.Config()

	err = doNetworksCreate(s, n, clientType)
	if err != nil {
		return err
	}

	logger.Debug("Created network on local cluster member", logger.Ctx{"project": projectName, "network": req.Name, "config": netConfig})

	// Remove this node's node specific config keys.
	for _, key := range db.NodeSpecificNetworkConfig {
		delete(netConfig, key)
	}

	// Notify other nodes to create the network.
	err = notifier(func(client incus.InstanceServer) error {
		server, _, err := client.GetServer()
		if err != nil {
			return err
		}

		// Clone the network config for this node so we don't modify it and potentially end up sending
		// this node's config to another node.
		nodeConfig := make(map[string]string, len(netConfig))
		for k, v := range netConfig {
			nodeConfig[k] = v
		}

		// Merge node specific config items into global config.
		for key, value := range nodeConfigs[server.Environment.ServerName] {
			nodeConfig[key] = value
		}

		// Create fresh request based on existing network to send to node.
		nodeReq := api.NetworksPost{
			NetworkPut: api.NetworkPut{
				Config:      nodeConfig,
				Description: n.Description(),
			},
			Name: n.Name(),
			Type: n.Type(),
		}

		err = client.UseProject(n.Project()).CreateNetwork(nodeReq)
		if err != nil {
			return err
		}

		logger.Debug("Created network on cluster member", logger.Ctx{"project": n.Project(), "network": n.Name(), "member": server.Environment.ServerName, "config": nodeReq.Config})

		return nil
	})
	if err != nil {
		return err
	}

	// Mark network global status as networkCreated now that all nodes have succeeded.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.NetworkCreated(projectName, req.Name)
	})
	if err != nil {
		return err
	}

	logger.Debug("Marked network global status as created", logger.Ctx{"project": projectName, "network": req.Name})

	return nil
}

// Create the network on the system. The clusterNotification flag is used to indicate whether creation request
// is coming from a cluster notification (and if so we should not delete the database record on error).
func doNetworksCreate(s *state.State, n network.Network, clientType clusterRequest.ClientType) error {
	revert := revert.New()
	defer revert.Fail()

	// Don't validate network config during pre-cluster-join phase, as if network has ACLs they won't exist
	// in the local database yet. Once cluster join is completed, network will be restarted to give chance for
	// ACL firewall config to be applied.
	if clientType != clusterRequest.ClientTypeJoiner {
		// Validate so that when run on a cluster node the full config (including node specific config)
		// is checked.
		err := n.Validate(n.Config())
		if err != nil {
			return err
		}
	}

	if n.LocalStatus() == api.NetworkStatusCreated {
		logger.Debug("Skipping local network create as already created", logger.Ctx{"project": n.Project(), "network": n.Name()})
		return nil
	}

	// Run initial creation setup for the network driver.
	err := n.Create(clientType)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = n.Delete(clientType) })

	// Only start networks when not doing a cluster pre-join phase (this ensures that networks are only started
	// once the node has fully joined the clustered database and has consistent config with rest of the nodes).
	if clientType != clusterRequest.ClientTypeJoiner {
		err = n.Start()
		if err != nil {
			return err
		}
	}

	// Mark local as status as networkCreated.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.NetworkNodeCreated(n.ID())
	})
	if err != nil {
		return err
	}

	logger.Debug("Marked network local status as created", logger.Ctx{"project": n.Project(), "network": n.Name()})

	revert.Success()
	return nil
}

// swagger:operation GET /1.0/networks/{name} networks network_get
//
//	Get the network
//
//	Gets a specific network.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: server01
//	responses:
//	  "200":
//	    description: Network
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
//	          $ref: "#/definitions/Network"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// If a target was specified, forward the request to the relevant node.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	networkName, err := url.PathUnescape(mux.Vars(r)["networkName"])
	if err != nil {
		return response.SmartError(err)
	}

	allNodes := false
	if s.ServerClustered && request.QueryParam(r, "target") == "" {
		allNodes = true
	}

	n, err := doNetworkGet(s, r, allNodes, projectName, reqProject.Config, networkName)
	if err != nil {
		return response.SmartError(err)
	}

	etag := []any{n.Name, n.Managed, n.Type, n.Description, n.Config}

	return response.SyncResponseETag(true, &n, etag)
}

// doNetworkGet returns information about the specified network.
// If the network being requested is a managed network and allNodes is true then node specific config is removed.
// Otherwise if allNodes is false then the network's local status is returned.
func doNetworkGet(s *state.State, r *http.Request, allNodes bool, projectName string, reqProjectConfig map[string]string, networkName string) (api.Network, error) {
	// Ignore veth pairs (for performance reasons).
	if strings.HasPrefix(networkName, "veth") {
		return api.Network{}, api.StatusErrorf(http.StatusNotFound, "Network not found")
	}

	// Get some information.
	n, err := network.LoadByName(s, projectName, networkName)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return api.Network{}, fmt.Errorf("Failed loading network: %w", err)
	}

	// Don't allow retrieving info about the local server interfaces when not using default project.
	if projectName != api.ProjectDefaultName && n == nil {
		return api.Network{}, api.StatusErrorf(http.StatusNotFound, "Network not found")
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(reqProjectConfig, networkName, n != nil && n.IsManaged()) {
		return api.Network{}, api.StatusErrorf(http.StatusNotFound, "Network not found")
	}

	osInfo, _ := net.InterfaceByName(networkName)

	// Quick check.
	if osInfo == nil && n == nil {
		return api.Network{}, api.StatusErrorf(http.StatusNotFound, "Network not found")
	}

	// Prepare the response.
	apiNet := api.Network{}
	apiNet.Name = networkName
	apiNet.UsedBy = []string{}
	apiNet.Config = map[string]string{}

	// Set the device type as needed.
	if n != nil {
		apiNet.Managed = true
		apiNet.Description = n.Description()
		apiNet.Type = n.Type()

		err = s.Authorizer.CheckPermission(r.Context(), r, auth.ObjectNetwork(projectName, networkName), auth.EntitlementCanEdit)
		if err == nil {
			// Only allow admins to see network config as sensitive info can be stored there.
			apiNet.Config = n.Config()
		} else if !api.StatusErrorCheck(err, http.StatusForbidden) {
			return api.Network{}, err
		}

		// If no member is specified, we omit the node-specific fields.
		if allNodes {
			for _, key := range db.NodeSpecificNetworkConfig {
				delete(apiNet.Config, key)
			}
		}
	} else if osInfo != nil && int(osInfo.Flags&net.FlagLoopback) > 0 {
		apiNet.Type = "loopback"
	} else if util.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", apiNet.Name)) {
		apiNet.Type = "bridge"
	} else if util.PathExists(fmt.Sprintf("/proc/net/vlan/%s", apiNet.Name)) {
		apiNet.Type = "vlan"
	} else if util.PathExists(fmt.Sprintf("/sys/class/net/%s/device", apiNet.Name)) {
		apiNet.Type = "physical"
	} else if util.PathExists(fmt.Sprintf("/sys/class/net/%s/bonding", apiNet.Name)) {
		apiNet.Type = "bond"
	} else {
		vswitch, err := ovs.NewVSwitch()
		if err != nil {
			return api.Network{}, fmt.Errorf("Failed to connect to OVS: %w", err)
		}

		exists, _ := vswitch.BridgeExists(apiNet.Name)
		if exists {
			apiNet.Type = "bridge"
		} else {
			apiNet.Type = "unknown"
		}
	}

	// Look for instances using the interface.
	if apiNet.Type != "loopback" {
		var networkID int64
		if n != nil {
			networkID = n.ID()
		}

		usedBy, err := network.UsedBy(s, projectName, networkID, apiNet.Name, apiNet.Type, false)
		if err != nil {
			return api.Network{}, err
		}

		apiNet.UsedBy = project.FilterUsedBy(s.Authorizer, r, usedBy)
	}

	if n != nil {
		if allNodes {
			apiNet.Status = n.Status()
		} else {
			apiNet.Status = n.LocalStatus()
		}

		apiNet.Locations = n.Locations()
	}

	return apiNet, nil
}

// swagger:operation DELETE /1.0/networks/{name} networks network_delete
//
//	Delete the network
//
//	Removes the network.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	networkName, err := url.PathUnescape(mux.Vars(r)["networkName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the existing network.
	n, err := network.LoadByName(s, projectName, networkName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(reqProject.Config, networkName, n.IsManaged()) {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "Network not found"))
	}

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	clusterNotification := isClusterNotification(r)
	if !clusterNotification {
		// Quick checks.
		inUse, err := n.IsUsed()
		if err != nil {
			return response.SmartError(err)
		}

		if inUse {
			return response.BadRequest(fmt.Errorf("The network is currently in use"))
		}
	}

	if n.LocalStatus() != api.NetworkStatusPending {
		err = n.Delete(clientType)
		if err != nil {
			return response.InternalError(err)
		}
	}

	// If this is a cluster notification, we're done, any database work will be done by the node that is
	// originally serving the request.
	if clusterNotification {
		return response.EmptySyncResponse
	}

	// If we are clustered, also notify all other nodes, if any.
	if s.ServerClustered {
		notifier, err := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAll)
		if err != nil {
			return response.SmartError(err)
		}

		err = notifier(func(client incus.InstanceServer) error {
			return client.UseProject(n.Project()).DeleteNetwork(n.Name())
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Remove the network from the database.
	err = s.DB.Cluster.DeleteNetwork(n.Project(), n.Name())
	if err != nil {
		return response.SmartError(err)
	}

	err = s.Authorizer.DeleteNetwork(r.Context(), projectName, networkName)
	if err != nil {
		logger.Error("Failed to remove network from authorizer", logger.Ctx{"name": networkName, "project": projectName, "error": err})
	}

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(projectName, lifecycle.NetworkDeleted.Event(n, requestor, nil))

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/networks/{name} networks network_post
//
//	Rename the network
//
//	Renames an existing network.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: network
//	    description: Network rename request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// FIXME: renaming a network is currently not supported in clustering
	//        mode. The difficulty is that network.Start() depends on the
	//        network having already been renamed in the database, which is
	//        a chicken-and-egg problem for cluster notifications (the
	//        serving node should typically do the database job, so the
	//        network is not yet renamed inthe db when the notified node
	//        runs network.Start).
	if s.ServerClustered {
		return response.BadRequest(fmt.Errorf("Renaming clustered network not supported"))
	}

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	networkName, err := url.PathUnescape(mux.Vars(r)["networkName"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.NetworkPost{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the existing network.
	n, err := network.LoadByName(s, projectName, networkName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(reqProject.Config, networkName, n.IsManaged()) {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "Network not found"))
	}

	if n.Status() != api.NetworkStatusCreated {
		return response.BadRequest(fmt.Errorf("Cannot rename network when not in created state"))
	}

	// Ensure new name is supplied.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("New network name not provided"))
	}

	err = n.ValidateName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check network isn't in use.
	inUse, err := n.IsUsed()
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed checking network in use: %w", err))
	}

	if inUse {
		return response.BadRequest(fmt.Errorf("Network is currently in use"))
	}

	// Check that the name isn't already in used by an existing managed network.
	networks, err := s.DB.Cluster.GetNetworks(projectName)
	if err != nil {
		return response.InternalError(err)
	}

	if util.ValueInSlice(req.Name, networks) {
		return response.Conflict(fmt.Errorf("Network %q already exists", req.Name))
	}

	// Rename it.
	err = n.Rename(req.Name)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.Authorizer.RenameNetwork(r.Context(), projectName, networkName, req.Name)
	if err != nil {
		logger.Error("Failed to rename network in authorizer", logger.Ctx{"old_name": networkName, "new_name": req.Name, "project": projectName, "error": err})
	}

	requestor := request.CreateRequestor(r)
	lc := lifecycle.NetworkRenamed.Event(n, requestor, map[string]any{"old_name": networkName})
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation PUT /1.0/networks/{name} networks network_put
//
//	Update the network
//
//	Updates the entire network configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: server01
//	  - in: body
//	    name: network
//	    description: Network configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkPut"
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
func networkPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// If a target was specified, forward the request to the relevant node.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	networkName, err := url.PathUnescape(mux.Vars(r)["networkName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the existing network.
	n, err := network.LoadByName(s, projectName, networkName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(reqProject.Config, networkName, n.IsManaged()) {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "Network not found"))
	}

	targetNode := request.QueryParam(r, "target")

	if targetNode == "" && n.Status() != api.NetworkStatusCreated {
		return response.BadRequest(fmt.Errorf("Cannot update network global config when not in created state"))
	}

	// Duplicate config for etag modification and generation.
	etagConfig := localUtil.CopyConfig(n.Config())

	// If no target node is specified and the daemon is clustered, we omit the node-specific fields so that
	// the e-tag can be generated correctly. This is because the GET request used to populate the request
	// will also remove node-specific keys when no target is specified.
	if targetNode == "" && s.ServerClustered {
		for _, key := range db.NodeSpecificNetworkConfig {
			delete(etagConfig, key)
		}
	}

	// Validate the ETag.
	etag := []any{n.Name(), n.IsManaged(), n.Type(), n.Description(), etagConfig}
	err = localUtil.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Decode the request.
	req := api.NetworkPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// In clustered mode, we differentiate between node specific and non-node specific config keys based on
	// whether the user has specified a target to apply the config to.
	if s.ServerClustered {
		if targetNode == "" {
			// If no target is specified, then ensure only non-node-specific config keys are changed.
			for k := range req.Config {
				if util.ValueInSlice(k, db.NodeSpecificNetworkConfig) {
					return response.BadRequest(fmt.Errorf("Config key %q is cluster member specific", k))
				}
			}
		} else {
			curConfig := n.Config()

			// If a target is specified, then ensure only node-specific config keys are changed.
			for k, v := range req.Config {
				if !util.ValueInSlice(k, db.NodeSpecificNetworkConfig) && curConfig[k] != v {
					return response.BadRequest(fmt.Errorf("Config key %q may not be used as member-specific key", k))
				}
			}
		}
	}

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	response := doNetworkUpdate(projectName, n, req, targetNode, clientType, r.Method, s.ServerClustered)

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(projectName, lifecycle.NetworkUpdated.Event(n, requestor, nil))

	return response
}

// swagger:operation PATCH /1.0/networks/{name} networks network_patch
//
//	Partially update the network
//
//	Updates a subset of the network configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: server01
//	  - in: body
//	    name: network
//	    description: Network configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkPut"
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
func networkPatch(d *Daemon, r *http.Request) response.Response {
	return networkPut(d, r)
}

// doNetworkUpdate loads the current local network config, merges with the requested network config, validates
// and applies the changes. Will also notify other cluster nodes of non-node specific config if needed.
func doNetworkUpdate(projectName string, n network.Network, req api.NetworkPut, targetNode string, clientType clusterRequest.ClientType, httpMethod string, clustered bool) response.Response {
	if req.Config == nil {
		req.Config = map[string]string{}
	}

	// Normally a "put" request will replace all existing config, however when clustered, we need to account
	// for the node specific config keys and not replace them when the request doesn't specify a specific node.
	if targetNode == "" && httpMethod != http.MethodPatch && clustered {
		// If non-node specific config being updated via "put" method in cluster, then merge the current
		// node-specific network config with the submitted config to allow validation.
		// This allows removal of non-node specific keys when they are absent from request config.
		for k, v := range n.Config() {
			if util.ValueInSlice(k, db.NodeSpecificNetworkConfig) {
				req.Config[k] = v
			}
		}
	} else if httpMethod == http.MethodPatch {
		// If config being updated via "patch" method, then merge all existing config with the keys that
		// are present in the request config.
		for k, v := range n.Config() {
			_, ok := req.Config[k]
			if !ok {
				req.Config[k] = v
			}
		}
	}

	// Validate the merged configuration.
	err := n.Validate(req.Config)
	if err != nil {
		return response.BadRequest(err)
	}

	// Apply the new configuration (will also notify other cluster nodes if needed).
	err = n.Update(req, targetNode, clientType)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/networks/{name}/leases networks networks_leases_get
//
//	Get the DHCP leases
//
//	Returns a list of DHCP leases for the network.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: server01
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
//	          description: List of DHCP leases
//	          items:
//	            $ref: "#/definitions/NetworkLease"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkLeasesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	networkName, err := url.PathUnescape(mux.Vars(r)["networkName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Attempt to load the network.
	n, err := network.LoadByName(s, projectName, networkName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(reqProject.Config, networkName, n.IsManaged()) {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "Network not found"))
	}

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))
	leases, err := n.Leases(reqProject.Name, clientType)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, leases)
}

func networkStartup(s *state.State) error {
	var err error

	// Get a list of projects.
	var projectNames []string

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectNames, err = dbCluster.GetProjectNames(ctx, tx.Tx())
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed to load projects: %w", err)
	}

	// Build a list of networks to initialise, keyed by project and network name.
	const networkPriorityStandalone = 0 // Start networks not dependent on any other network first.
	const networkPriorityPhysical = 1   // Start networks dependent on physical interfaces second.
	const networkPriorityLogical = 2    // Start networks dependent logical networks third.
	initNetworks := []map[network.ProjectNetwork]struct{}{
		networkPriorityStandalone: make(map[network.ProjectNetwork]struct{}),
		networkPriorityPhysical:   make(map[network.ProjectNetwork]struct{}),
		networkPriorityLogical:    make(map[network.ProjectNetwork]struct{}),
	}

	for _, projectName := range projectNames {
		networkNames, err := s.DB.Cluster.GetCreatedNetworks(projectName)
		if err != nil {
			return fmt.Errorf("Failed to load networks for project %q: %w", projectName, err)
		}

		for _, networkName := range networkNames {
			pn := network.ProjectNetwork{
				ProjectName: projectName,
				NetworkName: networkName,
			}

			// Assume all networks are networkPriorityStandalone initially.
			initNetworks[networkPriorityStandalone][pn] = struct{}{}
		}
	}

	loadedNetworks := make(map[network.ProjectNetwork]network.Network)

	initNetwork := func(n network.Network, priority int) error {
		err = n.Start()
		if err != nil {
			err = fmt.Errorf("Failed starting: %w", err)
			_ = s.DB.Cluster.UpsertWarningLocalNode(n.Project(), dbCluster.TypeNetwork, int(n.ID()), warningtype.NetworkUnvailable, err.Error())

			return err
		}

		logger.Info("Initialized network", logger.Ctx{"project": n.Project(), "name": n.Name()})

		// Network initialized successfully so remove it from the list so its not retried.
		pn := network.ProjectNetwork{
			ProjectName: n.Project(),
			NetworkName: n.Name(),
		}

		delete(initNetworks[priority], pn)

		_ = warnings.ResolveWarningsByLocalNodeAndProjectAndTypeAndEntity(s.DB.Cluster, n.Project(), warningtype.NetworkUnvailable, dbCluster.TypeNetwork, int(n.ID()))

		return nil
	}

	loadAndInitNetwork := func(pn network.ProjectNetwork, priority int, firstPass bool) error {
		var err error
		var n network.Network

		if firstPass && loadedNetworks[pn] != nil {
			// Check if network already loaded from during first pass phase.
			n = loadedNetworks[pn]
		} else {
			n, err = network.LoadByName(s, pn.ProjectName, pn.NetworkName)
			if err != nil {
				if api.StatusErrorCheck(err, http.StatusNotFound) {
					// Network has been deleted since we began trying to start it so delete
					// entry.
					delete(initNetworks[priority], pn)

					return nil
				}

				return fmt.Errorf("Failed loading: %w", err)
			}
		}

		netConfig := n.Config()
		err = n.Validate(netConfig)
		if err != nil {
			return fmt.Errorf("Failed validating: %w", err)
		}

		// Update network start priority based on dependencies.
		if netConfig["parent"] != "" && priority != networkPriorityPhysical {
			// Start networks that depend on physical interfaces existing after
			// non-dependent networks.
			delete(initNetworks[priority], pn)
			initNetworks[networkPriorityPhysical][pn] = struct{}{}

			return nil
		} else if netConfig["network"] != "" && priority != networkPriorityLogical {
			// Start networks that depend on other logical networks after networks after
			// non-dependent networks and networks that depend on physical interfaces.
			delete(initNetworks[priority], pn)
			initNetworks[networkPriorityLogical][pn] = struct{}{}

			return nil
		}

		err = initNetwork(n, priority)
		if err != nil {
			return err
		}

		return nil
	}

	// Try initializing networks in priority order.
	for priority := range initNetworks {
		for pn := range initNetworks[priority] {
			err := loadAndInitNetwork(pn, priority, true)
			if err != nil {
				logger.Error("Failed initializing network", logger.Ctx{"project": pn.ProjectName, "network": pn.NetworkName, "err": err})

				continue
			}
		}
	}

	loadedNetworks = nil // Don't store loaded networks after first pass.

	remainingNetworks := 0
	for _, networks := range initNetworks {
		remainingNetworks += len(networks)
	}

	// For any remaining networks that were not successfully initialised, we now start a go routine to
	// periodically try to initialize them again in the background.
	if remainingNetworks > 0 {
		go func() {
			for {
				t := time.NewTimer(time.Duration(time.Minute))

				select {
				case <-s.ShutdownCtx.Done():
					t.Stop()
					return
				case <-t.C:
					t.Stop()

					tryInstancesStart := false

					// Try initializing networks in priority order.
					for priority := range initNetworks {
						for pn := range initNetworks[priority] {
							err := loadAndInitNetwork(pn, priority, false)
							if err != nil {
								logger.Error("Failed initializing network", logger.Ctx{"project": pn.ProjectName, "network": pn.NetworkName, "err": err})

								continue
							}

							tryInstancesStart = true // We initialized at least one network.
						}
					}

					remainingNetworks := 0
					for _, networks := range initNetworks {
						remainingNetworks += len(networks)
					}

					if remainingNetworks <= 0 {
						logger.Info("All networks initialized")
					}

					// At least one remaining network was initialized, check if any instances
					// can now start.
					if tryInstancesStart {
						instances, err := instance.LoadNodeAll(s, instancetype.Any)
						if err != nil {
							logger.Warn("Failed loading instances to start", logger.Ctx{"err": err})
						} else {
							instancesStart(s, instances)
						}
					}

					if remainingNetworks <= 0 {
						return // Our job here is done.
					}
				}
			}
		}()
	} else {
		logger.Info("All networks initialized")
	}

	return nil
}

func networkShutdown(s *state.State) {
	var err error

	// Get a list of projects.
	var projectNames []string

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectNames, err = dbCluster.GetProjectNames(ctx, tx.Tx())
		return err
	})
	if err != nil {
		logger.Error("Failed shutting down networks, couldn't load projects", logger.Ctx{"err": err})
		return
	}

	for _, projectName := range projectNames {
		// Get a list of managed networks.
		networks, err := s.DB.Cluster.GetNetworks(projectName)
		if err != nil {
			logger.Error("Failed shutting down networks, couldn't load networks for project", logger.Ctx{"project": projectName, "err": err})
			continue
		}

		// Bring them all down.
		for _, name := range networks {
			n, err := network.LoadByName(s, projectName, name)
			if err != nil {
				logger.Error("Failed shutting down network, couldn't load network", logger.Ctx{"network": name, "project": projectName, "err": err})
				continue
			}

			err = n.Stop()
			if err != nil {
				logger.Error("Failed to bring down network", logger.Ctx{"err": err, "project": projectName, "name": name})
			}
		}
	}
}

// networkRestartOVN is used to trigger a restart of all OVN networks.
func networkRestartOVN(s *state.State) error {
	logger.Infof("Restarting OVN networks")

	// Get a list of projects.
	var projectNames []string
	var err error
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectNames, err = dbCluster.GetProjectNames(ctx, tx.Tx())
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed to load projects: %w", err)
	}

	// Go over all the networks in every project.
	for _, projectName := range projectNames {
		networkNames, err := s.DB.Cluster.GetCreatedNetworks(projectName)
		if err != nil {
			return fmt.Errorf("Failed to load networks for project %q: %w", projectName, err)
		}

		for _, networkName := range networkNames {
			// Load the network struct.
			n, err := network.LoadByName(s, projectName, networkName)
			if err != nil {
				return fmt.Errorf("Failed to load network %q in project %q: %w", networkName, projectName, err)
			}

			// Skip non-OVN networks.
			if n.DBType() != db.NetworkTypeOVN {
				continue
			}

			// Restart the network.
			err = n.Start()
			if err != nil {
				return fmt.Errorf("Failed to restart network %q in project %q: %w", networkName, projectName, err)
			}
		}
	}

	return nil
}

// swagger:operation GET /1.0/networks/{name}/state networks networks_state_get
//
//	Get the network state
//
//	Returns the current network state information.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: server01
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
//	          $ref: "#/definitions/NetworkState"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkStateGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// If a target was specified, forward the request to the relevant node.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	networkName, err := url.PathUnescape(mux.Vars(r)["networkName"])
	if err != nil {
		return response.SmartError(err)
	}

	n, err := network.LoadByName(s, projectName, networkName)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(reqProject.Config, networkName, n != nil && n.IsManaged()) {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "Network not found"))
	}

	var state *api.NetworkState
	if n != nil {
		state, err = n.State()
		if err != nil {
			return response.SmartError(err)
		}
	} else {
		state, err = resources.GetNetworkState(networkName)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponse(true, state)
}
