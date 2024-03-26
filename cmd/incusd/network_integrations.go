package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/mux"

	"github.com/lxc/incus/internal/server/auth"
	"github.com/lxc/incus/internal/server/db"
	dbCluster "github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/lifecycle"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/request"
	"github.com/lxc/incus/internal/server/response"
	localUtil "github.com/lxc/incus/internal/server/util"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/validate"
)

var networkIntegrationsCmd = APIEndpoint{
	Path: "network-integrations",

	Get:  APIEndpointAction{Handler: networkIntegrationsGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: networkIntegrationsPost, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanCreateNetworkIntegrations)},
}

var networkIntegrationCmd = APIEndpoint{
	Path: "network-integrations/{integration}",

	Delete: APIEndpointAction{Handler: networkIntegrationDelete, AccessHandler: allowPermission(auth.ObjectTypeNetworkIntegration, auth.EntitlementCanEdit, "integration")},
	Get:    APIEndpointAction{Handler: networkIntegrationGet, AccessHandler: allowPermission(auth.ObjectTypeNetworkIntegration, auth.EntitlementCanView, "integration")},
	Put:    APIEndpointAction{Handler: networkIntegrationPut, AccessHandler: allowPermission(auth.ObjectTypeNetworkIntegration, auth.EntitlementCanEdit, "integration")},
	Patch:  APIEndpointAction{Handler: networkIntegrationPut, AccessHandler: allowPermission(auth.ObjectTypeNetworkIntegration, auth.EntitlementCanEdit, "integration")},
	Post:   APIEndpointAction{Handler: networkIntegrationPost, AccessHandler: allowPermission(auth.ObjectTypeNetworkIntegration, auth.EntitlementCanEdit, "integration")},
}

// API endpoints.

// swagger:operation GET /1.0/network-integrations network-integrations network_integrations_get
//
//  Get the network integrations
//
//  Returns a list of network integrations (URLs).
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
//                "/1.0/network-integrations/region2",
//                "/1.0/network-integrations/region3"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/network-integrations?recursion=1 network-integrations network_integrations_get_recursion1
//
//	Get the network integrations
//
//	Returns a list of network integrations (structs).
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
//	          description: List of network integrations
//	          items:
//	            $ref: "#/definitions/NetworkIntegration"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkIntegrationsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	recursion := localUtil.IsRecursionRequest(r)

	// Network integrations aren't project aware, we only load the per-project data to apply name restrictions.
	projectName := request.ProjectParam(r)

	// Get list of Network integrations.
	resultString := []string{}
	resultMap := []api.NetworkIntegration{}

	err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Load the project.
		dbProject, err := dbCluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return fmt.Errorf("Failed to load network restrictions from project %q: %w", projectName, err)
		}

		p, err := dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed to load network restrictions from project %q: %w", projectName, err)
		}

		// Load the integrations.
		integrations, err := dbCluster.GetNetworkIntegrations(ctx, tx.Tx())
		if err != nil {
			return err
		}

		for _, integration := range integrations {
			// Filter for project restrictions.
			if !project.NetworkIntegrationAllowed(p.Config, integration.Name) {
				continue
			}

			if !recursion {
				resultString = append(resultString, api.NewURL().Path(version.APIVersion, "network-integrations", integration.Name).String())
			} else {
				// Get the integration.
				result, err := integration.ToAPI(r.Context(), tx.Tx())
				if err != nil {
					return err
				}

				// Check if the user should see the configuration.
				err = s.Authorizer.CheckPermission(r.Context(), r, auth.ObjectNetworkIntegration(result.Name), auth.EntitlementCanEdit)
				if err != nil {
					result.Config = map[string]string{}
				}

				// Add UsedBy field.
				usedBy, err := tx.GetNetworkPeersURLByIntegration(ctx, integration.Name)
				if err != nil {
					return err
				}

				result.UsedBy = usedBy

				resultMap = append(resultMap, *result)
			}
		}

		return nil
	})
	if err != nil {
		return response.InternalError(err)
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

// swagger:operation POST /1.0/network-integrations network-integrations network_integrations_post
//
//	Add a network integration
//
//	Creates a new network integration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: integration
//	    description: integration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkIntegrationsPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkIntegrationsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	req := api.NetworkIntegrationsPost{}

	// Parse the request into a record.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Validate the config.
	err = networkIntegrationValidate(req.Type, false, nil, req.Config)
	if err != nil {
		return response.BadRequest(err)
	}

	// Convert the API type to DB type.
	dbType := -1
	for k, v := range dbCluster.NetworkIntegrationTypeNames {
		if v == req.Type {
			dbType = k
			break
		}
	}

	if dbType == -1 {
		return response.BadRequest(fmt.Errorf("Unsupported integration type %q", req.Type))
	}

	// Create the DB record.
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbRecord := dbCluster.NetworkIntegration{
			Name:        req.Name,
			Description: req.Description,
			Type:        dbType,
		}

		id, err := dbCluster.CreateNetworkIntegration(ctx, tx.Tx(), dbRecord)
		if err != nil {
			return err
		}

		err = dbCluster.CreateNetworkIntegrationConfig(ctx, tx.Tx(), id, req.Config)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Add the integration to the auth backend.
	err = s.Authorizer.AddNetworkIntegration(r.Context(), req.Name)
	if err != nil {
		logger.Error("Failed to add network integration to authorizer", logger.Ctx{"name": req.Name, "error": err})
	}

	// Emit the lifecycle event.
	lc := lifecycle.NetworkIntegrationCreated.Event(req.Name, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(api.ProjectDefaultName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation DELETE /1.0/network-integrations/{integration} network-integrations network_integration_delete
//
//	Delete the network integration
//
//	Removes the network integration.
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
func networkIntegrationDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the integration name.
	integrationName, err := url.PathUnescape(mux.Vars(r)["integration"])
	if err != nil {
		return response.SmartError(err)
	}

	// Delete the DB record.
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get UsedBy for the integration.
		usedBy, err := tx.GetNetworkPeersURLByIntegration(ctx, integrationName)
		if err != nil {
			return err
		}

		if len(usedBy) > 0 {
			return fmt.Errorf("Network integration is currently in use")
		}

		err = dbCluster.DeleteNetworkIntegration(ctx, tx.Tx(), integrationName)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Delete the integration from the auth backend.
	err = s.Authorizer.DeleteNetworkIntegration(r.Context(), integrationName)
	if err != nil {
		logger.Error("Failed to remove network integration from authorizer", logger.Ctx{"name": integrationName, "error": err})
	}

	// Emit the lifecycle event.
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.NetworkIntegrationDeleted.Event(integrationName, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/network-integrations/{integration} network-integrations network_integration_get
//
//	Get the network integration
//
//	Gets a specific network integration.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: integration
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
//	          $ref: "#/definitions/NetworkIntegration"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkIntegrationGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the integration name.
	integrationName, err := url.PathUnescape(mux.Vars(r)["integration"])
	if err != nil {
		return response.SmartError(err)
	}

	// Network integrations aren't project aware, we only load the per-project data to apply name restrictions.
	projectName := request.ProjectParam(r)

	// Get the integration.
	var info *api.NetworkIntegration

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the project.
		dbProject, err := dbCluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return fmt.Errorf("Failed to load network restrictions from project %q: %w", projectName, err)
		}

		p, err := dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed to load network restrictions from project %q: %w", projectName, err)
		}

		// Filter for project restrictions.
		if !project.NetworkIntegrationAllowed(p.Config, integrationName) {
			return nil
		}

		// Get the integration.
		dbRecord, err := dbCluster.GetNetworkIntegration(ctx, tx.Tx(), integrationName)
		if err != nil {
			return err
		}

		info, err = dbRecord.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		// Check if the user should see the configuration.
		err = s.Authorizer.CheckPermission(r.Context(), r, auth.ObjectNetworkIntegration(info.Name), auth.EntitlementCanEdit)
		if err != nil {
			info.Config = map[string]string{}
		}

		// Add UsedBy field.
		usedBy, err := tx.GetNetworkPeersURLByIntegration(ctx, info.Name)
		if err != nil {
			return err
		}

		info.UsedBy = usedBy

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if info == nil {
		return response.NotFound(nil)
	}

	return response.SyncResponseETag(true, info, info.Writable())
}

// swagger:operation PATCH /1.0/network-integrations/{integration} network-integrations network_integration_patch
//
//  Partially update the network integration
//
//  Updates a subset of the network integration configuration.
//
//  ---
//  consumes:
//    - application/json
//  produces:
//    - application/json
//  parameters:
//    - in: body
//      name: integration
//      description: integration configuration
//      required: true
//      schema:
//        $ref: "#/definitions/NetworkIntegrationPut"
//  responses:
//    "200":
//      $ref: "#/responses/EmptySyncResponse"
//    "400":
//      $ref: "#/responses/BadRequest"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "412":
//      $ref: "#/responses/PreconditionFailed"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation PUT /1.0/network-integrations/{integration} network-integrations network_integration_put
//
//	Update the network integration
//
//	Updates the entire network integration configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: integration
//	    description: integration configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkIntegrationPut"
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
func networkIntegrationPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	integrationName, err := url.PathUnescape(mux.Vars(r)["integration"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the existing network integration.
	var dbRecord *dbCluster.NetworkIntegration
	var info *api.NetworkIntegration
	var usedBy []string

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbRecord, err = dbCluster.GetNetworkIntegration(ctx, tx.Tx(), integrationName)
		if err != nil {
			return err
		}

		info, err = dbRecord.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		usedBy, err = tx.GetNetworkPeersURLByIntegration(ctx, integrationName)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag.
	err = localUtil.EtagCheck(r, info.Writable())
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Decode the request.
	req := api.NetworkIntegrationPut{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if r.Method == http.MethodPatch {
		// If config being updated via "patch" method, then merge all existing config with the keys that
		// are present in the request config.
		for k, v := range info.Config {
			_, ok := req.Config[k]
			if !ok {
				req.Config[k] = v
			}
		}

		if req.Description == "" {
			req.Description = info.Description
		}
	}

	// Validate the resulting config.
	err = networkIntegrationValidate(info.Type, len(usedBy) > 0, info.Config, req.Config)
	if err != nil {
		return response.BadRequest(err)
	}

	// Update the database record.
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Update the description if needed.
		if dbRecord.Description != req.Description {
			dbRecord.Description = req.Description
			err := dbCluster.UpdateNetworkIntegration(ctx, tx.Tx(), integrationName, *dbRecord)
			if err != nil {
				return err
			}
		}

		// Update the configuration.
		err := dbCluster.UpdateNetworkIntegrationConfig(ctx, tx.Tx(), int64(dbRecord.ID), req.Config)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Emit the lifecycle event.
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.NetworkIntegrationUpdated.Event(integrationName, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/network-integrations/{integration} network-integrations network_integration_post
//
//	Rename the network integration
//
//	Renames the network integration.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: integration
//	    description: integration configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkIntegrationPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkIntegrationPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the integration name.
	integrationName, err := url.PathUnescape(mux.Vars(r)["integration"])
	if err != nil {
		return response.SmartError(err)
	}

	// Decode the request.
	req := api.NetworkIntegrationPost{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Rename the DB record.
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := dbCluster.RenameNetworkIntegration(ctx, tx.Tx(), integrationName, req.Name)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Rename the integration in the auth backend.
	err = s.Authorizer.RenameNetworkIntegration(r.Context(), integrationName, req.Name)
	if err != nil {
		logger.Error("Failed to remove network integration from authorizer", logger.Ctx{"name": integrationName, "error": err})
	}

	// Emit the lifecycle event.
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.NetworkIntegrationDeleted.Event(req.Name, request.CreateRequestor(r), logger.Ctx{"old_name": integrationName}))

	return response.EmptySyncResponse
}

// networkIntegrationValidate validates the configuration keys/values for network integration.
func networkIntegrationValidate(integrationType string, inUse bool, oldConfig map[string]string, config map[string]string) error {
	if integrationType != "ovn" {
		return fmt.Errorf("Invalid integration type %q", integrationType)
	}

	configKeys := map[string]func(value string) error{
		// gendoc:generate(entity=network_integration, group=ovn, key=ovn.northbound_connection)
		//
		// ---
		//  type: string
		//  scope: global
		//  shortdesc: OVN northbound inter-connection connection string
		"ovn.northbound_connection": validate.IsAny,

		// gendoc:generate(entity=network_integration, group=ovn, key=ovn.southbound_connection)
		//
		// ---
		//  type: string
		//  scope: global
		//  shortdesc: OVN southbound inter-connection connection string
		"ovn.southbound_connection": validate.IsAny,

		// gendoc:generate(entity=network_integration, group=ovn, key=ovn.ca_cert)
		//
		// ---
		//  type: string
		//  scope: global
		//  shortdesc: OVN SSL certificate authority for the inter-connection database
		"ovn.ca_cert": validate.Optional(validate.IsAny),

		// gendoc:generate(entity=network_integration, group=ovn, key=ovn.client_cert)
		//
		// ---
		//  type: string
		//  scope: global
		//  shortdesc: OVN SSL client certificate
		"ovn.client_cert": validate.Optional(validate.IsAny),

		// gendoc:generate(entity=network_integration, group=ovn, key=ovn.client_key)
		//
		// ---
		//  type: string
		//  scope: global
		//  shortdesc: OVN SSL client key
		"ovn.client_key": validate.Optional(validate.IsAny),

		// gendoc:generate(entity=network_integration, group=ovn, key=ovn.transit.pattern)
		// Specify a Pongo2 template string that represents the transit switch name.
		// This template gets access to the project name (`projectName`), integration name (`integrationName`) and network name (`networkName`).
		//
		// ---
		//  type: string
		//  defaultdesc: `ts-incus-{{ integrationName }}-{{ projectName }}-{{ networkname }}`
		//  shortdesc: Template for the transit switch name
		"ovn.transit.pattern": validate.IsAny,
	}

	for k, v := range config {
		// User keys are free for all.

		// gendoc:generate(entity=network_integration, group=common, key=user.*)
		// User keys can be used in search.
		// ---
		//  type: string
		//  shortdesc: Free form user key/value storage
		if strings.HasPrefix(k, "user.") {
			continue
		}

		validator, ok := configKeys[k]
		if !ok {
			return fmt.Errorf("Invalid network integration configuration key %q", k)
		}

		err := validator(v)
		if err != nil {
			return fmt.Errorf("Invalid network integration configuration key %q value", k)
		}
	}

	if oldConfig != nil && oldConfig["ovn.transit.pattern"] != config["ovn.transit.pattern"] && inUse {
		return fmt.Errorf("The OVN transit switch pattern cannot be changed while the integration is in use")
	}

	return nil
}
