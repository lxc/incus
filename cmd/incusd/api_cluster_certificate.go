package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"

	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/revert"
	"github.com/lxc/incus/v6/internal/server/acme"
	"github.com/lxc/incus/v6/internal/server/auth"
	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/db/warningtype"
	"github.com/lxc/incus/v6/internal/server/lifecycle"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/internal/server/warnings"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	localtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/lxc/incus/v6/shared/util"
)

var clusterCertificateCmd = APIEndpoint{
	Path: "cluster/certificate",

	Put: APIEndpointAction{Handler: clusterCertificatePut, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
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
	reverter := revert.New()
	defer reverter.Fail()

	newClusterCertFilename := internalUtil.VarPath(acme.ClusterCertFilename)

	// First node forwards request to all other cluster nodes
	if r == nil || !isClusterNotification(r) {
		var err error

		reverter.Add(func() {
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

		reverter.Add(func() {
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

		var c incus.InstanceServer

		for i := range members {
			member := members[i]

			if member.Address == localClusterAddress {
				continue
			}

			c, err = cluster.Connect(member.Address, s.Endpoints.NetworkCert(), s.ServerCert(), r, true)
			if err != nil {
				return err
			}

			err = c.UpdateClusterCertificate(req, "")
			if err != nil {
				return err
			}

			// When reverting the certificate, we need to connect to the cluster members using the
			// new certificate otherwise we'll get a bad certificate error.
			reverter.Add(func() {
				c, err := cluster.Connect(member.Address, newCertInfo, s.ServerCert(), r, true)
				if err != nil {
					logger.Error("Failed to connect to cluster member", logger.Ctx{"address": member.Address, "err": err})
					return
				}

				err = c.UpdateClusterCertificate(oldReq, "")
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

	reverter.Success()

	return nil
}
