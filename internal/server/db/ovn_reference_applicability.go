//go:build linux && cgo && !agent

package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/version"
)

func (c *ClusterTx) ovnReferenceApplicability(ctx context.Context) (string, string, error) {
	var behind int
	err := c.tx.QueryRowContext(ctx, `SELECT count(*) FROM nodes WHERE schema<>? OR api_extensions<>?`, cluster.SchemaVersion, version.APIExtensionsCount()).Scan(&behind)
	if err != nil {
		return "", "", err
	}

	if behind != 0 {
		return "", "", errors.New("OVN reference applicability requires matching cluster member versions")
	}

	var state, root string
	err = c.tx.QueryRowContext(ctx, `SELECT state,nb_root FROM ovn_reference_applicability WHERE id=1`).Scan(&state, &root)
	if err != nil {
		return "", "", fmt.Errorf("OVN reference applicability is unknown: %w", err)
	}

	switch state {
	case "never", "unknown", "activating":
		if root != "" {
			return "", "", errors.New("Malformed inactive OVN reference root")
		}

	case "active":
		id, e := uuid.Parse(root)
		if e != nil || id == uuid.Nil {
			return "", "", errors.New("Malformed active OVN reference root")
		}

	default:
		return "", "", errors.New("Malformed OVN reference applicability")
	}

	return state, root, nil
}

// OVNReferencesNeverActivated is only a durable fresh-cluster authority, not a catalog inference.
func (c *ClusterTx) OVNReferencesNeverActivated(ctx context.Context) (bool, error) {
	state, _, err := c.ovnReferenceApplicability(ctx)
	return state == "never", err
}

// OVNReferencesInapplicable reports that no OVN reference can exist: OVN was never activated, or no
// OVN backend root was ever bound and the cluster has no OVN network beyond targeted definitions that
// were never created and have no operation in progress (such as an upgraded or touched cluster that
// does not use OVN). Once a root is bound, references are always checked against it. Callers decide
// it in the transaction that publishes their change, so a concurrently created OVN network, which
// holds its create operation, cannot be missed.
func (c *ClusterTx) OVNReferencesInapplicable(ctx context.Context) (bool, error) {
	state, _, err := c.ovnReferenceApplicability(ctx)
	if err != nil || state == "never" || state == "active" {
		return state == "never", err
	}

	var networks int
	err = c.tx.QueryRowContext(ctx, `SELECT count(*) FROM networks n WHERE n.type=? AND (n.state<>? OR EXISTS (SELECT 1 FROM networks_ovn_operations op WHERE op.project_id=n.project_id AND op.name=n.name))`, NetworkTypeOVN, networkPending).Scan(&networks)
	if err != nil {
		return false, err
	}

	return networks == 0, nil
}

// OVNReferenceRoot returns the Northbound root the cluster has bound, or "" before activation.
func (c *ClusterTx) OVNReferenceRoot(ctx context.Context) (string, error) {
	state, root, err := c.ovnReferenceApplicability(ctx)
	if err != nil || state != "active" {
		return "", err
	}

	return root, nil
}

// BeginOVNReferenceActivation commits before connection construction or any accepted OVN effects.
func (c *ClusterTx) BeginOVNReferenceActivation(ctx context.Context) error {
	state, _, err := c.ovnReferenceApplicability(ctx)
	if err != nil {
		return err
	}

	if state != "never" {
		return nil
	}

	_, err = c.tx.ExecContext(ctx, `UPDATE ovn_reference_applicability SET state='activating' WHERE id=1 AND state='never'`)
	return err
}

// BindOVNReferenceRoot never resets to never; a replacement root needs a cluster without OVN definitions or work.
func (c *ClusterTx) BindOVNReferenceRoot(ctx context.Context, root string) error {
	id, err := uuid.Parse(root)
	if err != nil || id == uuid.Nil {
		return errors.New("Invalid OVN reference NB root")
	}

	state, previous, err := c.ovnReferenceApplicability(ctx)
	if err != nil {
		return err
	}

	if state == "active" {
		if previous == root {
			return nil
		}

		// A replacement root is only accepted once nothing can still refer to the original one.
		blocked, err := c.OVNBackendRebindBlocked(ctx)
		if err != nil {
			return err
		}

		if blocked {
			return errors.New("OVN reference root changed; original active root remains authoritative")
		}

		_, err = c.tx.ExecContext(ctx, `UPDATE ovn_reference_applicability SET nb_root=? WHERE id=1 AND state='active' AND nb_root=?`, root, previous)
		return err
	}

	if state == "never" {
		return errors.New("OVN reference activation intent must commit before NB root binding")
	}

	_, err = c.tx.ExecContext(ctx, `UPDATE ovn_reference_applicability SET state='active',nb_root=? WHERE id=1 AND state IN ('unknown','activating')`, root)
	return err
}

// CheckOVNReferencePublication belongs in the same transaction as the actual catalog mutation.
func (c *ClusterTx) CheckOVNReferencePublication(ctx context.Context, root string) error {
	state, current, err := c.ovnReferenceApplicability(ctx)
	if err != nil {
		return err
	}

	if root == "" {
		inapplicable, err := c.OVNReferencesInapplicable(ctx)
		if err != nil {
			return err
		}

		if !inapplicable {
			return errors.New("OVN activated during resource publication; retry with the original backend")
		}

		return nil
	}

	if state != "active" || root != current {
		return errors.New("OVN original root applicability changed during resource publication")
	}

	return nil
}
