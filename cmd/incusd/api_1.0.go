package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/revert"
	"github.com/lxc/incus/v6/internal/server/auth"
	"github.com/lxc/incus/v6/internal/server/auth/oidc"
	"github.com/lxc/incus/v6/internal/server/cluster"
	clusterConfig "github.com/lxc/incus/v6/internal/server/cluster/config"
	"github.com/lxc/incus/v6/internal/server/config"
	"github.com/lxc/incus/v6/internal/server/db"
	instanceDrivers "github.com/lxc/incus/v6/internal/server/instance/drivers"
	"github.com/lxc/incus/v6/internal/server/lifecycle"
	"github.com/lxc/incus/v6/internal/server/node"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	scriptletLoad "github.com/lxc/incus/v6/internal/server/scriptlet/load"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/osarch"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

var api10Cmd = APIEndpoint{
	Get:   APIEndpointAction{Handler: api10Get, AllowUntrusted: true},
	Patch: APIEndpointAction{Handler: api10Patch, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
	Put:   APIEndpointAction{Handler: api10Put, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var api10 = []APIEndpoint{
	api10Cmd,
	api10ResourcesCmd,
	certificateCmd,
	certificatesCmd,
	clusterCmd,
	clusterGroupCmd,
	clusterGroupsCmd,
	clusterNodeCmd,
	clusterNodeStateCmd,
	clusterNodesCmd,
	clusterCertificateCmd,
	instanceBackupCmd,
	instanceBackupExportCmd,
	instanceBackupsCmd,
	instanceCmd,
	instanceConsoleCmd,
	instanceExecCmd,
	instanceFileCmd,
	instanceExecOutputCmd,
	instanceExecOutputsCmd,
	instanceLogCmd,
	instanceLogsCmd,
	instanceMetadataCmd,
	instanceMetadataTemplatesCmd,
	instancesCmd,
	instanceRebuildCmd,
	instanceSFTPCmd,
	instanceSnapshotCmd,
	instanceSnapshotsCmd,
	instanceStateCmd,
	eventsCmd,
	imageAliasCmd,
	imageAliasesCmd,
	imageCmd,
	imageExportCmd,
	imageRefreshCmd,
	imagesCmd,
	imageSecretCmd,
	metadataConfigurationCmd,
	networkCmd,
	networkLeasesCmd,
	networksCmd,
	networkStateCmd,
	networkACLCmd,
	networkACLsCmd,
	networkACLLogCmd,
	networkAllocationsCmd,
	networkForwardCmd,
	networkForwardsCmd,
	networkIntegrationCmd,
	networkIntegrationsCmd,
	networkLoadBalancerCmd,
	networkLoadBalancersCmd,
	networkPeerCmd,
	networkPeersCmd,
	networkZoneCmd,
	networkZonesCmd,
	networkZoneRecordCmd,
	networkZoneRecordsCmd,
	operationCmd,
	operationsCmd,
	operationWait,
	operationWebsocket,
	profileCmd,
	profilesCmd,
	projectCmd,
	projectsCmd,
	projectStateCmd,
	storagePoolCmd,
	storagePoolResourcesCmd,
	storagePoolsCmd,
	storagePoolBucketsCmd,
	storagePoolBucketCmd,
	storagePoolBucketKeysCmd,
	storagePoolBucketKeyCmd,
	storagePoolBucketBackupsCmd,
	storagePoolBucketBackupCmd,
	storagePoolBucketBackupsExportCmd,
	storagePoolVolumesCmd,
	storagePoolVolumeSnapshotsTypeCmd,
	storagePoolVolumeSnapshotTypeCmd,
	storagePoolVolumesTypeCmd,
	storagePoolVolumeTypeCmd,
	storagePoolVolumeTypeCustomBackupsCmd,
	storagePoolVolumeTypeCustomBackupCmd,
	storagePoolVolumeTypeCustomBackupExportCmd,
	storagePoolVolumeTypeStateCmd,
	warningsCmd,
	warningCmd,
	metricsCmd,
}

// swagger:operation GET /1.0?public server server_get_untrusted
//
//  Get the server environment
//
//  Shows a small subset of the server environment and configuration
//  which is required by untrusted clients to reach a server.
//
//  The `?public` part of the URL isn't required, it's simply used to
//  separate the two behaviors of this endpoint.
//
//  ---
//  produces:
//    - application/json
//  responses:
//    "200":
//      description: Server environment and configuration
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
//            $ref: "#/definitions/ServerUntrusted"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0 server server_get
//
//	Get the server environment and configuration
//
//	Shows the full server environment and configuration.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: server01
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: Server environment and configuration
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
//	          $ref: "#/definitions/Server"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func api10Get(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the authentication methods.
	authMethods := []string{api.AuthenticationMethodTLS}

	oidcIssuer, oidcClientID, _, _ := s.GlobalConfig.OIDCServer()
	if oidcIssuer != "" && oidcClientID != "" {
		authMethods = append(authMethods, api.AuthenticationMethodOIDC)
	}

	srv := api.ServerUntrusted{
		APIExtensions: version.APIExtensions,
		APIStatus:     "stable",
		APIVersion:    version.APIVersion,
		Public:        false,
		Auth:          "untrusted",
		AuthMethods:   authMethods,
	}

	// If untrusted, return now
	if d.checkTrustedClient(r) != nil {
		return response.SyncResponseETag(true, srv, nil)
	}

	// If not authorized, return now.
	err := s.Authorizer.CheckPermission(r.Context(), r, auth.ObjectServer(), auth.EntitlementCanView)
	if err != nil {
		return response.SmartError(err)
	}

	// If a target was specified, forward the request to the relevant node.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	srv.Auth = "trusted"

	localHTTPSAddress := s.LocalConfig.HTTPSAddress()

	addresses, err := localUtil.ListenAddresses(localHTTPSAddress)
	if err != nil {
		return response.InternalError(err)
	}

	// When clustered, use the node name, otherwise use the hostname.
	var serverName string
	if s.ServerClustered {
		serverName = s.ServerName
	} else {
		hostname, err := os.Hostname()
		if err != nil {
			return response.SmartError(err)
		}

		serverName = hostname
	}

	certificate := string(s.Endpoints.NetworkPublicKey())
	var certificateFingerprint string
	if certificate != "" {
		certificateFingerprint, err = localtls.CertFingerprintStr(certificate)
		if err != nil {
			return response.InternalError(err)
		}
	}

	architectures := []string{}

	for _, architecture := range s.OS.Architectures {
		architectureName, err := osarch.ArchitectureName(architecture)
		if err != nil {
			return response.InternalError(err)
		}

		architectures = append(architectures, architectureName)
	}

	projectName := r.FormValue("project")
	if projectName == "" {
		projectName = api.ProjectDefaultName
	}

	env := api.ServerEnvironment{
		Addresses:              addresses,
		Architectures:          architectures,
		Certificate:            certificate,
		CertificateFingerprint: certificateFingerprint,
		Kernel:                 s.OS.Uname.Sysname,
		KernelArchitecture:     s.OS.Uname.Machine,
		KernelVersion:          s.OS.Uname.Release,
		OSName:                 s.OS.ReleaseInfo["NAME"],
		OSVersion:              s.OS.ReleaseInfo["VERSION_ID"],
		Project:                projectName,
		Server:                 "incus",
		ServerPid:              os.Getpid(),
		ServerVersion:          version.Version,
		ServerClustered:        s.ServerClustered,
		ServerEventMode:        string(cluster.ServerEventMode()),
		ServerName:             serverName,
		Firewall:               s.Firewall.String(),
	}

	env.KernelFeatures = map[string]string{
		"netnsid_getifaddrs":        fmt.Sprintf("%v", s.OS.NetnsGetifaddrs),
		"uevent_injection":          fmt.Sprintf("%v", s.OS.UeventInjection),
		"unpriv_binfmt":             fmt.Sprintf("%v", s.OS.UnprivBinfmt),
		"unpriv_fscaps":             fmt.Sprintf("%v", s.OS.VFS3Fscaps),
		"seccomp_listener":          fmt.Sprintf("%v", s.OS.SeccompListener),
		"seccomp_listener_continue": fmt.Sprintf("%v", s.OS.SeccompListenerContinue),
		"idmapped_mounts":           fmt.Sprintf("%v", s.OS.IdmappedMounts),
	}

	drivers := instanceDrivers.DriverStatuses()
	for _, driver := range drivers {
		// Only report the supported drivers.
		if !driver.Supported {
			continue
		}

		if env.Driver != "" {
			env.Driver = env.Driver + " | " + driver.Info.Name
		} else {
			env.Driver = driver.Info.Name
		}

		// Get the version of the instance drivers in use.
		if env.DriverVersion != "" {
			env.DriverVersion = env.DriverVersion + " | " + driver.Info.Version
		} else {
			env.DriverVersion = driver.Info.Version
		}
	}

	if s.OS.LXCFeatures != nil {
		env.LXCFeatures = map[string]string{}
		for k, v := range s.OS.LXCFeatures {
			env.LXCFeatures[k] = fmt.Sprintf("%v", v)
		}
	}

	supportedStorageDrivers, usedStorageDrivers := readStoragePoolDriversCache()
	for driver, version := range usedStorageDrivers {
		if env.Storage != "" {
			env.Storage = env.Storage + " | " + driver
		} else {
			env.Storage = driver
		}

		// Get the version of the storage drivers in use.
		if env.StorageVersion != "" {
			env.StorageVersion = env.StorageVersion + " | " + version
		} else {
			env.StorageVersion = version
		}
	}

	env.StorageSupportedDrivers = supportedStorageDrivers

	fullSrv := api.Server{ServerUntrusted: srv}
	fullSrv.Environment = env
	requestor := request.CreateRequestor(r)
	fullSrv.AuthUserName = requestor.Username
	fullSrv.AuthUserMethod = requestor.Protocol

	err = s.Authorizer.CheckPermission(r.Context(), r, auth.ObjectServer(), auth.EntitlementCanEdit)
	if err == nil {
		fullSrv.Config, err = daemonConfigRender(s)
		if err != nil {
			return response.InternalError(err)
		}
	} else if !api.StatusErrorCheck(err, http.StatusForbidden) {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, fullSrv, fullSrv.Config)
}

// swagger:operation PUT /1.0 server server_put
//
//	Update the server configuration
//
//	Updates the entire server configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: server01
//	  - in: body
//	    name: server
//	    description: Server configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ServerPut"
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
func api10Put(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// If a target was specified, forward the request to the relevant node.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	// Don't apply changes to settings until daemon is fully started.
	<-d.waitReady.Done()

	req := api.ServerPut{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// If this is a notification from a cluster node, just run the triggers
	// for reacting to the values that changed.
	if isClusterNotification(r) {
		logger.Debug("Handling config changed notification")
		changed := make(map[string]string)
		for key, value := range req.Config {
			changed[key] = value
		}

		// Get the current (updated) config.
		var config *clusterConfig.Config
		err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error
			config, err = clusterConfig.Load(ctx, tx)
			return err
		})
		if err != nil {
			return response.SmartError(err)
		}

		// Update the daemon config.
		d.globalConfigMu.Lock()
		d.globalConfig = config
		d.globalConfigMu.Unlock()

		// Run any update triggers.
		err = doApi10UpdateTriggers(d, nil, changed, s.LocalConfig, config)
		if err != nil {
			return response.SmartError(err)
		}

		return response.EmptySyncResponse
	}

	render, err := daemonConfigRender(s)
	if err != nil {
		return response.SmartError(err)
	}

	err = localUtil.EtagCheck(r, render)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	return doApi10Update(d, r, req, false)
}

// swagger:operation PATCH /1.0 server server_patch
//
//	Partially update the server configuration
//
//	Updates a subset of the server configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: server01
//	  - in: body
//	    name: server
//	    description: Server configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ServerPut"
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
func api10Patch(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// If a target was specified, forward the request to the relevant node.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	// Don't apply changes to settings until daemon is fully started.
	<-d.waitReady.Done()

	render, err := daemonConfigRender(s)
	if err != nil {
		return response.InternalError(err)
	}

	err = localUtil.EtagCheck(r, render)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.ServerPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Config == nil {
		return response.EmptySyncResponse
	}

	return doApi10Update(d, r, req, true)
}

func doApi10Update(d *Daemon, r *http.Request, req api.ServerPut, patch bool) response.Response {
	s := d.State()

	// First deal with config specific to the local daemon
	nodeValues := map[string]string{}

	for key := range node.ConfigSchema {
		value, ok := req.Config[key]
		if ok {
			nodeValues[key] = value
			delete(req.Config, key)
		}
	}

	nodeChanged := map[string]string{}
	var newNodeConfig *node.Config
	oldNodeConfig := make(map[string]string)

	err := s.DB.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
		var err error
		newNodeConfig, err = node.ConfigLoad(ctx, tx)
		if err != nil {
			return fmt.Errorf("Failed to load node config: %w", err)
		}

		// Keep old config around in case something goes wrong. In that case the config will be reverted.
		for k, v := range newNodeConfig.Dump() {
			oldNodeConfig[k] = v
		}

		// We currently don't allow changing the cluster.https_address once it's set.
		if s.ServerClustered {
			curConfig, err := tx.Config(ctx)
			if err != nil {
				return fmt.Errorf("Cannot fetch node config from database: %w", err)
			}

			newClusterHTTPSAddress, found := nodeValues["cluster.https_address"]
			if !found && patch {
				newClusterHTTPSAddress = curConfig["cluster.https_address"]
			} else if !found {
				newClusterHTTPSAddress = ""
			}

			if curConfig["cluster.https_address"] != newClusterHTTPSAddress {
				return fmt.Errorf("Changing cluster.https_address is currently not supported")
			}
		}

		// Validate the storage volumes
		if nodeValues["storage.backups_volume"] != "" && nodeValues["storage.backups_volume"] != newNodeConfig.StorageBackupsVolume() {
			err := daemonStorageValidate(s, nodeValues["storage.backups_volume"])
			if err != nil {
				return fmt.Errorf("Failed validation of %q: %w", "storage.backups_volume", err)
			}
		}

		if nodeValues["storage.images_volume"] != "" && nodeValues["storage.images_volume"] != newNodeConfig.StorageImagesVolume() {
			err := daemonStorageValidate(s, nodeValues["storage.images_volume"])
			if err != nil {
				return fmt.Errorf("Failed validation of %q: %w", "storage.images_volume", err)
			}
		}

		if patch {
			nodeChanged, err = newNodeConfig.Patch(nodeValues)
		} else {
			nodeChanged, err = newNodeConfig.Replace(nodeValues)
		}

		return err
	})
	if err != nil {
		switch err.(type) {
		case config.ErrorList:
			return response.BadRequest(err)
		default:
			return response.SmartError(err)
		}
	}

	revert := revert.New()
	defer revert.Fail()

	revert.Add(func() {
		for key := range nodeValues {
			val, ok := oldNodeConfig[key]
			if !ok {
				nodeValues[key] = ""
			} else {
				nodeValues[key] = val
			}
		}

		err = s.DB.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
			newNodeConfig, err := node.ConfigLoad(ctx, tx)
			if err != nil {
				return fmt.Errorf("Failed to load node config: %w", err)
			}

			_, err = newNodeConfig.Replace(nodeValues)
			if err != nil {
				return fmt.Errorf("Failed updating node config: %w", err)
			}

			return nil
		})

		if err != nil {
			logger.Warn("Failed reverting node config", logger.Ctx{"err": err})
		}
	})

	// Then deal with cluster wide configuration
	var clusterChanged map[string]string
	var newClusterConfig *clusterConfig.Config
	oldClusterConfig := make(map[string]string)

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		newClusterConfig, err = clusterConfig.Load(ctx, tx)
		if err != nil {
			return fmt.Errorf("Failed to load cluster config: %w", err)
		}

		// Keep old config around in case something goes wrong. In that case the config will be reverted.
		for k, v := range newClusterConfig.Dump() {
			oldClusterConfig[k] = v
		}

		if patch {
			clusterChanged, err = newClusterConfig.Patch(req.Config)
		} else {
			clusterChanged, err = newClusterConfig.Replace(req.Config)
		}

		return err
	})
	if err != nil {
		switch err.(type) {
		case config.ErrorList:
			return response.BadRequest(err)
		default:
			return response.SmartError(err)
		}
	}

	revert.Add(func() {
		for key := range req.Config {
			val, ok := oldClusterConfig[key]
			if !ok {
				req.Config[key] = ""
			} else {
				req.Config[key] = val
			}
		}

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			newClusterConfig, err = clusterConfig.Load(ctx, tx)
			if err != nil {
				return fmt.Errorf("Failed to load cluster config: %w", err)
			}

			_, err = newClusterConfig.Replace(req.Config)
			if err != nil {
				return fmt.Errorf("Failed updating cluster config: %w", err)
			}

			return nil
		})

		if err != nil {
			logger.Warn("Failed reverting cluster config", logger.Ctx{"err": err})
		}
	})

	// Notify the other nodes about changes
	notifier, err := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAlive)
	if err != nil {
		return response.SmartError(err)
	}

	err = notifier(func(client incus.InstanceServer) error {
		server, etag, err := client.GetServer()
		if err != nil {
			return err
		}

		serverPut := server.Writable()
		serverPut.Config = make(map[string]string)
		// Only propagated cluster-wide changes
		for key, value := range clusterChanged {
			serverPut.Config[key] = value
		}

		return client.UpdateServer(serverPut, etag)
	})
	if err != nil {
		logger.Error("Failed to notify other members about config change", logger.Ctx{"err": err})
		return response.SmartError(err)
	}

	// Update the daemon config.
	d.globalConfigMu.Lock()
	d.globalConfig = newClusterConfig
	d.localConfig = newNodeConfig
	d.globalConfigMu.Unlock()

	// Run any update triggers.
	err = doApi10UpdateTriggers(d, nodeChanged, clusterChanged, newNodeConfig, newClusterConfig)
	if err != nil {
		return response.SmartError(err)
	}

	revert.Success()

	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ConfigUpdated.Event(request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

func doApi10UpdateTriggers(d *Daemon, nodeChanged, clusterChanged map[string]string, nodeConfig *node.Config, clusterConfig *clusterConfig.Config) error {
	s := d.State()

	acmeChanged := false
	bgpChanged := false
	dnsChanged := false
	lokiChanged := false
	oidcChanged := false
	openFGAChanged := false
	ovnChanged := false
	syslogChanged := false

	for key := range clusterChanged {
		switch key {
		case "acme.ca_url", "acme.domain":
			acmeChanged = true

		case "cluster.images_minimal_replica":
			err := autoSyncImages(s.ShutdownCtx, s)
			if err != nil {
				logger.Warn("Could not auto-sync images", logger.Ctx{"err": err})
			}

		case "cluster.offline_threshold":
			d.gateway.HeartbeatOfflineThreshold = clusterConfig.OfflineThreshold()
			d.taskClusterHeartbeat.Reset()

		case "core.bgp_asn":
			bgpChanged = true

		case "core.https_trusted_proxy":
			s.Endpoints.NetworkUpdateTrustedProxy(clusterChanged[key])

		case "core.proxy_http", "core.proxy_https", "core.proxy_ignore_hosts":
			daemonConfigSetProxy(d, clusterConfig)

		case "images.auto_update_interval", "images.remote_cache_expiry":
			if !s.OS.MockMode {
				d.taskPruneImages.Reset()
			}

		case "loki.api.url", "loki.auth.username", "loki.auth.password", "loki.api.ca_cert", "loki.instance", "loki.labels", "loki.loglevel", "loki.types":
			lokiChanged = true

		case "network.ovn.northbound_connection", "network.ovn.ca_cert", "network.ovn.client_cert", "network.ovn.client_key":
			ovnChanged = true

		case "oidc.issuer", "oidc.client.id", "oidc.audience", "oidc.claim":
			oidcChanged = true

		case "openfga.api.url", "openfga.api.token", "openfga.store.id":
			openFGAChanged = true
		}
	}

	for key := range nodeChanged {
		switch key {
		case "core.bgp_address", "core.bgp_routerid":
			bgpChanged = true

		case "core.dns_address":
			dnsChanged = true

		case "core.syslog_socket":
			syslogChanged = true
		}
	}

	// Process some additional keys. We do it sequentially because some keys are
	// correlated with others, and need to be processed first (for example
	// core.https_address need to be processed before
	// cluster.https_address).
	value, ok := nodeChanged["core.https_address"]
	if ok {
		err := s.Endpoints.NetworkUpdateAddress(value)
		if err != nil {
			return err
		}

		s.Endpoints.NetworkUpdateTrustedProxy(clusterConfig.HTTPSTrustedProxy())
	}

	value, ok = nodeChanged["cluster.https_address"]
	if ok {
		err := s.Endpoints.ClusterUpdateAddress(value)
		if err != nil {
			return err
		}

		s.Endpoints.NetworkUpdateTrustedProxy(clusterConfig.HTTPSTrustedProxy())
	}

	value, ok = nodeChanged["core.debug_address"]
	if ok {
		err := s.Endpoints.PprofUpdateAddress(value)
		if err != nil {
			return err
		}
	}

	value, ok = nodeChanged["core.metrics_address"]
	if ok {
		err := s.Endpoints.MetricsUpdateAddress(value, s.Endpoints.NetworkCert())
		if err != nil {
			return err
		}
	}

	value, ok = nodeChanged["core.storage_buckets_address"]
	if ok {
		err := s.Endpoints.StorageBucketsUpdateAddress(value, s.Endpoints.NetworkCert())
		if err != nil {
			return err
		}
	}

	value, ok = nodeChanged["storage.backups_volume"]
	if ok {
		err := daemonStorageMove(s, "backups", value)
		if err != nil {
			return err
		}
	}

	value, ok = nodeChanged["storage.images_volume"]
	if ok {
		err := daemonStorageMove(s, "images", value)
		if err != nil {
			return err
		}
	}

	// Apply larger changes.
	if acmeChanged {
		err := autoRenewCertificate(s.ShutdownCtx, d, true)
		if err != nil {
			return err
		}
	}

	if bgpChanged {
		address := nodeConfig.BGPAddress()
		asn := clusterConfig.BGPASN()
		routerid := nodeConfig.BGPRouterID()

		err := s.BGP.Reconfigure(address, uint32(asn), net.ParseIP(routerid))
		if err != nil {
			return fmt.Errorf("Failed reconfiguring BGP: %w", err)
		}
	}

	if dnsChanged {
		address := nodeConfig.DNSAddress()

		err := s.DNS.Reconfigure(address)
		if err != nil {
			return fmt.Errorf("Failed reconfiguring DNS: %w", err)
		}
	}

	if lokiChanged {
		lokiURL, lokiUsername, lokiPassword, lokiCACert, lokiInstance, lokiLoglevel, lokiLabels, lokiTypes := clusterConfig.LokiServer()

		if lokiURL == "" || lokiLoglevel == "" || len(lokiTypes) == 0 {
			d.internalListener.RemoveHandler("loki")
		} else {
			err := d.setupLoki(lokiURL, lokiUsername, lokiPassword, lokiCACert, lokiInstance, lokiLoglevel, lokiLabels, lokiTypes)
			if err != nil {
				return err
			}
		}
	}

	if oidcChanged {
		oidcIssuer, oidcClientID, oidcAudience, oidcClaim := clusterConfig.OIDCServer()

		if oidcIssuer == "" || oidcClientID == "" {
			d.oidcVerifier = nil
		} else {
			var err error
			d.oidcVerifier, err = oidc.NewVerifier(oidcIssuer, oidcClientID, oidcAudience, oidcClaim)
			if err != nil {
				return fmt.Errorf("Failed creating verifier: %w", err)
			}
		}
	}

	if openFGAChanged {
		openfgaAPIURL, openfgaAPIToken, openfgaStoreID := d.globalConfig.OpenFGA()
		err := d.setupOpenFGA(openfgaAPIURL, openfgaAPIToken, openfgaStoreID)
		if err != nil {
			return err
		}
	}

	if ovnChanged {
		err := d.setupOVN()
		if err != nil {
			return err
		}
	}

	if syslogChanged {
		err := d.setupSyslogSocket(nodeConfig.SyslogSocket())
		if err != nil {
			return err
		}
	}

	// Compile and load the instance placement scriptlet.
	value, ok = clusterChanged["instances.placement.scriptlet"]
	if ok {
		err := scriptletLoad.InstancePlacementSet(value)
		if err != nil {
			return fmt.Errorf("Failed saving instance placement scriptlet: %w", err)
		}
	}

	return nil
}
