package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/instance/operationlock"
	"github.com/lxc/incus/internal/server/migration"
	"github.com/lxc/incus/internal/server/operations"
	"github.com/lxc/incus/internal/server/state"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
)

func newMigrationSource(inst instance.Instance, stateful bool, instanceOnly bool, allowInconsistent bool, clusterMoveSourceName string, pushTarget *api.InstancePostTarget) (*migrationSourceWs, error) {
	ret := migrationSourceWs{
		migrationFields: migrationFields{
			instance:          inst,
			allowInconsistent: allowInconsistent,
		},
		clusterMoveSourceName: clusterMoveSourceName,
	}

	if pushTarget != nil {
		ret.pushCertificate = pushTarget.Certificate
		ret.pushOperationURL = pushTarget.Operation
		ret.pushSecrets = pushTarget.Websockets
	}

	ret.instanceOnly = instanceOnly

	secretNames := []string{api.SecretNameControl, api.SecretNameFilesystem}
	if stateful && inst.IsRunning() {
		if inst.Type() == instancetype.Container {
			_, err := exec.LookPath("criu")
			if err != nil {
				return nil, migration.ErrNoLiveMigrationSource
			}
		}

		ret.live = true
		secretNames = append(secretNames, api.SecretNameState)
	}

	ret.conns = make(map[string]*migrationConn, len(secretNames))
	for _, connName := range secretNames {
		if ret.pushOperationURL != "" {
			if ret.pushSecrets[connName] == "" {
				return nil, fmt.Errorf("Expected %q connection secret missing from migration source target request", connName)
			}

			dialer, err := setupWebsocketDialer(ret.pushCertificate)
			if err != nil {
				return nil, fmt.Errorf("Failed setting up websocket dialer for migration source %q connection: %w", connName, err)
			}

			u, err := url.Parse(fmt.Sprintf("wss://%s/websocket", strings.TrimPrefix(ret.pushOperationURL, "https://")))
			if err != nil {
				return nil, fmt.Errorf("Failed parsing websocket URL for migration source %q connection: %w", connName, err)
			}

			ret.conns[connName] = newMigrationConn(ret.pushSecrets[connName], dialer, u)
		} else {
			secret, err := internalUtil.RandomHexString(32)
			if err != nil {
				return nil, fmt.Errorf("Failed creating migration source secret for %q connection: %w", connName, err)
			}

			ret.conns[connName] = newMigrationConn(secret, nil, nil)
		}
	}

	return &ret, nil
}

func (s *migrationSourceWs) Do(state *state.State, migrateOp *operations.Operation) error {
	l := logger.AddContext(logger.Ctx{"project": s.instance.Project().Name, "instance": s.instance.Name(), "live": s.live, "clusterMoveSourceName": s.clusterMoveSourceName, "push": s.pushOperationURL != ""})

	ctx, cancel := context.WithTimeout(context.TODO(), time.Second*10)
	defer cancel()

	l.Info("Waiting for migration control connection on source")

	_, err := s.conns[api.SecretNameControl].WebSocket(ctx)
	if err != nil {
		return fmt.Errorf("Failed waiting for migration control connection on source: %w", err)
	}

	l.Info("Migration control connection established on source")

	defer l.Info("Migration channels disconnected on source")
	defer s.disconnect()

	stateConnFunc := func(ctx context.Context) (io.ReadWriteCloser, error) {
		conn := s.conns[api.SecretNameState]
		if conn == nil {
			return nil, fmt.Errorf("Migration source control connection not initialized")
		}

		wsConn, err := conn.WebsocketIO(ctx)
		if err != nil {
			return nil, fmt.Errorf("Failed getting migration source control connection: %w", err)
		}

		return wsConn, nil
	}

	filesystemConnFunc := func(ctx context.Context) (io.ReadWriteCloser, error) {
		conn := s.conns[api.SecretNameFilesystem]
		if conn == nil {
			return nil, fmt.Errorf("Migration source filesystem connection not initialized")
		}

		wsConn, err := conn.WebsocketIO(ctx)
		if err != nil {
			return nil, fmt.Errorf("Failed getting migration source filesystem connection: %w", err)
		}

		return wsConn, nil
	}

	s.instance.SetOperation(migrateOp)
	err = s.instance.MigrateSend(instance.MigrateSendArgs{
		MigrateArgs: instance.MigrateArgs{
			ControlSend:    s.send,
			ControlReceive: s.recv,
			StateConn:      stateConnFunc,
			FilesystemConn: filesystemConnFunc,
			Snapshots:      !s.instanceOnly,
			Live:           s.live,
			Disconnect: func() {
				for connName, conn := range s.conns {
					if connName != api.SecretNameControl {
						conn.Close()
					}
				}
			},
			ClusterMoveSourceName: s.clusterMoveSourceName,
		},
		AllowInconsistent: s.allowInconsistent,
	})
	if err != nil {
		l.Error("Failed migration on source", logger.Ctx{"err": err})

		errMsg := fmt.Errorf("Failed migration on source: %w", err)
		s.sendControl(errMsg)
		return errMsg
	}

	return nil
}

func newMigrationSink(args *migrationSinkArgs) (*migrationSink, error) {
	sink := migrationSink{
		migrationFields: migrationFields{
			instance:     args.Instance,
			instanceOnly: args.InstanceOnly,
			live:         args.Live,
		},
		url:                   args.URL,
		clusterMoveSourceName: args.ClusterMoveSourceName,
		push:                  args.Push,
		refresh:               args.Refresh,
	}

	secretNames := []string{api.SecretNameControl, api.SecretNameFilesystem}
	if sink.live {
		if sink.instance.Type() == instancetype.Container {
			_, err := exec.LookPath("criu")
			if err != nil {
				return nil, migration.ErrNoLiveMigrationTarget
			}
		}

		secretNames = append(secretNames, api.SecretNameState)
	}

	sink.conns = make(map[string]*migrationConn, len(secretNames))
	for _, connName := range secretNames {
		if !sink.push {
			if args.Secrets[connName] == "" {
				return nil, fmt.Errorf("Expected %q connection secret missing from migration sink target request", connName)
			}

			u, err := url.Parse(fmt.Sprintf("wss://%s/websocket", strings.TrimPrefix(args.URL, "https://")))
			if err != nil {
				return nil, fmt.Errorf("Failed parsing websocket URL for migration sink %q connection: %w", connName, err)
			}

			sink.conns[connName] = newMigrationConn(args.Secrets[connName], args.Dialer, u)
		} else {
			secret, err := internalUtil.RandomHexString(32)
			if err != nil {
				return nil, fmt.Errorf("Failed creating migration sink secret for %q connection: %w", connName, err)
			}

			sink.conns[connName] = newMigrationConn(secret, nil, nil)
		}
	}

	return &sink, nil
}

func (c *migrationSink) Do(state *state.State, instOp *operationlock.InstanceOperation) error {
	l := logger.AddContext(logger.Ctx{"project": c.instance.Project().Name, "instance": c.instance.Name(), "live": c.live, "clusterMoveSourceName": c.clusterMoveSourceName, "push": c.push})

	ctx, cancel := context.WithTimeout(context.TODO(), time.Second*10)
	defer cancel()

	l.Info("Waiting for migration control connection on target")

	_, err := c.conns[api.SecretNameControl].WebSocket(ctx)
	if err != nil {
		return fmt.Errorf("Failed waiting for migration control connection on target: %w", err)
	}

	l.Info("Migration control connection established on target")

	defer l.Info("Migration channels disconnected on target")

	if c.push {
		defer c.disconnect()
	}

	stateConnFunc := func(ctx context.Context) (io.ReadWriteCloser, error) {
		conn := c.conns[api.SecretNameState]
		if conn == nil {
			return nil, fmt.Errorf("Migration target control connection not initialized")
		}

		wsConn, err := conn.WebsocketIO(ctx)
		if err != nil {
			return nil, fmt.Errorf("Failed getting migration target control connection: %w", err)
		}

		return wsConn, nil
	}

	filesystemConnFunc := func(ctx context.Context) (io.ReadWriteCloser, error) {
		conn := c.conns[api.SecretNameFilesystem]
		if conn == nil {
			return nil, fmt.Errorf("Migration target filesystem connection not initialized")
		}

		wsConn, err := conn.WebsocketIO(ctx)
		if err != nil {
			return nil, fmt.Errorf("Failed getting migration target filesystem connection: %w", err)
		}

		return wsConn, nil
	}

	err = c.instance.MigrateReceive(instance.MigrateReceiveArgs{
		MigrateArgs: instance.MigrateArgs{
			ControlSend:    c.send,
			ControlReceive: c.recv,
			StateConn:      stateConnFunc,
			FilesystemConn: filesystemConnFunc,
			Snapshots:      !c.instanceOnly,
			Live:           c.live,
			Disconnect: func() {
				for connName, conn := range c.conns {
					if connName != api.SecretNameControl {
						conn.Close()
					}
				}
			},
			ClusterMoveSourceName: c.clusterMoveSourceName,
		},
		InstanceOperation: instOp,
		Refresh:           c.refresh,
	})
	if err != nil {
		l.Error("Failed migration on target", logger.Ctx{"err": err})

		errMsg := fmt.Errorf("Failed migration on target: %w", err)
		c.sendControl(errMsg)
		return errMsg
	}

	return nil
}
