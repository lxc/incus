package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/lxc/incus/v7/internal/server/acme"
	"github.com/lxc/incus/v7/internal/server/auth"
	"github.com/lxc/incus/v7/internal/server/cluster"
	"github.com/lxc/incus/v7/internal/server/db/operationtype"
	"github.com/lxc/incus/v7/internal/server/operations"
	"github.com/lxc/incus/v7/internal/server/response"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/internal/server/task"
	"github.com/lxc/incus/v7/internal/util"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
	localtls "github.com/lxc/incus/v7/shared/tls"
)

var apiACME = []APIEndpoint{
	acmeChallengeCmd,
}

var acmeChallengeCmd = APIEndpoint{
	Path: ".well-known/acme-challenge/{token}",

	Get: APIEndpointAction{Handler: acmeProvideChallenge, AllowUntrusted: true},
}

// acmeChallengePath matches an HTTP-01 challenge path (RFC 8555 tokens are base64url).
var acmeChallengePath = regexp.MustCompile(`^/\.well-known/acme-challenge/[A-Za-z0-9_-]+$`)

var internalACMEChallengeCmd = APIEndpoint{
	Path: "acme/challenge",

	Post:   APIEndpointAction{Handler: internalACMEChallengePost, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
	Delete: APIEndpointAction{Handler: internalACMEChallengeDelete, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

// internalACMEChallengePost starts the HTTP-01 challenge listener on a cluster member.
func internalACMEChallengePost(d *Daemon, r *http.Request) response.Response {
	err := acmeChallengeListenerStart(d)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// internalACMEChallengeDelete stops the HTTP-01 challenge listener on a cluster member.
func internalACMEChallengeDelete(d *Daemon, r *http.Request) response.Response {
	acmeChallengeListenerStop(d, nil)

	return response.EmptySyncResponse
}

// acmeChallengeListenerStart listens on the ACME HTTP port and forwards challenges to the leader.
func acmeChallengeListenerStart(d *Daemon) error {
	s := d.State()

	d.acmeListenerMu.Lock()
	defer d.acmeListenerMu.Unlock()

	if d.acmeListener != nil {
		return nil
	}

	listener, err := net.Listen("tcp", s.GlobalConfig.ACMEHTTP())
	if err != nil {
		return err
	}

	server := &http.Server{
		ReadHeaderTimeout: 3 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The request is replayed with our cluster credentials, so only let actual challenges through.
			if r.Method != http.MethodGet || !acmeChallengePath.MatchString(r.URL.Path) {
				http.NotFound(w, r)
				return
			}

			err := acmeForwardChallenge(s, r).Render(w)
			if err != nil {
				_ = response.SmartError(err).Render(w)
			}
		}),
	}

	go func() {
		_ = server.Serve(listener)
	}()

	// Stop on our own in case the leader never asks us to.
	time.AfterFunc(15*time.Minute, func() {
		acmeChallengeListenerStop(d, server)
	})

	d.acmeListener = server

	return nil
}

// acmeChallengeListenerStop stops the HTTP-01 challenge listener (or only the given one).
func acmeChallengeListenerStop(d *Daemon, server *http.Server) {
	d.acmeListenerMu.Lock()
	defer d.acmeListenerMu.Unlock()

	if d.acmeListener == nil || (server != nil && d.acmeListener != server) {
		return
	}

	_ = d.acmeListener.Close()
	d.acmeListener = nil
}

// acmeForwardChallenge forwards a challenge request to the cluster leader.
func acmeForwardChallenge(s *state.State, r *http.Request) response.Response {
	leader, err := s.Cluster.LeaderAddress()
	if err != nil {
		return response.SmartError(err)
	}

	// The leader answers through lego itself, never through this listener.
	if s.LocalConfig.ClusterAddress() == leader {
		return response.NotFound(errors.New("Challenge isn't being forwarded"))
	}

	client, err := cluster.Connect(leader, s.Endpoints.NetworkCert(), s.ServerCert(), r, true)
	if err != nil {
		return response.SmartError(err)
	}

	return response.ForwardedResponse(client, r)
}

func acmeProvideChallenge(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Redirect to the leader when clustered.
	if s.ServerClustered {
		leader, err := s.Cluster.LeaderAddress()
		if err != nil {
			return response.SmartError(err)
		}

		if s.LocalConfig.ClusterAddress() != leader {
			return acmeForwardChallenge(s, r)
		}
	}

	// Forward to the lego listener.
	addr := s.GlobalConfig.ACMEHTTP()
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}

	client := http.Client{}
	client.Transport = &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("tcp", addr)
		},
	}

	// Get the forwarded host with fallback to the one from the request.
	host := r.Header.Get("X-Incus-forwarded-host")

	if host == "" {
		host = r.Host
	}

	if host == "" {
		host = r.URL.Host
	}

	// Prepare the request to lego.
	req, err := http.NewRequest("GET", "http://"+host+r.URL.String(), nil)
	if err != nil {
		return response.InternalError(err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return response.InternalError(err)
	}

	defer logger.WarnOnError(resp.Body.Close, "Failed to close response body")

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
		// Free the HTTP port for lego in case we were forwarding challenges before becoming leader.
		acmeChallengeListenerStop(d, nil)

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

	return f, task.Hourly()
}
