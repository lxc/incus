package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/gorilla/mux"

	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/server/auth"
	"github.com/lxc/incus/v6/internal/server/cluster"
	clusterRequest "github.com/lxc/incus/v6/internal/server/cluster/request"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/lifecycle"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/internal/server/state"
	storagePools "github.com/lxc/incus/v6/internal/server/storage"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
)

// Lock to prevent concurent storage pools creation.
var storagePoolCreateLock sync.Mutex

var storagePoolsCmd = APIEndpoint{
	Path: "storage-pools",

	Get:  APIEndpointAction{Handler: storagePoolsGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: storagePoolsPost, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanCreateStoragePools)},
}

var storagePoolCmd = APIEndpoint{
	Path: "storage-pools/{poolName}",

	Delete: APIEndpointAction{Handler: storagePoolDelete, AccessHandler: allowPermission(auth.ObjectTypeStoragePool, auth.EntitlementCanEdit, "poolName")},
	Get:    APIEndpointAction{Handler: storagePoolGet, AccessHandler: allowPermission(auth.ObjectTypeStoragePool, auth.EntitlementCanView, "poolName")},
	Patch:  APIEndpointAction{Handler: storagePoolPatch, AccessHandler: allowPermission(auth.ObjectTypeStoragePool, auth.EntitlementCanEdit, "poolName")},
	Put:    APIEndpointAction{Handler: storagePoolPut, AccessHandler: allowPermission(auth.ObjectTypeStoragePool, auth.EntitlementCanEdit, "poolName")},
}

// swagger:operation GET /1.0/storage-pools storage storage_pools_get
//
//  Get the storage pools
//
//  Returns a list of storage pools (URLs).
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
//                "/1.0/storage-pools/local",
//                "/1.0/storage-pools/remote"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools?recursion=1 storage storage_pools_get_recursion1
//
//	Get the storage pools
//
//	Returns a list of storage pools (structs).
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
//	          description: List of storage pools
//	          items:
//	            $ref: "#/definitions/StoragePool"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	recursion := localUtil.IsRecursionRequest(r)

	var poolNames []string

	err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		poolNames, err = tx.GetStoragePoolNames(ctx)

		return err
	})
	if err != nil && !response.IsNotFoundError(err) {
		return response.SmartError(err)
	}

	hasEditPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanEdit, auth.ObjectTypeStoragePool)
	if err != nil {
		return response.InternalError(err)
	}

	resultString := []string{}
	resultMap := []api.StoragePool{}
	for _, poolName := range poolNames {
		if !recursion {
			resultString = append(resultString, fmt.Sprintf("/%s/storage-pools/%s", version.APIVersion, poolName))
		} else {
			pool, err := storagePools.LoadByName(s, poolName)
			if err != nil {
				return response.SmartError(err)
			}

			// Get all users of the storage pool.
			poolUsedBy, err := storagePools.UsedBy(r.Context(), s, pool, false, false)
			if err != nil {
				return response.SmartError(err)
			}

			poolAPI := pool.ToAPI()
			poolAPI.UsedBy = project.FilterUsedBy(s.Authorizer, r, poolUsedBy)

			if !hasEditPermission(auth.ObjectStoragePool(poolName)) {
				// Don't allow non-admins to see pool config as sensitive info can be stored there.
				poolAPI.Config = nil
			}

			// If no member is specified and the daemon is clustered, we omit the node-specific fields.
			if s.ServerClustered {
				for _, key := range db.NodeSpecificStorageConfig {
					delete(poolAPI.Config, key)
				}
			} else {
				// Use local status if not clustered. To allow seeing unavailable pools.
				poolAPI.Status = pool.LocalStatus()
			}

			resultMap = append(resultMap, poolAPI)
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

// swagger:operation POST /1.0/storage-pools storage storage_pools_post
//
//	Add a storage pool
//
//	Creates a new storage pool.
//	When clustered, storage pools require individual POST for each cluster member prior to a global POST.
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
//	    name: storage
//	    description: Storage pool
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StoragePoolsPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	storagePoolCreateLock.Lock()
	defer storagePoolCreateLock.Unlock()

	req := api.StoragePoolsPost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return response.BadRequest(fmt.Errorf("Storage pool names may not contain slashes"))
	}

	if req.Driver == "" {
		return response.BadRequest(fmt.Errorf("No driver provided"))
	}

	if req.Config == nil {
		req.Config = map[string]string{}
	}

	ctx := logger.Ctx{}

	targetNode := request.QueryParam(r, "target")
	if targetNode != "" {
		ctx["target"] = targetNode
	}

	lc := lifecycle.StoragePoolCreated.Event(req.Name, request.CreateRequestor(r), ctx)
	resp := response.SyncResponseLocation(true, nil, lc.Source)

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	if isClusterNotification(r) {
		// This is an internal request which triggers the actual
		// creation of the pool across all nodes, after they have been
		// previously defined.
		err = storagePoolValidate(s, req.Name, req.Driver, req.Config)
		if err != nil {
			return response.BadRequest(err)
		}

		var poolID int64

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error

			poolID, err = tx.GetStoragePoolID(ctx, req.Name)

			return err
		})
		if err != nil {
			return response.SmartError(err)
		}

		_, err = storagePoolCreateLocal(r.Context(), s, poolID, req, clientType)
		if err != nil {
			return response.SmartError(err)
		}

		return resp
	}

	if targetNode != "" {
		// A targetNode was specified, let's just define the node's storage without actually creating it.
		// The only legal key values for the storage config are the ones in NodeSpecificStorageConfig.
		for key := range req.Config {
			if !slices.Contains(db.NodeSpecificStorageConfig, key) {
				return response.SmartError(fmt.Errorf("Config key %q may not be used as member-specific key", key))
			}
		}

		err = storagePoolValidate(s, req.Name, req.Driver, req.Config)
		if err != nil {
			return response.BadRequest(err)
		}

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.CreatePendingStoragePool(ctx, targetNode, req.Name, req.Driver, req.Config)
		})
		if err != nil {
			if err == db.ErrAlreadyDefined {
				return response.BadRequest(fmt.Errorf("The storage pool already defined on member %q", targetNode))
			}

			return response.SmartError(err)
		}

		return resp
	}

	var pool *api.StoragePool

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Load existing pool if exists, if not don't fail.
		_, pool, _, err = tx.GetStoragePoolInAnyState(ctx, req.Name)

		return err
	})
	if err != nil && !response.IsNotFoundError(err) {
		return response.InternalError(err)
	}

	// Check if we're clustered.
	count, err := cluster.Count(s)
	if err != nil {
		return response.SmartError(err)
	}

	// No targetNode was specified and we're clustered or there is an existing partially created single node
	// pool, either way finalize the config in the db and actually create the pool on all nodes in the cluster.
	if count > 1 || (pool != nil && pool.Status != api.StoragePoolStatusCreated) {
		err = storagePoolsPostCluster(r.Context(), s, pool, req, clientType)
		if err != nil {
			return response.InternalError(err)
		}
	} else {
		// Create new single node storage pool.
		err = storagePoolCreateGlobal(r.Context(), s, req, clientType)
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Add the storage pool to the authorizer.
	err = s.Authorizer.AddStoragePool(r.Context(), req.Name)
	if err != nil {
		logger.Error("Failed to add storage pool to authorizer", logger.Ctx{"name": req.Name, "error": err})
	}

	s.Events.SendLifecycle(api.ProjectDefaultName, lc)

	return resp
}

// storagePoolPartiallyCreated returns true of supplied storage pool has properties that indicate it has had
// previous create attempts run on it but failed on one or more nodes.
func storagePoolPartiallyCreated(pool *api.StoragePool) bool {
	// If the pool status is StoragePoolStatusErrored, this means create has been run in the past and has
	// failed on one or more nodes. Hence it is partially created.
	if pool.Status == api.StoragePoolStatusErrored {
		return true
	}

	// If the pool has global config keys, then it has previously been created by having its global config
	// inserted, and this means it is partialled created.
	for key := range pool.Config {
		if !slices.Contains(db.NodeSpecificStorageConfig, key) {
			return true
		}
	}

	return false
}

// storagePoolsPostCluster handles creating storage pools after the per-node config records have been created.
// Accepts an optional existing pool record, which will exist when performing subsequent re-create attempts.
func storagePoolsPostCluster(ctx context.Context, s *state.State, pool *api.StoragePool, req api.StoragePoolsPost, clientType clusterRequest.ClientType) error {
	// Check that no node-specific config key has been defined.
	for key := range req.Config {
		if slices.Contains(db.NodeSpecificStorageConfig, key) {
			return fmt.Errorf("Config key %q is cluster member specific", key)
		}
	}

	// If pool already exists, perform quick checks.
	if pool != nil {
		// Check pool isn't already created.
		if pool.Status == api.StoragePoolStatusCreated {
			return fmt.Errorf("The storage pool is already created")
		}

		// Check the requested pool type matches the type created when adding the local member config.
		if req.Driver != pool.Driver {
			return fmt.Errorf("Requested storage pool driver %q doesn't match driver in existing database record %q", req.Driver, pool.Driver)
		}
	}

	// Check that the pool is properly defined, fetch the node-specific configs and insert the global config.
	var configs map[string]map[string]string
	var poolID int64
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Check that the pool was defined at all. Must come before partially created checks.
		poolID, err = tx.GetStoragePoolID(ctx, req.Name)
		if err != nil {
			return err
		}

		// Check if any global config exists already, if so we should not create global config again.
		if pool != nil && storagePoolPartiallyCreated(pool) {
			if len(req.Config) > 0 {
				return fmt.Errorf("Storage pool already partially created. Please do not specify any global config when re-running create")
			}

			logger.Debug("Skipping global storage pool create as global config already partially created", logger.Ctx{"pool": req.Name})
			return nil
		}

		// Fetch the node-specific configs and check the pool is defined for all nodes.
		configs, err = tx.GetStoragePoolNodeConfigs(ctx, poolID)
		if err != nil {
			return err
		}

		// Insert the global config keys.
		err = tx.CreateStoragePoolConfig(poolID, 0, req.Config)
		if err != nil {
			return err
		}

		// Assume failure unless we succeed later on.
		return tx.StoragePoolErrored(req.Name)
	})
	if err != nil {
		if response.IsNotFoundError(err) {
			return fmt.Errorf("Pool not pending on any node (use --target <node> first)")
		}

		return err
	}

	// Create notifier for other nodes to create the storage pool.
	notifier, err := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAll)
	if err != nil {
		return err
	}

	// Create the pool on this node.
	nodeReq := req

	// Merge node specific config items into global config.
	for key, value := range configs[s.ServerName] {
		nodeReq.Config[key] = value
	}

	updatedConfig, err := storagePoolCreateLocal(ctx, s, poolID, req, clientType)
	if err != nil {
		return err
	}

	req.Config = updatedConfig
	logger.Debug("Created storage pool on local cluster member", logger.Ctx{"pool": req.Name})

	// Strip node specific config keys from config. Very important so we don't forward node-specific config.
	for _, k := range db.NodeSpecificStorageConfig {
		delete(req.Config, k)
	}

	// Notify all other nodes to create the pool.
	err = notifier(func(client incus.InstanceServer) error {
		server, _, err := client.GetServer()
		if err != nil {
			return err
		}

		nodeReq := req

		// Clone fresh node config so we don't modify req.Config with this node's specific config which
		// could result in it being sent to other nodes later.
		nodeReq.Config = make(map[string]string, len(req.Config))
		for k, v := range req.Config {
			nodeReq.Config[k] = v
		}

		// Merge node specific config items into global config.
		for key, value := range configs[server.Environment.ServerName] {
			nodeReq.Config[key] = value
		}

		err = client.CreateStoragePool(nodeReq)
		if err != nil {
			return err
		}

		logger.Debug("Created storage pool on cluster member", logger.Ctx{"pool": req.Name, "member": server.Environment.ServerName})

		return nil
	})
	if err != nil {
		return err
	}

	// Finally update the storage pool state.
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.StoragePoolCreated(req.Name)
	})
	if err != nil {
		return err
	}

	logger.Debug("Marked storage pool global status as created", logger.Ctx{"pool": req.Name})

	return nil
}

// swagger:operation GET /1.0/storage-pools/{poolName} storage storage_pool_get
//
//	Get the storage pool
//
//	Gets a specific storage pool.
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
//	    description: Storage pool
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
//	          $ref: "#/definitions/StoragePool"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// If a target was specified, forward the request to the relevant node.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	memberSpecific := false
	if request.QueryParam(r, "target") != "" {
		memberSpecific = true
	}

	// Get the existing storage pool.
	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get all users of the storage pool.
	poolUsedBy, err := storagePools.UsedBy(r.Context(), s, pool, false, memberSpecific)
	if err != nil {
		return response.SmartError(err)
	}

	poolAPI := pool.ToAPI()
	poolAPI.UsedBy = project.FilterUsedBy(s.Authorizer, r, poolUsedBy)

	err = s.Authorizer.CheckPermission(r.Context(), r, auth.ObjectStoragePool(poolName), auth.EntitlementCanEdit)
	if err != nil && api.StatusErrorCheck(err, http.StatusForbidden) {
		// Don't allow non-admins to see pool config as sensitive info can be stored there.
		poolAPI.Config = nil
	} else if err != nil {
		return response.SmartError(err)
	}

	// If no member is specified and the daemon is clustered, we omit the node-specific fields.
	if s.ServerClustered && !memberSpecific {
		for _, key := range db.NodeSpecificStorageConfig {
			delete(poolAPI.Config, key)
		}
	} else {
		// Use local status if not clustered or memberSpecific. To allow seeing unavailable pools.
		poolAPI.Status = pool.LocalStatus()
	}

	etag := []any{pool.Name(), pool.Driver().Info().Name, pool.Description(), poolAPI.Config}

	return response.SyncResponseETag(true, &poolAPI, etag)
}

// swagger:operation PUT /1.0/storage-pools/{poolName} storage storage_pool_put
//
//	Update the storage pool
//
//	Updates the entire storage pool configuration.
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
//	    name: storage pool
//	    description: Storage pool configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StoragePoolPut"
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
func storagePoolPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// If a target was specified, forward the request to the relevant node.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the existing storage pool.
	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	targetNode := request.QueryParam(r, "target")

	if targetNode == "" && pool.Status() != api.StoragePoolStatusCreated {
		return response.BadRequest(fmt.Errorf("Cannot update storage pool global config when not in created state"))
	}

	// Duplicate config for etag modification and generation.
	etagConfig := localUtil.CopyConfig(pool.Driver().Config())

	// If no target node is specified and the daemon is clustered, we omit the node-specific fields so that
	// the e-tag can be generated correctly. This is because the GET request used to populate the request
	// will also remove node-specific keys when no target is specified.
	if targetNode == "" && s.ServerClustered {
		for _, key := range db.NodeSpecificStorageConfig {
			delete(etagConfig, key)
		}
	}

	// Validate the ETag.
	etag := []any{pool.Name(), pool.Driver().Info().Name, pool.Description(), etagConfig}

	err = localUtil.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Decode the request.
	req := api.StoragePoolPut{}
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
				if slices.Contains(db.NodeSpecificStorageConfig, k) {
					return response.BadRequest(fmt.Errorf("Config key %q is cluster member specific", k))
				}
			}
		} else {
			curConfig := pool.Driver().Config()

			// If a target is specified, then ensure only node-specific config keys are changed.
			for k, v := range req.Config {
				if !slices.Contains(db.NodeSpecificStorageConfig, k) && curConfig[k] != v {
					return response.BadRequest(fmt.Errorf("Config key %q may not be used as cluster member specific key", k))
				}
			}
		}
	}

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	response := doStoragePoolUpdate(s, pool, req, targetNode, clientType, r.Method, s.ServerClustered)

	requestor := request.CreateRequestor(r)

	ctx := logger.Ctx{}
	if targetNode != "" {
		ctx["target"] = targetNode
	}

	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.StoragePoolUpdated.Event(pool.Name(), requestor, ctx))

	return response
}

// swagger:operation PATCH /1.0/storage-pools/{poolName} storage storage_pool_patch
//
//	Partially update the storage pool
//
//	Updates a subset of the storage pool configuration.
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
//	    name: storage pool
//	    description: Storage pool configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StoragePoolPut"
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
func storagePoolPatch(d *Daemon, r *http.Request) response.Response {
	return storagePoolPut(d, r)
}

// doStoragePoolUpdate takes the current local storage pool config, merges with the requested storage pool config,
// validates and applies the changes. Will also notify other cluster nodes of non-node specific config if needed.
func doStoragePoolUpdate(s *state.State, pool storagePools.Pool, req api.StoragePoolPut, targetNode string, clientType clusterRequest.ClientType, httpMethod string, clustered bool) response.Response {
	if req.Config == nil {
		req.Config = map[string]string{}
	}

	// Normally a "put" request will replace all existing config, however when clustered, we need to account
	// for the node specific config keys and not replace them when the request doesn't specify a specific node.
	if targetNode == "" && httpMethod != http.MethodPatch && clustered {
		// If non-node specific config being updated via "put" method in cluster, then merge the current
		// node-specific network config with the submitted config to allow validation.
		// This allows removal of non-node specific keys when they are absent from request config.
		for k, v := range pool.Driver().Config() {
			if slices.Contains(db.NodeSpecificStorageConfig, k) {
				req.Config[k] = v
			}
		}
	} else if httpMethod == http.MethodPatch {
		// If config being updated via "patch" method, then merge all existing config with the keys that
		// are present in the request config.
		for k, v := range pool.Driver().Config() {
			_, ok := req.Config[k]
			if !ok {
				req.Config[k] = v
			}
		}
	}

	// Validate the configuration.
	err := pool.Validate(req.Config)
	if err != nil {
		return response.BadRequest(err)
	}

	// Notify the other nodes, unless this is itself a notification.
	if clustered && clientType != clusterRequest.ClientTypeNotifier && targetNode == "" {
		notifier, err := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAll)
		if err != nil {
			return response.SmartError(err)
		}

		sendPool := req
		sendPool.Config = make(map[string]string)
		for k, v := range req.Config {
			// Don't forward node specific keys (these will be merged in on recipient node).
			if slices.Contains(db.NodeSpecificStorageConfig, k) {
				continue
			}

			sendPool.Config[k] = v
		}

		err = notifier(func(client incus.InstanceServer) error {
			return client.UpdateStoragePool(pool.Name(), sendPool, "")
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	err = pool.Update(clientType, req.Description, req.Config, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/storage-pools/{poolName} storage storage_pools_delete
//
//	Delete the storage pool
//
//	Removes the storage pool.
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
func storagePoolDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))
	clusterNotification := isClusterNotification(r)
	var notifier cluster.Notifier
	if !clusterNotification {
		// Quick checks.
		inUse, err := pool.IsUsed()
		if err != nil {
			return response.SmartError(err)
		}

		if inUse {
			return response.BadRequest(fmt.Errorf("The storage pool is currently in use"))
		}

		// Get the cluster notifier
		notifier, err = cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAll)
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Only perform the deletion of remote image volumes on the server handling the request.
	// Otherwise delete local image volumes on each server.
	if !clusterNotification || !pool.Driver().Info().Remote {
		var removeImgFingerprints []string

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Get all the volumes using the storage pool on this server.
			// Only image volumes should remain now.
			volumes, err := tx.GetStoragePoolVolumes(ctx, pool.ID(), true)
			if err != nil {
				return fmt.Errorf("Failed loading storage volumes: %w", err)
			}

			for _, vol := range volumes {
				if vol.Type != db.StoragePoolVolumeTypeNameImage {
					return fmt.Errorf("Volume %q of type %q in project %q still exists in storage pool %q", vol.Name, vol.Type, vol.Project, pool.Name())
				}

				removeImgFingerprints = append(removeImgFingerprints, vol.Name)
			}

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

		for _, removeImgFingerprint := range removeImgFingerprints {
			err = pool.DeleteImage(removeImgFingerprint, nil)
			if err != nil {
				return response.InternalError(fmt.Errorf("Error deleting image %q from storage pool %q: %w", removeImgFingerprint, pool.Name(), err))
			}
		}
	}

	if pool.LocalStatus() != api.StoragePoolStatusPending {
		err = pool.Delete(clientType, nil)
		if err != nil {
			return response.InternalError(err)
		}
	}

	// If this is a cluster notification, we're done, any database work will be done by the node that is
	// originally serving the request.
	if clusterNotification {
		return response.EmptySyncResponse
	}

	// If we are clustered, also notify all other nodes.
	err = notifier(func(client incus.InstanceServer) error {
		_, _, err := client.GetServer()
		if err != nil {
			return err
		}

		return client.DeleteStoragePool(pool.Name())
	})
	if err != nil {
		return response.SmartError(err)
	}

	err = dbStoragePoolDeleteAndUpdateCache(r.Context(), s, pool.Name())
	if err != nil {
		return response.SmartError(err)
	}

	// Remove the storage pool from the authorizer.
	err = s.Authorizer.DeleteStoragePool(r.Context(), pool.Name())
	if err != nil {
		logger.Error("Failed to remove storage pool from authorizer", logger.Ctx{"name": pool.Name(), "error": err})
	}

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.StoragePoolDeleted.Event(pool.Name(), requestor, nil))

	return response.EmptySyncResponse
}
