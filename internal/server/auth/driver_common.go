package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/lxc/incus/v6/internal/server/auth/common"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
)

type commonAuthorizer struct {
	driverName string
	logger     logger.Logger
}

func (c *commonAuthorizer) init(driverName string, l logger.Logger) error {
	if l == nil {
		return errors.New("Cannot initialize authorizer: nil logger provided")
	}

	l = l.AddContext(logger.Ctx{"driver": driverName})

	c.driverName = driverName
	c.logger = l
	return nil
}

type requestDetails struct {
	common.RequestDetails

	forwardedUsername string
	forwardedProtocol string
}

func (r *requestDetails) isInternalOrUnix() bool {
	if r.Protocol == "unix" {
		return true
	}

	if r.Protocol == "cluster" && (r.forwardedProtocol == "unix" || r.forwardedProtocol == "cluster" || r.forwardedProtocol == "") {
		return true
	}

	return false
}

func (r *requestDetails) username() string {
	if r.Protocol == "cluster" && r.forwardedUsername != "" {
		return r.forwardedUsername
	}

	return r.Username
}

func (r *requestDetails) authenticationProtocol() string {
	if r.Protocol == "cluster" {
		return r.forwardedProtocol
	}

	return r.Protocol
}

func (r *requestDetails) actualDetails() *common.RequestDetails {
	return &common.RequestDetails{
		Username:             r.username(),
		Protocol:             r.authenticationProtocol(),
		IsAllProjectsRequest: r.IsAllProjectsRequest,
		ProjectName:          r.ProjectName,
	}
}

func (c *commonAuthorizer) requestDetails(r *http.Request) (*requestDetails, error) {
	if r == nil {
		return nil, errors.New("Cannot inspect nil request")
	} else if r.URL == nil {
		return nil, errors.New("Request URL is not set")
	}

	val := r.Context().Value(request.CtxUsername)
	if val == nil {
		return nil, errors.New("Username not present in request context")
	}

	username, ok := val.(string)
	if !ok {
		return nil, errors.New("Request context username has incorrect type")
	}

	val = r.Context().Value(request.CtxProtocol)
	if val == nil {
		return nil, errors.New("Protocol not present in request context")
	}

	protocol, ok := val.(string)
	if !ok {
		return nil, errors.New("Request context protocol has incorrect type")
	}

	var forwardedUsername string
	val = r.Context().Value(request.CtxForwardedUsername)
	if val != nil {
		forwardedUsername, ok = val.(string)
		if !ok {
			return nil, errors.New("Request context forwarded username has incorrect type")
		}
	}

	var forwardedProtocol string
	val = r.Context().Value(request.CtxForwardedProtocol)
	if val != nil {
		forwardedProtocol, ok = val.(string)
		if !ok {
			return nil, errors.New("Request context forwarded username has incorrect type")
		}
	}

	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse request query parameters: %w", err)
	}

	return &requestDetails{
		RequestDetails: common.RequestDetails{
			Username:             username,
			Protocol:             protocol,
			IsAllProjectsRequest: util.IsTrue(values.Get("all-projects")),
			ProjectName:          request.ProjectParam(r),
		},

		forwardedUsername: forwardedUsername,
		forwardedProtocol: forwardedProtocol,
	}, nil
}

func (c *commonAuthorizer) Driver() string {
	return c.driverName
}

// StopService is a no-op.
func (c *commonAuthorizer) StopService(ctx context.Context) error {
	return nil
}

// ApplyPatch is a no-op.
func (c *commonAuthorizer) ApplyPatch(ctx context.Context, name string) error {
	return nil
}

// AddProject is a no-op.
func (c *commonAuthorizer) AddProject(ctx context.Context, projectID int64, name string) error {
	return nil
}

// DeleteProject is a no-op.
func (c *commonAuthorizer) DeleteProject(ctx context.Context, projectID int64, name string) error {
	return nil
}

// RenameProject is a no-op.
func (c *commonAuthorizer) RenameProject(ctx context.Context, projectID int64, oldName string, newName string) error {
	return nil
}

// AddCertificate is a no-op.
func (c *commonAuthorizer) AddCertificate(ctx context.Context, fingerprint string) error {
	return nil
}

// DeleteCertificate is a no-op.
func (c *commonAuthorizer) DeleteCertificate(ctx context.Context, fingerprint string) error {
	return nil
}

// AddStoragePool is a no-op.
func (c *commonAuthorizer) AddStoragePool(ctx context.Context, storagePoolName string) error {
	return nil
}

// DeleteStoragePool is a no-op.
func (c *commonAuthorizer) DeleteStoragePool(ctx context.Context, storagePoolName string) error {
	return nil
}

// AddImage is a no-op.
func (c *commonAuthorizer) AddImage(ctx context.Context, projectName string, fingerprint string) error {
	return nil
}

// DeleteImage is a no-op.
func (c *commonAuthorizer) DeleteImage(ctx context.Context, projectName string, fingerprint string) error {
	return nil
}

// AddImageAlias is a no-op.
func (c *commonAuthorizer) AddImageAlias(ctx context.Context, projectName string, imageAliasName string) error {
	return nil
}

// DeleteImageAlias is a no-op.
func (c *commonAuthorizer) DeleteImageAlias(ctx context.Context, projectName string, imageAliasName string) error {
	return nil
}

// RenameImageAlias is a no-op.
func (c *commonAuthorizer) RenameImageAlias(ctx context.Context, projectName string, oldAliasName string, newAliasName string) error {
	return nil
}

// AddInstance is a no-op.
func (c *commonAuthorizer) AddInstance(ctx context.Context, projectName string, instanceName string) error {
	return nil
}

// DeleteInstance is a no-op.
func (c *commonAuthorizer) DeleteInstance(ctx context.Context, projectName string, instanceName string) error {
	return nil
}

// RenameInstance is a no-op.
func (c *commonAuthorizer) RenameInstance(ctx context.Context, projectName string, oldInstanceName string, newInstanceName string) error {
	return nil
}

// AddNetwork is a no-op.
func (c *commonAuthorizer) AddNetwork(ctx context.Context, projectName string, networkName string) error {
	return nil
}

// DeleteNetwork is a no-op.
func (c *commonAuthorizer) DeleteNetwork(ctx context.Context, projectName string, networkName string) error {
	return nil
}

// RenameNetwork is a no-op.
func (c *commonAuthorizer) RenameNetwork(ctx context.Context, projectName string, oldNetworkName string, newNetworkName string) error {
	return nil
}

// AddNetworkZone is a no-op.
func (c *commonAuthorizer) AddNetworkZone(ctx context.Context, projectName string, networkZoneName string) error {
	return nil
}

// DeleteNetworkZone is a no-op.
func (c *commonAuthorizer) DeleteNetworkZone(ctx context.Context, projectName string, networkZoneName string) error {
	return nil
}

// AddNetworkIntegration is a no-op.
func (c *commonAuthorizer) AddNetworkIntegration(ctx context.Context, networkIntegrationName string) error {
	return nil
}

// DeleteNetworkIntegration is a no-op.
func (c *commonAuthorizer) DeleteNetworkIntegration(ctx context.Context, networkIntegrationName string) error {
	return nil
}

// RenameNetworkIntegration is a no-op.
func (c *commonAuthorizer) RenameNetworkIntegration(ctx context.Context, oldNetworkIntegrationName string, newNetworkIntegrationName string) error {
	return nil
}

// AddNetworkACL is a no-op.
func (c *commonAuthorizer) AddNetworkACL(ctx context.Context, projectName string, networkACLName string) error {
	return nil
}

// DeleteNetworkACL is a no-op.
func (c *commonAuthorizer) DeleteNetworkACL(ctx context.Context, projectName string, networkACLName string) error {
	return nil
}

// RenameNetworkACL is a no-op.
func (c *commonAuthorizer) RenameNetworkACL(ctx context.Context, projectName string, oldNetworkACLName string, newNetworkACLName string) error {
	return nil
}

// AddNetworkAddressSet is a no-op.
func (c *commonAuthorizer) AddNetworkAddressSet(ctx context.Context, projectName string, networkAddressSetName string) error {
	return nil
}

// DeleteNetworkAddressSet is a no-op.
func (c *commonAuthorizer) DeleteNetworkAddressSet(ctx context.Context, projectName string, networkAddressSetName string) error {
	return nil
}

// RenameNetworkAddressSet is a no-op.
func (c *commonAuthorizer) RenameNetworkAddressSet(ctx context.Context, projectName string, oldNetworkAddressSetName string, newNetworkAddressSetName string) error {
	return nil
}

// AddProfile is a no-op.
func (c *commonAuthorizer) AddProfile(ctx context.Context, projectName string, profileName string) error {
	return nil
}

// DeleteProfile is a no-op.
func (c *commonAuthorizer) DeleteProfile(ctx context.Context, projectName string, profileName string) error {
	return nil
}

// RenameProfile is a no-op.
func (c *commonAuthorizer) RenameProfile(ctx context.Context, projectName string, oldProfileName string, newProfileName string) error {
	return nil
}

// AddStoragePoolVolume is a no-op.
func (c *commonAuthorizer) AddStoragePoolVolume(ctx context.Context, projectName string, storagePoolName string, storageVolumeType string, storageVolumeName string, storageVolumeLocation string) error {
	return nil
}

// DeleteStoragePoolVolume is a no-op.
func (c *commonAuthorizer) DeleteStoragePoolVolume(ctx context.Context, projectName string, storagePoolName string, storageVolumeType string, storageVolumeName string, storageVolumeLocation string) error {
	return nil
}

// RenameStoragePoolVolume is a no-op.
func (c *commonAuthorizer) RenameStoragePoolVolume(ctx context.Context, projectName string, storagePoolName string, storageVolumeType string, oldStorageVolumeName string, newStorageVolumeName string, storageVolumeLocation string) error {
	return nil
}

// AddStorageBucket is a no-op.
func (c *commonAuthorizer) AddStorageBucket(ctx context.Context, projectName string, storagePoolName string, storageBucketName string, storageBucketLocation string) error {
	return nil
}

// DeleteStorageBucket is a no-op.
func (c *commonAuthorizer) DeleteStorageBucket(ctx context.Context, projectName string, storagePoolName string, storageBucketName string, storageBucketLocation string) error {
	return nil
}
