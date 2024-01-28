package qmp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/shared/api"
)

// FdsetFdInfo contains information about a file descriptor that belongs to an FD set.
type FdsetFdInfo struct {
	FD     int    `json:"fd"`
	Opaque string `json:"opaque"`
}

// FdsetInfo contains information about an FD set.
type FdsetInfo struct {
	ID  int           `json:"fdset-id"`
	FDs []FdsetFdInfo `json:"fds"`
}

// AddFdInfo contains information about a file descriptor that was added to an fd set.
type AddFdInfo struct {
	ID int `json:"fdset-id"`
	FD int `json:"fd"`
}

// CPUInstanceProperties contains CPU instance properties.
type CPUInstanceProperties struct {
	NodeID    int `json:"node-id,omitempty"`
	SocketID  int `json:"socket-id,omitempty"`
	DieID     int `json:"die-id,omitempty"`
	ClusterID int `json:"cluster-id,omitempty"`
	CoreID    int `json:"core-id,omitempty"`
	ThreadID  int `json:"thread-id,omitempty"`
}

// CPU contains information about a CPU.
type CPU struct {
	Index    int    `json:"cpu-index,omitempty"`
	QOMPath  string `json:"qom-path,omitempty"`
	ThreadID int    `json:"thread-id,omitempty"`
	Target   string `json:"target,omitempty"`

	Props CPUInstanceProperties `json:"props"`
}

// HotpluggableCPU contains information about a hotpluggable CPU.
type HotpluggableCPU struct {
	Type       string `json:"type"`
	VCPUsCount int    `json:"vcpus-count"`
	QOMPath    string `json:"qom-path,omitempty"`

	Props CPUInstanceProperties `json:"props"`
}

// QueryCPUs returns a list of CPUs.
func (m *Monitor) QueryCPUs() ([]CPU, error) {
	// Prepare the response.
	var resp struct {
		Return []CPU `json:"return"`
	}

	err := m.run("query-cpus-fast", nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed to query CPUs: %w", err)
	}

	return resp.Return, nil
}

// QueryHotpluggableCPUs returns a list of hotpluggable CPUs.
func (m *Monitor) QueryHotpluggableCPUs() ([]HotpluggableCPU, error) {
	// Prepare the response.
	var resp struct {
		Return []HotpluggableCPU `json:"return"`
	}

	err := m.run("query-hotpluggable-cpus", nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed to query hotpluggable CPUs: %w", err)
	}

	return resp.Return, nil
}

// Status returns the current VM status.
func (m *Monitor) Status() (string, error) {
	// Prepare the response.
	var resp struct {
		Return struct {
			Status string `json:"status"`
		} `json:"return"`
	}

	// Query the status.
	err := m.run("query-status", nil, &resp)
	if err != nil {
		return "", err
	}

	return resp.Return.Status, nil
}

// SendFile adds a new file descriptor to the QMP fd table associated to name.
func (m *Monitor) SendFile(name string, file *os.File) error {
	// Check if disconnected.
	if m.disconnected {
		return ErrMonitorDisconnect
	}

	var req struct {
		Execute   string `json:"execute"`
		Arguments struct {
			FDName string `json:"fdname"`
		} `json:"arguments"`
	}

	req.Execute = "getfd"
	req.Arguments.FDName = name

	reqJSON, err := json.Marshal(req)
	if err != nil {
		return err
	}

	// Query the status.
	_, err = m.qmp.RunWithFile(reqJSON, file)
	if err != nil {
		// Confirm the daemon didn't die.
		errPing := m.ping()
		if errPing != nil {
			return errPing
		}

		return err
	}

	return nil
}

// CloseFile closes an existing file descriptor in the QMP fd table associated to name.
func (m *Monitor) CloseFile(name string) error {
	var req struct {
		FDName string `json:"fdname"`
	}

	req.FDName = name

	err := m.run("closefd", req, nil)
	if err != nil {
		return err
	}

	return nil
}

// SendFileWithFDSet adds a new file descriptor to an FD set.
func (m *Monitor) SendFileWithFDSet(name string, file *os.File, readonly bool) (*AddFdInfo, error) {
	// Check if disconnected.
	if m.disconnected {
		return nil, ErrMonitorDisconnect
	}

	var req struct {
		Execute   string `json:"execute"`
		Arguments struct {
			Opaque string `json:"opaque"`
		} `json:"arguments"`
	}

	permissions := "rdwr"
	if readonly {
		permissions = "rdonly"
	}

	req.Execute = "add-fd"
	req.Arguments.Opaque = fmt.Sprintf("%s:%s", permissions, name)

	reqJSON, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	ret, err := m.qmp.RunWithFile(reqJSON, file)
	if err != nil {
		// Confirm the daemon didn't die.
		errPing := m.ping()
		if errPing != nil {
			return nil, errPing
		}

		return nil, err
	}

	// Prepare the response.
	var resp struct {
		Return AddFdInfo `json:"return"`
	}

	err = json.Unmarshal(ret, &resp)
	if err != nil {
		return nil, err
	}

	return &resp.Return, nil
}

// RemoveFDFromFDSet removes an FD with the given name from an FD set.
func (m *Monitor) RemoveFDFromFDSet(name string) error {
	// Prepare the response.
	var resp struct {
		Return []FdsetInfo `json:"return"`
	}

	err := m.run("query-fdsets", nil, &resp)
	if err != nil {
		return fmt.Errorf("Failed to query fd sets: %w", err)
	}

	for _, fdSet := range resp.Return {
		for _, fd := range fdSet.FDs {
			fields := strings.SplitN(fd.Opaque, ":", 2)
			opaque := ""

			if len(fields) == 2 {
				opaque = fields[1]
			} else {
				opaque = fields[0]
			}

			if opaque == name {
				args := map[string]any{
					"fdset-id": fdSet.ID,
				}

				err = m.run("remove-fd", args, nil)
				if err != nil {
					return fmt.Errorf("Failed to remove fd from fd set: %w", err)
				}
			}
		}
	}

	return nil
}

// MigrateSetCapabilities sets the capabilities used during migration.
func (m *Monitor) MigrateSetCapabilities(caps map[string]bool) error {
	var args struct {
		Capabilities []struct {
			Capability string `json:"capability"`
			State      bool   `json:"state"`
		} `json:"capabilities"`
	}

	for capName, state := range caps {
		args.Capabilities = append(args.Capabilities, struct {
			Capability string `json:"capability"`
			State      bool   `json:"state"`
		}{
			Capability: capName,
			State:      state,
		})
	}

	err := m.run("migrate-set-capabilities", args, nil)
	if err != nil {
		return err
	}

	return nil
}

// Migrate starts a migration stream.
func (m *Monitor) Migrate(uri string) error {
	// Query the status.
	args := map[string]string{"uri": uri}
	err := m.run("migrate", args, nil)
	if err != nil {
		return err
	}

	return nil
}

// MigrateWait waits until migration job reaches the specified status.
// Returns nil if the migraton job reaches the specified status or an error if the migration job is in the failed
// status.
func (m *Monitor) MigrateWait(state string) error {
	// Wait until it completes or fails.
	for {
		// Prepare the response.
		var resp struct {
			Return struct {
				Status string `json:"status"`
			} `json:"return"`
		}

		err := m.run("query-migrate", nil, &resp)
		if err != nil {
			return err
		}

		if resp.Return.Status == "failed" {
			return fmt.Errorf("Migrate call failed")
		}

		if resp.Return.Status == state {
			return nil
		}

		time.Sleep(1 * time.Second)
	}
}

// MigrateContinue continues a migration stream.
func (m *Monitor) MigrateContinue(fromState string) error {
	var args struct {
		State string `json:"state"`
	}

	args.State = fromState

	err := m.run("migrate-continue", args, nil)
	if err != nil {
		return err
	}

	return nil
}

// MigrateIncoming starts the receiver of a migration stream.
func (m *Monitor) MigrateIncoming(ctx context.Context, uri string) error {
	// Query the status.
	args := map[string]string{"uri": uri}
	err := m.run("migrate-incoming", args, nil)
	if err != nil {
		return err
	}

	// Wait until it completes or fails.
	for {
		// Prepare the response.
		var resp struct {
			Return struct {
				Status string `json:"status"`
			} `json:"return"`
		}

		err := m.run("query-migrate", nil, &resp)
		if err != nil {
			return err
		}

		if resp.Return.Status == "failed" {
			return fmt.Errorf("Migrate incoming call failed")
		}

		if resp.Return.Status == "completed" {
			return nil
		}

		// Check context is cancelled last after checking job status.
		// This way if the context is cancelled when the migration stream is ended this gives a chance to
		// check for job success/failure before checking if the context has been cancelled.
		err = ctx.Err()
		if err != nil {
			return err
		}

		time.Sleep(1 * time.Second)
	}
}

// Powerdown tells the VM to gracefully shutdown.
func (m *Monitor) Powerdown() error {
	return m.run("system_powerdown", nil, nil)
}

// Start tells QEMU to start the emulation.
func (m *Monitor) Start() error {
	return m.run("cont", nil, nil)
}

// Pause tells QEMU to temporarily stop the emulation.
func (m *Monitor) Pause() error {
	return m.run("stop", nil, nil)
}

// Quit tells QEMU to exit immediately.
func (m *Monitor) Quit() error {
	return m.run("quit", nil, nil)
}

// GetCPUs fetches the vCPU information for pinning.
func (m *Monitor) GetCPUs() ([]int, error) {
	// Prepare the response.
	var resp struct {
		Return []struct {
			CPU int `json:"cpu-index"`
			PID int `json:"thread-id"`
		} `json:"return"`
	}

	// Query the consoles.
	err := m.run("query-cpus-fast", nil, &resp)
	if err != nil {
		return nil, err
	}

	// Make a slice of PIDs.
	pids := []int{}
	for _, cpu := range resp.Return {
		pids = append(pids, cpu.PID)
	}

	return pids, nil
}

// GetMemorySizeBytes returns the current size of the base memory in bytes.
func (m *Monitor) GetMemorySizeBytes() (int64, error) {
	// Prepare the response.
	var resp struct {
		Return struct {
			BaseMemory int64 `json:"base-memory"`
		} `json:"return"`
	}

	err := m.run("query-memory-size-summary", nil, &resp)
	if err != nil {
		return -1, err
	}

	return resp.Return.BaseMemory, nil
}

// GetMemoryBalloonSizeBytes returns effective size of the memory in bytes (considering the current balloon size).
func (m *Monitor) GetMemoryBalloonSizeBytes() (int64, error) {
	// Prepare the response.
	var resp struct {
		Return struct {
			Actual int64 `json:"actual"`
		} `json:"return"`
	}

	err := m.run("query-balloon", nil, &resp)
	if err != nil {
		return -1, err
	}

	return resp.Return.Actual, nil
}

// SetMemoryBalloonSizeBytes sets the size of the memory in bytes (which will resize the balloon as needed).
func (m *Monitor) SetMemoryBalloonSizeBytes(sizeBytes int64) error {
	args := map[string]int64{"value": sizeBytes}
	return m.run("balloon", args, nil)
}

// AddBlockDevice adds a block device.
func (m *Monitor) AddBlockDevice(blockDev map[string]any, device map[string]string) error {
	revert := revert.New()
	defer revert.Fail()

	nodeName, ok := blockDev["node-name"].(string)
	if !ok {
		return fmt.Errorf("Device node name must be a string")
	}

	if blockDev != nil {
		err := m.run("blockdev-add", blockDev, nil)
		if err != nil {
			return fmt.Errorf("Failed adding block device: %w", err)
		}

		revert.Add(func() {
			_ = m.RemoveBlockDevice(nodeName)
		})
	}

	err := m.AddDevice(device)
	if err != nil {
		return fmt.Errorf("Failed adding device: %w", err)
	}

	revert.Success()
	return nil
}

// RemoveBlockDevice removes a block device.
func (m *Monitor) RemoveBlockDevice(blockDevName string) error {
	if blockDevName != "" {
		blockDevName := map[string]string{
			"node-name": blockDevName,
		}

		err := m.run("blockdev-del", blockDevName, nil)
		if err != nil {
			if strings.Contains(err.Error(), "is in use") {
				return api.StatusErrorf(http.StatusLocked, err.Error())
			}

			if strings.Contains(err.Error(), "Failed to find") {
				return nil
			}

			return fmt.Errorf("Failed removing block device: %w", err)
		}
	}

	return nil
}

// AddCharDevice adds a new character device.
func (m *Monitor) AddCharDevice(device map[string]any) error {
	if device != nil {
		err := m.run("chardev-add", device, nil)
		if err != nil {
			return err
		}
	}

	return nil
}

// RemoveCharDevice removes a character device.
func (m *Monitor) RemoveCharDevice(deviceID string) error {
	if deviceID != "" {
		deviceID := map[string]string{
			"id": deviceID,
		}

		err := m.run("chardev-remove", deviceID, nil)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return nil
			}

			return err
		}
	}

	return nil
}

// AddDevice adds a new device.
func (m *Monitor) AddDevice(device map[string]string) error {
	if device != nil {
		err := m.run("device_add", device, nil)
		if err != nil {
			return err
		}
	}

	return nil
}

// RemoveDevice removes a device.
func (m *Monitor) RemoveDevice(deviceID string) error {
	if deviceID != "" {
		deviceID := map[string]string{
			"id": deviceID,
		}

		err := m.run("device_del", deviceID, nil)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return nil
			}

			return err
		}
	}

	return nil
}

// AddNIC adds a NIC device.
func (m *Monitor) AddNIC(netDev map[string]any, device map[string]string) error {
	revert := revert.New()
	defer revert.Fail()

	if netDev != nil {
		err := m.run("netdev_add", netDev, nil)
		if err != nil {
			return fmt.Errorf("Failed adding NIC netdev: %w", err)
		}

		revert.Add(func() {
			netDevDel := map[string]any{
				"id": netDev["id"],
			}

			err = m.run("netdev_del", netDevDel, nil)
			if err != nil {
				return
			}
		})
	}

	err := m.AddDevice(device)
	if err != nil {
		return fmt.Errorf("Failed adding NIC device: %w", err)
	}

	revert.Success()
	return nil
}

// RemoveNIC removes a NIC device.
func (m *Monitor) RemoveNIC(netDevID string) error {
	if netDevID != "" {
		netDevID := map[string]string{
			"id": netDevID,
		}

		err := m.run("netdev_del", netDevID, nil)

		// Not all NICs need a netdev, so if its missing, its not a problem.
		if err != nil && !strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("Failed removing NIC netdev: %w", err)
		}
	}

	return nil
}

// SetAction sets the actions the VM will take for certain scenarios.
func (m *Monitor) SetAction(actions map[string]string) error {
	err := m.run("set-action", actions, nil)
	if err != nil {
		return fmt.Errorf("Failed setting actions: %w", err)
	}

	return nil
}

// Reset VM.
func (m *Monitor) Reset() error {
	err := m.run("system_reset", nil, nil)
	if err != nil {
		return fmt.Errorf("Failed resetting: %w", err)
	}

	return nil
}

// PCIClassInfo info about a device's class.
type PCIClassInfo struct {
	Class       int    `json:"class"`
	Description string `json:"desc"`
}

// PCIDevice represents a PCI device.
type PCIDevice struct {
	DevID    string       `json:"qdev_id"`
	Bus      int          `json:"bus"`
	Slot     int          `json:"slot"`
	Function int          `json:"function"`
	Devices  []PCIDevice  `json:"devices"`
	Class    PCIClassInfo `json:"class_info"`
	Bridge   PCIBridge    `json:"pci_bridge"`
}

// PCIBridge represents a PCI bridge.
type PCIBridge struct {
	Devices []PCIDevice `json:"devices"`
}

// QueryPCI returns info about PCI devices.
func (m *Monitor) QueryPCI() ([]PCIDevice, error) {
	// Prepare the response.
	var resp struct {
		Return []struct {
			Devices []PCIDevice `json:"devices"`
		} `json:"return"`
	}

	err := m.run("query-pci", nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed querying PCI devices: %w", err)
	}

	if len(resp.Return) > 0 {
		return resp.Return[0].Devices, nil
	}

	return nil, nil
}

// BlockStats represents block device stats.
type BlockStats struct {
	BytesWritten    int `json:"wr_bytes"`
	WritesCompleted int `json:"wr_operations"`
	BytesRead       int `json:"rd_bytes"`
	ReadsCompleted  int `json:"rd_operations"`
}

// GetBlockStats return block device stats.
func (m *Monitor) GetBlockStats() (map[string]BlockStats, error) {
	// Prepare the response
	var resp struct {
		Return []struct {
			Stats BlockStats `json:"stats"`
			QDev  string     `json:"qdev"`
		} `json:"return"`
	}

	err := m.run("query-blockstats", nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed querying block stats: %w", err)
	}

	out := make(map[string]BlockStats)

	for _, res := range resp.Return {
		out[res.QDev] = res.Stats
	}

	return out, nil
}

// AddSecret adds a secret object with the given ID and secret. This function won't return an error
// if the secret object already exists.
func (m *Monitor) AddSecret(id string, secret string) error {
	args := map[string]any{
		"qom-type": "secret",
		"id":       id,
		"data":     secret,
		"format":   "base64",
	}

	err := m.run("object-add", &args, nil)
	if err != nil && !strings.Contains(err.Error(), "attempt to add duplicate property") {
		return fmt.Errorf("Failed adding object: %w", err)
	}

	return nil
}

// AMDSEVCapabilities represents the SEV capabilities of QEMU.
type AMDSEVCapabilities struct {
	PDH             string `json:"pdh"`               // Platform Diffie-Hellman key (base64-encoded)
	CertChain       string `json:"cert-chain"`        // PDH certificate chain (base64-encoded)
	CPU0Id          string `json:"cpu0-id"`           // Unique ID of CPU0 (base64-encoded)
	CBitPos         int    `json:"cbitpos"`           // C-bit location in page table entry
	ReducedPhysBits int    `json:"reduced-phys-bits"` // Number of physical address bit reduction when SEV is enabled
}

// SEVCapabilities is used to get the SEV capabilities, and is supported on AMD X86 platforms only.
func (m *Monitor) SEVCapabilities() (AMDSEVCapabilities, error) {
	// Prepare the response
	var resp struct {
		Return AMDSEVCapabilities `json:"return"`
	}

	err := m.run("query-sev-capabilities", nil, &resp)
	if err != nil {
		return AMDSEVCapabilities{}, fmt.Errorf("Failed querying SEV capability for QEMU: %w", err)
	}

	return resp.Return, nil
}

// NBDServerStart starts internal NBD server and returns a connection to it.
func (m *Monitor) NBDServerStart() (net.Conn, error) {
	var args struct {
		Addr struct {
			Data struct {
				Path     string `json:"path"`
				Abstract bool   `json:"abstract"`
			} `json:"data"`
			Type string `json:"type"`
		} `json:"addr"`
		MaxConnections int `json:"max-connections"`
	}

	// Create abstract unix listener.
	listener, err := net.Listen("unix", "")
	if err != nil {
		return nil, fmt.Errorf("Failed creating unix listener: %w", err)
	}

	// Get the random address, and then close the listener, and pass the address for use with nbd-server-start.
	listenAddress := listener.Addr().String()
	_ = listener.Close()

	args.Addr.Type = "unix"
	args.Addr.Data.Path = strings.TrimPrefix(listenAddress, "@")
	args.Addr.Data.Abstract = true
	args.MaxConnections = 1

	err = m.run("nbd-server-start", args, nil)
	if err != nil {
		return nil, err
	}

	// Connect to the NBD server and return the connection.
	conn, err := net.Dial("unix", listenAddress)
	if err != nil {
		return nil, fmt.Errorf("Failed connecting to NBD server: %w", err)
	}

	return conn, nil
}

// NBDServerStop stops the internal NBD server.
func (m *Monitor) NBDServerStop() error {
	err := m.run("nbd-server-stop", nil, nil)
	if err != nil {
		return err
	}

	return nil
}

// NBDBlockExportAdd exports a writable device via the NBD server.
func (m *Monitor) NBDBlockExportAdd(deviceNodeName string) error {
	var args struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		NodeName string `json:"node-name"`
		Writable bool   `json:"writable"`
	}

	args.ID = deviceNodeName
	args.Type = "nbd"
	args.NodeName = deviceNodeName
	args.Writable = true

	err := m.run("block-export-add", args, nil)
	if err != nil {
		return err
	}

	return nil
}

// BlockDevSnapshot creates a snapshot of a device using the specified snapshot device.
func (m *Monitor) BlockDevSnapshot(deviceNodeName string, snapshotNodeName string) error {
	var args struct {
		Node    string `json:"node"`
		Overlay string `json:"overlay"`
	}

	args.Node = deviceNodeName
	args.Overlay = snapshotNodeName

	err := m.run("blockdev-snapshot", args, nil)
	if err != nil {
		return err
	}

	return nil
}

// blockJobWaitReady waits until the specified jobID is ready, errored or missing.
// Returns nil if the job is ready, otherwise an error.
func (m *Monitor) blockJobWaitReady(jobID string) error {
	for {
		var resp struct {
			Return []struct {
				Device string `json:"device"`
				Ready  bool   `json:"ready"`
				Error  string `json:"error"`
			} `json:"return"`
		}

		err := m.run("query-block-jobs", nil, &resp)
		if err != nil {
			return err
		}

		found := false
		for _, job := range resp.Return {
			if job.Device != jobID {
				continue
			}

			if job.Error != "" {
				return fmt.Errorf("Failed block job: %s", job.Error)
			}

			if job.Ready {
				return nil
			}

			found = true
		}

		if !found {
			return fmt.Errorf("Specified block job not found")
		}

		time.Sleep(1 * time.Second)
	}
}

// BlockCommit merges a snapshot device back into its parent device.
func (m *Monitor) BlockCommit(deviceNodeName string) error {
	var args struct {
		Device string `json:"device"`
		JobID  string `json:"job-id"`
	}

	args.Device = deviceNodeName
	args.JobID = args.Device

	err := m.run("block-commit", args, nil)
	if err != nil {
		return err
	}

	err = m.blockJobWaitReady(args.JobID)
	if err != nil {
		return err
	}

	err = m.BlockJobComplete(args.JobID)
	if err != nil {
		return err
	}

	return nil
}

// BlockDevMirror mirrors the top device to the target device.
func (m *Monitor) BlockDevMirror(deviceNodeName string, targetNodeName string) error {
	var args struct {
		Device   string `json:"device"`
		Target   string `json:"target"`
		Sync     string `json:"sync"`
		JobID    string `json:"job-id"`
		CopyMode string `json:"copy-mode"`
	}

	args.Device = deviceNodeName
	args.Target = targetNodeName
	args.JobID = deviceNodeName

	// Only synchronise the top level device (usually a snapshot).
	args.Sync = "top"

	// When data is written to the source, write it (synchronously) to the target as well.
	// In addition, data is copied in background just like in background mode.
	// This ensures that the source and target converge at the cost of I/O performance during sync.
	args.CopyMode = "write-blocking"

	err := m.run("blockdev-mirror", args, nil)
	if err != nil {
		return err
	}

	err = m.blockJobWaitReady(args.JobID)
	if err != nil {
		return err
	}

	return nil
}

// BlockJobCancel cancels an ongoing block job.
func (m *Monitor) BlockJobCancel(deviceNodeName string) error {
	var args struct {
		Device string `json:"device"`
	}

	args.Device = deviceNodeName

	err := m.run("block-job-cancel", args, nil)
	if err != nil {
		return err
	}

	return nil
}

// BlockJobComplete completes a block job that is in reader state.
func (m *Monitor) BlockJobComplete(deviceNodeName string) error {
	var args struct {
		Device string `json:"device"`
	}

	args.Device = deviceNodeName

	err := m.run("block-job-complete", args, nil)
	if err != nil {
		return err
	}

	return nil
}

// Eject ejects a removable drive.
func (m *Monitor) Eject(id string) error {
	var args struct {
		ID string `json:"id"`
	}

	args.ID = id

	err := m.run("eject", args, nil)
	if err != nil {
		return err
	}

	return nil
}
