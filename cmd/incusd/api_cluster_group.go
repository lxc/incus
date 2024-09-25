package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/gorilla/mux"

	"github.com/lxc/incus/v6/internal/server/auth"
	"github.com/lxc/incus/v6/internal/server/db"
	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	instanceDrivers "github.com/lxc/incus/v6/internal/server/instance/drivers"
	"github.com/lxc/incus/v6/internal/server/lifecycle"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/internal/server/state"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/osarch"
	"github.com/lxc/incus/v6/shared/validate"
)

var targetGroupPrefix = "@"

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

	err = clusterGroupValidate(req.Config)
	if err != nil {
		return response.BadRequest(err)
	}

	// Fill in the auto values.
	err = clusterGroupFill(r.Context(), s, nil, &req.ClusterGroupPut)
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

		err = dbCluster.CreateClusterGroupConfig(ctx, tx.Tx(), groupID, req.Config)
		if err != nil {
			return err
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

	var apiGroup *api.ClusterGroup
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the cluster group.
		group, err := dbCluster.GetClusterGroup(ctx, tx.Tx(), name)
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

		apiGroup, err = group.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
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

	// Validate the config.
	err = clusterGroupValidate(req.Config)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the current state.
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

	// Fill in the auto values.
	err = clusterGroupFill(r.Context(), s, dbClusterGroup.Nodes, &req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Update the database.
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

		err = dbCluster.UpdateClusterGroupConfig(ctx, tx.Tx(), int64(group.ID), req.Config)
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

		clusterGroup, err = dbClusterGroup.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		return nil
	})
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

	if req.Config == nil {
		req.Config = clusterGroup.Config
	} else {
		for k, v := range clusterGroup.Config {
			_, ok := req.Config[k]
			if !ok {
				req.Config[k] = v
			}
		}
	}

	// Validate the config.
	err = clusterGroupValidate(req.Config)
	if err != nil {
		return response.BadRequest(err)
	}

	// Fill in the auto values.
	err = clusterGroupFill(r.Context(), s, dbClusterGroup.Nodes, &req)
	if err != nil {
		return response.BadRequest(err)
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

		err = dbCluster.UpdateClusterGroupConfig(ctx, tx.Tx(), groupID, req.Config)
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

// clusterGroupValidate validates the configuration keys/values for cluster groups.
func clusterGroupValidate(config map[string]string) error {
	configKeys := map[string]func(value string) error{}

	// Add architecture keys.
	for _, arch := range osarch.SupportedArchitectures() {
		// gendoc:generate(entity=cluster_group, group=common, key=instances.vm.cpu.ARCHITECTURE.baseline)
		// The CPU base architecture name as can be found through `qemu -cpu ?`.
		//
		// This can be a generic definition like `qemu64` or `kvm64`, or it can be a specific hardware architecture like `EPYC-v2`.
		// It's important to ensure that all servers in the group match that baseline.
		// ---
		//  type: string
		//  shortdesc: CPU base architecture name
		configKeys[fmt.Sprintf("instances.vm.cpu.%s.baseline", arch)] = validate.Optional(validate.IsAny)

		// gendoc:generate(entity=cluster_group, group=common, key=instances.vm.cpu.ARCHITECTURE.flags)
		// A comma separated list of CPU flags to add on top of CPU baseline or a list of flags to remove from it.
		//
		// To remove a flag, use `-flag`.
		// ---
		//  type: string
		//  shortdesc: CPU flags to add/remove to/from the baseline
		configKeys[fmt.Sprintf("instances.vm.cpu.%s.flags", arch)] = validate.Optional(validate.IsListOf(validate.IsAny))
	}

	for k, v := range config {
		// User keys are free for all.

		// gendoc:generate(entity=cluster_group, group=common, key=user.*)
		// User keys can be used in search.
		// ---
		//  type: string
		//  shortdesc: Free form user key/value storage
		if strings.HasPrefix(k, "user.") {
			continue
		}

		validator, ok := configKeys[k]
		if !ok {
			return fmt.Errorf("Invalid cluster group configuration key %q", k)
		}

		err := validator(v)
		if err != nil {
			return fmt.Errorf("Invalid cluster group configuration key %q value", k)
		}
	}

	return nil
}

// clusterGroupFill fills in automatic values.
func clusterGroupFill(ctx context.Context, s *state.State, servers []string, req *api.ClusterGroupPut) error {
	// If no config, nothing to fill.
	if req.Config == nil {
		return nil
	}

	for _, arch := range osarch.SupportedArchitectures() {
		baseline := req.Config[fmt.Sprintf("instances.vm.cpu.%s.baseline", arch)]
		flags := req.Config[fmt.Sprintf("instances.vm.cpu.%s.flags", arch)]

		// Check whether we need to fill in values.
		if flags != "auto" {
			continue
		}

		if baseline != "kvm64" || arch != "x86_64" {
			return fmt.Errorf("Automatic CPU flags are currently only supported on \"x86_64\" with the \"kvm64\" baseline")
		}

		if len(servers) == 0 {
			return fmt.Errorf("Can't compute automatic CPU flags when no servers are in the cluster group")
		}

		// Fill in the flags.
		cpuFlags, err := instanceDrivers.GetClusterCPUFlags(ctx, s, servers, arch)
		if err != nil {
			return fmt.Errorf("Couldn't compute automatic CPU flags: %w", err)
		}

		req.Config[fmt.Sprintf("instances.vm.cpu.%s.flags", arch)] = strings.Join(cpuFlags, ",")
	}

	return nil
}
