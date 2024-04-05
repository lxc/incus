package main

import (
	"net/http"

	"github.com/lxc/incus/v6/internal/server/metadata"
	"github.com/lxc/incus/v6/internal/server/response"
)

var metadataConfigurationCmd = APIEndpoint{
	Path: "metadata/configuration",

	Get: APIEndpointAction{Handler: metadataConfigurationGet, AllowUntrusted: true},
}

// swagger:operation GET /1.0/metadata/configuration metadata_configuration_get
//
//	Get the metadata configuration
//
//	Returns the generated metadata configuration in YAML format.
//
//	---
//	produces:
//	  - text/plain
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
//	          type: string
//	          description: The generated metadata configuration
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func metadataConfigurationGet(d *Daemon, r *http.Request) response.Response {
	return response.SyncResponse(true, metadata.Data)
}
