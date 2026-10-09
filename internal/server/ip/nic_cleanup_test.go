package ip

import (
	"errors"
	"net"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
)

func TestNICLinkCleanupOriginalAllocation(t *testing.T) {
	for _, name := range []string{"delete", "lost-delete-reply", "delete-refused", "replacement-index", "replacement-alias", "replacement-name", "missing-with-name-reuse", "changed-namespace", "changed-boot", "missing-generation"} {
		t.Run(name, func(t *testing.T) {
			boot := uuid.NewString()
			original := &netlink.GenericLink{LinkAttrs: netlink.LinkAttrs{Index: 17, Name: "original", HardwareAddr: net.HardwareAddr{0, 1, 2, 3, 4, 5}}, LinkType: "veth"}
			links := []netlink.Link{original}
			deletes := 0
			ops := nicLinkCleanupOps{
				root: func() (string, uint64, uint64, error) { return boot, 1, 23, nil },
				byName: func(s string) (netlink.Link, error) {
					for _, l := range links {
						if l.Attrs().Name == s {
							return l, nil
						}
					}

					return nil, netlink.LinkNotFoundError{}
				},
				byIndex: func(i int) (netlink.Link, error) {
					for _, l := range links {
						if l.Attrs().Index == i {
							return l, nil
						}
					}

					return nil, netlink.LinkNotFoundError{}
				},
				list:  func() ([]netlink.Link, error) { return links, nil },
				alias: func(l netlink.Link, s string) error { l.Attrs().Alias = s; return nil },
				remove: func(l netlink.Link) error {
					deletes++
					if name == "delete-refused" {
						return errors.New("injected delete failure")
					}

					links = nil
					if name == "lost-delete-reply" {
						return errors.New("reply lost")
					}

					return nil
				},
			}

			plan, err := captureNICLinkCleanup(ops, "original", "veth", nil)
			require.NoError(t, err)
			require.NoError(t, plan.Validate())
			captured, err := captureNICLinkCleanup(ops, "original", "veth", &plan)
			require.NoError(t, err)
			require.Equal(t, plan, captured)
			switch name {
			case "replacement-index":
				original.Index++
			case "replacement-alias":
				original.Alias = "foreign"
			case "replacement-name":
				original.Name = "foreign"
			case "missing-with-name-reuse":
				links = []netlink.Link{&netlink.GenericLink{LinkAttrs: netlink.LinkAttrs{Index: 18, Name: "original", Alias: "foreign"}, LinkType: "veth"}}
			case "changed-namespace":
				ops.root = func() (string, uint64, uint64, error) { return boot, 1, 24, nil }
			case "changed-boot":
				boot = uuid.NewString()
			case "missing-generation":
				plan.Alias = ""
			}

			err = applyNICLinkCleanup(ops, plan)
			success := name == "delete" || name == "lost-delete-reply" || name == "missing-with-name-reuse" || name == "changed-boot"
			if success {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			if name == "delete" || name == "lost-delete-reply" {
				require.Equal(t, 1, deletes)
				require.NoError(t, applyNICLinkCleanup(ops, plan))
			} else {
				switch name {
				case "delete-refused":
					require.Equal(t, 1, deletes)
				default:
					require.Zero(t, deletes)
				}
			}

			if name == "replacement-index" { // Original alias remains: a moved/reindexed allocation cannot be acknowledged.
				require.Error(t, err)
			}
		})
	}
}

func TestNICLinkCleanupPhysicalMarkerRelease(t *testing.T) {
	for _, name := range []string{"success", "lost-reply", "refused", "replacement-before-release", "replacement-after-release"} {
		t.Run(name, func(t *testing.T) {
			p := NICLinkCleanup{Version: 1, BootID: uuid.NewString(), NamespaceDevice: 1, NamespaceInode: 23, Index: 17, Name: "representor", Kind: "device", HardwareAddr: "00:01:02:03:04:05", Alias: "incus-ovn-" + uuid.NewString()}
			addr, err := net.ParseMAC(p.HardwareAddr)
			require.NoError(t, err)
			link := &netlink.GenericLink{LinkAttrs: netlink.LinkAttrs{Index: p.Index, Name: p.Name, HardwareAddr: addr, Alias: p.Alias}, LinkType: p.Kind}
			if name == "replacement-before-release" {
				link.Alias = "replacement"
			}

			clears := 0
			ops := nicLinkCleanupOps{
				root:    func() (string, uint64, uint64, error) { return p.BootID, p.NamespaceDevice, p.NamespaceInode, nil },
				byIndex: func(index int) (netlink.Link, error) { require.Equal(t, p.Index, index); return link, nil },
				alias: func(original netlink.Link, alias string) error {
					clears++
					require.Same(t, link, original)
					require.Empty(t, alias)
					if name == "refused" {
						return errors.New("clear refused")
					}

					link.Alias = alias
					if name == "replacement-after-release" {
						link.Alias = "replacement"
					}

					if name == "lost-reply" {
						return errors.New("clear reply lost")
					}

					return nil
				},
			}

			err = releaseNICPhysicalLinkCleanup(ops, p)
			if name == "success" || name == "lost-reply" {
				require.NoError(t, err)
				require.Empty(t, link.Alias)
			} else {
				require.Error(t, err)
			}

			if name == "replacement-before-release" {
				require.Zero(t, clears)
				require.Equal(t, "replacement", link.Alias)
			}
		})
	}
}

func TestNICLinkCleanupCreationIntent(t *testing.T) {
	for _, kind := range []string{"veth", "tuntap"} {
		for _, mode := range []string{"success", "occupied-name", "create-refused", "lost-create-reply", "unmarked-ambiguous", "replacement-after-create"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				p := NICLinkCleanup{Version: 1, BootID: uuid.NewString(), NamespaceDevice: 1, NamespaceInode: 23, Name: "original", Kind: kind, Alias: "incus-ovn-" + uuid.NewString()}
				var links []netlink.Link
				if mode == "occupied-name" {
					links = []netlink.Link{&netlink.GenericLink{LinkAttrs: netlink.LinkAttrs{Index: 99, Name: p.Name, Alias: "foreign"}, LinkType: kind}}
				}

				creates, deletes := 0, 0
				lookup := func(name string) (netlink.Link, error) {
					for _, l := range links {
						if l.Attrs().Name == name {
							return l, nil
						}
					}

					return nil, netlink.LinkNotFoundError{}
				}

				ops := nicLinkCleanupOps{
					root:   func() (string, uint64, uint64, error) { return p.BootID, p.NamespaceDevice, p.NamespaceInode, nil },
					byName: lookup,
					byIndex: func(index int) (netlink.Link, error) {
						for _, l := range links {
							if l.Attrs().Index == index {
								return l, nil
							}
						}

						return nil, netlink.LinkNotFoundError{}
					},
					list: func() ([]netlink.Link, error) { return links, nil },
					remove: func(l netlink.Link) error {
						deletes++
						require.Equal(t, p.Alias, l.Attrs().Alias)
						links = nil
						return nil
					},
				}

				plan, err := createNICLinkCleanup(ops, p, func() error {
					creates++
					if mode == "create-refused" {
						return errors.New("exclusive create refused")
					}

					links = []netlink.Link{&netlink.GenericLink{LinkAttrs: netlink.LinkAttrs{Index: 17, Name: p.Name, Alias: p.Alias, HardwareAddr: net.HardwareAddr{0, 1, 2, 3, 4, 5}}, LinkType: kind}}
					if mode == "unmarked-ambiguous" {
						links[0].Attrs().Alias = ""
					}

					if mode == "replacement-after-create" {
						links[0].Attrs().Alias = "foreign"
					}

					if mode == "lost-create-reply" {
						return errors.New("create reply lost")
					}

					return nil
				})
				switch mode {
				case "success":
					require.NoError(t, err)
					require.NoError(t, plan.Validate())
				default:
					require.Error(t, err)
				}

				if mode == "occupied-name" {
					require.Zero(t, creates)
					require.Zero(t, deletes)
					require.Equal(t, "foreign", links[0].Attrs().Alias)
					return
				}

				cleanupErr := applyNICLinkCleanup(ops, plan)
				if mode == "unmarked-ambiguous" || mode == "replacement-after-create" {
					require.Error(t, cleanupErr)
					require.Zero(t, deletes)
					require.Len(t, links, 1)
				} else {
					require.NoError(t, cleanupErr)
					require.Empty(t, links)
				}
			})
		}
	}
}

func TestNICLinkCleanupRetryPreflightNoEffects(t *testing.T) {
	for _, mode := range []string{"original", "guarded-absent", "changed-boot", "unknown-boot", "replacement", "namespace-changed"} {
		t.Run(mode, func(t *testing.T) {
			p := NICLinkCleanup{Version: 1, BootID: uuid.NewString(), NamespaceDevice: 1, NamespaceInode: 23, Index: 17, Name: "original", Kind: "veth", HardwareAddr: "00:01:02:03:04:05", Alias: "incus-ovn-" + uuid.NewString()}
			mac, err := net.ParseMAC(p.HardwareAddr)
			require.NoError(t, err)
			original := &netlink.GenericLink{LinkAttrs: netlink.LinkAttrs{Index: p.Index, Name: p.Name, HardwareAddr: mac, Alias: p.Alias}, LinkType: p.Kind}
			links := []netlink.Link{original}
			boot, namespace := p.BootID, p.NamespaceInode
			switch mode {
			case "guarded-absent":
				links = nil
			case "changed-boot":
				boot = uuid.NewString()
			case "unknown-boot":
				boot = "unknown"
			case "replacement":
				original.Alias = "foreign"
			case "namespace-changed":
				namespace++
			}

			ops := nicLinkCleanupOps{root: func() (string, uint64, uint64, error) { return boot, 1, namespace, nil }, byIndex: func(int) (netlink.Link, error) {
				if len(links) == 0 {
					return nil, netlink.LinkNotFoundError{}
				}

				return links[0], nil
			}, list: func() ([]netlink.Link, error) { return links, nil }, remove: func(netlink.Link) error { t.Fatal("preflight issued delete"); return nil }, alias: func(netlink.Link, string) error { t.Fatal("preflight issued marker write"); return nil }}
			_, err = verifyNICVirtualLinkCleanup(ops, p)
			if mode == "original" || mode == "guarded-absent" || mode == "changed-boot" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestNICLinkCleanupMACBoundIntent(t *testing.T) {
	for _, mode := range []string{"unmarked-original", "lost-create-reply", "failed-create-foreign", "wrong-mac", "wrong-root", "wrong-type", "foreign-alias", "replaced-before-mark", "lost-mark-reply", "mark-refused", "replacement-at-mark", "replaced-after-capture", "legacy-unmarked"} {
		t.Run(mode, func(t *testing.T) {
			intent := NICLinkCleanup{Version: 1, BootID: uuid.NewString(), NamespaceDevice: 1, NamespaceInode: 23, Name: "original", Kind: "veth", HardwareAddr: "02:01:02:03:04:05", Alias: "incus-ovn-" + uuid.NewString()}
			if mode == "legacy-unmarked" {
				intent.HardwareAddr = ""
			}

			var links []netlink.Link
			marks, removes := 0, 0
			ops := nicLinkCleanupOps{
				root: func() (string, uint64, uint64, error) {
					if mode == "wrong-root" {
						return intent.BootID, intent.NamespaceDevice, intent.NamespaceInode + 1, nil
					}

					return intent.BootID, intent.NamespaceDevice, intent.NamespaceInode, nil
				},
				byName: func(string) (netlink.Link, error) {
					if len(links) == 0 {
						return nil, netlink.LinkNotFoundError{}
					}

					return links[0], nil
				},
				byIndex: func(index int) (netlink.Link, error) {
					if len(links) == 0 {
						return nil, netlink.LinkNotFoundError{}
					}

					if mode == "replaced-before-mark" && marks == 0 {
						links[0].Attrs().HardwareAddr = net.HardwareAddr{2, 9, 9, 9, 9, 9}
					}

					if links[0].Attrs().Index != index {
						return nil, netlink.LinkNotFoundError{}
					}

					return links[0], nil
				},
				alias: func(link netlink.Link, alias string) error {
					marks++
					if mode == "mark-refused" {
						return errors.New("mark refused")
					}

					if mode == "replacement-at-mark" {
						link.Attrs().HardwareAddr = net.HardwareAddr{2, 9, 9, 9, 9, 9}
					}

					link.Attrs().Alias = alias
					if mode == "lost-mark-reply" {
						return errors.New("mark reply lost")
					}

					return nil
				},
				list: func() ([]netlink.Link, error) { return links, nil },
				remove: func(netlink.Link) error {
					removes++
					links = nil
					return nil
				},
			}

			plan, err := createNICLinkCleanup(ops, intent, func() error {
				links = []netlink.Link{&netlink.GenericLink{LinkAttrs: netlink.LinkAttrs{Index: 17, Name: intent.Name, HardwareAddr: net.HardwareAddr{2, 1, 2, 3, 4, 5}}, LinkType: intent.Kind}}
				switch mode {
				case "wrong-mac", "failed-create-foreign":
					links[0].Attrs().HardwareAddr = net.HardwareAddr{2, 8, 8, 8, 8, 8}
				case "wrong-type":
					links[0].(*netlink.GenericLink).LinkType = "dummy"
				case "foreign-alias":
					links[0].Attrs().Alias = "foreign"
				}

				if mode == "lost-create-reply" || mode == "failed-create-foreign" {
					return errors.New("create reply lost")
				}

				return nil
			})
			success := mode == "unmarked-original" || mode == "lost-mark-reply" || mode == "replaced-after-capture"
			if success {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			if success || mode == "lost-create-reply" {
				require.NoError(t, plan.Validate())
				require.Equal(t, 1, marks)
				switch mode {
				case "replaced-after-capture":
					links[0].Attrs().Alias = "foreign"
					require.Error(t, applyNICLinkCleanup(ops, plan))
					require.Zero(t, removes)
				default:
					require.NoError(t, applyNICLinkCleanup(ops, plan))
					require.Equal(t, 1, removes)
				}
			} else if mode == "mark-refused" || mode == "replacement-at-mark" {
				require.Equal(t, 1, marks)
				require.Error(t, applyNICLinkCleanup(ops, plan))
				require.Zero(t, removes)
				require.Len(t, links, 1)
			} else {
				require.Zero(t, marks)
				require.Zero(t, removes)
			}
		})
	}
}

func TestNICClaimFromEarlierBoot(t *testing.T) {
	boot, err := CurrentBootID()
	require.NoError(t, err)

	earlier, err := NICClaimFromEarlierBoot(NICLinkCleanup{})
	require.NoError(t, err)
	require.False(t, earlier, "a claim without a recorded boot is never treated as discarded")

	earlier, err = NICClaimFromEarlierBoot(NICLinkCleanup{BootID: boot})
	require.NoError(t, err)
	require.False(t, earlier)

	earlier, err = NICClaimFromEarlierBoot(NICLinkCleanup{BootID: "00000000-0000-4000-8000-000000000001"})
	require.NoError(t, err)
	require.True(t, earlier)
}
