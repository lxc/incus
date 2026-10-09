//go:build linux

package ip

import (
	"errors"
	"fmt"
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
)

// Run only in an independently owned user/network namespace; never use the host namespace.
func TestNICLinkCleanupPrivateKernelAllocation(t *testing.T) {
	if os.Getenv("INCUS_NIC_PRIVATE_KERNEL_TEST") != "1" {
		t.Skip("requires isolated user/network namespace runner")
	}

	ns, err := os.Readlink("/proc/self/ns/net")
	require.NoError(t, err)
	require.Equal(t, os.Getenv("INCUS_NIC_PRIVATE_KERNEL_NAMESPACE"), ns)
	require.NotEqual(t, os.Getenv("INCUS_NIC_PARENT_KERNEL_NAMESPACE"), ns)
	for _, kind := range []string{"veth", "tuntap"} {
		t.Run(kind, func(t *testing.T) {
			intent, err := NewNICLinkCleanupIntent(map[string]string{"veth": "incusclaim0", "tuntap": "incustap0"}[kind], kind)
			require.NoError(t, err)
			plan, err := CreateNICLinkCleanup(intent, func() error {
				if kind == "veth" {
					address, err := net.ParseMAC(intent.HardwareAddr)
					if err != nil {
						return err
					}

					veth := &Veth{Link: Link{Name: intent.Name, Alias: intent.Alias, Address: address}, Peer: Link{Name: "incuspeer0"}}
					return veth.Add()
				}

				return (&Tuntap{Name: intent.Name, Alias: intent.Alias, Exclusive: true, Mode: "tap"}).Add()
			})
			link, lookupErr := netlink.LinkByName(intent.Name)
			if lookupErr == nil {
				t.Logf("intent=%+v actual=%+v kind=%s", intent, link.Attrs(), link.Type())
			}

			require.NoError(t, err)
			require.NoError(t, plan.Validate())
			require.Equal(t, intent.Alias, plan.Alias)
			require.NoError(t, VerifyNICVirtualLinkCleanup(plan))
			require.NoError(t, ApplyNICLinkCleanup(plan))
			require.NoError(t, ApplyNICLinkCleanup(plan))
		})
	}
}

func TestNICLinkCleanupPrivateKernelIntentRecovery(t *testing.T) {
	if os.Getenv("INCUS_NIC_PRIVATE_KERNEL_TEST") != "1" {
		t.Skip("requires isolated user/network namespace runner")
	}

	ns, err := os.Readlink("/proc/self/ns/net")
	require.NoError(t, err)
	require.Equal(t, os.Getenv("INCUS_NIC_PRIVATE_KERNEL_NAMESPACE"), ns)
	require.NotEqual(t, os.Getenv("INCUS_NIC_PARENT_KERNEL_NAMESPACE"), ns)
	for index, mode := range []string{"original-unmarked-recovery", "legacy-unmarked", "wrong-host-mac", "changed-alias", "lost-create-reply"} {
		t.Run(mode, func(t *testing.T) {
			intent, err := NewNICLinkCleanupIntent(fmt.Sprintf("intent%d", index), "veth")
			require.NoError(t, err)
			address, err := net.ParseMAC(intent.HardwareAddr)
			require.NoError(t, err)
			create := func() error {
				veth := &Veth{Link: Link{Name: intent.Name, Address: address}, Peer: Link{Name: fmt.Sprintf("intentpeer%d", index)}}
				return veth.Add()
			}

			if mode == "lost-create-reply" {
				plan, err := CreateNICLinkCleanup(intent, func() error {
					err := create()
					if err != nil {
						return err
					}

					return errors.New("injected lost create reply")
				})
				require.ErrorContains(t, err, "injected lost create reply")
				require.NoError(t, plan.Validate())
				require.NoError(t, ApplyNICLinkCleanup(plan))
				return
			}

			require.NoError(t, create())
			link, err := netlink.LinkByName(intent.Name)
			require.NoError(t, err)
			require.Empty(t, link.Attrs().Alias)
			switch mode {
			case "legacy-unmarked":
				intent.HardwareAddr = ""
			case "wrong-host-mac":
				require.NoError(t, netlink.LinkSetHardwareAddr(link, net.HardwareAddr{2, 9, 9, 9, 9, 9}))
			case "changed-alias":
				require.NoError(t, netlink.LinkSetAlias(link, "foreign-private"))
			}

			err = ApplyNICLinkCleanup(intent)
			if mode == "original-unmarked-recovery" {
				require.NoError(t, err)
				_, err = netlink.LinkByName(intent.Name)
				require.Error(t, err)
			} else {
				require.Error(t, err)
				preserved, err := netlink.LinkByIndex(link.Attrs().Index)
				require.NoError(t, err)
				require.Equal(t, intent.Name, preserved.Attrs().Name)
			}
		})
	}
}
