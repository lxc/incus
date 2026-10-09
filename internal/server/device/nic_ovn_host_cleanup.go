package device

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/lxc/incus/v7/internal/server/db"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/ip"
	"github.com/lxc/incus/v7/internal/server/network"
	"github.com/lxc/incus/v7/internal/server/network/ovs"
	"github.com/lxc/incus/v7/shared/util"
)

// nicOVNPhysicalCleanup binds a VF restore to the original PF/VF and representor.
type nicOVNPhysicalCleanup struct {
	Version      int
	NetworkID    int64
	SourceNodeID int64
	InstanceUUID string
	InstanceID   int
	DeviceName   string
	Mode         string
	Parent       string
	VFID         int
	PFPath       string
	PFInode      uint64
	VFPath       string
	VFInode      uint64
	Representor  ip.NICLinkCleanup
	VDPAName     string
	VDPAInode    uint64
}

func nicOVNPhysicalPaths(parent string, id int) (string, uint64, string, uint64, error) {
	if parent == "" || filepath.Base(parent) != parent || id < 0 {
		return "", 0, "", 0, errors.New("Original PF/VF identity is incomplete")
	}

	pf, err := filepath.EvalSymlinks(filepath.Join("/sys/class/net", parent, "device"))
	if err != nil {
		return "", 0, "", 0, err
	}

	vf, err := filepath.EvalSymlinks(filepath.Join(pf, fmt.Sprintf("virtfn%d", id)))
	if err != nil {
		return "", 0, "", 0, err
	}

	var pfStat, vfStat syscall.Stat_t
	err = syscall.Stat(pf, &pfStat)
	if err == nil {
		err = syscall.Stat(vf, &vfStat)
	}

	return pf, pfStat.Ino, vf, vfStat.Ino, err
}

func nicOVNVDPAInode(name string) (uint64, error) {
	if name == "" || filepath.Base(name) != name {
		return 0, errors.New("Original vDPA device identity is missing")
	}

	var stat syscall.Stat_t
	err := syscall.Stat(filepath.Join("/sys/bus/vdpa/devices", name), &stat)
	return stat.Ino, err
}

func (d *nicOVN) physicalAliasCompleted(plan nicOVNPhysicalCleanup, alias string) (bool, error) {
	if alias == "" {
		return false, nil
	}

	var found bool
	err := d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		rows, err := tx.Tx().QueryContext(ctx, `SELECT payload FROM networks_ovn_nic_cleanup WHERE source_node_id=? AND completed=1 AND payload LIKE ?`, d.state.DB.Cluster.GetNodeID(), "%"+alias+"%")
		if err != nil {
			return err
		}

		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var payload string
			err = rows.Scan(&payload)
			if err != nil {
				return err
			}

			var source struct{ HostVolatile map[string]string }
			err = json.Unmarshal([]byte(payload), &source)
			if err != nil {
				return err
			}

			var prior nicOVNPhysicalCleanup
			raw := source.HostVolatile["last_state.ovn.physical"]
			if raw == "" {
				continue
			}

			err = json.Unmarshal([]byte(raw), &prior)
			if err != nil {
				return err
			}

			if prior.Representor.Alias == alias && prior.PFPath == plan.PFPath && prior.PFInode == plan.PFInode && prior.VFPath == plan.VFPath && prior.VFInode == plan.VFInode && prior.Representor.Name == plan.Representor.Name {
				err = ip.VerifyNICLinkCleanup(prior.Representor)
				if err != nil {
					return err
				}

				found = true
			}
		}

		return rows.Err()
	})
	return found, err
}

func (d *nicOVN) capturePhysicalHost(volatile map[string]string, representor string, fresh bool) error {
	network.SRIOVVirtualFunctionMutex.Lock()
	defer network.SRIOVVirtualFunctionMutex.Unlock()
	return d.capturePhysicalHostLocked(volatile, representor, fresh)
}

// physicalClaimFromEarlierBoot reports whether the recorded claim of this exact original allocation
// comes from a positively earlier kernel boot. That reboot already reset the VF settings, the
// representor marker and any vDPA device, so no host restore remains, as upstream assumes after a
// host crash. Only the OVS and OVN cleanup of the original port remains.
func (d *nicOVN) physicalClaimFromEarlierBoot(volatile map[string]string) (bool, error) {
	var p nicOVNPhysicalCleanup
	err := json.Unmarshal([]byte(volatile["last_state.ovn.physical"]), &p)
	if err != nil {
		return false, fmt.Errorf("Original physical NIC cleanup identity is missing: %w", err)
	}

	id, err := strconv.Atoi(volatile["last_state.vf.id"])
	if err != nil {
		return false, err
	}

	if p.Version != 1 || p.NetworkID != d.network.ID() || p.SourceNodeID != d.state.DB.Cluster.GetNodeID() || p.InstanceUUID != d.inst.LocalConfig()["volatile.uuid"] || p.InstanceID != d.inst.ID() || p.DeviceName != d.name || p.Mode != d.config["acceleration"] || p.Parent != volatile["last_state.vf.parent"] || p.VFID != id {
		return false, errors.New("Original physical cleanup allocation disagrees with source")
	}

	return ip.NICClaimFromEarlierBoot(p.Representor)
}

func (d *nicOVN) capturePhysicalHostLocked(volatile map[string]string, representor string, fresh bool) error {
	if volatile["last_state.ovn.physical"] != "" && !fresh {
		earlier, err := d.physicalClaimFromEarlierBoot(volatile)
		if err != nil {
			return err
		}

		if earlier {
			return nil
		}
	}

	id, err := strconv.Atoi(volatile["last_state.vf.id"])
	if err != nil {
		return err
	}

	parent := volatile["last_state.vf.parent"]
	pf, pfi, vf, vfi, err := nicOVNPhysicalPaths(parent, id)
	if err != nil {
		return err
	}

	plan := nicOVNPhysicalCleanup{Version: 1, NetworkID: d.network.ID(), SourceNodeID: d.state.DB.Cluster.GetNodeID(), InstanceUUID: d.inst.LocalConfig()["volatile.uuid"], InstanceID: d.inst.ID(), DeviceName: d.name, Mode: d.config["acceleration"], Parent: parent, VFID: id, PFPath: pf, PFInode: pfi, VFPath: vf, VFInode: vfi}
	var expected *ip.NICLinkCleanup
	raw := volatile["last_state.ovn.physical"]
	if raw != "" && !fresh {
		var prior nicOVNPhysicalCleanup
		err = json.Unmarshal([]byte(raw), &prior)
		if err != nil {
			return err
		}

		if prior.Version != 1 || prior.NetworkID != plan.NetworkID || prior.SourceNodeID != plan.SourceNodeID || prior.InstanceUUID != plan.InstanceUUID || prior.InstanceID != plan.InstanceID || prior.DeviceName != plan.DeviceName || prior.Mode != plan.Mode || prior.Parent != parent || prior.VFID != id || prior.PFPath != pf || prior.PFInode != pfi || prior.VFPath != vf || prior.VFInode != vfi {
			return errors.New("Original physical NIC allocation changed")
		}

		expected = &prior.Representor
		plan.VDPAName, plan.VDPAInode = prior.VDPAName, prior.VDPAInode
	}

	info, err := ip.LinkByName(representor)
	if err != nil {
		return err
	}

	plan.Representor.Name = info.Name
	// Reusing an old marker requires its full original terminal receipt.
	var reuse bool
	if expected == nil && fresh {
		alias, err := os.ReadFile(filepath.Join("/sys/class/net", representor, "ifalias"))
		if err != nil {
			return err
		}

		reuse, err = d.physicalAliasCompleted(plan, strings.TrimSpace(string(alias)))
		if err != nil {
			return err
		}
	}

	plan.Representor, err = ip.CaptureNICPhysicalLinkCleanup(representor, expected, reuse)
	if err != nil {
		return err
	}

	if plan.Mode == "vdpa" && volatile["last_state.vdpa.name"] != "" {
		name := volatile["last_state.vdpa.name"]
		inode, err := nicOVNVDPAInode(name)
		if err != nil {
			return err
		}

		if expected != nil && plan.VDPAName != "" && (plan.VDPAName != name || plan.VDPAInode != inode) {
			return errors.New("Original vDPA allocation changed")
		}

		plan.VDPAName, plan.VDPAInode = name, inode
	}

	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}

	volatile["last_state.ovn.physical"] = string(data)
	return d.volatileSet(volatile)
}

type nicOVNPhysicalOps struct {
	paths              func(string, int) (string, uint64, string, uint64, error)
	verify             func(ip.NICLinkCleanup) error
	vdpaInode          func(string) (uint64, error)
	deleteVDPA         func(string) error
	restore            func(map[string]string) error
	acknowledgeRestore func(nicOVNPhysicalCleanup, map[string]string) error
}

func (d *nicOVN) postStopPhysical(volatile map[string]string) error {
	return d.postStopPhysicalWithOps(volatile, d.physicalCleanupOps())
}

func (d *nicOVN) physicalCleanupOps() nicOVNPhysicalOps {
	return nicOVNPhysicalOps{
		paths: nicOVNPhysicalPaths, verify: ip.VerifyNICLinkCleanup,
		vdpaInode: nicOVNVDPAInode, deleteVDPA: ip.DeleteVDPADevice,
		restore:            func(v map[string]string) error { return networkSRIOVRestoreVF(d.deviceCommon, false, v) },
		acknowledgeRestore: d.acknowledgePhysicalRestore,
	}
}

func (d *nicOVN) postStopPhysicalWithOps(volatile map[string]string, ops nicOVNPhysicalOps) error {
	network.SRIOVVirtualFunctionMutex.Lock()
	defer network.SRIOVVirtualFunctionMutex.Unlock()
	return d.postStopPhysicalLocked(volatile, ops)
}

func (d *nicOVN) postStopPhysicalLocked(volatile map[string]string, ops nicOVNPhysicalOps) error {
	earlier, err := d.physicalClaimFromEarlierBoot(volatile)
	if err != nil {
		return err
	}

	if earlier {
		return nil
	}

	var p nicOVNPhysicalCleanup
	err = json.Unmarshal([]byte(volatile["last_state.ovn.physical"]), &p)
	if err != nil {
		return fmt.Errorf("Original physical NIC cleanup identity is missing: %w", err)
	}

	id, err := strconv.Atoi(volatile["last_state.vf.id"])
	if err != nil {
		return err
	}

	if p.Version != 1 || p.NetworkID != d.network.ID() || p.SourceNodeID != d.state.DB.Cluster.GetNodeID() || p.InstanceUUID != d.inst.LocalConfig()["volatile.uuid"] || p.InstanceID != d.inst.ID() || p.DeviceName != d.name || p.Mode != d.config["acceleration"] || p.Parent != volatile["last_state.vf.parent"] || p.VFID != id {
		return errors.New("Original physical cleanup allocation disagrees with source")
	}

	pf, pfi, vf, vfi, err := ops.paths(p.Parent, p.VFID)
	if err != nil {
		return err
	}

	if pf != p.PFPath || pfi != p.PFInode || vf != p.VFPath || vfi != p.VFInode {
		return errors.New("Original PF/VF PCI identity changed; preserving replacement")
	}

	err = ops.verify(p.Representor)
	if err != nil {
		return err
	}

	if p.Mode == "vdpa" && (p.VDPAName != "" || volatile["last_state.vdpa.name"] != "") {
		if p.VDPAName != volatile["last_state.vdpa.name"] || p.VDPAInode == 0 {
			return errors.New("Original vDPA identity disagrees with source")
		}

		inode, readErr := ops.vdpaInode(p.VDPAName)
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}

		if readErr == nil {
			if inode != p.VDPAInode {
				return errors.New("Original vDPA device replaced; preserving replacement")
			}

			deleteErr := ops.deleteVDPA(p.VDPAName)
			_, readErr = ops.vdpaInode(p.VDPAName)
			if !os.IsNotExist(readErr) {
				return errors.Join(deleteErr, errors.New("Original vDPA delete lacks absence acknowledgment"), readErr)
			}
		}
	}

	err = ops.restore(volatile)
	if err != nil {
		return err
	}

	err = ops.acknowledgeRestore(p, volatile)
	if err != nil {
		return err
	}

	return ops.verify(p.Representor)
}

func (d *nicOVN) acknowledgePhysicalRestore(p nicOVNPhysicalCleanup, volatile map[string]string) error {
	// Verify the restored interface belongs to this exact original VF PCI device.
	host := volatile["host_name"]
	target, err := filepath.EvalSymlinks(filepath.Join("/sys/class/net", host, "device"))
	if err != nil {
		return err
	}

	if target != p.VFPath {
		return errors.New("Restored VF interface has a different PCI identity")
	}

	link := &ip.Link{Name: host}
	err = link.SetDown()
	if err != nil {
		return err
	}

	info, err := ip.LinkByName(host)
	if err != nil {
		return err
	}

	if info.Up {
		return errors.New("Original restored VF remains up")
	}

	if volatile["last_state.hwaddr"] != "" && info.Address.String() != volatile["last_state.hwaddr"] {
		return errors.New("Original VF MAC restore was not acknowledged")
	}

	{
		value := volatile["last_state.mtu"]
		if value != "" {
			mtu, err := strconv.ParseUint(value, 10, 32)
			if err != nil || uint32(mtu) != info.MTU {
				return errors.New("Original VF MTU restore was not acknowledged")
			}
		}
	}

	vfInfo, err := (&ip.Link{Name: p.Parent}).GetVFInfo(p.VFID)
	if err != nil {
		return err
	}

	{
		value := volatile["last_state.vf.hwaddr"]
		if value != "" && vfInfo.Address.String() != value {
			return errors.New("Original PF/VF MAC restore was not acknowledged")
		}
	}

	{
		value := volatile["last_state.vf.vlan"]
		if value != "" {
			vlan, err := strconv.Atoi(value)
			if err != nil || vlan != vfInfo.VLAN {
				return errors.New("Original VF VLAN restore was not acknowledged")
			}
		}
	}

	{
		value := volatile["last_state.vf.spoofcheck"]
		if value != "" && vfInfo.SpoofCheck != util.IsTrue(value) {
			return errors.New("Original VF spoof-check restore was not acknowledged")
		}
	}

	{
		value := volatile["last_state.vf.trusted"]
		if value != "" && vfInfo.Trusted != boolToUint(util.IsTrue(value)) {
			return errors.New("Original VF trust restore was not acknowledged")
		}
	}

	if d.inst.Type() == instancetype.VM {
		driver, err := filepath.EvalSymlinks(filepath.Join(p.VFPath, "driver"))
		if err != nil {
			return err
		}

		if filepath.Base(driver) != volatile["last_state.pci.driver"] {
			return errors.New("Original VF PCI driver restore was not acknowledged")
		}
	}

	return nil
}

func boolToUint(value bool) uint32 {
	if value {
		return 1
	}

	return 0
}

// rollbackPhysicalHost retires only a positively restored, never-published Start claim.
func (d *nicOVN) rollbackPhysicalHost(volatile map[string]string, backendAttempted bool) error {
	return d.rollbackPhysicalHostWithOps(volatile, backendAttempted, d.physicalCleanupOps(), ip.ReleaseNICPhysicalLinkCleanup, d.retireNICPreclaim)
}

func (d *nicOVN) retireNICPreclaim(v map[string]string) error {
	if d.nicMigrationOperation() != "" {
		capability, ok := d.inst.(interface {
			OVNNICMigrationRetire(string, map[string]string) error
		})
		if !ok {
			return errors.New("Migration target retirement capability is missing")
		}

		return capability.OVNNICMigrationRetire(d.name, v)
	}

	err := d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.RetireOVNNICPreclaim(ctx, d.inst.ID(), d.inst.LocalConfig()["volatile.uuid"], d.name, v)
	})
	if err != nil {
		readErr := d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.EnsureOVNNICPreclaimRetired(ctx, d.inst.ID(), d.inst.LocalConfig()["volatile.uuid"], d.name)
		})
		if readErr != nil {
			return errors.Join(err, readErr)
		}
	}

	syncSource, ok := d.inst.(interface {
		OVNStopCleanupVolatileRetired(string, string, map[string]string) error
	})
	if !ok {
		return errors.New("Driver cannot synchronize original NIC preclaim retirement")
	}

	return syncSource.OVNStopCleanupVolatileRetired(d.inst.LocalConfig()["volatile.uuid"], d.name, maps.Clone(v))
}

func (d *nicOVN) rollbackPhysicalHostWithOps(volatile map[string]string, backendAttempted bool, ops nicOVNPhysicalOps, release func(ip.NICLinkCleanup) error, retire func(map[string]string) error) error {
	network.SRIOVVirtualFunctionMutex.Lock()
	defer network.SRIOVVirtualFunctionMutex.Unlock()
	earlier, err := d.physicalClaimFromEarlierBoot(volatile)
	if err != nil {
		return err
	}

	err = d.postStopPhysicalLocked(volatile, ops)
	if err != nil || backendAttempted {
		return err
	}

	if earlier {
		return retire(volatile)
	}

	var original nicOVNPhysicalCleanup
	err = json.Unmarshal([]byte(volatile["last_state.ovn.physical"]), &original)
	if err != nil {
		return err
	}

	err = release(original.Representor)
	if err != nil {
		return err
	}

	return retire(volatile)
}

type nicOVNVirtualClaim struct {
	ip.NICLinkCleanup
	NetworkID    int64
	SourceNodeID int64
	InstanceUUID string
	InstanceID   int
	DeviceName   string
}

func (d *nicOVN) encodeVirtualHostClaim(plan ip.NICLinkCleanup) (string, error) {
	claim := nicOVNVirtualClaim{NICLinkCleanup: plan, NetworkID: d.network.ID(), SourceNodeID: d.state.DB.Cluster.GetNodeID(), InstanceUUID: d.inst.LocalConfig()["volatile.uuid"], InstanceID: d.inst.ID(), DeviceName: d.name}
	raw, err := json.Marshal(claim)
	return string(raw), err
}

func (d *nicOVN) persistVirtualHostIntent(v map[string]string, kind string) (ip.NICLinkCleanup, error) {
	intent, err := ip.NewNICLinkCleanupIntent(v["host_name"], kind)
	if err != nil {
		return intent, err
	}

	raw, err := d.encodeVirtualHostClaim(intent)
	if err != nil {
		return intent, err
	}

	v["last_state.ovn.host"] = raw
	return intent, d.volatileSet(v)
}

func (d *nicOVN) nicMigrationOperation() string {
	capability, ok := d.inst.(interface{ OVNNICMigrationOperation() string })
	if !ok {
		return ""
	}

	return capability.OVNNICMigrationOperation()
}

func (d *nicOVN) rollbackMigrationShared(operation string) error {
	capability, ok := d.network.(interface {
		InstanceDevicePortMigrationRollback(context.Context, string, string) error
	})
	if !ok {
		return errors.New("Migration shared rollback capability is missing")
	}

	return capability.InstanceDevicePortMigrationRollback(d.state.ShutdownCtx, operation, d.name)
}

// stopMigrationTarget cleans only its staged allocation before state restore begins.
// After restore dispatch, unknown or positive handover keeps the stage quarantined.
func (d *nicOVN) stopMigrationTarget(operation string) (*deviceConfig.RunConfig, error) {
	capability, ok := d.inst.(interface{ OVNNICMigrationCanRollback() bool })
	if !ok || !capability.OVNNICMigrationCanRollback() {
		return nil, errors.New("Migration state restore was dispatched; target cleanup requires a positive outcome")
	}

	var m db.OVNNICMigration
	err := d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		m, err = tx.OVNNICMigrationDevice(ctx, operation, d.name)
		return err
	})
	if err != nil {
		return nil, err
	}

	if m.TargetNodeID != d.state.DB.Cluster.GetNodeID() || m.InstanceID != d.inst.ID() || m.InstanceUUID != d.inst.LocalConfig()["volatile.uuid"] || m.Phase != "authorized" {
		return nil, errors.New("Migration target Stop authority changed")
	}

	if len(m.TargetVolatile) == 0 && m.SharedPlan == "" && !m.OVSAttempted {
		return &deviceConfig.RunConfig{}, nil
	}

	var stopErr error
	if m.OVSAttempted {
		if m.TargetOVS == "" {
			stopErr = errors.New("Migration target OVS attempt has no exact receipt")
		} else {
			var plan ovs.NICPortCleanup
			stopErr = json.Unmarshal([]byte(m.TargetOVS), &plan)
			if stopErr == nil {
				var vswitch *ovs.VSwitch
				vswitch, stopErr = d.state.OVS()
				if stopErr == nil {
					stopErr = vswitch.ApplyNICPortCleanup(d.state.ShutdownCtx, plan)
				}
			}

			if stopErr == nil {
				writer, ok := d.inst.(interface {
					OVNNICMigrationOVS(string, *ovs.NICPortCleanup, bool) error
				})
				if !ok {
					stopErr = errors.New("Migration OVS receipt writer missing")
				} else {
					stopErr = writer.OVNNICMigrationOVS(d.name, nil, false)
				}
			}
		}
	}

	stopErr = errors.Join(stopErr, d.rollbackMigrationShared(operation))
	stopErr = errors.Join(stopErr, nicOVNStopBGP(m.InstanceID, d.name, d.state.BGP.RemovePrefixByOwner))
	selected := *d
	selected.config = d.config.Clone()
	selected.config["host_name"] = m.TargetVolatile["host_name"]
	return &deviceConfig.RunConfig{PostHooks: []func() error{nicOVNStopPostHook(stopErr, func() error { return selected.postStopOriginal(m.TargetVolatile) }, func() error { return d.retireNICPreclaim(m.TargetVolatile) })}}, nil
}

func (d *nicOVN) verifyOriginalPhysicalHost(v map[string]string) error {
	network.SRIOVVirtualFunctionMutex.Lock()
	defer network.SRIOVVirtualFunctionMutex.Unlock()
	earlier, err := d.physicalClaimFromEarlierBoot(v)
	if err != nil {
		return err
	}

	if earlier {
		return nil
	}

	var p nicOVNPhysicalCleanup
	err = json.Unmarshal([]byte(v["last_state.ovn.physical"]), &p)
	if err != nil {
		return err
	}

	id, err := strconv.Atoi(v["last_state.vf.id"])
	if err != nil {
		return err
	}

	if p.Version != 1 || p.NetworkID != d.network.ID() || p.SourceNodeID != d.state.DB.Cluster.GetNodeID() || p.InstanceUUID != d.inst.LocalConfig()["volatile.uuid"] || p.InstanceID != d.inst.ID() || p.DeviceName != d.name || p.Mode != d.config["acceleration"] || p.Parent != v["last_state.vf.parent"] || p.VFID != id {
		return errors.New("Original physical retry allocation changed")
	}

	pf, pfi, vf, vfi, err := nicOVNPhysicalPaths(p.Parent, p.VFID)
	if err != nil {
		return err
	}

	if pf != p.PFPath || pfi != p.PFInode || vf != p.VFPath || vfi != p.VFInode {
		return errors.New("Original physical retry PCI identity changed")
	}

	if p.Mode == "vdpa" && (p.VDPAName != "" || v["last_state.vdpa.name"] != "") {
		if p.VDPAName != v["last_state.vdpa.name"] || p.VDPAInode == 0 {
			return errors.New("Original retry vDPA identity changed")
		}

		inode, err := nicOVNVDPAInode(p.VDPAName)
		if err != nil && !os.IsNotExist(err) {
			return err
		}

		if err == nil && inode != p.VDPAInode {
			return errors.New("Original retry vDPA allocation changed")
		}
	}

	return ip.VerifyNICLinkCleanup(p.Representor)
}
