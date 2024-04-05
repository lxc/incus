package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/lxc/incus/v6/internal/filter"
	"github.com/lxc/incus/v6/internal/server/auth"
	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/db"
	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/query"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	localtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/lxc/incus/v6/shared/util"
)

// urlInstanceTypeDetect detects what sort of instance type filter is being requested. Either
// explicitly via the instance-type query param or implicitly via the endpoint URL used.
func urlInstanceTypeDetect(r *http.Request) (instancetype.Type, error) {
	reqInstanceType := r.URL.Query().Get("instance-type")
	if reqInstanceType != "" {
		instanceType, err := instancetype.New(reqInstanceType)
		if err != nil {
			return instancetype.Any, err
		}

		return instanceType, nil
	}

	return instancetype.Any, nil
}

// swagger:operation GET /1.0/instances instances instances_get
//
//  Get the instances
//
//  Returns a list of instances (URLs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: filter
//      description: Collection filter
//      type: string
//      example: default
//    - in: query
//      name: all-projects
//      description: Retrieve instances from all projects
//      type: boolean
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/instances/foo",
//                "/1.0/instances/bar"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/instances?recursion=1 instances instances_get_recursion1
//
//  Get the instances
//
//  Returns a list of instances (basic structs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: filter
//      description: Collection filter
//      type: string
//      example: default
//    - in: query
//      name: all-projects
//      description: Retrieve instances from all projects
//      type: boolean
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of instances
//            items:
//              $ref: "#/definitions/Instance"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/instances?recursion=2 instances instances_get_recursion2
//
//  Get the instances
//
//  Returns a list of instances (full structs).
//
//  The main difference between recursion=1 and recursion=2 is that the
//  latter also includes state and snapshot information allowing for a
//  single API call to return everything needed by most clients.
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: filter
//      description: Collection filter
//      type: string
//      example: default
//    - in: query
//      name: all-projects
//      description: Retrieve instances from all projects
//      type: boolean
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of instances
//            items:
//              $ref: "#/definitions/InstanceFull"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

func instancesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	for i := 0; i < 100; i++ {
		result, err := doInstancesGet(s, r)
		if err == nil {
			return response.SyncResponse(true, result)
		}

		if !query.IsRetriableError(err) {
			logger.Debugf("DBERR: containersGet: error %q", err)
			return response.SmartError(err)
		}
		// 100 ms may seem drastic, but we really don't want to thrash
		// perhaps we should use a random amount
		time.Sleep(100 * time.Millisecond)
	}

	logger.Debugf("DBERR: containersGet, db is locked")
	logger.Debugf(logger.GetStack())
	return response.InternalError(fmt.Errorf("DB is locked"))
}

func doInstancesGet(s *state.State, r *http.Request) (any, error) {
	resultFullList := []*api.InstanceFull{}
	resultMu := sync.Mutex{}

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return nil, err
	}

	// Parse the recursion field.
	recursion, err := strconv.Atoi(r.FormValue("recursion"))
	if err != nil {
		recursion = 0
	}

	// Parse filter value.
	filterStr := r.FormValue("filter")
	clauses, err := filter.Parse(filterStr, filter.QueryOperatorSet())
	if err != nil {
		return nil, fmt.Errorf("Invalid filter: %w", err)
	}

	mustLoadObjects := recursion > 0 || (recursion == 0 && clauses != nil && len(clauses.Clauses) > 0)

	// Detect project mode.
	projectName := request.QueryParam(r, "project")
	allProjects := util.IsTrue(r.FormValue("all-projects"))

	if allProjects && projectName != "" {
		return nil, api.StatusErrorf(http.StatusBadRequest, "Cannot specify a project when requesting all projects")
	} else if !allProjects && projectName == "" {
		projectName = api.ProjectDefaultName
	}

	// Get the list and location of all instances.
	var filteredProjects []string
	var memberAddressInstances map[string][]db.Instance

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		if allProjects {
			projects, err := dbCluster.GetProjects(context.Background(), tx.Tx())
			if err != nil {
				return err
			}

			for _, project := range projects {
				filteredProjects = append(filteredProjects, project.Name)
			}
		} else {
			filteredProjects = []string{projectName}
		}

		offlineThreshold := s.GlobalConfig.OfflineThreshold()

		memberAddressInstances, err = tx.GetInstancesByMemberAddress(ctx, offlineThreshold, filteredProjects, instanceType)
		if err != nil {
			return fmt.Errorf("Failed getting instances by member address: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanView, auth.ObjectTypeInstance)
	if err != nil {
		return nil, err
	}

	// Removes instances the user doesn't have access to.
	for address, instances := range memberAddressInstances {
		var filteredInstances []db.Instance

		for _, inst := range instances {
			if !userHasPermission(auth.ObjectInstance(inst.Project, inst.Name)) {
				continue
			}

			filteredInstances = append(filteredInstances, inst)
		}

		memberAddressInstances[address] = filteredInstances
	}

	resultErrListAppend := func(inst db.Instance, err error) {
		instFull := &api.InstanceFull{
			Instance: api.Instance{
				Name:       inst.Name,
				Status:     api.Error.String(),
				StatusCode: api.Error,
				Location:   inst.Location,
				Project:    inst.Project,
				Type:       inst.Type.String(),
			},
		}

		resultMu.Lock()
		resultFullList = append(resultFullList, instFull)
		resultMu.Unlock()
	}

	resultFullListAppend := func(instFull *api.InstanceFull) {
		if instFull != nil {
			resultMu.Lock()
			resultFullList = append(resultFullList, instFull)
			resultMu.Unlock()
		}
	}

	// Get the data
	wg := sync.WaitGroup{}
	networkCert := s.Endpoints.NetworkCert()
	for memberAddress, instances := range memberAddressInstances {
		// If this is an internal request from another cluster node, ignore instances from other
		// projectInstanceToNodeName, and return only the ones on this member.
		if isClusterNotification(r) && memberAddress != "" {
			continue
		}

		// Mark instances on unavailable projectInstanceToNodeName as down.
		if mustLoadObjects && memberAddress == "0.0.0.0" {
			for _, inst := range instances {
				resultErrListAppend(inst, fmt.Errorf("unavailable"))
			}

			continue
		}

		// For recursion requests we need to fetch the state of remote instances from their respective
		// projectInstanceToNodeName.
		if mustLoadObjects && memberAddress != "" && !isClusterNotification(r) {
			wg.Add(1)

			go func(memberAddress string, instances []db.Instance) {
				defer wg.Done()

				if recursion == 1 {
					apiInsts, err := doContainersGetFromNode(filteredProjects, memberAddress, allProjects, networkCert, s.ServerCert(), r, instanceType)
					if err != nil {
						for _, inst := range instances {
							resultErrListAppend(inst, err)
						}

						return
					}

					for _, apiInst := range apiInsts {
						apiInst := apiInst // Local variable for append.
						resultFullListAppend(&api.InstanceFull{Instance: apiInst})
					}

					return
				}

				cs, err := doContainersFullGetFromNode(filteredProjects, memberAddress, allProjects, networkCert, s.ServerCert(), r, instanceType)
				if err != nil {
					for _, inst := range instances {
						resultErrListAppend(inst, err)
					}

					return
				}

				for _, c := range cs {
					c := c // Local variable for append.
					resultFullListAppend(&c)
				}
			}(memberAddress, instances)

			continue
		}

		if !mustLoadObjects {
			for _, inst := range instances {
				resultFullListAppend(&api.InstanceFull{Instance: api.Instance{
					Project:  inst.Project,
					Name:     inst.Name,
					Location: inst.Location,
				}})
			}
		} else {
			threads := 4
			if len(instances) < threads {
				threads = len(instances)
			}

			hostInterfaces, _ := net.Interfaces()

			// Get the local instances.
			localInstancesByID := make(map[int64]instance.Instance)
			for _, projectName := range filteredProjects {
				insts, err := instanceLoadNodeProjectAll(r.Context(), s, projectName, instanceType)
				if err != nil {
					return nil, fmt.Errorf("Failed loading instances for project %q: %w", projectName, err)
				}

				for _, inst := range insts {
					localInstancesByID[int64(inst.ID())] = inst
				}
			}

			queue := make(chan db.Instance, threads)

			for i := 0; i < threads; i++ {
				wg.Add(1)

				go func() {
					for {
						dbInst, more := <-queue
						if !more {
							break
						}

						inst, found := localInstancesByID[dbInst.ID]
						if !found {
							continue
						}

						if recursion < 2 {
							c, _, err := inst.Render()
							if err != nil {
								resultErrListAppend(dbInst, err)
							} else {
								resultFullListAppend(&api.InstanceFull{Instance: *c.(*api.Instance)})
							}

							continue
						}

						c, _, err := inst.RenderFull(hostInterfaces)
						if err != nil {
							resultErrListAppend(dbInst, err)
						} else {
							resultFullListAppend(c)
						}
					}

					wg.Done()
				}()
			}

			for _, inst := range instances {
				queue <- inst
			}

			close(queue)
		}
	}
	wg.Wait()

	// Sort the result list by project and then instance name.
	sort.SliceStable(resultFullList, func(i, j int) bool {
		if resultFullList[i].Project == resultFullList[j].Project {
			return resultFullList[i].Name < resultFullList[j].Name
		}

		return resultFullList[i].Project < resultFullList[j].Project
	})

	// Filter result list if needed.
	if clauses != nil && len(clauses.Clauses) > 0 {
		resultFullList, err = instance.FilterFull(resultFullList, *clauses)
		if err != nil {
			return nil, err
		}
	}

	if recursion == 0 {
		resultList := make([]string, 0, len(resultFullList))
		for i := range resultFullList {
			url := api.NewURL().Path(version.APIVersion, "instances", resultFullList[i].Name).Project(resultFullList[i].Project)
			resultList = append(resultList, url.String())
		}

		return resultList, nil
	}

	if recursion == 1 {
		resultList := make([]*api.Instance, 0, len(resultFullList))
		for i := range resultFullList {
			resultList = append(resultList, &resultFullList[i].Instance)
		}

		return resultList, nil
	}

	return resultFullList, nil
}

// Fetch information about the containers on the given remote node, using the
// rest API and with a timeout of 30 seconds.
func doContainersGetFromNode(projects []string, node string, allProjects bool, networkCert *localtls.CertInfo, serverCert *localtls.CertInfo, r *http.Request, instanceType instancetype.Type) ([]api.Instance, error) {
	f := func() ([]api.Instance, error) {
		client, err := cluster.Connect(node, networkCert, serverCert, r, true)
		if err != nil {
			return nil, fmt.Errorf("Failed to connect to member %s: %w", node, err)
		}

		var containers []api.Instance
		if allProjects {
			containers, err = client.GetInstancesAllProjects(api.InstanceType(instanceType.String()))
			if err != nil {
				return nil, fmt.Errorf("Failed to get instances from member %s: %w", node, err)
			}
		} else {
			for _, project := range projects {
				client = client.UseProject(project)

				tmpContainers, err := client.GetInstances(api.InstanceType(instanceType.String()))
				if err != nil {
					return nil, fmt.Errorf("Failed to get instances from member %s: %w", node, err)
				}

				containers = append(containers, tmpContainers...)
			}
		}

		return containers, nil
	}

	timeout := time.After(30 * time.Second)
	done := make(chan struct{})

	var containers []api.Instance
	var err error

	go func() {
		containers, err = f()
		done <- struct{}{}
	}()

	select {
	case <-timeout:
		err = fmt.Errorf("Timeout getting instances from member %s", node)
	case <-done:
	}

	return containers, err
}

func doContainersFullGetFromNode(projects []string, node string, allProjects bool, networkCert *localtls.CertInfo, serverCert *localtls.CertInfo, r *http.Request, instanceType instancetype.Type) ([]api.InstanceFull, error) {
	f := func() ([]api.InstanceFull, error) {
		client, err := cluster.Connect(node, networkCert, serverCert, r, true)
		if err != nil {
			return nil, fmt.Errorf("Failed to connect to member %s: %w", node, err)
		}

		var instances []api.InstanceFull
		if allProjects {
			instances, err = client.GetInstancesFullAllProjects(api.InstanceType(instanceType.String()))
			if err != nil {
				return nil, fmt.Errorf("Failed to get instances from member %s: %w", node, err)
			}
		} else {
			for _, project := range projects {
				client = client.UseProject(project)

				tmpInstances, err := client.GetInstancesFull(api.InstanceType(instanceType.String()))
				if err != nil {
					return nil, fmt.Errorf("Failed to get instances from member %s: %w", node, err)
				}

				instances = append(instances, tmpInstances...)
			}
		}

		return instances, nil
	}

	timeout := time.After(30 * time.Second)
	done := make(chan struct{})

	var instances []api.InstanceFull
	var err error

	go func() {
		instances, err = f()
		done <- struct{}{}
	}()

	select {
	case <-timeout:
		err = fmt.Errorf("Timeout getting instances from member %s", node)
	case <-done:
	}

	return instances, err
}
