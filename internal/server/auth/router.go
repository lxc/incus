package auth

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"sync/atomic"

	"github.com/lxc/incus/v7/internal/server/certificate"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
)

// clientClass identifies the authentication class of a request.
type clientClass string

const (
	// clientClassDefault is the fallback used when no more specific class matches.
	clientClassDefault clientClass = "default"

	// clientClassUnix covers local unix-socket.
	clientClassUnix clientClass = "unix"

	// clientClassTLS covers unrestricted client certificates.
	clientClassTLS clientClass = "tls"

	// clientClassTLSRestricted covers restricted (project-scoped) client certificates.
	clientClassTLSRestricted clientClass = "tls-restricted"

	// clientClassOIDC covers OIDC-authenticated clients.
	clientClassOIDC clientClass = "oidc"
)

// allClientClasses lists every routable client class.
var allClientClasses = []clientClass{
	clientClassUnix,
	clientClassTLS,
	clientClassTLSRestricted,
	clientClassOIDC,
	clientClassDefault,
}

// defaultRoutes is the built-in routing used for any client class with no
// explicit configuration and no explicit default.
func defaultRoutes() map[clientClass]string {
	return map[clientClass]string{
		clientClassDefault:       DriverDeny,
		clientClassUnix:          DriverAllow,
		clientClassTLS:           DriverAllow,
		clientClassTLSRestricted: DriverTLS,
		clientClassOIDC:          DriverAllow,
	}
}

// routerState is an immutable snapshot of the router's configuration.
type routerState struct {
	// drivers holds the loaded authorizers.
	drivers map[string]Authorizer

	// routes maps each client class to the authorizer.
	routes map[clientClass]Authorizer
}

// Router is an Authorizer that dispatches each request to other authorizers.
type Router struct {
	commonAuthorizer

	certificates *certificate.Cache
	state        atomic.Pointer[routerState]
}

// baseDrivers are the always present drivers.
var baseDrivers = []string{DriverAllow, DriverDeny, DriverTLS}

// NewRouter returns a Router with the base drivers loaded.
func NewRouter(ctx context.Context, l logger.Logger, certificateCache *certificate.Cache) (*Router, error) {
	rt := &Router{certificates: certificateCache}

	err := rt.init("router", l)
	if err != nil {
		return nil, err
	}

	// Load the base drivers into the driver set. They are reused across
	// reconfigurations and never stopped.
	drivers := make(map[string]Authorizer, len(baseDrivers))
	for _, name := range baseDrivers {
		driver, err := LoadAuthorizer(ctx, name, l, certificateCache)
		if err != nil {
			return nil, err
		}

		drivers[name] = driver
	}

	// Install the built-in default routing over the base drivers.
	err = rt.store(nil, drivers)
	if err != nil {
		return nil, err
	}

	return rt, nil
}

// Configure installs the routing table (from the explicit authorization.client.* configuration).
func (rt *Router) Configure(routes map[string]string, optional map[string]Authorizer) error {
	previous := rt.state.Load().drivers

	// Keep the base drivers loaded at construction and swap in the new optional
	// set (openfga/scriptlet).
	drivers := make(map[string]Authorizer, len(baseDrivers)+len(optional))
	for _, name := range baseDrivers {
		drivers[name] = previous[name]
	}

	maps.Copy(drivers, optional)

	return rt.store(routes, drivers)
}

// LoadedDriver returns the loaded driver registered under name.
func (rt *Router) LoadedDriver(name string) Authorizer {
	return rt.state.Load().drivers[name]
}

// store resolves the routing table from the explicit authorization.client.* configuration.
func (rt *Router) store(routes map[string]string, drivers map[string]Authorizer) error {
	base := defaultRoutes()
	defaultTarget := routes[string(clientClassDefault)]

	resolved := make(map[clientClass]Authorizer, len(allClientClasses))
	for _, class := range allClientClasses {
		var target string
		switch {
		case routes[string(class)] != "":
			target = routes[string(class)]
		case defaultTarget != "":
			target = defaultTarget
		default:
			target = base[class]
		}

		driver, ok := drivers[target]
		if !ok {
			driver = drivers[DriverDeny]
			rt.logger.Error("No loaded authorizer for route target", logger.Ctx{"target": target})
		}

		resolved[class] = driver
	}

	rt.state.Store(&routerState{drivers: drivers, routes: resolved})

	return nil
}

// classify determines the client class of a request from its details.
func (rt *Router) classify(details *requestDetails) clientClass {
	if details.isInternalOrUnix() {
		return clientClassUnix
	}

	switch details.authenticationProtocol() {
	case api.AuthenticationMethodTLS:
		if rt.isRestrictedTLS(details.username()) {
			return clientClassTLSRestricted
		}

		return clientClassTLS
	case api.AuthenticationMethodOIDC:
		return clientClassOIDC
	}

	return clientClassDefault
}

// isRestrictedTLS reports whether the given certificate fingerprint corresponds
// to a restricted (project-scoped) certificate.
func (rt *Router) isRestrictedTLS(fingerprint string) bool {
	if rt.certificates == nil {
		return false
	}

	_, projects := rt.certificates.GetCertificatesAndProjects()
	projectNames, ok := projects[fingerprint]

	return ok && projectNames != nil
}

// isRootUnixRequest reports whether the request was made by the root user over the local unix socket.
func isRootUnixRequest(r *http.Request, details *requestDetails) bool {
	if details.Protocol != "unix" {
		return false
	}

	val := r.Context().Value(request.CtxUnixIsRoot)
	if val == nil {
		return false
	}

	isRoot, ok := val.(bool)
	if !ok {
		return false
	}

	return isRoot
}

// authorizerForRequest returns the single authorizer responsible for the given request.
func (rt *Router) authorizerForRequest(r *http.Request) Authorizer {
	st := rt.state.Load()

	if r == nil {
		return st.drivers[DriverDeny]
	}

	details, err := rt.requestDetails(r)
	if err != nil {
		return st.drivers[DriverDeny]
	}

	// The root user is always granted full access over the local unix socket.
	if isRootUnixRequest(r, details) {
		return st.drivers[DriverAllow]
	}

	class := rt.classify(details)
	a, ok := st.routes[class]
	if !ok {
		a, ok = st.routes[clientClassDefault]
		if !ok {
			return st.drivers[DriverDeny]
		}
	}

	return a
}

// fanout runs fn against every loaded driver, joining any errors.
func (rt *Router) fanout(fn func(Authorizer) error) error {
	st := rt.state.Load()

	var errs []error
	for _, drv := range st.drivers {
		err := fn(drv)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// Request-scoped methods: route to a single authorizer.

// CheckPermission returns an error if the user does not have the given Entitlement on the given Object.
func (rt *Router) CheckPermission(ctx context.Context, r *http.Request, object Object, entitlement Entitlement) error {
	return rt.authorizerForRequest(r).CheckPermission(ctx, r, object, entitlement)
}

// GetPermissionChecker returns a function that checks whether a user has the required entitlement on an object.
func (rt *Router) GetPermissionChecker(ctx context.Context, r *http.Request, entitlement Entitlement, objectType ObjectType) (PermissionChecker, error) {
	return rt.authorizerForRequest(r).GetPermissionChecker(ctx, r, entitlement, objectType)
}

// Access queries: union across every loaded driver.

// GetInstanceAccess returns the union of entities who have access to the instance across all loaded drivers.
func (rt *Router) GetInstanceAccess(ctx context.Context, projectName string, instanceName string) (*api.Access, error) {
	st := rt.state.Load()

	var merged api.Access
	var errs []error
	for _, drv := range st.drivers {
		access, err := drv.GetInstanceAccess(ctx, projectName, instanceName)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if access != nil {
			merged = append(merged, *access...)
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return &merged, nil
}

// GetProjectAccess returns the union of entities who have access to the project across all loaded drivers.
func (rt *Router) GetProjectAccess(ctx context.Context, projectName string) (*api.Access, error) {
	st := rt.state.Load()

	var merged api.Access
	var errs []error
	for _, drv := range st.drivers {
		access, err := drv.GetProjectAccess(ctx, projectName)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if access != nil {
			merged = append(merged, *access...)
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return &merged, nil
}

// Lifecycle methods: fan out to every loaded driver.

// StopService stops every loaded driver.
func (rt *Router) StopService(ctx context.Context) error {
	return rt.fanout(func(a Authorizer) error { return a.StopService(ctx) })
}

// ApplyPatch applies the named patch to every loaded driver.
func (rt *Router) ApplyPatch(ctx context.Context, name string) error {
	return rt.fanout(func(a Authorizer) error { return a.ApplyPatch(ctx, name) })
}

// Resource-notification methods: fan out to every loaded driver.

// AddProject notifies every loaded driver of a new project.
func (rt *Router) AddProject(ctx context.Context, projectID int64, projectName string) error {
	return rt.fanout(func(a Authorizer) error { return a.AddProject(ctx, projectID, projectName) })
}

// DeleteProject notifies every loaded driver of a deleted project.
func (rt *Router) DeleteProject(ctx context.Context, projectID int64, projectName string) error {
	return rt.fanout(func(a Authorizer) error { return a.DeleteProject(ctx, projectID, projectName) })
}

// RenameProject notifies every loaded driver of a renamed project.
func (rt *Router) RenameProject(ctx context.Context, projectID int64, oldName string, newName string) error {
	return rt.fanout(func(a Authorizer) error { return a.RenameProject(ctx, projectID, oldName, newName) })
}

// AddCertificate notifies every loaded driver of a new certificate.
func (rt *Router) AddCertificate(ctx context.Context, fingerprint string) error {
	return rt.fanout(func(a Authorizer) error { return a.AddCertificate(ctx, fingerprint) })
}

// DeleteCertificate notifies every loaded driver of a deleted certificate.
func (rt *Router) DeleteCertificate(ctx context.Context, fingerprint string) error {
	return rt.fanout(func(a Authorizer) error { return a.DeleteCertificate(ctx, fingerprint) })
}

// AddStoragePool notifies every loaded driver of a new storage pool.
func (rt *Router) AddStoragePool(ctx context.Context, storagePoolName string) error {
	return rt.fanout(func(a Authorizer) error { return a.AddStoragePool(ctx, storagePoolName) })
}

// DeleteStoragePool notifies every loaded driver of a deleted storage pool.
func (rt *Router) DeleteStoragePool(ctx context.Context, storagePoolName string) error {
	return rt.fanout(func(a Authorizer) error { return a.DeleteStoragePool(ctx, storagePoolName) })
}

// AddImage notifies every loaded driver of a new image.
func (rt *Router) AddImage(ctx context.Context, projectName string, fingerprint string) error {
	return rt.fanout(func(a Authorizer) error { return a.AddImage(ctx, projectName, fingerprint) })
}

// DeleteImage notifies every loaded driver of a deleted image.
func (rt *Router) DeleteImage(ctx context.Context, projectName string, fingerprint string) error {
	return rt.fanout(func(a Authorizer) error { return a.DeleteImage(ctx, projectName, fingerprint) })
}

// AddImageAlias notifies every loaded driver of a new image alias.
func (rt *Router) AddImageAlias(ctx context.Context, projectName string, imageAliasName string) error {
	return rt.fanout(func(a Authorizer) error { return a.AddImageAlias(ctx, projectName, imageAliasName) })
}

// DeleteImageAlias notifies every loaded driver of a deleted image alias.
func (rt *Router) DeleteImageAlias(ctx context.Context, projectName string, imageAliasName string) error {
	return rt.fanout(func(a Authorizer) error { return a.DeleteImageAlias(ctx, projectName, imageAliasName) })
}

// RenameImageAlias notifies every loaded driver of a renamed image alias.
func (rt *Router) RenameImageAlias(ctx context.Context, projectName string, oldAliasName string, newAliasName string) error {
	return rt.fanout(func(a Authorizer) error { return a.RenameImageAlias(ctx, projectName, oldAliasName, newAliasName) })
}

// AddInstance notifies every loaded driver of a new instance.
func (rt *Router) AddInstance(ctx context.Context, projectName string, instanceName string) error {
	return rt.fanout(func(a Authorizer) error { return a.AddInstance(ctx, projectName, instanceName) })
}

// DeleteInstance notifies every loaded driver of a deleted instance.
func (rt *Router) DeleteInstance(ctx context.Context, projectName string, instanceName string) error {
	return rt.fanout(func(a Authorizer) error { return a.DeleteInstance(ctx, projectName, instanceName) })
}

// RenameInstance notifies every loaded driver of a renamed instance.
func (rt *Router) RenameInstance(ctx context.Context, projectName string, oldInstanceName string, newInstanceName string) error {
	return rt.fanout(func(a Authorizer) error { return a.RenameInstance(ctx, projectName, oldInstanceName, newInstanceName) })
}

// AddNetwork notifies every loaded driver of a new network.
func (rt *Router) AddNetwork(ctx context.Context, projectName string, networkName string) error {
	return rt.fanout(func(a Authorizer) error { return a.AddNetwork(ctx, projectName, networkName) })
}

// DeleteNetwork notifies every loaded driver of a deleted network.
func (rt *Router) DeleteNetwork(ctx context.Context, projectName string, networkName string) error {
	return rt.fanout(func(a Authorizer) error { return a.DeleteNetwork(ctx, projectName, networkName) })
}

// RenameNetwork notifies every loaded driver of a renamed network.
func (rt *Router) RenameNetwork(ctx context.Context, projectName string, oldNetworkName string, newNetworkName string) error {
	return rt.fanout(func(a Authorizer) error { return a.RenameNetwork(ctx, projectName, oldNetworkName, newNetworkName) })
}

// AddNetworkZone notifies every loaded driver of a new network zone.
func (rt *Router) AddNetworkZone(ctx context.Context, projectName string, networkZoneName string) error {
	return rt.fanout(func(a Authorizer) error { return a.AddNetworkZone(ctx, projectName, networkZoneName) })
}

// DeleteNetworkZone notifies every loaded driver of a deleted network zone.
func (rt *Router) DeleteNetworkZone(ctx context.Context, projectName string, networkZoneName string) error {
	return rt.fanout(func(a Authorizer) error { return a.DeleteNetworkZone(ctx, projectName, networkZoneName) })
}

// AddNetworkIntegration notifies every loaded driver of a new network integration.
func (rt *Router) AddNetworkIntegration(ctx context.Context, networkIntegrationName string) error {
	return rt.fanout(func(a Authorizer) error { return a.AddNetworkIntegration(ctx, networkIntegrationName) })
}

// DeleteNetworkIntegration notifies every loaded driver of a deleted network integration.
func (rt *Router) DeleteNetworkIntegration(ctx context.Context, networkIntegrationName string) error {
	return rt.fanout(func(a Authorizer) error { return a.DeleteNetworkIntegration(ctx, networkIntegrationName) })
}

// RenameNetworkIntegration notifies every loaded driver of a renamed network integration.
func (rt *Router) RenameNetworkIntegration(ctx context.Context, oldNetworkIntegrationName string, newNetworkIntegrationName string) error {
	return rt.fanout(func(a Authorizer) error {
		return a.RenameNetworkIntegration(ctx, oldNetworkIntegrationName, newNetworkIntegrationName)
	})
}

// AddNetworkACL notifies every loaded driver of a new network ACL.
func (rt *Router) AddNetworkACL(ctx context.Context, projectName string, networkACLName string) error {
	return rt.fanout(func(a Authorizer) error { return a.AddNetworkACL(ctx, projectName, networkACLName) })
}

// DeleteNetworkACL notifies every loaded driver of a deleted network ACL.
func (rt *Router) DeleteNetworkACL(ctx context.Context, projectName string, networkACLName string) error {
	return rt.fanout(func(a Authorizer) error { return a.DeleteNetworkACL(ctx, projectName, networkACLName) })
}

// RenameNetworkACL notifies every loaded driver of a renamed network ACL.
func (rt *Router) RenameNetworkACL(ctx context.Context, projectName string, oldNetworkACLName string, newNetworkACLName string) error {
	return rt.fanout(func(a Authorizer) error {
		return a.RenameNetworkACL(ctx, projectName, oldNetworkACLName, newNetworkACLName)
	})
}

// AddNetworkAddressSet notifies every loaded driver of a new network address set.
func (rt *Router) AddNetworkAddressSet(ctx context.Context, projectName string, networkAddressSetName string) error {
	return rt.fanout(func(a Authorizer) error { return a.AddNetworkAddressSet(ctx, projectName, networkAddressSetName) })
}

// DeleteNetworkAddressSet notifies every loaded driver of a deleted network address set.
func (rt *Router) DeleteNetworkAddressSet(ctx context.Context, projectName string, networkAddressSetName string) error {
	return rt.fanout(func(a Authorizer) error { return a.DeleteNetworkAddressSet(ctx, projectName, networkAddressSetName) })
}

// RenameNetworkAddressSet notifies every loaded driver of a renamed network address set.
func (rt *Router) RenameNetworkAddressSet(ctx context.Context, projectName string, oldNetworkAddressSetName string, newNetworkAddressSetName string) error {
	return rt.fanout(func(a Authorizer) error {
		return a.RenameNetworkAddressSet(ctx, projectName, oldNetworkAddressSetName, newNetworkAddressSetName)
	})
}

// AddProfile notifies every loaded driver of a new profile.
func (rt *Router) AddProfile(ctx context.Context, projectName string, profileName string) error {
	return rt.fanout(func(a Authorizer) error { return a.AddProfile(ctx, projectName, profileName) })
}

// DeleteProfile notifies every loaded driver of a deleted profile.
func (rt *Router) DeleteProfile(ctx context.Context, projectName string, profileName string) error {
	return rt.fanout(func(a Authorizer) error { return a.DeleteProfile(ctx, projectName, profileName) })
}

// RenameProfile notifies every loaded driver of a renamed profile.
func (rt *Router) RenameProfile(ctx context.Context, projectName string, oldProfileName string, newProfileName string) error {
	return rt.fanout(func(a Authorizer) error { return a.RenameProfile(ctx, projectName, oldProfileName, newProfileName) })
}

// AddStoragePoolVolume notifies every loaded driver of a new storage pool volume.
func (rt *Router) AddStoragePoolVolume(ctx context.Context, projectName string, storagePoolName string, storageVolumeType string, storageVolumeName string, storageVolumeLocation string) error {
	return rt.fanout(func(a Authorizer) error {
		return a.AddStoragePoolVolume(ctx, projectName, storagePoolName, storageVolumeType, storageVolumeName, storageVolumeLocation)
	})
}

// DeleteStoragePoolVolume notifies every loaded driver of a deleted storage pool volume.
func (rt *Router) DeleteStoragePoolVolume(ctx context.Context, projectName string, storagePoolName string, storageVolumeType string, storageVolumeName string, storageVolumeLocation string) error {
	return rt.fanout(func(a Authorizer) error {
		return a.DeleteStoragePoolVolume(ctx, projectName, storagePoolName, storageVolumeType, storageVolumeName, storageVolumeLocation)
	})
}

// RenameStoragePoolVolume notifies every loaded driver of a renamed storage pool volume.
func (rt *Router) RenameStoragePoolVolume(ctx context.Context, projectName string, storagePoolName string, storageVolumeType string, oldStorageVolumeName string, newStorageVolumeName string, storageVolumeLocation string) error {
	return rt.fanout(func(a Authorizer) error {
		return a.RenameStoragePoolVolume(ctx, projectName, storagePoolName, storageVolumeType, oldStorageVolumeName, newStorageVolumeName, storageVolumeLocation)
	})
}

// AddStorageBucket notifies every loaded driver of a new storage bucket.
func (rt *Router) AddStorageBucket(ctx context.Context, projectName string, storagePoolName string, storageBucketName string, storageBucketLocation string) error {
	return rt.fanout(func(a Authorizer) error {
		return a.AddStorageBucket(ctx, projectName, storagePoolName, storageBucketName, storageBucketLocation)
	})
}

// DeleteStorageBucket notifies every loaded driver of a deleted storage bucket.
func (rt *Router) DeleteStorageBucket(ctx context.Context, projectName string, storagePoolName string, storageBucketName string, storageBucketLocation string) error {
	return rt.fanout(func(a Authorizer) error {
		return a.DeleteStorageBucket(ctx, projectName, storagePoolName, storageBucketName, storageBucketLocation)
	})
}
