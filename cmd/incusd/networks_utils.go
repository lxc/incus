package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/lxc/incus/v7/internal/server/cluster"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/network"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/internal/server/response"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
)

var networkOVNChassis = struct {
	sync.Mutex
	value     *bool
	running   bool
	dirty     bool
	last      time.Time
	failedIDs []int64
}{}

// networkUpdateOVNChassis gets called on heartbeats to check if OVN needs reconfiguring.
func networkUpdateOVNChassis(s *state.State, heartbeatData *cluster.APIHeartbeat, localAddress string) error {
	networkInitializeOVNMembers(s)

	// Check if we have at least one active OVN chassis.
	hasOVNChassis := false
	localOVNChassis := false
	for _, n := range heartbeatData.Members {
		if slices.Contains(n.Roles, string(db.ClusterRoleOVNChassis)) {
			if n.Address == localAddress {
				localOVNChassis = true
			}

			hasOVNChassis = true
		}
	}

	runChassis := !hasOVNChassis || localOVNChassis
	networkOVNChassis.Lock()
	defer networkOVNChassis.Unlock()
	if s.DB.Cluster.LocalNodeIsEvacuated() {
		networkOVNChassis.value = &runChassis
		return nil
	}

	if networkOVNChassis.running {
		if networkOVNChassis.value != nil && *networkOVNChassis.value != runChassis {
			networkOVNChassis.dirty = true
		}

		return nil
	}

	changed := networkOVNChassis.dirty || networkOVNChassis.value != nil && *networkOVNChassis.value != runChassis
	retry := len(networkOVNChassis.failedIDs) > 0 && time.Since(networkOVNChassis.last) >= time.Minute
	networkOVNChassis.value = &runChassis
	if changed || retry {
		onlyIDs := slices.Clone(networkOVNChassis.failedIDs)
		if changed {
			onlyIDs = nil
		}

		networkOVNChassis.running = true
		networkOVNChassis.dirty = false
		networkOVNChassis.last = time.Now()
		go func() {
			failedIDs, err := networkRestartOVN(s, onlyIDs...)
			if err != nil {
				logger.Error("Error restarting OVN networks", logger.Ctx{"err": err})
				if len(failedIDs) == 0 {
					failedIDs = []int64{-1} // Enumeration failed before any network was restarted.
				}
			}

			networkOVNChassis.Lock()
			networkOVNChassis.failedIDs = failedIDs
			networkOVNChassis.running = false
			networkOVNChassis.Unlock()
		}()
	}

	return nil
}

// networkLoadForOperation reloads OVN state after acquiring its member-local lifecycle lock.
func networkLoadForOperation(s *state.State, projectName string, networkName string, operation string, notification bool, requests ...*http.Request) (network.Network, func() error, error) {
	n, err := network.LoadByName(s, projectName, networkName)
	if err != nil {
		return nil, nil, err
	}

	if n.Type() != "ovn" {
		return n, func() error { return nil }, nil
	}

	unlock, err := network.LockOVNLifecycle(projectName, networkName)
	if operation == "" || notification {
		if unlock != nil {
			unlock()
		}

		lockCtx := s.ShutdownCtx
		if notification && len(requests) == 1 {
			ctx, cancel := context.WithTimeout(requests[0].Context(), 50*time.Second)
			defer cancel()
			lockCtx = ctx
		}

		if !notification && lockCtx.Err() != nil {
			lockCtx = context.Background()
		}

		unlock, err = network.WaitOVNLifecycle(lockCtx, projectName, networkName)
	}

	if err != nil {
		return nil, nil, err
	}

	release := func() error { return nil }
	var token string
	if operation != "" && !notification {
		release, token, err = networkOVNAcquireOperation(s, projectName, networkName, operation)
		if err != nil {
			unlock()
			return nil, nil, err
		}
	}

	fresh, err := network.LoadByName(s, projectName, networkName)
	if err != nil || fresh.ID() != n.ID() {
		_ = release()
		unlock()
		if err != nil {
			return nil, nil, err
		}

		return nil, nil, fmt.Errorf("Network changed while acquiring its lifecycle lock")
	}

	if notification {
		if len(requests) != 1 {
			_ = release()
			unlock()
			return nil, nil, fmt.Errorf("OVN notification is missing its request identity")
		}

		release, err = network.AcceptOVNNotification(s, fresh, request.QueryParam(requests[0], "ovn-operation"), request.QueryParam(requests[0], "ovn-network-id"), operation, request.QueryParam(requests[0], "ovn-notification"))
		if err != nil {
			unlock()
			return nil, nil, err
		}
	}

	if operation != "" {
		if notification {
			token = request.QueryParam(requests[0], "ovn-operation")
		}

		network.AuthorizeOVNInitialization(fresh, token)
	}

	return fresh, func() error {
		defer unlock()
		return release()
	}, nil
}

func networkOVNAcquireOperation(s *state.State, projectName string, networkName string, operation string) (func() error, string, error) {
	return network.AcquireOVNOperation(s, projectName, networkName, operation)
}

func networkOVNReleaseResponse(release func() error, result *response.Response) {
	err := release()
	if err != nil {
		if *result != nil && (*result).Code() >= http.StatusBadRequest {
			logger.Error("Failed releasing OVN network operation", logger.Ctx{"err": err})
			return
		}

		*result = response.SmartError(fmt.Errorf("Failed releasing OVN network operation: %w", err))
	}
}

var networkOVNInitialization = struct {
	sync.Mutex
	running bool
	last    time.Time
}{}

// networkInitializeOVNMembers attempts only additive initialization when cluster heartbeats resume.
func networkInitializeOVNMembers(s *state.State) {
	if s.ShutdownCtx.Err() != nil || s.DB.Cluster.LocalNodeIsEvacuated() {
		return
	}

	networkOVNInitialization.Lock()
	if networkOVNInitialization.running || time.Since(networkOVNInitialization.last) < time.Minute {
		networkOVNInitialization.Unlock()
		return
	}

	networkOVNInitialization.running = true
	networkOVNInitialization.last = time.Now()
	networkOVNInitialization.Unlock()

	go func() {
		defer func() {
			networkOVNInitialization.Lock()
			networkOVNInitialization.running = false
			networkOVNInitialization.Unlock()
		}()

		networks, err := network.LoadAllCreated(s.ShutdownCtx, s)
		if err != nil {
			logger.Warn("Failed loading returning-member OVN networks", logger.Ctx{"err": err})
			return
		}

		for _, n := range networks {
			if n == nil || !network.OVNNeedsInitialization(n) {
				continue
			}

			err := network.EnsureOVNReturning(n)
			if err != nil {
				logger.Warn("Failed initializing returning-member OVN network", logger.Ctx{"project": n.Project(), "name": n.Name(), "err": err})
			}
		}
	}()
}

// networkOVNMaintenance includes raw member rows from every global network state.
func networkOVNMaintenance(s *state.State, restore bool) error {
	var names map[string][]string
	err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		names, err = tx.GetNetworksAllProjects(ctx)
		return err
	})
	if err != nil {
		return err
	}

	if restore {
		for projectName, networkNames := range names {
			for _, name := range networkNames {
				n, err := network.LoadByName(s, projectName, name)
				if err != nil {
					if api.StatusErrorCheck(err, http.StatusNotFound) {
						// Deleted after listing; its deletion owned the local cleanup.
						continue
					}

					return err
				}

				err = network.ClearOVNRestoreReadiness(n)
				if err != nil {
					return err
				}
			}
		}
	}
	for projectName, networkNames := range names {
		for _, name := range networkNames {
			n, err := network.LoadByName(s, projectName, name)
			if err != nil {
				if api.StatusErrorCheck(err, http.StatusNotFound) {
					// Deleted after listing; its deletion owned the local cleanup.
					continue
				}

				return err
			}

			if n.Type() != "ovn" {
				continue
			}

			if restore {
				// Only globally created networks are started. An uncreated network returns to Stopped
				// here, as after a failed creation, so its create retry repairs it on this member.
				if networkOVNCreating(s, n) {
					continue
				}

				if n.Status() != api.NetworkStatusCreated {
					err = network.RestoreOVNLocalUncreated(n)
				} else {
					err = network.RestoreOVNLocal(n)
				}
			} else {
				err = network.PrepareOVNLocal(n)
			}

			if err != nil {
				if networkOVNMaintenanceDeleted(s, n.ID()) {
					// A concurrent deletion held the reservation and removed this exact network.
					continue
				}

				return fmt.Errorf("OVN network %q maintenance failed: %w", name, err)
			}
		}
	}

	return nil
}

// networkOVNCreating reports whether the exact network is still being created by its origin
// and was never initialized on this member. It stays Pending here; returning-member
// initialization handles it once the member is active again.
func networkOVNCreating(s *state.State, n network.Network) bool {
	if n.Status() != api.NetworkStatusErrored || n.LocalStatus() != api.NetworkStatusPending {
		return false
	}

	creating := false
	_ = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		id, _, _, err := tx.GetNetworkInAnyState(ctx, n.Project(), n.Name())
		if err != nil || id != n.ID() {
			return nil
		}

		operation, err := tx.OVNNetworkOperation(ctx, n.Project(), n.Name())
		creating = err == nil && operation == "create"
		return nil
	})

	return creating
}

// networkOVNMaintenanceDeleted reports whether the exact network ID no longer exists.
// A reused name has a new ID and is never mistaken for the deleted network.
func networkOVNMaintenanceDeleted(s *state.State, networkID int64) bool {
	deleted := false
	_ = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, _, err := tx.GetNetworkNameAndProjectWithID(ctx, int(networkID))
		deleted = api.StatusErrorCheck(err, http.StatusNotFound)
		return nil
	})

	return deleted
}
