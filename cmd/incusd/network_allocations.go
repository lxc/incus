package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"

	"github.com/lxc/incus/v7/internal/server/auth"
	clusterRequest "github.com/lxc/incus/v7/internal/server/cluster/request"
	"github.com/lxc/incus/v7/internal/server/db"
	dbCluster "github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/network"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/internal/server/response"
	"github.com/lxc/incus/v7/internal/version"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/util"
)

var networkAllocationsCmd = APIEndpoint{
	Path: "network-allocations",

	Get: APIEndpointAction{Handler: networkAllocationsGet, AccessHandler: allowAuthenticated},
}

// swagger:operation GET /1.0/network-allocations network-allocations network_allocations_get
//
//	Get the network allocations in use (`network`, `network-forward`, `network-load-balancer` and `instance`)
//
//	Returns a list of network allocations.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    x-example: default
//	  - in: query
//	    name: all-projects
//	    description: Retrieve entities from all projects
//	    type: boolean
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of network allocations used by a consuming entity
//	          items:
//	            $ref: "#/definitions/NetworkAllocations"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "409":
//	    $ref: "#/responses/Conflict"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkAllocationsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	_, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	allProjects := util.IsTrue(request.QueryParam(r, "all-projects"))

	// Get the projects to report on and the networks of every network project they rely on.
	projects := []api.Project{*reqProject}
	projectNetworks := map[string][]string{}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		if allProjects {
			dbProjects, err := dbCluster.GetProjects(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed loading projects: %w", err)
			}

			projects = make([]api.Project, 0, len(dbProjects))
			for _, dbProject := range dbProjects {
				p, err := dbProject.ToAPI(ctx, tx.Tx())
				if err != nil {
					return fmt.Errorf("Failed loading project %q: %w", dbProject.Name, err)
				}

				projects = append(projects, *p)
			}
		}

		for _, p := range projects {
			for _, networkProjectName := range []string{project.NetworkProjectFromRecord(&p), api.ProjectDefaultName} {
				_, ok := projectNetworks[networkProjectName]
				if ok {
					continue
				}

				projectNetworks[networkProjectName], err = tx.GetNetworks(ctx, networkProjectName)
				if err != nil {
					return fmt.Errorf("Failed loading networks: %w", err)
				}
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	canViewNetwork, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanView, auth.ObjectTypeNetwork)
	if err != nil {
		return response.SmartError(err)
	}

	canViewInstance, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanView, auth.ObjectTypeInstance)
	if err != nil {
		return response.SmartError(err)
	}

	// Work out which projects get to see each network.
	type allocationNetwork struct {
		project  string
		name     string
		projects []string
	}

	networks := []*allocationNetwork{}
	networksByName := map[network.ProjectNetwork]*allocationNetwork{}

	for _, p := range projects {
		networkProjectName := project.NetworkProjectFromRecord(&p)
		keys := make([]network.ProjectNetwork, 0, len(projectNetworks[networkProjectName]))

		for _, networkName := range projectNetworks[networkProjectName] {
			keys = append(keys, network.ProjectNetwork{ProjectName: networkProjectName, NetworkName: networkName})
		}

		// Include networks shared from the default project.
		for _, networkName := range projectNetworks[api.ProjectDefaultName] {
			if project.NetworkSharedFromDefault(&p, networkName) {
				keys = append(keys, network.ProjectNetwork{ProjectName: api.ProjectDefaultName, NetworkName: networkName})
			}
		}

		for _, key := range keys {
			if !canViewNetwork(auth.ObjectNetwork(key.ProjectName, key.NetworkName)) {
				continue
			}

			if !project.NetworkAllowed(p.Config, key.NetworkName, true) {
				continue
			}

			entry, ok := networksByName[key]
			if !ok {
				entry = &allocationNetwork{project: key.ProjectName, name: key.NetworkName}
				networksByName[key] = entry
				networks = append(networks, entry)
			}

			entry.projects = append(entry.projects, p.Name)
		}
	}

	// Returns IP address in its canonical CIDR form and whether the network is using NAT for that IP family.
	ipToCIDR := func(addr string, netConf map[string]string) (string, bool, error) {
		ip := net.ParseIP(addr)
		if ip == nil {
			return "", false, fmt.Errorf("Invalid IP address %q", addr)
		}

		if ip.To4() != nil {
			return fmt.Sprintf("%s/32", ip.String()), util.IsTrue(netConf["ipv4.nat"]), nil
		}

		return fmt.Sprintf("%s/128", ip.String()), util.IsTrue(netConf["ipv6.nat"]), nil
	}

	result := make([]api.NetworkAllocations, 0)

	// Get all the networks, their attached instances, their network forwards and their network load balancers.
	for _, entry := range networks {
		networkName := entry.name
		projectName := entry.project

		n, err := network.LoadByName(s, projectName, networkName)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed loading network %q in project %q: %w", networkName, projectName, err))
		}

		netConf := n.Config()

		for _, keyPrefix := range []string{"ipv4", "ipv6"} {
			ipNet, _ := network.ParseIPCIDRToNet(netConf[fmt.Sprintf("%s.address", keyPrefix)])
			if ipNet == nil {
				continue
			}

			result = append(result, api.NetworkAllocations{
				Network: networkName,
				Project: projectName,
				Address: ipNet.String(),
				UsedBy:  api.NewURL().Path(version.APIVersion, "networks", networkName).Project(projectName).String(),
				Type:    "network",
				NAT:     util.IsTrue(netConf[fmt.Sprintf("%s.nat", keyPrefix)]),
			})
		}

		// Only query the projects that have instances on the network.
		if allProjects {
			instanceProjects := []string{}
			err = network.UsedByInstanceDevices(s, projectName, networkName, n.Type(), func(inst db.InstanceArgs, _ string, _ map[string]string) error {
				if !slices.Contains(instanceProjects, inst.Project) {
					instanceProjects = append(instanceProjects, inst.Project)
				}

				return nil
			})
			if err != nil {
				return response.SmartError(fmt.Errorf("Failed getting instances for network %q in project %q: %w", networkName, projectName, err))
			}

			entry.projects = slices.DeleteFunc(entry.projects, func(name string) bool {
				return name != projectName && !slices.Contains(instanceProjects, name)
			})
		}

		for _, instanceProjectName := range entry.projects {
			leases, err := n.Leases(instanceProjectName, clusterRequest.ClientTypeNormal)
			if err != nil && !errors.Is(err, network.ErrNotImplemented) {
				return response.SmartError(fmt.Errorf("Failed getting leases for network %q in project %q: %w", networkName, projectName, err))
			}

			for _, lease := range leases {
				if !slices.Contains([]string{"static", "dynamic"}, lease.Type) {
					continue
				}

				// Leases without a MAC can't be tied to a project, only report them for the network's own.
				if lease.Hwaddr == "" && instanceProjectName != projectName {
					continue
				}

				if !canViewInstance(auth.ObjectInstance(instanceProjectName, lease.Hostname)) {
					continue
				}

				cidrAddr, nat, err := ipToCIDR(lease.Address, netConf)
				if err != nil {
					return response.SmartError(err)
				}

				result = append(result, api.NetworkAllocations{
					Network: networkName,
					Project: projectName,
					Address: cidrAddr,
					UsedBy:  api.NewURL().Path(version.APIVersion, "instances", lease.Hostname).Project(instanceProjectName).String(),
					Type:    "instance",
					Hwaddr:  lease.Hwaddr,
					NAT:     nat,
				})
			}
		}

		var forwards []*api.NetworkForward

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			networkID := n.ID()
			dbRecords, err := dbCluster.GetNetworkForwards(ctx, tx.Tx(), dbCluster.NetworkForwardFilter{
				NetworkID: &networkID,
			})
			if err != nil {
				return err
			}

			forwards = make([]*api.NetworkForward, 0, len(dbRecords))
			for _, dbRecord := range dbRecords {
				forward, err := dbRecord.ToAPI(ctx, tx.Tx())
				if err != nil {
					return err
				}

				forwards = append(forwards, forward)
			}

			return nil
		})
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed getting forwards for network %q in project %q: %w", networkName, projectName, err))
		}

		for _, forward := range forwards {
			cidrAddr, _, err := ipToCIDR(forward.ListenAddress, netConf)
			if err != nil {
				return response.SmartError(err)
			}

			result = append(
				result,
				api.NetworkAllocations{
					Network: networkName,
					Project: projectName,
					Address: cidrAddr,
					UsedBy:  api.NewURL().Path(version.APIVersion, "networks", networkName, "forwards", forward.ListenAddress).Project(projectName).String(),
					Type:    "network-forward",
					NAT:     false, // Network forwards are ingress and so aren't affected by SNAT.
				},
			)
		}

		var dbLoadBalancers []dbCluster.NetworkLoadBalancer
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			networkID := n.ID()

			// Get the load balancers.
			dbLoadBalancers, err = dbCluster.GetNetworkLoadBalancers(ctx, tx.Tx(), dbCluster.NetworkLoadBalancerFilter{NetworkID: &networkID})
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed getting load-balancers for network %q in project %q: %w", networkName, projectName, err))
		}

		for _, loadBalancer := range dbLoadBalancers {
			cidrAddr, _, err := ipToCIDR(loadBalancer.ListenAddress, netConf)
			if err != nil {
				return response.SmartError(err)
			}

			result = append(
				result,
				api.NetworkAllocations{
					Network: networkName,
					Project: projectName,
					Address: cidrAddr,
					UsedBy:  api.NewURL().Path(version.APIVersion, "networks", networkName, "load-balancers", loadBalancer.ListenAddress).Project(projectName).String(),
					Type:    "network-load-balancer",
					NAT:     false, // Network load-balancers are ingress and so aren't affected by SNAT.
				},
			)
		}
	}

	return response.SyncResponse(true, result)
}
