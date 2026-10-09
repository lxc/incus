//go:build linux && cgo && !agent

package device

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"

	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/ip"
)

// The caller runner creates a private user/network namespace before starting this test binary.
func TestNICOVNPrivateKernelVethCaller(t *testing.T) {
	if os.Getenv("INCUS_NIC_PRIVATE_KERNEL_TEST") != "1" {
		t.Skip("requires isolated user/network namespace runner")
	}

	ns, err := os.Readlink("/proc/self/ns/net")
	require.NoError(t, err)
	require.Equal(t, os.Getenv("INCUS_NIC_PRIVATE_KERNEL_NAMESPACE"), ns)
	require.NotEqual(t, os.Getenv("INCUS_NIC_PARENT_KERNEL_NAMESPACE"), ns)
	intent, err := ip.NewNICLinkCleanupIntent("callerclaim0", "veth")
	require.NoError(t, err)
	guest := "00:16:3e:01:02:03"
	var peer string
	plan, err := ip.CreateNICLinkCleanup(intent, func() error {
		var mtu uint32
		var err error
		peer, mtu, err = networkCreateVethPair(intent.Name, deviceConfig.Device{"hwaddr": guest, "mtu": "1450"}, &intent)
		if err == nil {
			require.Equal(t, uint32(1450), mtu)
		}

		return err
	})
	require.NoError(t, err)
	host, err := netlink.LinkByIndex(plan.Index)
	require.NoError(t, err)
	require.Equal(t, intent.HardwareAddr, host.Attrs().HardwareAddr.String())
	require.Equal(t, intent.Alias, host.Attrs().Alias)
	guestLink, err := netlink.LinkByName(peer)
	require.NoError(t, err)
	require.Equal(t, guest, guestLink.Attrs().HardwareAddr.String())
	require.Equal(t, 1450, host.Attrs().MTU)
	require.NoError(t, ip.VerifyNICVirtualLinkCleanup(plan))
	require.NoError(t, ip.ApplyNICLinkCleanup(plan))
	_, err = netlink.LinkByName(peer)
	require.Error(t, err)
	require.NoError(t, ip.ApplyNICLinkCleanup(plan))
}
