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

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/revert"
)

// ChardevChangeInfo contains information required to change the backend of a chardev.
type ChardevChangeInfo struct {
	Type   string   `json:"type"`
	File   *os.File `json:"file,omitempty"`
	FDName string   `json:"fdname,omitempty"`
}

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

// CPUModel contains information about a CPU model.
type CPUModel struct {
	Name  string         `json:"name"`
	Flags map[string]any `json:"props"`
}

// QueryCPUs returns a list of CPUs.
func (m *Monitor) QueryCPUs() ([]CPU, error) {
	// Prepare the response.
	var resp struct {
		Return []CPU `json:"return"`
	}

	err := m.Run("query-cpus-fast", nil, &resp)
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

	err := m.Run("query-hotpluggable-cpus", nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed to query hotpluggable CPUs: %w", err)
	}

	return resp.Return, nil
}

// QueryCPUModel returns a CPUModel for the specified model name.
func (m *Monitor) QueryCPUModel(model string) (*CPUModel, error) {
	// Prepare the response.
	var resp struct {
		Return struct {
			Model CPUModel `json:"model"`
		} `json:"return"`
	}

	args := map[string]any{
		"model": map[string]string{"name": model},
		"type":  "full",
	}

	err := m.Run("query-cpu-model-expansion", args, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed to query CPU model: %w", err)
	}

	return &resp.Return.Model, nil
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
	err := m.Run("query-status", nil, &resp)
	if err != nil {
		return "", err
	}

	return resp.Return.Status, nil
}

// MachineDefinition returns the current QEMU machine definition name.
func (m *Monitor) MachineDefinition() (string, error) {
	// Prepare the request.
	var req struct {
		Path     string `json:"path"`
		Property string `json:"property"`
	}

	req.Path = "/machine"
	req.Property = "type"

	// Prepare the response.
	var resp struct {
		Return string `json:"return"`
	}

	// Query the machine.
	err := m.Run("qom-get", req, &resp)
	if err != nil {
		return "", err
	}

	return strings.TrimSuffix(resp.Return, "-machine"), nil
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

	err := m.Run("closefd", req, nil)
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

	err := m.Run("query-fdsets", nil, &resp)
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

				err = m.Run("remove-fd", args, nil)
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

	err := m.Run("migrate-set-capabilities", args, nil)
	if err != nil {
		return err
	}

	return nil
}

// Migrate starts a migration stream.
func (m *Monitor) Migrate(name string) error {
	// Query the status.

	type migrateArgsChannel struct {
		ChannelType string            `json:"channel-type"`
		Address     map[string]string `json:"addr"`
	}

	type migrateArgs struct {
		Channels []migrateArgsChannel `json:"channels"`
	}

	args := migrateArgs{}
	args.Channels = []migrateArgsChannel{{
		ChannelType: "main",
		Address: map[string]string{
			"transport": "socket",
			"type":      "fd",
			"str":       name,
		},
	}}

	err := m.Run("migrate", args, nil)
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

		err := m.Run("query-migrate", nil, &resp)
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

	err := m.Run("migrate-continue", args, nil)
	if err != nil {
		return err
	}

	return nil
}

// MigrateIncoming starts the receiver of a migration stream.
func (m *Monitor) MigrateIncoming(ctx context.Context, name string) error {
	type migrateArgsChannel struct {
		ChannelType string            `json:"channel-type"`
		Address     map[string]string `json:"addr"`
	}

	type migrateArgs struct {
		Channels []migrateArgsChannel `json:"channels"`
	}

	args := migrateArgs{}
	args.Channels = []migrateArgsChannel{{
		ChannelType: "main",
		Address: map[string]string{
			"transport": "socket",
			"type":      "fd",
			"str":       name,
		},
	}}

	// Query the status.
	err := m.Run("migrate-incoming", args, nil)
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

		err := m.Run("query-migrate", nil, &resp)
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
	return m.Run("system_powerdown", nil, nil)
}

// Start tells QEMU to start the emulation.
func (m *Monitor) Start() error {
	return m.Run("cont", nil, nil)
}

// Pause tells QEMU to temporarily stop the emulation.
func (m *Monitor) Pause() error {
	return m.Run("stop", nil, nil)
}

// Quit tells QEMU to exit immediately.
func (m *Monitor) Quit() error {
	return m.Run("quit", nil, nil)
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
	err := m.Run("query-cpus-fast", nil, &resp)
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

	err := m.Run("query-memory-size-summary", nil, &resp)
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

	err := m.Run("query-balloon", nil, &resp)
	if err != nil {
		return -1, err
	}

	return resp.Return.Actual, nil
}

// SetMemoryBalloonSizeBytes sets the size of the memory in bytes (which will resize the balloon as needed).
func (m *Monitor) SetMemoryBalloonSizeBytes(sizeBytes int64) error {
	args := map[string]int64{"value": sizeBytes}
	return m.Run("balloon", args, nil)
}

// AddBlockDevice adds a block device.
func (m *Monitor) AddBlockDevice(blockDev map[string]any, device map[string]any) error {
	revert := revert.New()
	defer revert.Fail()

	nodeName, ok := blockDev["node-name"].(string)
	if !ok {
		return fmt.Errorf("Device node name must be a string")
	}

	if blockDev != nil {
		err := m.Run("blockdev-add", blockDev, nil)
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

		err := m.Run("blockdev-del", blockDevName, nil)
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
		err := m.Run("chardev-add", device, nil)
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

		err := m.Run("chardev-remove", deviceID, nil)
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
func (m *Monitor) AddDevice(device map[string]any) error {
	if device != nil {
		err := m.Run("device_add", device, nil)
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

		err := m.Run("device_del", deviceID, nil)
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
func (m *Monitor) AddNIC(netDev map[string]any, device map[string]any) error {
	revert := revert.New()
	defer revert.Fail()

	if netDev != nil {
		err := m.Run("netdev_add", netDev, nil)
		if err != nil {
			return fmt.Errorf("Failed adding NIC netdev: %w", err)
		}

		revert.Add(func() {
			netDevDel := map[string]any{
				"id": netDev["id"],
			}

			err = m.Run("netdev_del", netDevDel, nil)
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

		err := m.Run("netdev_del", netDevID, nil)

		// Not all NICs need a netdev, so if its missing, its not a problem.
		if err != nil && !strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("Failed removing NIC netdev: %w", err)
		}
	}

	return nil
}

// SetAction sets the actions the VM will take for certain scenarios.
func (m *Monitor) SetAction(actions map[string]string) error {
	err := m.Run("set-action", actions, nil)
	if err != nil {
		return fmt.Errorf("Failed setting actions: %w", err)
	}

	return nil
}

// Reset VM.
func (m *Monitor) Reset() error {
	err := m.Run("system_reset", nil, nil)
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

	err := m.Run("query-pci", nil, &resp)
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

	err := m.Run("query-blockstats", nil, &resp)
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

	err := m.Run("object-add", &args, nil)
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

	err := m.Run("query-sev-capabilities", nil, &resp)
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

	err = m.Run("nbd-server-start", args, nil)
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
	err := m.Run("nbd-server-stop", nil, nil)
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

	err := m.Run("block-export-add", args, nil)
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

	err := m.Run("blockdev-snapshot", args, nil)
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

		err := m.Run("query-block-jobs", nil, &resp)
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

	err := m.Run("block-commit", args, nil)
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

	err := m.Run("blockdev-mirror", args, nil)
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

	err := m.Run("block-job-cancel", args, nil)
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

	err := m.Run("block-job-complete", args, nil)
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

	err := m.Run("eject", args, nil)
	if err != nil {
		return err
	}

	return nil
}

// UpdateBlockSize updates the size of a disk.
func (m *Monitor) UpdateBlockSize(id string) error {
	var args struct {
		NodeName string `json:"node-name"`
		Size     int64  `json:"size"`
	}

	args.NodeName = id
	args.Size = 1

	err := m.Run("block_resize", args, nil)
	if err != nil {
		return err
	}

	return nil
}

// SetBlockThrottle applies an I/O limit on a disk.
func (m *Monitor) SetBlockThrottle(id string, bytesRead int, bytesWrite int, iopsRead int, iopsWrite int) error {
	var args struct {
		ID string `json:"id"`

		Bytes      int `json:"bps"`
		BytesRead  int `json:"bps_rd"`
		BytesWrite int `json:"bps_wr"`
		IOPs       int `json:"iops"`
		IOPsRead   int `json:"iops_rd"`
		IOPsWrite  int `json:"iops_wr"`
	}

	args.ID = id
	args.BytesRead = bytesRead
	args.BytesWrite = bytesWrite
	args.IOPsRead = iopsRead
	args.IOPsWrite = iopsWrite

	err := m.Run("block_set_io_throttle", args, nil)
	if err != nil {
		return err
	}

	return nil
}

// CheckPCIDevice checks if the deviceID exists as a bridged PCI device.
func (m *Monitor) CheckPCIDevice(deviceID string) (bool, error) {
	pciDevs, err := m.QueryPCI()
	if err != nil {
		return false, err
	}

	for _, pciDev := range pciDevs {
		for _, bridgeDev := range pciDev.Bridge.Devices {
			if bridgeDev.DevID == deviceID {
				return true, nil
			}
		}
	}

	return false, nil
}

// RingbufRead returns the complete contents of the specified ring buffer.
func (m *Monitor) RingbufRead(device string) (string, error) {
	// Begin by ensuring the device specified is actually a ring buffer.
	var queryResp struct {
		Return []struct {
			Label        string `json:"label"`
			Filename     string `json:"filename"`
			FrontendOpen bool   `json:"frontend_open"`
		} `json:"return"`
	}

	err := m.Run("query-chardev", nil, &queryResp)
	if err != nil {
		return "", err
	}

	deviceFound := true
	for _, qemuDevice := range queryResp.Return {
		if qemuDevice.Label == device {
			deviceFound = true
			if qemuDevice.Filename != "ringbuf" {
				// Can't call `ringbuf-read` on a non-ringbuf device.
				return "", ErrNotARingbuf
			}

			break
		}
	}
	if !deviceFound {
		return "", fmt.Errorf("Specified qemu device %q doesn't exist", device)
	}

	// Now actually read from the ring buffer.
	var args struct {
		Device string `json:"device"`
		Size   int    `json:"size"`
	}

	args.Device = device
	args.Size = 10000

	var readResp struct {
		Return string `json:"return"`
	}

	var sb strings.Builder

	for {
		err := m.Run("ringbuf-read", args, &readResp)
		if err != nil {
			return "", err
		}

		if len(readResp.Return) == 0 {
			break
		}

		sb.WriteString(readResp.Return)
	}

	return sb.String(), nil
}

// ChardevChange changes the backend of a specified chardev. Currently supports the socket and ringbuf backends.
func (m *Monitor) ChardevChange(device string, info ChardevChangeInfo) error {
	if info.Type == "socket" {
		// Share the existing file descriptor with qemu.
		err := m.SendFile(info.FDName, info.File)
		if err != nil {
			return err
		}

		var args struct {
			ID      string `json:"id"`
			Backend struct {
				Type string `json:"type"`
				Data struct {
					Addr struct {
						Type string `json:"type"`
						Data struct {
							Str string `json:"str"`
						} `json:"data"`
					} `json:"addr"`
					Server bool `json:"server"`
					Wait   bool `json:"wait"`
				} `json:"data"`
			} `json:"backend"`
		}

		args.ID = device
		args.Backend.Type = info.Type
		args.Backend.Data.Addr.Type = "fd"
		args.Backend.Data.Addr.Data.Str = info.FDName
		args.Backend.Data.Server = true
		args.Backend.Data.Wait = false

		err = m.Run("chardev-change", args, nil)
		if err != nil {
			// If the chardev-change command failed for some reason, ensure qemu cleans up its file descriptor.
			_ = m.CloseFile(info.FDName)
			return err
		}

		return nil
	} else if info.Type == "ringbuf" {
		var args struct {
			ID      string `json:"id"`
			Backend struct {
				Type string `json:"type"`
				Data struct {
					Size int `json:"size"`
				} `json:"data"`
			} `json:"backend"`
		}

		args.ID = device
		args.Backend.Type = info.Type
		args.Backend.Data.Size = 1048576

		return m.Run("chardev-change", args, nil)
	}

	return fmt.Errorf("Unsupported chardev type %q", info.Type)
}

// Screendump takes a screenshot of the current VGA console.
// The screendump is stored to the filename provided as argument.
func (m *Monitor) Screendump(filename string) error {
	var args struct {
		Filename string `json:"filename"`
		Device   string `json:"device,omitempty"`
		Head     int    `json:"head,omitempty"`
		Format   string `json:"format,omitempty"`
	}

	args.Filename = filename
	args.Format = "png"

	var queryResp struct {
		Return struct{} `json:"return"`
	}

	return m.Run("screendump", args, &queryResp)
}

// DumpGuestMemory dumps guest memory to a file.
func (m *Monitor) DumpGuestMemory(path string, format string) error {
	var args struct {
		Paging   bool   `json:"paging"`
		Protocol string `json:"protocol"`
		Format   string `json:"format,omitempty"`
		Detach   bool   `json:"detach"`
	}

	args.Protocol = "fd:" + path
	args.Format = format

	var queryResp struct {
		Return struct{} `json:"return"`
	}

	return m.Run("dump-guest-memory", args, &queryResp)
}
