package main

import (
	"context"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/lxc/incus/internal/server/acme"
	"github.com/lxc/incus/internal/server/cluster"
	"github.com/lxc/incus/internal/server/db/operationtype"
	"github.com/lxc/incus/internal/server/operations"
	"github.com/lxc/incus/internal/server/response"
	"github.com/lxc/incus/internal/server/task"
	"github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	localtls "github.com/lxc/incus/shared/tls"
)

var apiACME = []APIEndpoint{
	acmeChallengeCmd,
}

var acmeChallengeCmd = APIEndpoint{
	Path: ".well-known/acme-challenge/{token}",

	Get: APIEndpointAction{Handler: acmeProvideChallenge, AllowUntrusted: true},
}

func acmeProvideChallenge(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	token, err := url.PathUnescape(mux.Vars(r)["token"])
	if err != nil {
		return response.SmartError(err)
	}

	if s.ServerClustered {
		leader, err := d.gateway.LeaderAddress()
		if err != nil {
			return response.SmartError(err)
		}

		// This gives me the correct value
		clusterAddress := s.LocalConfig.ClusterAddress()

		if clusterAddress != "" && clusterAddress != leader {
			// Forward the request to the leader
			client, err := cluster.Connect(leader, s.Endpoints.NetworkCert(), s.ServerCert(), r, true)
			if err != nil {
				return response.SmartError(err)
			}

			return response.ForwardedResponse(client, r)
		}
	}

	if d.http01Provider == nil || d.http01Provider.Token() != token {
		return response.NotFound(nil)
	}

	return response.ManualResponse(func(w http.ResponseWriter) error {
		w.Header().Set("Content-Type", "text/plain")

		_, err := w.Write([]byte(d.http01Provider.KeyAuth()))
		if err != nil {
			return err
		}

		return nil
	})
}

func autoRenewCertificate(ctx context.Context, d *Daemon, force bool) error {
	s := d.State()

	domain, email, caURL, agreeToS := s.GlobalConfig.ACME()

	if domain == "" || email == "" || !agreeToS {
		return nil
	}

	// If we are clustered, let the leader handle the certificate renewal.
	if s.ServerClustered {
		leader, err := d.gateway.LeaderAddress()
		if err != nil {
			return err
		}

		// Figure out our own cluster address.
		clusterAddress := s.LocalConfig.ClusterAddress()

		if clusterAddress != leader {
			return nil
		}
	}

	opRun := func(op *operations.Operation) error {
		newCert, err := acme.UpdateCertificate(s, d.http01Provider, s.ServerClustered, domain, email, caURL, force)
		if err != nil {
			return err
		}

		// If cert is nil, there's no need to update it as it's still valid.
		if newCert == nil {
			return nil
		}

		if s.ServerClustered {
			req := api.ClusterCertificatePut{
				ClusterCertificate:    string(newCert.Certificate),
				ClusterCertificateKey: string(newCert.PrivateKey),
			}

			err = updateClusterCertificate(s.ShutdownCtx, s, d.gateway, nil, req)
			if err != nil {
				return err
			}

			return nil
		}

		cert, err := localtls.KeyPairFromRaw(newCert.Certificate, newCert.PrivateKey)
		if err != nil {
			return err
		}

		s.Endpoints.NetworkUpdateCert(cert)

		err = util.WriteCert(s.OS.VarDir, "server", newCert.Certificate, newCert.PrivateKey, nil)
		if err != nil {
			return err
		}

		return nil
	}

	op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.RenewServerCertificate, nil, nil, opRun, nil, nil, nil)
	if err != nil {
		logger.Error("Failed creating renew server certificate operation", logger.Ctx{"err": err})
		return err
	}

	logger.Info("Starting automatic server certificate renewal check")

	err = op.Start()
	if err != nil {
		logger.Error("Failed starting renew server certificate operation", logger.Ctx{"err": err})
		return err
	}

	err = op.Wait(ctx)
	if err != nil {
		logger.Error("Failed server certificate renewal", logger.Ctx{"err": err})
		return err
	}

	logger.Info("Done automatic server certificate renewal check")

	return nil
}

func autoRenewCertificateTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		_ = autoRenewCertificate(ctx, d, false)
	}

	return f, task.Daily()
}
