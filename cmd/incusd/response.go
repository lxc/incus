package main

import (
	"net/http"

	"github.com/lxc/incus/v7/internal/server/cluster"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/internal/server/response"
	"github.com/lxc/incus/v7/internal/server/state"
)

func forwardedResponseToNode(s *state.State, r *http.Request, memberName string) response.Response {
	// Figure out the address of the target member (which is possibly this very same member).
	address, err := cluster.ResolveTarget(r.Context(), s, memberName)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward the response if not local.
	if address != "" {
		client, err := cluster.Connect(address, s.Endpoints.NetworkCert(), s.ServerCert(), r, false)
		if err != nil {
			return response.SmartError(err)
		}

		return response.ForwardedResponse(client, r)
	}

	return nil
}

// forwardedResponseIfTargetIsRemote forwards a request to the request has a target parameter pointing to a member
// which is not the local one.
func forwardedResponseIfTargetIsRemote(s *state.State, r *http.Request) response.Response {
	targetNode := request.QueryParam(r, "target")
	if targetNode == "" {
		return nil
	}

	return forwardedResponseToNode(s, r, targetNode)
}

// forwardedResponseIfInstanceIsRemote redirects a request to the node running
// the container with the given name. If the container is local, nothing gets
// done and nil is returned.
func forwardedResponseIfInstanceIsRemote(s *state.State, r *http.Request, project, name string) (response.Response, error) {
	client, err := cluster.ConnectIfInstanceIsRemote(s, project, name, r)
	if err != nil {
		return nil, err
	}

	if client == nil {
		return nil, nil
	}

	return response.ForwardedResponse(client, r), nil
}

// forwardedResponseIfVolumeIsRemote redirects a request to the node hosting
// the volume with the given pool ID, name and type. If the container is local,
// nothing gets done and nil is returned. If more than one node has a matching
// volume, an error is returned.
//
// This is used when no targetNode is specified, and saves users some typing
// when the volume name/type is unique to a node.
func forwardedResponseIfVolumeIsRemote(s *state.State, r *http.Request, poolName string, projectName string, volumeName string, volumeType int) response.Response {
	if request.QueryParam(r, "target") != "" {
		return nil
	}

	client, err := cluster.ConnectIfVolumeIsRemote(s, poolName, projectName, volumeName, volumeType, s.Endpoints.NetworkCert(), s.ServerCert(), r)
	if err != nil {
		return response.SmartError(err)
	}

	if client == nil {
		return nil
	}

	return response.ForwardedResponse(client, r)
}

// forwardedResponseIfBucketIsRemote redirects a request to the node hosting
// the bucket with the given pool and name. If the bucket is local, nothing
// gets done and nil is returned. If more than one node has a matching bucket,
// an error is returned.
//
// This is used when no targetNode is specified, and saves users some typing
// when the bucket name is unique to a node.
func forwardedResponseIfBucketIsRemote(s *state.State, r *http.Request, poolName string, projectName string, bucketName string) response.Response {
	if request.QueryParam(r, "target") != "" {
		return nil
	}

	client, err := cluster.ConnectIfBucketIsRemote(s, poolName, projectName, bucketName, s.Endpoints.NetworkCert(), s.ServerCert(), r)
	if err != nil {
		return response.SmartError(err)
	}

	if client == nil {
		return nil
	}

	return response.ForwardedResponse(client, r)
}
