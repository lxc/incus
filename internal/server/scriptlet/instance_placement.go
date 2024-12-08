package scriptlet

import (
	"context"
	"fmt"

	"go.starlark.net/starlark"

	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/db"
	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	internalInstance "github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/resources"
	scriptletLoad "github.com/lxc/incus/v6/internal/server/scriptlet/load"
	"github.com/lxc/incus/v6/internal/server/scriptlet/log"
	"github.com/lxc/incus/v6/internal/server/scriptlet/marshal"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/api"
	apiScriptlet "github.com/lxc/incus/v6/shared/api/scriptlet"
	"github.com/lxc/incus/v6/shared/logger"
)

// InstancePlacementRun runs the instance placement scriptlet and returns the chosen cluster member target.
func InstancePlacementRun(ctx context.Context, l logger.Logger, s *state.State, req *apiScriptlet.InstancePlacement, candidateMembers []db.NodeInfo, leaderAddress string) (*db.NodeInfo, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	logFunc := log.CreateLogger(l, "Instance placement scriptlet")

	var targetMember *db.NodeInfo

	setTargetFunc := func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var memberName string

		err := starlark.UnpackArgs(b.Name(), args, kwargs, "member_name", &memberName)
		if err != nil {
			return nil, err
		}

		for i := range candidateMembers {
			if candidateMembers[i].Name == memberName {
				targetMember = &candidateMembers[i]
				break
			}
		}

		if targetMember == nil {
			l.Error("Instance placement scriptlet set invalid member target", logger.Ctx{"member": memberName})
			return starlark.String("Invalid member name"), fmt.Errorf("Invalid member name: %s", memberName)
		}

		l.Info("Instance placement scriptlet set member target", logger.Ctx{"member": targetMember.Name})

		return starlark.None, nil
	}

	getClusterMemberResourcesFunc := func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var memberName string

		err := starlark.UnpackArgs(b.Name(), args, kwargs, "member_name", &memberName)
		if err != nil {
			return nil, err
		}

		var res *api.Resources

		// Get the local resource usage.
		if memberName == s.ServerName {
			res, err = resources.GetResources()
			if err != nil {
				return nil, err
			}
		} else {
			// Get remote member resource usage.
			var targetMember *db.NodeInfo
			for i := range candidateMembers {
				if candidateMembers[i].Name == memberName {
					targetMember = &candidateMembers[i]
					break
				}
			}

			if targetMember == nil {
				return starlark.String("Invalid member name"), nil
			}

			client, err := cluster.Connect(targetMember.Address, s.Endpoints.NetworkCert(), s.ServerCert(), nil, true)
			if err != nil {
				return nil, err
			}

			res, err = client.GetServerResources()
			if err != nil {
				return nil, err
			}
		}

		rv, err := marshal.StarlarkMarshal(res)
		if err != nil {
			return nil, fmt.Errorf("Marshalling cluster member resources for %q failed: %w", memberName, err)
		}

		return rv, nil
	}

	getClusterMemberStateFunc := func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var memberName string

		err := starlark.UnpackArgs(b.Name(), args, kwargs, "member_name", &memberName)
		if err != nil {
			return nil, err
		}

		var memberState *api.ClusterMemberState

		// Get the local resource usage.
		if memberName == s.ServerName {
			memberState, err = cluster.MemberState(ctx, s, memberName)
			if err != nil {
				return nil, err
			}
		} else {
			// Get remote member resource usage.
			var targetMember *db.NodeInfo
			for i := range candidateMembers {
				if candidateMembers[i].Name == memberName {
					targetMember = &candidateMembers[i]
					break
				}
			}

			if targetMember == nil {
				return starlark.String("Invalid member name"), nil
			}

			client, err := cluster.Connect(targetMember.Address, s.Endpoints.NetworkCert(), s.ServerCert(), nil, true)
			if err != nil {
				return nil, err
			}

			memberState, _, err = client.GetClusterMemberState(memberName)
			if err != nil {
				return nil, err
			}
		}

		rv, err := marshal.StarlarkMarshal(memberState)
		if err != nil {
			return nil, fmt.Errorf("Marshalling cluster member state for %q failed: %w", memberName, err)
		}

		return rv, nil
	}

	getInstanceResourcesFunc := func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var err error
		var res apiScriptlet.InstanceResources

		usageCPU, usageMemory, usageDisk, err := internalInstance.ResourceUsage(req.Config, req.Devices, req.Type)
		if err != nil {
			return nil, fmt.Errorf("Failed to calculate instance resource usage: %w", err)
		}

		res.CPUCores = uint64(usageCPU)
		res.MemorySize = uint64(usageMemory)
		res.RootDiskSize = uint64(usageDisk)

		rv, err := marshal.StarlarkMarshal(res)
		if err != nil {
			return nil, fmt.Errorf("Marshalling instance resources failed: %w", err)
		}

		return rv, nil
	}

	getInstancesFunc := func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var project string
		var location string

		err := starlark.UnpackArgs(b.Name(), args, kwargs, "project??", &project, "location??", &location)
		if err != nil {
			return nil, err
		}

		instanceList := []api.Instance{}

		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			var objects []dbCluster.Instance

			if project != "" || location != "" {
				// Prepare a filter.
				filter := dbCluster.InstanceFilter{}

				if project != "" {
					filter.Project = &project
				}

				if location != "" {
					filter.Node = &location
				}

				// Get instances based on Project and/or Location filters.
				objects, err = dbCluster.GetInstances(ctx, tx.Tx(), filter)
				if err != nil {
					return err
				}
			} else {
				// Get all instances.
				objects, err = dbCluster.GetInstances(ctx, tx.Tx())
				if err != nil {
					return err
				}
			}

			objectDevices, err := dbCluster.GetDevices(ctx, tx.Tx(), "instance")
			if err != nil {
				return err
			}

			// Convert the []Instances into []api.Instances.
			for _, obj := range objects {
				instance, err := obj.ToAPI(ctx, tx.Tx(), objectDevices, nil, nil)
				if err != nil {
					return err
				}

				instanceList = append(instanceList, *instance)
			}

			return nil
		})
		if err != nil {
			return nil, err
		}

		rv, err := marshal.StarlarkMarshal(instanceList)
		if err != nil {
			return nil, fmt.Errorf("Marshalling instances failed: %w", err)
		}

		return rv, nil
	}

	getInstancesCountFunc := func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var projectName string
		var locationName string
		var includePending bool

		err := starlark.UnpackArgs(b.Name(), args, kwargs, "project??", &projectName, "location??", &locationName, "pending??", &includePending)
		if err != nil {
			return nil, err
		}

		var count int

		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			count, err = tx.GetInstancesCount(ctx, projectName, locationName, includePending)
			return err
		})
		if err != nil {
			return nil, err
		}

		rv, err := marshal.StarlarkMarshal(count)
		if err != nil {
			return nil, fmt.Errorf("Marshalling instance count failed: %w", err)
		}

		return rv, nil
	}

	getClusterMembersFunc := func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var group string
		var allMembers []db.NodeInfo

		err := starlark.UnpackArgs(b.Name(), args, kwargs, "group??", &group)
		if err != nil {
			return nil, err
		}

		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			allMembers, err = tx.GetNodes(ctx)
			if err != nil {
				return err
			}

			allMembers, err = tx.GetCandidateMembers(ctx, allMembers, nil, group, nil, s.GlobalConfig.OfflineThreshold())
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, err
		}

		var raftNodes []db.RaftNode
		err = s.DB.Node.Transaction(ctx, func(ctx context.Context, tx *db.NodeTx) error {
			raftNodes, err = tx.GetRaftNodes(ctx)
			if err != nil {
				return fmt.Errorf("Failed loading RAFT nodes: %w", err)
			}

			return nil
		})
		if err != nil {
			return nil, err
		}

		allMembersInfo := make([]*api.ClusterMember, 0, len(allMembers))
		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			failureDomains, err := tx.GetFailureDomainsNames(ctx)
			if err != nil {
				return fmt.Errorf("Failed loading failure domains names: %w", err)
			}

			memberFailureDomains, err := tx.GetNodesFailureDomains(ctx)
			if err != nil {
				return fmt.Errorf("Failed loading member failure domains: %w", err)
			}

			maxVersion, err := tx.GetNodeMaxVersion(ctx)
			if err != nil {
				return fmt.Errorf("Failed getting max member version: %w", err)
			}

			args := db.NodeInfoArgs{
				LeaderAddress:        leaderAddress,
				FailureDomains:       failureDomains,
				MemberFailureDomains: memberFailureDomains,
				OfflineThreshold:     s.GlobalConfig.OfflineThreshold(),
				MaxMemberVersion:     maxVersion,
				RaftNodes:            raftNodes,
			}

			for i := range allMembers {
				candidateMemberInfo, err := allMembers[i].ToAPI(ctx, tx, args)
				if err != nil {
					return err
				}

				allMembersInfo = append(allMembersInfo, candidateMemberInfo)
			}

			return nil
		})
		if err != nil {
			return nil, err
		}

		rv, err := marshal.StarlarkMarshal(allMembersInfo)
		if err != nil {
			return nil, fmt.Errorf("Marshalling cluster members failed: %w", err)
		}

		return rv, nil
	}

	getProjectFunc := func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string

		err := starlark.UnpackArgs(b.Name(), args, kwargs, "name??", &name)
		if err != nil {
			return nil, err
		}

		var p *api.Project

		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			dbProject, err := dbCluster.GetProject(ctx, tx.Tx(), name)
			if err != nil {
				return err
			}

			p, err = dbProject.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, err
		}

		rv, err := marshal.StarlarkMarshal(p)
		if err != nil {
			return nil, fmt.Errorf("Marshalling project failed: %w", err)
		}

		return rv, nil
	}

	var err error
	var raftNodes []db.RaftNode
	err = s.DB.Node.Transaction(ctx, func(ctx context.Context, tx *db.NodeTx) error {
		raftNodes, err = tx.GetRaftNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading RAFT nodes: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	candidateMembersInfo := make([]*api.ClusterMember, 0, len(candidateMembers))
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		failureDomains, err := tx.GetFailureDomainsNames(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading failure domains names: %w", err)
		}

		memberFailureDomains, err := tx.GetNodesFailureDomains(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading member failure domains: %w", err)
		}

		maxVersion, err := tx.GetNodeMaxVersion(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting max member version: %w", err)
		}

		args := db.NodeInfoArgs{
			LeaderAddress:        leaderAddress,
			FailureDomains:       failureDomains,
			MemberFailureDomains: memberFailureDomains,
			OfflineThreshold:     s.GlobalConfig.OfflineThreshold(),
			MaxMemberVersion:     maxVersion,
			RaftNodes:            raftNodes,
		}

		for i := range candidateMembers {
			candidateMemberInfo, err := candidateMembers[i].ToAPI(ctx, tx, args)
			if err != nil {
				return err
			}

			candidateMembersInfo = append(candidateMembersInfo, candidateMemberInfo)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Remember to match the entries in scriptletLoad.InstancePlacementCompile() with this list so Starlark can
	// perform compile time validation of functions used.
	env := starlark.StringDict{
		"log_info":                     starlark.NewBuiltin("log_info", logFunc),
		"log_warn":                     starlark.NewBuiltin("log_warn", logFunc),
		"log_error":                    starlark.NewBuiltin("log_error", logFunc),
		"set_target":                   starlark.NewBuiltin("set_target", setTargetFunc),
		"get_cluster_member_resources": starlark.NewBuiltin("get_cluster_member_resources", getClusterMemberResourcesFunc),
		"get_cluster_member_state":     starlark.NewBuiltin("get_cluster_member_state", getClusterMemberStateFunc),
		"get_instance_resources":       starlark.NewBuiltin("get_instance_resources", getInstanceResourcesFunc),
		"get_instances":                starlark.NewBuiltin("get_instances", getInstancesFunc),
		"get_instances_count":          starlark.NewBuiltin("get_instances_count", getInstancesCountFunc),
		"get_cluster_members":          starlark.NewBuiltin("get_cluster_members", getClusterMembersFunc),
		"get_project":                  starlark.NewBuiltin("get_project", getProjectFunc),
	}

	prog, thread, err := scriptletLoad.InstancePlacementProgram()
	if err != nil {
		return nil, err
	}

	go func() {
		<-ctx.Done()
		thread.Cancel("Request finished")
	}()

	globals, err := prog.Init(thread, env)
	if err != nil {
		return nil, fmt.Errorf("Failed initializing: %w", err)
	}

	globals.Freeze()

	// Retrieve a global variable from starlark environment.
	instancePlacement := globals["instance_placement"]
	if instancePlacement == nil {
		return nil, fmt.Errorf("Scriptlet missing instance_placement function")
	}

	rv, err := marshal.StarlarkMarshal(req)
	if err != nil {
		return nil, fmt.Errorf("Marshalling request failed: %w", err)
	}

	candidateMembersv, err := marshal.StarlarkMarshal(candidateMembersInfo)
	if err != nil {
		return nil, fmt.Errorf("Marshalling candidate members failed: %w", err)
	}

	// Call starlark function from Go.
	v, err := starlark.Call(thread, instancePlacement, nil, []starlark.Tuple{
		{
			starlark.String("request"),
			rv,
		}, {
			starlark.String("candidate_members"),
			candidateMembersv,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("Failed to run: %w", err)
	}

	if v.Type() != "NoneType" {
		return nil, fmt.Errorf("Failed with unexpected return value: %v", v)
	}

	return targetMember, nil
}
