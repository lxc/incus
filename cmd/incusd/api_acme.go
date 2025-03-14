package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/lxc/incus/v6/internal/server/acme"
	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/db/operationtype"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/internal/server/task"
	"github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	localtls "github.com/lxc/incus/v6/shared/tls"
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

	// Redirect to the leader when clustered.
	if s.ServerClustered {
		leader, err := s.Cluster.LeaderAddress()
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

	// Forward to the lego listener.
	addr := s.GlobalConfig.ACMEHTTP()
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}

	domain, _, _, _, _ := s.GlobalConfig.ACME()

	client := http.Client{}
	client.Transport = &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("tcp", addr)
		},
	}

	req, err := http.NewRequest("GET", "http://"+domain+r.URL.String(), nil)
	if err != nil {
		return response.InternalError(err)
	}

	req.Header = r.Header

	resp, err := client.Do(req)
	if err != nil {
		return response.InternalError(err)
	}

	defer resp.Body.Close()

	challenge, err := io.ReadAll(resp.Body)
	if err != nil {
		return response.InternalError(err)
	}

	return response.ManualResponse(func(w http.ResponseWriter) error {
		w.Header().Set("Content-Type", "text/plain")

		_, err = w.Write(challenge)
		if err != nil {
			return err
		}

		return nil
	})
}

func autoRenewCertificate(ctx context.Context, d *Daemon, force bool) error {
	s := d.State()

	domain, email, caURL, agreeToS, challengeType := s.GlobalConfig.ACME()

	if domain == "" || email == "" || !agreeToS || challengeType == "" {
		return nil
	}

	// If we are clustered, let the leader handle the certificate renewal.
	if s.ServerClustered {
		leader, err := s.Cluster.LeaderAddress()
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
		newCert, err := acme.UpdateCertificate(s, challengeType, s.ServerClustered, domain, email, caURL, force)
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
