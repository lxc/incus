package network

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/internal/server/cluster"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
)

// OVNNotify identifies each bounded notification by network ID and the active origin's operation token.
func OVNNotify(s *state.State, n interface {
	ID() int64
	Project() string
	Name() string
	OVNOperationToken() string
}, client incus.InstanceServer, method string, path []string, payload any,
) (err error) {
	var token string
	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		token, err = tx.OVNNetworkOperationToken(ctx, n.Project(), n.Name())
		return err
	})
	if err != nil {
		return err
	}

	if token == "" || token != n.OVNOperationToken() {
		return fmt.Errorf("OVN network notification has no active origin operation")
	}

	receipt := uuid.NewString()
	defer func() { err = errors.Join(err, waitOVNNotification(s, receipt)) }()
	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.AddOVNNotification(ctx, receipt, token)
	})
	if err != nil {
		return err
	}
	// A timeout is not an acknowledgement. Keep the origin reserved until entered work drains.
	u := api.NewURL().Path(path...).Project(n.Project()).WithQuery("ovn-network-id", strconv.FormatInt(n.ID(), 10)).WithQuery("ovn-operation", token).WithQuery("ovn-notification", receipt)
	ctx, cancel := context.WithTimeout(s.ShutdownCtx, time.Minute)
	defer cancel()
	contextClient, ok := client.(interface {
		WithContext(context.Context) incus.InstanceServer
	})
	if !ok {
		return fmt.Errorf("OVN notification client does not support request deadlines")
	}

	_, _, err = contextClient.WithContext(ctx).RawQuery(method, u.String(), payload, "")
	return err
}

// ValidateOVNNotification rejects expired notifications and requests referring to a replaced network name.
func ValidateOVNNotification(s *state.State, n Network, token string, networkID string, operation string) error {
	if token == "" || networkID != strconv.FormatInt(n.ID(), 10) {
		return api.StatusErrorf(http.StatusConflict, "Expired or mismatched OVN network notification")
	}

	return s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		current, err := tx.OVNNetworkOperationToken(ctx, n.Project(), n.Name())
		if err != nil {
			return err
		}

		kind, err := tx.OVNNetworkOperation(ctx, n.Project(), n.Name())
		if err != nil {
			return err
		}

		if current != token || kind != operation {
			return api.StatusErrorf(http.StatusConflict, "OVN network notification's operation has ended")
		}

		return nil
	})
}

// OVNDeleteNotifier excludes only members proven never initialized under the new local-state contract.
func OVNDeleteNotifier(s *state.State, n Network) (cluster.Notifier, []int64, error) {
	var selected []int64
	var skipped []int64
	err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		id, _, nodes, err := tx.GetNetworkInAnyState(ctx, n.Project(), n.Name())
		if err != nil {
			return err
		}

		operation, err := tx.OVNNetworkOperation(ctx, n.Project(), n.Name())
		if err != nil {
			return err
		}

		if id != n.ID() || operation != "delete" {
			return api.StatusErrorf(http.StatusConflict, "OVN deletion has no active origin guard")
		}

		// Cleanup authority must be settled on every member before deletion takes any effect.
		err = tx.EnsureOVNNetworkCleanupFree(ctx, n.ID())
		if err != nil {
			return err
		}

		enabled, err := tx.OVNLocalInitializationEnabled(ctx, n.ID())
		if err != nil {
			return err
		}

		members, err := tx.GetNodes(ctx)
		if err != nil {
			return err
		}

		for _, member := range members {
			node, exists := nodes[member.ID]
			prepared := exists && db.NetworkStateToAPIStatus(node.State) == api.NetworkStatusPrepared && member.State == db.ClusterMemberStateEvacuated
			if prepared || (enabled && exists && db.NetworkStateToAPIStatus(node.State) == api.NetworkStatusPending) {
				skipped = append(skipped, member.ID)
			} else {
				selected = append(selected, member.ID)
			}
		}

		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	notifier, err := cluster.NewNotifierForMembers(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAll, selected)
	return notifier, skipped, err
}

// AcceptOVNNotification pins the exact operation until the recipient finishes its driver work.
func AcceptOVNNotification(s *state.State, n Network, token string, networkID string, operation string, receipt string) (func() error, error) {
	err := ValidateOVNNotification(s, n, token, networkID, operation)
	if err != nil {
		return nil, err
	}

	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.AcceptOVNNotification(ctx, receipt, token)
	})
	if err != nil {
		return nil, err
	}

	return func() error {
		for {
			err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.FinishOVNNotification(ctx, receipt)
			})
			if err == nil {
				return nil
			}

			select {
			case <-s.ShutdownCtx.Done():
				return err
			case <-time.After(time.Second):
			}
		}
	}, nil
}

func waitOVNNotification(s *state.State, receipt string) error {
	lastWarning := time.Time{}
	for {
		var active bool
		err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error
			active, err = tx.CancelOVNNotification(ctx, receipt)
			return err
		})
		if err == nil && !active {
			return nil
		}

		if time.Since(lastWarning) >= 30*time.Second {
			logger.Warn("Waiting for accepted OVN notification before releasing operation", logger.Ctx{"notification": receipt, "err": err})
			lastWarning = time.Now()
		}

		select {
		case <-s.ShutdownCtx.Done():
			return fmt.Errorf("OVN notification remains unacknowledged during shutdown: %w", s.ShutdownCtx.Err())
		case <-time.After(time.Second):
		}
	}
}

// OVNNotifier skips acknowledged maintenance members while preserving the driver's availability policy.
func OVNNotifier(s *state.State, n interface {
	ID() int64
	Project() string
	Name() string
	OVNOperationToken() string
}, policy cluster.NotifierPolicy,
) (cluster.Notifier, error) {
	return ovnNotifier(s, n, policy, false)
}

func ovnNotifier(s *state.State, n interface {
	ID() int64
	Project() string
	Name() string
	OVNOperationToken() string
}, policy cluster.NotifierPolicy, metadataOnly bool,
) (cluster.Notifier, error) {
	var selected []int64
	err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		id, _, nodes, err := tx.GetNetworkInAnyState(ctx, n.Project(), n.Name())
		if err != nil {
			return err
		}

		token, err := tx.OVNNetworkOperationToken(ctx, n.Project(), n.Name())
		if err != nil {
			return err
		}

		if id != n.ID() || token == "" || token != n.OVNOperationToken() {
			return api.StatusErrorf(http.StatusConflict, "OVN notifier no longer owns the network operation")
		}

		enabled, err := tx.OVNLocalInitializationEnabled(ctx, n.ID())
		if err != nil {
			return err
		}

		members, err := tx.GetNodes(ctx)
		if err != nil {
			return err
		}

		var localAddress string
		if metadataOnly && policy == cluster.NotifyAlive {
			localAddress = s.LocalConfig.ClusterAddress()
		}

		selected, policy = ovnNotifierSelection(policy, metadataOnly, nodes, members, enabled, localAddress)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return cluster.NewNotifierForMembers(s, s.Endpoints.NetworkCert(), s.ServerCert(), policy, selected)
}

// ovnNotifierSelection classifies metadata updates using the same snapshot as member selection.
func ovnNotifierSelection(policy cluster.NotifierPolicy, metadataOnly bool, nodes map[int64]db.NetworkNode, members []db.NodeInfo, enabled bool, localAddress string) ([]int64, cluster.NotifierPolicy) {
	var selected []int64
	for _, member := range members {
		node, exists := nodes[member.ID]
		prepared := exists && db.NetworkStateToAPIStatus(node.State) == api.NetworkStatusPrepared
		untouchedMaintenance := enabled && exists && db.NetworkStateToAPIStatus(node.State) == api.NetworkStatusPending && member.State != db.ClusterMemberStateCreated
		if prepared || untouchedMaintenance {
			continue
		}

		selected = append(selected, member.ID)
		if metadataOnly && policy == cluster.NotifyAlive && member.Address != localAddress && member.Address != "0.0.0.0" {
			if !exists || node.ID != member.ID || db.NetworkStateToAPIStatus(node.State) != api.NetworkStatusCreated {
				policy = cluster.NotifyAll
			}
		}
	}

	return selected, policy
}

// OVNRevert avoids shared rollback during shutdown while accepted recipients still own the old operation.
func OVNRevert(s *state.State, n interface {
	OVNOperationToken() string
	Type() string
}, rollback func(),
) {
	ovnNet, ok := n.(*ovn)
	if ok && ovnNet.ovnOperationUncertain {
		logger.Warn("Retaining OVN peering state after an uncertain interconnect transaction")
		return
	}

	if n.Type() != "ovn" || s.ShutdownCtx.Err() == nil {
		rollback()
		return
	}

	token := n.OVNOperationToken()
	if token == "" {
		rollback()
		return
	}

	var pending bool
	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		pending, err = tx.OVNNotificationsPending(ctx, token)
		return err
	})
	if err != nil || pending {
		logger.Warn("Retaining OVN operation without rollback until outstanding notifications finish", logger.Ctx{"operation": token, "err": err})
		return
	}

	rollback()
}
