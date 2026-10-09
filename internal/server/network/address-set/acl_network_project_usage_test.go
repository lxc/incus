//go:build linux && cgo && !agent

package addressset

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

func TestACLCurrentProjectManagedNICs(t *testing.T) {
	for _, tc := range []struct {
		name          string
		config        map[string]string
		sharedProject string
		localProject  string
	}{
		{"restricted-mixed", map[string]string{"features.networks": "true", "restricted": "true", "restricted.networks.access": " shared "}, "default", "tenant"},
		{"ordinary-shared", map[string]string{"features.networks": "false"}, "default", "default"},
		{"unrestricted-local", map[string]string{"features.networks": "true", "restricted.networks.access": "shared"}, "tenant", "tenant"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newACLCurrentProjectFixture(t, tc.config)
			for _, query := range []string{"default", "tenant"} {
				t.Run(query, func(t *testing.T) {
					want := []aclCurrentProjectRow{}
					if query == "default" {
						for _, name := range []string{"shared", "local", "over", "drop"} {
							want = append(want, f.row("profile", "default", "template", name, f.template[name]))
						}
					}
					for _, item := range []struct{ device, resourceProject string }{{"shared", tc.sharedProject}, {"local", tc.localProject}} {
						if query == item.resourceProject {
							want = append(want, f.row("profile", "tenant", "standalone", item.device, f.standalone[item.device]))
						}
					}
					for _, item := range []struct{ device, resourceProject string }{{"shared", tc.sharedProject}, {"local", tc.localProject}, {"over", tc.localProject}} {
						if query == item.resourceProject {
							config := f.template[item.device]
							if item.device == "over" {
								config = f.local["over"]
							}

							want = append(want, f.row("instance", "tenant", "consumer", item.device, config))
						}
					}
					got := []aclCurrentProjectRow{}
					require.NoError(t, ACLUsedBy(f.s, query, func(ctx context.Context, tx *db.ClusterTx, matched []string, usage any, device string, config map[string]string) error {
						row := f.callback(t, usage, device, config, matched)
						got = append(got, row)
						return nil
					}, "acl-b", "acl-a"))
					require.ElementsMatch(t, want, got, "exact current callback owners and managed NICs")
					nets := map[string]NetworkACLUsage{}
					require.NoError(t, ACLNetworkUsage(f.s, query, []string{"acl-a", "acl-b"}, nets))
					expected := map[string]NetworkACLUsage{}
					for _, row := range want {
						name := row.Config["network"]
						entry := NetworkACLUsage{ID: f.networks[query+"/"+name], Name: name, Type: map[string]string{"shared": "bridge", "local": "ovn"}[name], Config: f.networkConfigs[query+"/"+name]}
						key := name
						if name == "shared" && row.Kind == "instance" {
							key = name + "/" + row.Project + "/" + row.Name + "/" + row.Device
							entry.InstanceProject = row.Project
							entry.InstanceName = row.Name
							entry.DeviceName = row.Device
						}

						expected[key] = entry
					}

					require.Equal(t, expected, nets, "exact resource project network IDs, types and owners")
					addresses := []string{"192.0.2.1"}
					setNets := map[string]AddressSetUsage{}
					require.NoError(t, AddressSetNetworkUsage(f.s, query, query+"-set", addresses, setNets))
					expectedSets := map[string]AddressSetUsage{}
					for key, n := range expected {
						expectedSets[key] = AddressSetUsage{ID: int(n.ID), Name: n.Name, Type: n.Type, InstanceProject: n.InstanceProject, InstanceName: n.InstanceName, DeviceName: n.DeviceName, Addresses: addresses, Config: n.Config, ACLNames: []string{"acl-a", "acl-b"}}
					}

					for key, n := range setNets {
						require.ElementsMatch(t, []string{"acl-a", "acl-b"}, n.ACLNames)
						n.ACLNames = []string{"acl-a", "acl-b"}
						setNets[key] = n
					}

					require.Equal(t, expectedSets, setNets, "project-local set rules reach exact current resource IDs")
					foreignSet := "tenant-set"
					if query == "tenant" {
						foreignSet = "default-set"
					}

					foreign := map[string]AddressSetUsage{}
					require.NoError(t, AddressSetNetworkUsage(f.s, query, foreignSet, addresses, foreign))
					require.Empty(t, foreign, "foreign project ACL rules are not imported")
				})
			}
		})
	}
}

type aclCurrentProjectRow struct {
	Kind    string
	Project string
	Name    string
	ID      int
	Device  string
	Config  map[string]string
	Matched []string
}

type aclCurrentProjectFixture struct {
	c              *db.Cluster
	s              *state.State
	networks       map[string]int64
	networkConfigs map[string]map[string]string
	template       map[string]map[string]string
	standalone     map[string]map[string]string
	local          map[string]map[string]string
	profiles       map[string]cluster.Profile
	instance       db.InstanceArgs
}

func newACLCurrentProjectFixture(t *testing.T, config map[string]string) *aclCurrentProjectFixture {
	t.Helper()
	c, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	nic := func(network, acls, marker string) map[string]string {
		return map[string]string{"type": "nic", "network": network, "security.acls": acls, "user.marker": marker}
	}

	f := &aclCurrentProjectFixture{c: c, s: &state.State{DB: &db.DB{Cluster: c}}, networks: map[string]int64{}, networkConfigs: map[string]map[string]string{}, profiles: map[string]cluster.Profile{}}
	f.template = map[string]map[string]string{"shared": nic("shared", "acl-a, acl-b", "profile-shared"), "local": nic("local", "acl-a", "profile-local"), "over": nic("shared", "acl-a", "overridden"), "drop": nic("shared", "acl-a", "removed-acl")}
	f.standalone = map[string]map[string]string{"shared": nic("shared", "acl-a, acl-b", "standalone-shared"), "local": nic("local", "acl-a", "standalone-local"), "unmanaged": {"type": "nic", "nictype": "bridged", "parent": "external", "security.acls": "acl-a"}, "empty": {"type": "nic", "network": "", "security.acls": "acl-a"}, "disk": {"type": "disk", "path": "/data", "network": "shared", "security.acls": "acl-a"}}
	f.local = map[string]map[string]string{"over": nic("local", "acl-b", "local-override"), "drop": {"type": "nic", "network": "shared"}, "unmanaged": f.standalone["unmanaged"], "empty": f.standalone["empty"], "disk": f.standalone["disk"]}
	require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "tenant"})
		require.NoError(t, err)
		config["features.profiles"] = "false"
		require.NoError(t, cluster.CreateProjectConfig(ctx, tx.Tx(), id, config))
		for _, owner := range []string{"default", "tenant"} {
			for _, name := range []string{"shared", "local"} {
				kind := db.NetworkTypeBridge
				if name == "local" {
					kind = db.NetworkTypeOVN
				}

				cfg := map[string]string{"user.identity": owner + "/" + name}
				f.networkConfigs[owner+"/"+name] = cfg
				f.networks[owner+"/"+name], err = tx.CreateNetwork(ctx, owner, name, "", kind, cfg)
				require.NoError(t, err)
			}

			for _, name := range []string{"acl-a", "acl-b"} {
				_, err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: owner, Name: name, Ingress: []api.NetworkACLRule{{Source: "$" + owner + "-set"}}})
				require.NoError(t, err)
			}

			_, err = cluster.CreateNetworkAddressSet(ctx, tx.Tx(), cluster.NetworkAddressSet{Project: owner, Name: owner + "-set", Addresses: []string{"192.0.2.1"}})
			require.NoError(t, err)
		}

		for _, p := range []struct {
			owner, name string
			devices     map[string]map[string]string
		}{{"default", "template", f.template}, {"tenant", "standalone", f.standalone}} {
			profileID, err := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: p.owner, Name: p.name, Description: "original-" + p.owner})
			require.NoError(t, err)
			devices, err := cluster.APIToDevices(p.devices)
			require.NoError(t, err)
			require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), profileID, devices))
		}

		var node string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&node))
		instanceID, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: "tenant", Name: "consumer", Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now(), Description: "original-instance"})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(instanceID), "tenant", []string{"template"}))
		devices, err := cluster.APIToDevices(f.local)
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateInstanceDevices(ctx, tx.Tx(), instanceID, devices))
		profiles, err := cluster.GetProfiles(ctx, tx.Tx())
		require.NoError(t, err)
		for _, p := range profiles {
			f.profiles[p.Project+"/"+p.Name] = p
		}

		return tx.InstanceList(ctx, func(inst db.InstanceArgs, _ api.Project) error { f.instance = inst; return nil })
	}))
	require.Equal(t, "tenant", f.instance.Project)
	require.Equal(t, "consumer", f.instance.Name)
	return f
}

func (f *aclCurrentProjectFixture) row(kind, owner, name, device string, config map[string]string) aclCurrentProjectRow {
	id := f.instance.ID
	if kind == "profile" {
		id = f.profiles[owner+"/"+name].ID
	}

	matched := []string{"acl-a"}
	switch config["security.acls"] {
	case "acl-a, acl-b":
		matched = []string{"acl-a", "acl-b"}
	case "acl-b":
		matched = []string{"acl-b"}
	}

	return aclCurrentProjectRow{Kind: kind, Project: owner, Name: name, ID: id, Device: device, Config: config, Matched: matched}
}

func (f *aclCurrentProjectFixture) callback(t *testing.T, usage any, device string, config map[string]string, matched []string) aclCurrentProjectRow {
	t.Helper()
	row := aclCurrentProjectRow{Device: device, Config: config, Matched: matched}
	switch u := usage.(type) {
	case cluster.Profile:
		require.Equal(t, f.profiles[u.Project+"/"+u.Name], u, "original profile callback object")
		row.Kind = "profile"
		row.Project = u.Project
		row.Name = u.Name
		row.ID = u.ID
	case db.InstanceArgs:
		require.Equal(t, f.instance, u, "original instance callback object, local devices and profiles")
		row.Kind = "instance"
		row.Project = u.Project
		row.Name = u.Name
		row.ID = u.ID
	default:
		t.Fatalf("Unexpected usage %T", usage)
	}

	return row
}

func TestACLCurrentProjectBranchesAndCallbackErrors(t *testing.T) {
	for _, kind := range []string{"network", "profile", "rule", "instance"} {
		t.Run(kind, func(t *testing.T) {
			c, cleanup := db.NewTestCluster(t)
			t.Cleanup(cleanup)
			s := &state.State{DB: &db.DB{Cluster: c}}
			require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "tenant"})
				require.NoError(t, err)
				require.NoError(t, cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"features.networks": "true", "features.profiles": "true"}))
				var node string
				require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&node))
				for _, owner := range []string{"default", "tenant"} {
					for _, name := range []string{"a", "b"} {
						_, err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: owner, Name: name, Ingress: []api.NetworkACLRule{{Source: name}}, Egress: []api.NetworkACLRule{{Destination: name}}})
						require.NoError(t, err)
					}

					for _, name := range []string{"first", "second"} {
						cfg := map[string]string{"user.owner": owner}
						if kind == "network" {
							cfg["security.acls"] = "a, b"
						}

						_, err = tx.CreateNetwork(ctx, owner, name, "original-"+owner, db.NetworkTypeBridge, cfg)
						require.NoError(t, err)
						require.NoError(t, tx.NetworkCreated(owner, name))
						devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": name, "security.acls": "a, b", "user.owner": owner}})
						require.NoError(t, err)
						switch kind {
						case "profile":
							id, err := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: owner, Name: name, Description: "original-" + owner})
							require.NoError(t, err)
							require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), id, devices))
						case "instance":
							id, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: owner, Name: name, Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now(), Description: "original-" + owner})
							require.NoError(t, err)
							require.NoError(t, cluster.UpdateInstanceDevices(ctx, tx.Tx(), id, devices))
						case "rule":
							_, err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: owner, Name: name, Description: "original-" + owner, Ingress: []api.NetworkACLRule{{Source: "a, a, b"}}, Egress: []api.NetworkACLRule{{Destination: "b, a"}}})
							require.NoError(t, err)
						}
					}
				}

				return nil
			}))
			for _, owner := range []string{"default", "tenant"} {
				names := []string{}
				require.NoError(t, ACLUsedBy(s, owner, func(ctx context.Context, tx *db.ClusterTx, matched []string, usage any, device string, config map[string]string) error {
					require.Equal(t, []string{"a", "b"}, matched)
					var name string
					switch u := usage.(type) {
					case *api.Network:
						require.Equal(t, "network", kind)
						name = u.Name
						require.Equal(t, "original-"+owner, u.Description)
						require.Equal(t, api.ConfigMap{"security.acls": "a, b", "user.owner": owner}, u.Config)
						require.Empty(t, device)
						require.Nil(t, config)
					case *api.NetworkACL:
						require.Equal(t, "rule", kind)
						name = u.Name
						require.Equal(t, "original-"+owner, u.Description)
						require.Equal(t, []api.NetworkACLRule{{Source: "a, a, b"}}, u.Ingress)
						require.Equal(t, []api.NetworkACLRule{{Destination: "b, a"}}, u.Egress)
						require.Empty(t, device)
						require.Nil(t, config)
					case cluster.Profile:
						require.Equal(t, "profile", kind)
						name = u.Name
						require.Equal(t, owner, u.Project)
						require.Equal(t, "original-"+owner, u.Description)
					case db.InstanceArgs:
						require.Equal(t, "instance", kind)
						name = u.Name
						require.Equal(t, owner, u.Project)
						require.Equal(t, "original-"+owner, u.Description)
					default:
						t.Fatalf("Unexpected callback %T", usage)
					}

					if kind == "profile" || kind == "instance" {
						require.Equal(t, "eth0", device)
						require.Equal(t, map[string]string{"type": "nic", "network": name, "security.acls": "a, b", "user.owner": owner}, config)
					}

					names = append(names, name)
					return nil
				}, "b", "a"))
				require.ElementsMatch(t, []string{"first", "second"}, names, "project-local callbacks and self-rule exclusion")
				for _, stop := range []error{errors.New("callback failure"), db.ErrInstanceListStop} {
					calls := 0
					err := ACLUsedBy(s, owner, func(context.Context, *db.ClusterTx, []string, any, string, map[string]string) error {
						calls++
						return stop
					}, "a", "b")
					require.ErrorIs(t, err, stop)
					require.Equal(t, 1, calls, "stop before second callback")
				}

				calls := 0
				require.NoError(t, ACLUsedBy(s, owner, func(context.Context, *db.ClusterTx, []string, any, string, map[string]string) error {
					calls++
					return nil
				}))
				require.Zero(t, calls, "empty ACL selection")
			}
		})
	}
}

func TestACLCurrentProjectCallbacksRemainCurrentOnly(t *testing.T) {
	f := newAddressSetGCFixture(t, true)
	s := &state.State{DB: &db.DB{Cluster: f.c}}
	count := func(owner string) map[string]int {
		got := map[string]int{}
		require.NoError(t, ACLUsedBy(s, owner, func(_ context.Context, _ *db.ClusterTx, _ []string, usage any, _ string, _ map[string]string) error {
			switch usage.(type) {
			case cluster.Profile:
				got["profile"]++
			case db.InstanceArgs:
				got["instance"]++
			default:
				t.Fatalf("Unexpected usage %T", usage)
			}

			return nil
		}, "a"))
		return got
	}

	require.Equal(t, map[string]int{"profile": 1, "instance": 2}, count("default"))
	require.Empty(t, count("tenant"))
	f.commit(t, "", nil)
	require.Empty(t, count("default"))
	require.Empty(t, count("tenant"))
	for _, owner := range []string{"default", "tenant"} {
		nets := map[string]NetworkACLUsage{}
		require.NoError(t, ACLNetworkUsage(s, owner, []string{"a"}, nets))
		require.Empty(t, nets)
		sets := map[string]AddressSetUsage{}
		require.NoError(t, AddressSetNetworkUsage(s, owner, "oldset", nil, sets))
		require.Empty(t, sets)
	}

	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		retained, err := tx.RetainedProfileReferenceUsage(ctx, db.NetworkReferenceFilter{ACLID: f.acls["a"]})
		require.NoError(t, err)
		require.NotEmpty(t, retained)
		return nil
	})
	f.assertPlans(t, "newset", "unused")
}

func TestACLCurrentBridgeOwnerCollision(t *testing.T) {
	assertACLCurrentBridgeOwnerIdentity(t, "consumer", "consumer")
}

func TestACLCurrentBridgeOwnerPropagation(t *testing.T) {
	assertACLCurrentBridgeOwnerIdentity(t, "default-consumer", "tenant-consumer")
}

func assertACLCurrentBridgeOwnerIdentity(t *testing.T, defaultName string, tenantName string) {
	t.Helper()
	c, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	s := &state.State{DB: &db.DB{Cluster: c}}
	owners := map[string]string{"default": defaultName, "tenant": tenantName}
	addresses := []string{"192.0.2.10"}
	configs := map[string]map[string]string{
		"shared":  {"security.acls": "identity-acl", "user.identity": "default/shared"},
		"overlay": {"security.acls": "identity-acl", "user.identity": "default/overlay"},
	}

	networkIDs := map[string]int64{}
	require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectID, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "tenant"})
		require.NoError(t, err)
		require.NoError(t, cluster.CreateProjectConfig(ctx, tx.Tx(), projectID, map[string]string{"features.networks": "false", "features.profiles": "true"}))
		_, err = cluster.CreateNetworkAddressSet(ctx, tx.Tx(), cluster.NetworkAddressSet{Project: "default", Name: "identity-set", Addresses: addresses})
		require.NoError(t, err)
		_, err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "default", Name: "identity-acl", Ingress: []api.NetworkACLRule{{Source: "$identity-set"}}})
		require.NoError(t, err)
		for name, kind := range map[string]db.NetworkType{"shared": db.NetworkTypeBridge, "overlay": db.NetworkTypeOVN} {
			networkIDs[name], err = tx.CreateNetwork(ctx, "default", name, "", kind, configs[name])
			require.NoError(t, err)
			require.NoError(t, tx.NetworkCreated("default", name))
		}

		var node string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&node))
		for owner, name := range owners {
			devices, err := cluster.APIToDevices(map[string]map[string]string{
				"eth0": {"type": "nic", "network": "shared", "security.acls": "identity-acl"},
				"eth1": {"type": "nic", "network": "overlay", "security.acls": "identity-acl"},
			})
			require.NoError(t, err)
			// Standalone profile and direct-network references must share the aggregate entry.
			profileID, err := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: owner, Name: "standalone"})
			require.NoError(t, err)
			require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), profileID, devices))
			instanceID, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: owner, Name: name, Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
			require.NoError(t, err)
			require.NoError(t, cluster.UpdateInstanceDevices(ctx, tx.Tx(), instanceID, devices))
		}

		return nil
	}))

	t.Run("acl-usage", func(t *testing.T) {
		nets := map[string]NetworkACLUsage{}
		require.NoError(t, ACLNetworkUsage(s, "default", []string{"identity-acl"}, nets))
		gotOwners := map[string]string{}
		for _, usage := range nets {
			if usage.Type == "bridge" && usage.DeviceName != "" {
				gotOwners[usage.InstanceProject] = usage.InstanceName
			}
		}
		require.Equal(t, owners, gotOwners, "both actual owner projects must survive bridge usage collection")
		for owner, name := range owners {
			key := "shared/" + owner + "/" + name + "/eth0"
			require.Contains(t, nets, key)
			require.Equal(t, NetworkACLUsage{ID: networkIDs["shared"], Name: "shared", Type: "bridge", Config: configs["shared"], InstanceProject: owner, InstanceName: name, DeviceName: "eth0"}, nets[key])
		}

		for name, kind := range map[string]string{"shared": "bridge", "overlay": "ovn"} {
			require.Contains(t, nets, name)
			require.Equal(t, NetworkACLUsage{ID: networkIDs[name], Name: name, Type: kind, Config: configs[name]}, nets[name], "network/profile and OVN aggregates have no instance owner")
		}

		require.Len(t, nets, 4, "two bridge owners plus one bridge and one OVN aggregate")
	})

	t.Run("address-set-usage", func(t *testing.T) {
		nets := map[string]AddressSetUsage{}
		require.NoError(t, AddressSetNetworkUsage(s, "default", "identity-set", addresses, nets))
		for owner, name := range owners {
			key := "shared/" + owner + "/" + name + "/eth0"
			require.Contains(t, nets, key)
			require.Equal(t, AddressSetUsage{ID: int(networkIDs["shared"]), Name: "shared", Type: "bridge", Config: configs["shared"], InstanceProject: owner, InstanceName: name, DeviceName: "eth0", Addresses: addresses, ACLNames: []string{"identity-acl"}}, nets[key], "real conversion preserves owner metadata independently of key collisions")
		}

		for name, kind := range map[string]string{"shared": "bridge", "overlay": "ovn"} {
			require.Contains(t, nets, name)
			require.Equal(t, AddressSetUsage{ID: int(networkIDs[name]), Name: name, Type: kind, Config: configs[name], Addresses: addresses, ACLNames: []string{"identity-acl"}}, nets[name], "conversion leaves aggregate owner metadata empty")
		}

		require.Len(t, nets, 4, "conversion preserves bridge owners and existing aggregate deduplication")
	})
}

func TestACLProfileSnapshotRetry(t *testing.T) {
	for _, scenario := range []string{"replay", "change", "empty"} {
		t.Run(scenario, func(t *testing.T) {
			c, cleanup := db.NewTestCluster(t)
			t.Cleanup(cleanup)
			beforeConfig := map[string]string{"features.profiles": "true", "features.networks": "true", "restricted": "true", "restricted.networks.access": "shared", "user.marker": "before"}
			afterConfig := map[string]string{"features.profiles": "true", "features.networks": "false", "user.marker": "after"}
			ownerIDs := map[string]int{}
			createOwner := func(ctx context.Context, tx *db.ClusterTx, name string, config map[string]string) *api.Project {
				id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: name, Description: "owner-" + name})
				require.NoError(t, err)
				ownerIDs[name] = int(id)
				require.NoError(t, cluster.CreateProjectConfig(ctx, tx.Tx(), id, config))
				return &api.Project{Name: name, ProjectPut: api.ProjectPut{Description: "owner-" + name, Config: config}}
			}

			writeDevices := func(ctx context.Context, tx *db.ClusterTx, id int, name, marker string) map[string]cluster.Device {
				config := map[string]map[string]string{name: {"type": "nic", "network": "shared", "security.acls": "acl-a", "user.marker": marker}}
				devices, err := cluster.APIToDevices(config)
				require.NoError(t, err)
				require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), int64(id), devices))
				devices, err = cluster.GetProfileDevices(ctx, tx.Tx(), id)
				require.NoError(t, err)
				require.Equal(t, config, cluster.DevicesToAPI(devices))
				return devices
			}

			createProfile := func(ctx context.Context, tx *db.ClusterTx, owner, name string) cluster.Profile {
				id, err := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: owner, Name: name, Description: "profile-" + name})
				require.NoError(t, err)
				return cluster.Profile{ID: int(id), ProjectID: ownerIDs[owner], Project: owner, Name: name, Description: "profile-" + name}
			}

			var old, survivor cluster.Profile
			var oldDevices, survivorDevices map[string]cluster.Device
			var oldOwner, survivorOwner *api.Project
			require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				// Keep the default-project all-owner scan limited to these two exact fixture identities.
				require.NoError(t, cluster.DeleteProfile(ctx, tx.Tx(), "default", "default"))
				oldOwner = createOwner(ctx, tx, "obsolete", beforeConfig)
				survivorOwner = createOwner(ctx, tx, "tenant", beforeConfig)
				old = createProfile(ctx, tx, "obsolete", "old-only")
				survivor = createProfile(ctx, tx, "tenant", "survivor")
				oldDevices = writeDevices(ctx, tx, old.ID, "old-nic", "old-device")
				survivorDevices = writeDevices(ctx, tx, survivor.ID, "before-nic", "before-device")
				return nil
			}))
			beforeProfiles := []cluster.Profile{old, survivor}
			beforeDevices := map[int]map[string]cluster.Device{old.ID: oldDevices, survivor.ID: survivorDevices}
			beforeProjects := map[string]*api.Project{"obsolete": oldOwner, "tenant": survivorOwner}
			wantProfiles, wantDevices, wantProjects := beforeProfiles, beforeDevices, beforeProjects
			var snapshot, first aclProfileSnapshot
			attempts := 0
			err := c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				attempts++
				require.LessOrEqual(t, attempts, 2, "only the explicit post-capture error may replay")
				if attempts == 2 && scenario != "replay" {
					require.NoError(t, cluster.DeleteProfile(ctx, tx.Tx(), old.Project, old.Name))
					require.NoError(t, cluster.DeleteProject(ctx, tx.Tx(), old.Project))
					if scenario == "empty" {
						require.NoError(t, cluster.DeleteProfile(ctx, tx.Tx(), survivor.Project, survivor.Name))
						require.NoError(t, cluster.DeleteProject(ctx, tx.Tx(), survivor.Project))
						wantProfiles = nil
						wantDevices = map[int]map[string]cluster.Device{}
						wantProjects = map[string]*api.Project{}
					} else {
						changed := survivor
						changed.Description = "survivor-after"
						require.NoError(t, cluster.UpdateProfile(ctx, tx.Tx(), survivor.Project, survivor.Name, changed))
						changedDevices := writeDevices(ctx, tx, survivor.ID, "after-nic", "after-device")
						changedOwner := &api.Project{Name: "tenant", ProjectPut: api.ProjectPut{Description: "owner-after", Config: afterConfig}}
						require.NoError(t, cluster.UpdateProject(ctx, tx.Tx(), "tenant", changedOwner.ProjectPut))
						newOwner := createOwner(ctx, tx, "new-owner", afterConfig)
						added := createProfile(ctx, tx, "new-owner", "new-profile")
						require.NotEqual(t, old.ID, added.ID)
						addedDevices := writeDevices(ctx, tx, added.ID, "new-nic", "new-device")
						wantProfiles = []cluster.Profile{changed, added}
						wantDevices = map[int]map[string]cluster.Device{changed.ID: changedDevices, added.ID: addedDevices}
						wantProjects = map[string]*api.Project{"tenant": changedOwner, "new-owner": newOwner}
					}
				}

				err := snapshot.collect(ctx, tx, api.ProjectDefaultName)
				if err != nil {
					return err
				}

				if attempts == 1 {
					require.ElementsMatch(t, beforeProfiles, snapshot.profiles)
					require.Equal(t, beforeDevices, snapshot.devices)
					require.Equal(t, beforeProjects, snapshot.projects)
					first = snapshot
					// This injects a callback error after capture, not a Commit or Rollback failure.
					return sqlite3.ErrBusy
				}

				return nil
			})
			require.NoError(t, err)
			require.Equal(t, 2, attempts)
			require.ElementsMatch(t, wantProfiles, snapshot.profiles, "final snapshot contains each current fixture profile exactly once")
			require.Equal(t, wantDevices, snapshot.devices, "final device IDs and configurations replace the prior attempt")
			require.Equal(t, wantProjects, snapshot.projects, "final complete owner records replace the prior attempt")
			require.ElementsMatch(t, beforeProfiles, first.profiles, "prior profile snapshot remains unchanged")
			require.Equal(t, beforeDevices, first.devices, "prior device map aliases remain unchanged")
			require.Equal(t, beforeProjects, first.projects, "prior owner map aliases remain unchanged")
			if scenario != "replay" {
				_, found := snapshot.devices[old.ID]
				require.False(t, found, "removed profile has no stale device-map key")
				_, found = snapshot.projects[old.Project]
				require.False(t, found, "removed owner has no stale project-cache key")
			}

			if scenario == "empty" {
				require.Empty(t, snapshot.profiles)
				require.Empty(t, snapshot.devices)
				require.Empty(t, snapshot.projects)
			}
		})
	}
}
