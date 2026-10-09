package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/internal/server/response"
	"github.com/lxc/incus/v7/shared/api"
)

// The marker can only be installed by the actual authenticated staged effect handler.
type ovnNICMigrationRequestKey struct{}

func ovnNICMigrationTargetRequest(r *http.Request, req api.InstancesPost) (string, error) {
	protocol, validProtocol := r.Context().Value(request.CtxProtocol).(string)
	if !validProtocol || protocol != "cluster" || !isClusterNotification(r) {
		return "", errors.New("OVN staged target requires an authenticated cluster notification")
	}

	if req.Name == "" || req.Name != r.PathValue("name") || req.Source.Source != req.Name || req.Source.Type != "migration" || req.Source.Mode != "pull" || !req.Source.Live || req.Source.Refresh || req.Config["volatile.uuid"] == "" {
		return "", errors.New("OVN staged target requires the original same-name live pull request")
	}

	return ovnNICMigrationOperationFromURL(req.Source.Operation)
}

func ovnNICMigrationValidateTarget(ctx context.Context, tx *db.ClusterTx, operation, projectName string, req api.InstancesPost) error {
	u, err := url.Parse(req.Source.Operation)
	if err != nil {
		return err
	}

	source, err := tx.GetNodeByAddress(ctx, u.Host)
	if err != nil {
		return err
	}

	var id int
	var identity string
	err = tx.Tx().QueryRowContext(ctx, `SELECT i.id,v.value FROM instances i JOIN projects p ON p.id=i.project_id JOIN instances_config v ON v.instance_id=i.id AND v.key='volatile.uuid' WHERE p.name=? AND i.name=?`, projectName, req.Name).Scan(&id, &identity)
	if err != nil {
		return err
	}

	if identity != req.Config["volatile.uuid"] {
		return errors.New("Staged target request instance UUID changed")
	}

	rows, err := tx.Tx().QueryContext(ctx, "SELECT device_name FROM networks_ovn_nic_migration_devices WHERE operation=?", operation)
	if err != nil {
		return err
	}

	var devices []string
	for rows.Next() {
		var name string
		err = rows.Scan(&name)
		if err != nil {
			break
		}

		devices = append(devices, name)
	}

	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}

	if len(devices) == 0 {
		return errors.New("Staged target has no original NIC authorization")
	}

	for _, name := range devices {
		m, err := tx.OVNNICMigrationDevice(ctx, operation, name)
		if err != nil {
			return err
		}

		if m.SourceNodeID != source.ID {
			return errors.New("Staged target request source member changed")
		}

		err = tx.EnsureOVNNICMigrationStart(ctx, operation, id, identity, name, 0, nil)
		if err != nil {
			return err
		}

		var original struct{ HostVolatile map[string]string }
		err = json.Unmarshal([]byte(m.Source.Payload), &original)
		if err != nil {
			return err
		}

		for _, key := range db.OVNNICStopVolatileKeys() {
			if req.Config["volatile."+name+"."+key] != original.HostVolatile[key] {
				return fmt.Errorf("Staged target request original device %q allocation changed", name)
			}
		}
	}

	return nil
}

func instanceOVNMigrationPost(d *Daemon, r *http.Request) response.Response {
	var req api.InstancesPost
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	operation, err := ovnNICMigrationTargetRequest(r, req)
	if err != nil {
		return response.Forbidden(err)
	}

	err = d.State().DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return ovnNICMigrationValidateTarget(ctx, tx, operation, request.ProjectParam(r), req)
	})
	if err != nil {
		return response.SmartError(err)
	}

	data, err := json.Marshal(req)
	if err != nil {
		return response.InternalError(err)
	}

	r = r.WithContext(context.WithValue(r.Context(), ovnNICMigrationRequestKey{}, operation))
	r.Body = io.NopCloser(bytes.NewReader(data))
	return instancesPost(d, r)
}

// RawOperation adds /1.0 and retains the client's project and target attributes.
func ovnNICMigrationCreateTarget(target interface {
	CreateInstance(api.InstancesPost) (incus.Operation, error)
	RawOperation(string, string, any, string) (incus.Operation, string, error)
}, operation string, req api.InstancesPost,
) (incus.Operation, error) {
	if operation == "" {
		return target.CreateInstance(req)
	}

	actual, err := ovnNICMigrationOperationFromURL(req.Source.Operation)
	if err != nil {
		return nil, err
	}

	if actual != operation {
		return nil, errors.New("Staged target dispatch source operation changed")
	}

	op, _, err := target.RawOperation(http.MethodPost, "/instances/"+url.PathEscape(req.Name)+"/ovn-migration", req, "")
	return op, err
}
