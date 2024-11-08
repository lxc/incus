package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	internalInstance "github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/db"
	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/internal/server/task"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
)

// ServerScore represents server score taken into account during load balancing.
type ServerScore struct {
	NodeInfo  db.NodeInfo
	Resources *api.Resources
	Score     uint8
}

// ServerUsage represents current server load.
type ServerUsage struct {
	MemoryUsage uint64
	MemoryTotal uint64
	CPUUsage    float64
	CPUTotal    uint64
}

// sortAndGroupByArch sorts servers by its score and groups them by cpu architecture.
func sortAndGroupByArch(servers []*ServerScore) map[string][]*ServerScore {
	sort.Slice(servers, func(i, j int) bool {
		return servers[i].Score > servers[j].Score
	})

	result := make(map[string][]*ServerScore)
	for _, s := range servers {
		arch := s.Resources.CPU.Architecture
		_, ok := result[arch]
		if !ok {
			result[arch] = []*ServerScore{}
		}

		result[arch] = append(result[arch], s)
	}

	return result
}

// calculateScore calculates score for single server.
func calculateScore(su *ServerUsage, au *ServerUsage) uint8 {
	memoryUsage := su.MemoryUsage
	memoryTotal := su.MemoryTotal
	cpuUsage := su.CPUUsage
	cpuTotal := su.CPUTotal

	if au != nil {
		memoryUsage += au.MemoryUsage
		memoryTotal += au.MemoryTotal
		cpuUsage += au.CPUUsage
		cpuTotal += au.CPUTotal
	}

	memoryScore := uint8(float64(memoryUsage) * 100 / float64(memoryTotal))
	cpuScore := uint8((cpuUsage * 100) / float64(cpuTotal))

	return (memoryScore + cpuScore) / 2
}

// calculateServersScore calculates score based on memory and CPU usage for servers in cluster.
func calculateServersScore(s *state.State, members []db.NodeInfo) (map[string][]*ServerScore, error) {
	scores := []*ServerScore{}
	for _, member := range members {
		clusterMember, err := cluster.Connect(member.Address, s.Endpoints.NetworkCert(), s.ServerCert(), nil, true)
		if err != nil {
			return nil, fmt.Errorf("Failed to connect to cluster member: %w", err)
		}

		res, err := clusterMember.GetServerResources()
		if err != nil {
			return nil, fmt.Errorf("Failed to get resources for cluster member: %w", err)
		}

		su := &ServerUsage{
			MemoryUsage: res.Memory.Used,
			MemoryTotal: res.Memory.Total,
			CPUUsage:    res.Load.Average1Min,
			CPUTotal:    res.CPU.Total,
		}

		serverScore := calculateScore(su, nil)
		scores = append(scores, &ServerScore{NodeInfo: member, Resources: res, Score: serverScore})
	}

	return sortAndGroupByArch(scores), nil
}

// clusterRebalanceServers is responsible for instances migration from most to less busy server.
func clusterRebalanceServers(ctx context.Context, s *state.State, srcServer *ServerScore, dstServer *ServerScore, maxToMigrate int64) (int64, error) {
	numOfMigrated := int64(0)

	// Keep track of project restrictions.
	projectStatuses := map[string]bool{}

	// Get a list of migratable instances.
	var dbInstances []dbCluster.Instance
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Get the instance list.
		instType := instancetype.VM
		dbInstances, err = dbCluster.GetInstances(ctx, tx.Tx(), dbCluster.InstanceFilter{Node: &srcServer.NodeInfo.Name, Type: &instType})
		if err != nil {
			return fmt.Errorf("Failed to get instances: %w", err)
		}

		// Check project restrictions.
		for _, dbInst := range dbInstances {
			_, ok := projectStatuses[dbInst.Project]
			if ok {
				continue
			}

			dbProject, err := dbCluster.GetProject(ctx, tx.Tx(), dbInst.Project)
			if err != nil {
				return fmt.Errorf("Failed to get project: %w", err)
			}

			apiProject, err := dbProject.ToAPI(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed to load project: %w", err)
			}

			_, _, err = project.CheckTarget(ctx, s.Authorizer, nil, tx, apiProject, dstServer.NodeInfo.Name, []db.NodeInfo{dstServer.NodeInfo})
			projectStatuses[dbInst.Project] = err == nil
		}

		return nil
	})
	if err != nil {
		return -1, fmt.Errorf("Failed to get instances: %w", err)
	}

	// Filter for instances that can be live migrated to the new target.
	var instances []instance.Instance
	for _, dbInst := range dbInstances {
		if !projectStatuses[dbInst.Project] {
			// Project restrictions prevent moving to that target.
			continue
		}

		inst, err := instance.LoadByProjectAndName(s, dbInst.Project, dbInst.Name)
		if err != nil {
			return -1, fmt.Errorf("Failed to load instance: %w", err)
		}

		// Do not allow to migrate instance which doesn't support live migration.
		if inst.CanMigrate() != "live-migrate" {
			continue
		}

		// Check if instance is ready for next migration.
		lastMove := inst.LocalConfig()["volatile.rebalance.last_move"]
		cooldown := s.GlobalConfig.ClusterRebalanceCooldown()
		if lastMove != "" {
			v, err := strconv.ParseInt(lastMove, 10, 64)
			if err != nil {
				return -1, fmt.Errorf("Failed to parse last_move value: %w", err)
			}

			expiry, err := internalInstance.GetExpiry(time.Unix(v, 0), cooldown)
			if err != nil {
				return -1, fmt.Errorf("Failed to calculate expiration for cooldown time: %w", err)
			}

			if time.Now().Before(expiry) {
				continue
			}
		}

		instances = append(instances, inst)
	}

	// Calculate current and target scores.
	targetScore := (srcServer.Score + dstServer.Score) / 2
	currentScore := dstServer.Score
	targetServerUsage := &ServerUsage{
		MemoryUsage: dstServer.Resources.Memory.Used,
		MemoryTotal: dstServer.Resources.Memory.Total,
		CPUUsage:    dstServer.Resources.Load.Average1Min,
		CPUTotal:    dstServer.Resources.CPU.Total,
	}

	// Prepare the API client.
	srcNode, err := cluster.Connect(srcServer.NodeInfo.Address, s.Endpoints.NetworkCert(), s.ServerCert(), nil, true)
	if err != nil {
		return -1, fmt.Errorf("Failed to connect to cluster member: %w", err)
	}

	srcNode = srcNode.UseTarget(dstServer.NodeInfo.Name)

	for _, inst := range instances {
		if numOfMigrated >= maxToMigrate {
			// We're done moving instances for now.
			return numOfMigrated, nil
		}

		if currentScore >= targetScore {
			// We've balanced the load.
			return numOfMigrated, nil
		}

		// Calculate resource consumption.
		cpuUsage, memUsage, _, err := instance.ResourceUsage(inst.ExpandedConfig(), inst.ExpandedDevices().CloneNative(), api.InstanceType(inst.Type().String()))
		if err != nil {
			return -1, fmt.Errorf("Failed to establish instance resource usage: %w", err)
		}

		// Calculate impact of migration.
		additionalUsage := &ServerUsage{
			MemoryUsage: uint64(cpuUsage),
			CPUUsage:    float64(memUsage),
		}

		expectedScore := calculateScore(targetServerUsage, additionalUsage)
		if expectedScore >= targetScore {
			// Skip the instance as it would have too big an impact.
			continue
		}

		// Prepare for live migration.
		req := api.InstancePost{
			Migration: true,
			Live:      true,
		}

		migrationOp, err := srcNode.MigrateInstance(inst.Name(), req)
		if err != nil {
			return -1, fmt.Errorf("Migration API failure: %w", err)
		}

		err = migrationOp.Wait()
		if err != nil {
			return -1, fmt.Errorf("Failed to wait for migration to finish: %w", err)
		}

		// Record the migration in the instance volatile storage.
		err = inst.VolatileSet(map[string]string{"volatile.rebalance.last_move": strconv.FormatInt(time.Now().Unix(), 10)})
		if err != nil {
			return -1, err
		}

		// Update counters and scores.
		numOfMigrated += 1
		currentScore = expectedScore
		targetServerUsage.MemoryUsage += additionalUsage.MemoryUsage
		targetServerUsage.CPUUsage += additionalUsage.CPUUsage
	}

	return numOfMigrated, nil
}

// clusterRebalance performs cluster re-balancing.
func clusterRebalance(ctx context.Context, s *state.State, servers map[string][]*ServerScore) error {
	rebalanceThreshold := s.GlobalConfig.ClusterRebalanceThreshold()
	rebalanceBatch := s.GlobalConfig.ClusterRebalanceBatch()
	numOfMigrated := int64(0)

	for archName, v := range servers {
		if numOfMigrated >= rebalanceBatch {
			// Maximum number of instances already migrated in this run.
			continue
		}

		if len(v) < 2 {
			// Skip if there isn't at least 2 servers with specific arch.
			continue
		}

		if v[0].Score == 0 {
			// Don't migrate anything if most loaded isn't loaded.
			continue
		}

		leastBusyIndex := len(v) - 1
		percentageChange := int64(float64(v[0].Score-v[leastBusyIndex].Score) / float64(v[0].Score) * 100)
		logger.Debug("Automatic re-balancing", logger.Ctx{"Architecture": archName, "LeastBusy": v[leastBusyIndex].NodeInfo.Name, "LeastBusyScore": v[leastBusyIndex].Score, "MostBusy": v[0].NodeInfo.Name, "MostBusyScore": v[0].Score, "Difference": fmt.Sprintf("%d%%", percentageChange), "Threshold": fmt.Sprintf("%d%%", rebalanceThreshold)})

		if percentageChange < rebalanceThreshold {
			continue // Skip as threshold condition is not met.
		}

		n, err := clusterRebalanceServers(ctx, s, v[0], v[leastBusyIndex], rebalanceBatch-numOfMigrated)
		if err != nil {
			return fmt.Errorf("Failed to rebalance cluster: %w", err)
		}

		numOfMigrated += n
	}

	return nil
}

func autoRebalanceCluster(ctx context.Context, d *Daemon) error {
	s := d.State()

	// Confirm we should run the rebalance.
	leader, err := s.Cluster.LeaderAddress()
	if err != nil {
		if errors.Is(err, cluster.ErrNodeIsNotClustered) {
			// Not clustered.
			return nil
		}

		return fmt.Errorf("Failed to get leader cluster member address: %w", err)
	}

	if s.LocalConfig.ClusterAddress() != leader {
		// Not the leader.
		return nil
	}

	// Get all online members
	var onlineMembers []db.NodeInfo
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		members, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		onlineMembers, err = tx.GetCandidateMembers(ctx, members, nil, "", nil, s.GlobalConfig.OfflineThreshold())
		if err != nil {
			return fmt.Errorf("Failed getting online cluster members: %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed getting cluster members: %w", err)
	}

	servers, err := calculateServersScore(s, onlineMembers)
	if err != nil {
		return fmt.Errorf("Failed calculating servers score: %w", err)
	}

	err = clusterRebalance(ctx, s, servers)
	if err != nil {
		return fmt.Errorf("Failed rebalancing cluster: %w", err)
	}

	return nil
}

func autoRebalanceClusterTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := d.State()

		// Check that we should run now.
		interval := s.GlobalConfig.ClusterRebalanceInterval()
		if interval <= 0 {
			// Re-balance is disabled.
			return
		}

		now := time.Now()
		elapsed := int64(math.Round(now.Sub(s.StartTime).Minutes()))
		if elapsed%interval != 0 {
			// It's not time for a re-balance.
			return
		}

		// Run the rebalance.
		err := autoRebalanceCluster(ctx, d)
		if err != nil {
			logger.Error("Failed during cluster auto rebalancing", logger.Ctx{"err": err})
		}
	}

	return f, task.Every(time.Minute)
}
