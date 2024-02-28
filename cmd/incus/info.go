package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/client"
	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/i18n"
	"github.com/lxc/incus/internal/instance"
	"github.com/lxc/incus/shared/api"
	config "github.com/lxc/incus/shared/cliconfig"
	"github.com/lxc/incus/shared/units"
)

type cmdInfo struct {
	global *cmdGlobal

	flagShowLog   bool
	flagResources bool
	flagTarget    string
}

func (c *cmdInfo) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("info", i18n.G("[<remote>:][<instance>]"))
	cmd.Short = i18n.G("Show instance or server information")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show instance or server information`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus info [<remote>:]<instance> [--show-log]
    For instance information.

incus info [<remote>:] [--resources]
    For server information.`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagShowLog, "show-log", false, i18n.G("Show the instance's last 100 log lines?"))
	cmd.Flags().BoolVar(&c.flagResources, "resources", false, i18n.G("Show the resources available to the server"))
	cmd.Flags().StringVar(&c.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdInfo) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	var remote string
	var cName string
	if len(args) == 1 {
		remote, cName, err = conf.ParseRemote(args[0])
		if err != nil {
			return err
		}
	} else {
		remote, cName, err = conf.ParseRemote("")
		if err != nil {
			return err
		}
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	if cName == "" {
		return c.remoteInfo(d)
	}

	return c.instanceInfo(d, conf.Remotes[remote], cName, c.flagShowLog)
}

func (c *cmdInfo) renderGPU(gpu api.ResourcesGPUCard, prefix string, initial bool) {
	if initial {
		fmt.Print(prefix)
	}

	fmt.Printf(i18n.G("NUMA node: %v")+"\n", gpu.NUMANode)

	if gpu.Vendor != "" {
		fmt.Printf(prefix+i18n.G("Vendor: %v (%v)")+"\n", gpu.Vendor, gpu.VendorID)
	}

	if gpu.Product != "" {
		fmt.Printf(prefix+i18n.G("Product: %v (%v)")+"\n", gpu.Product, gpu.ProductID)
	}

	if gpu.PCIAddress != "" {
		fmt.Printf(prefix+i18n.G("PCI address: %v")+"\n", gpu.PCIAddress)
	}

	if gpu.Driver != "" {
		fmt.Printf(prefix+i18n.G("Driver: %v (%v)")+"\n", gpu.Driver, gpu.DriverVersion)
	}

	if gpu.DRM != nil {
		fmt.Printf(prefix + i18n.G("DRM:") + "\n")
		fmt.Printf(prefix+"  "+i18n.G("ID: %d")+"\n", gpu.DRM.ID)

		if gpu.DRM.CardName != "" {
			fmt.Printf(prefix+"  "+i18n.G("Card: %s (%s)")+"\n", gpu.DRM.CardName, gpu.DRM.CardDevice)
		}

		if gpu.DRM.ControlName != "" {
			fmt.Printf(prefix+"  "+i18n.G("Control: %s (%s)")+"\n", gpu.DRM.ControlName, gpu.DRM.ControlDevice)
		}

		if gpu.DRM.RenderName != "" {
			fmt.Printf(prefix+"  "+i18n.G("Render: %s (%s)")+"\n", gpu.DRM.RenderName, gpu.DRM.RenderDevice)
		}
	}

	if gpu.Nvidia != nil {
		fmt.Printf(prefix + i18n.G("NVIDIA information:") + "\n")
		fmt.Printf(prefix+"  "+i18n.G("Architecture: %v")+"\n", gpu.Nvidia.Architecture)
		fmt.Printf(prefix+"  "+i18n.G("Brand: %v")+"\n", gpu.Nvidia.Brand)
		fmt.Printf(prefix+"  "+i18n.G("Model: %v")+"\n", gpu.Nvidia.Model)
		fmt.Printf(prefix+"  "+i18n.G("CUDA Version: %v")+"\n", gpu.Nvidia.CUDAVersion)
		fmt.Printf(prefix+"  "+i18n.G("NVRM Version: %v")+"\n", gpu.Nvidia.NVRMVersion)
		fmt.Printf(prefix+"  "+i18n.G("UUID: %v")+"\n", gpu.Nvidia.UUID)
	}

	if gpu.SRIOV != nil {
		fmt.Printf(prefix + i18n.G("SR-IOV information:") + "\n")
		fmt.Printf(prefix+"  "+i18n.G("Current number of VFs: %d")+"\n", gpu.SRIOV.CurrentVFs)
		fmt.Printf(prefix+"  "+i18n.G("Maximum number of VFs: %d")+"\n", gpu.SRIOV.MaximumVFs)
		if len(gpu.SRIOV.VFs) > 0 {
			fmt.Printf(prefix+"  "+i18n.G("VFs: %d")+"\n", gpu.SRIOV.MaximumVFs)
			for _, vf := range gpu.SRIOV.VFs {
				fmt.Print(prefix + "  - ")
				c.renderGPU(vf, prefix+"    ", false)
			}
		}
	}

	if gpu.Mdev != nil {
		fmt.Printf(prefix + i18n.G("Mdev profiles:") + "\n")

		keys := make([]string, 0, len(gpu.Mdev))
		for k := range gpu.Mdev {
			keys = append(keys, k)
		}

		sort.Strings(keys)

		for _, k := range keys {
			v := gpu.Mdev[k]

			fmt.Println(prefix + "  - " + fmt.Sprintf(i18n.G("%s (%s) (%d available)"), k, v.Name, v.Available))
			if v.Description != "" {
				for _, line := range strings.Split(v.Description, "\n") {
					fmt.Printf(prefix+"      %s\n", line)
				}
			}
		}
	}
}

func (c *cmdInfo) renderNIC(nic api.ResourcesNetworkCard, prefix string, initial bool) {
	if initial {
		fmt.Print(prefix)
	}

	fmt.Printf(i18n.G("NUMA node: %v")+"\n", nic.NUMANode)

	if nic.Vendor != "" {
		fmt.Printf(prefix+i18n.G("Vendor: %v (%v)")+"\n", nic.Vendor, nic.VendorID)
	}

	if nic.Product != "" {
		fmt.Printf(prefix+i18n.G("Product: %v (%v)")+"\n", nic.Product, nic.ProductID)
	}

	if nic.PCIAddress != "" {
		fmt.Printf(prefix+i18n.G("PCI address: %v")+"\n", nic.PCIAddress)
	}

	if nic.Driver != "" {
		fmt.Printf(prefix+i18n.G("Driver: %v (%v)")+"\n", nic.Driver, nic.DriverVersion)
	}

	if len(nic.Ports) > 0 {
		fmt.Printf(prefix + i18n.G("Ports:") + "\n")
		for _, port := range nic.Ports {
			fmt.Printf(prefix+"  "+i18n.G("- Port %d (%s)")+"\n", port.Port, port.Protocol)
			fmt.Printf(prefix+"    "+i18n.G("ID: %s")+"\n", port.ID)

			if port.Address != "" {
				fmt.Printf(prefix+"    "+i18n.G("Address: %s")+"\n", port.Address)
			}

			if port.SupportedModes != nil {
				fmt.Printf(prefix+"    "+i18n.G("Supported modes: %s")+"\n", strings.Join(port.SupportedModes, ", "))
			}

			if port.SupportedPorts != nil {
				fmt.Printf(prefix+"    "+i18n.G("Supported ports: %s")+"\n", strings.Join(port.SupportedPorts, ", "))
			}

			if port.PortType != "" {
				fmt.Printf(prefix+"    "+i18n.G("Port type: %s")+"\n", port.PortType)
			}

			if port.TransceiverType != "" {
				fmt.Printf(prefix+"    "+i18n.G("Transceiver type: %s")+"\n", port.TransceiverType)
			}

			fmt.Printf(prefix+"    "+i18n.G("Auto negotiation: %v")+"\n", port.AutoNegotiation)
			fmt.Printf(prefix+"    "+i18n.G("Link detected: %v")+"\n", port.LinkDetected)
			if port.LinkSpeed > 0 {
				fmt.Printf(prefix+"    "+i18n.G("Link speed: %dMbit/s (%s duplex)")+"\n", port.LinkSpeed, port.LinkDuplex)
			}

			if port.Infiniband != nil {
				fmt.Printf(prefix + "    " + i18n.G("Infiniband:") + "\n")

				if port.Infiniband.IsSMName != "" {
					fmt.Printf(prefix+"      "+i18n.G("IsSM: %s (%s)")+"\n", port.Infiniband.IsSMName, port.Infiniband.IsSMDevice)
				}

				if port.Infiniband.MADName != "" {
					fmt.Printf(prefix+"      "+i18n.G("MAD: %s (%s)")+"\n", port.Infiniband.MADName, port.Infiniband.MADDevice)
				}

				if port.Infiniband.VerbName != "" {
					fmt.Printf(prefix+"      "+i18n.G("Verb: %s (%s)")+"\n", port.Infiniband.VerbName, port.Infiniband.VerbDevice)
				}
			}
		}
	}

	if nic.SRIOV != nil {
		fmt.Printf(prefix + i18n.G("SR-IOV information:") + "\n")
		fmt.Printf(prefix+"  "+i18n.G("Current number of VFs: %d")+"\n", nic.SRIOV.CurrentVFs)
		fmt.Printf(prefix+"  "+i18n.G("Maximum number of VFs: %d")+"\n", nic.SRIOV.MaximumVFs)
		if len(nic.SRIOV.VFs) > 0 {
			fmt.Printf(prefix+"  "+i18n.G("VFs: %d")+"\n", nic.SRIOV.MaximumVFs)
			for _, vf := range nic.SRIOV.VFs {
				fmt.Print(prefix + "  - ")
				c.renderNIC(vf, prefix+"    ", false)
			}
		}
	}
}

func (c *cmdInfo) renderDisk(disk api.ResourcesStorageDisk, prefix string, initial bool) {
	if initial {
		fmt.Print(prefix)
	}

	fmt.Printf(i18n.G("NUMA node: %v")+"\n", disk.NUMANode)

	fmt.Printf(prefix+i18n.G("ID: %s")+"\n", disk.ID)
	fmt.Printf(prefix+i18n.G("Device: %s")+"\n", disk.Device)

	if disk.Model != "" {
		fmt.Printf(prefix+i18n.G("Model: %s")+"\n", disk.Model)
	}

	if disk.Type != "" {
		fmt.Printf(prefix+i18n.G("Type: %s")+"\n", disk.Type)
	}

	fmt.Printf(prefix+i18n.G("Size: %s")+"\n", units.GetByteSizeStringIEC(int64(disk.Size), 2))

	if disk.WWN != "" {
		fmt.Printf(prefix+i18n.G("WWN: %s")+"\n", disk.WWN)
	}

	fmt.Printf(prefix+i18n.G("Read-Only: %v")+"\n", disk.ReadOnly)
	fmt.Printf(prefix+i18n.G("Removable: %v")+"\n", disk.Removable)

	if len(disk.Partitions) != 0 {
		fmt.Printf(prefix + i18n.G("Partitions:") + "\n")
		for _, partition := range disk.Partitions {
			fmt.Printf(prefix+"  "+i18n.G("- Partition %d")+"\n", partition.Partition)
			fmt.Printf(prefix+"    "+i18n.G("ID: %s")+"\n", partition.ID)
			fmt.Printf(prefix+"    "+i18n.G("Device: %s")+"\n", partition.Device)
			fmt.Printf(prefix+"    "+i18n.G("Read-Only: %v")+"\n", partition.ReadOnly)
			fmt.Printf(prefix+"    "+i18n.G("Size: %s")+"\n", units.GetByteSizeStringIEC(int64(partition.Size), 2))
		}
	}
}

func (c *cmdInfo) renderCPU(cpu api.ResourcesCPUSocket, prefix string) {
	if cpu.Vendor != "" {
		fmt.Printf(prefix+i18n.G("Vendor: %v")+"\n", cpu.Vendor)
	}

	if cpu.Name != "" {
		fmt.Printf(prefix+i18n.G("Name: %v")+"\n", cpu.Name)
	}

	if cpu.Cache != nil {
		fmt.Printf(prefix + i18n.G("Caches:") + "\n")
		for _, cache := range cpu.Cache {
			fmt.Printf(prefix+"  "+i18n.G("- Level %d (type: %s): %s")+"\n", cache.Level, cache.Type, units.GetByteSizeStringIEC(int64(cache.Size), 0))
		}
	}

	fmt.Printf(prefix + i18n.G("Cores:") + "\n")
	for _, core := range cpu.Cores {
		fmt.Printf(prefix+"  - "+i18n.G("Core %d")+"\n", core.Core)
		fmt.Printf(prefix+"    "+i18n.G("Frequency: %vMhz")+"\n", core.Frequency)
		fmt.Printf(prefix + "    " + i18n.G("Threads:") + "\n")
		for _, thread := range core.Threads {
			fmt.Printf(prefix+"      - "+i18n.G("%d (id: %d, online: %v, NUMA node: %v)")+"\n", thread.Thread, thread.ID, thread.Online, thread.NUMANode)
		}
	}

	if cpu.Frequency > 0 {
		if cpu.FrequencyTurbo > 0 && cpu.FrequencyMinimum > 0 {
			fmt.Printf(prefix+i18n.G("Frequency: %vMhz (min: %vMhz, max: %vMhz)")+"\n", cpu.Frequency, cpu.FrequencyMinimum, cpu.FrequencyTurbo)
		} else {
			fmt.Printf(prefix+i18n.G("Frequency: %vMhz")+"\n", cpu.Frequency)
		}
	}
}

func (c *cmdInfo) remoteInfo(d incus.InstanceServer) error {
	// Targeting
	if c.flagTarget != "" {
		if !d.IsClustered() {
			return fmt.Errorf(i18n.G("To use --target, the destination remote must be a cluster"))
		}

		d = d.UseTarget(c.flagTarget)
	}

	if c.flagResources {
		if !d.HasExtension("resources_v2") {
			return fmt.Errorf(i18n.G("The server doesn't implement the newer v2 resources API"))
		}

		resources, err := d.GetServerResources()
		if err != nil {
			return err
		}

		// CPU
		if len(resources.CPU.Sockets) == 1 {
			fmt.Printf(i18n.G("CPU (%s):")+"\n", resources.CPU.Architecture)
			c.renderCPU(resources.CPU.Sockets[0], "  ")
		} else if len(resources.CPU.Sockets) > 1 {
			fmt.Printf(i18n.G("CPUs (%s):")+"\n", resources.CPU.Architecture)
			for _, cpu := range resources.CPU.Sockets {
				fmt.Printf("  "+i18n.G("Socket %d:")+"\n", cpu.Socket)
				c.renderCPU(cpu, "    ")
			}
		}

		// Memory
		fmt.Printf("\n" + i18n.G("Memory:") + "\n")
		if resources.Memory.HugepagesTotal > 0 {
			fmt.Printf("  " + i18n.G("Hugepages:"+"\n"))
			fmt.Printf("    "+i18n.G("Free: %v")+"\n", units.GetByteSizeStringIEC(int64(resources.Memory.HugepagesTotal-resources.Memory.HugepagesUsed), 2))
			fmt.Printf("    "+i18n.G("Used: %v")+"\n", units.GetByteSizeStringIEC(int64(resources.Memory.HugepagesUsed), 2))
			fmt.Printf("    "+i18n.G("Total: %v")+"\n", units.GetByteSizeStringIEC(int64(resources.Memory.HugepagesTotal), 2))
		}

		if len(resources.Memory.Nodes) > 1 {
			fmt.Printf("  " + i18n.G("NUMA nodes:"+"\n"))
			for _, node := range resources.Memory.Nodes {
				fmt.Printf("    "+i18n.G("Node %d:"+"\n"), node.NUMANode)
				if node.HugepagesTotal > 0 {
					fmt.Printf("      " + i18n.G("Hugepages:"+"\n"))
					fmt.Printf("        "+i18n.G("Free: %v")+"\n", units.GetByteSizeStringIEC(int64(node.HugepagesTotal-node.HugepagesUsed), 2))
					fmt.Printf("        "+i18n.G("Used: %v")+"\n", units.GetByteSizeStringIEC(int64(node.HugepagesUsed), 2))
					fmt.Printf("        "+i18n.G("Total: %v")+"\n", units.GetByteSizeStringIEC(int64(node.HugepagesTotal), 2))
				}

				fmt.Printf("      "+i18n.G("Free: %v")+"\n", units.GetByteSizeStringIEC(int64(node.Total-node.Used), 2))
				fmt.Printf("      "+i18n.G("Used: %v")+"\n", units.GetByteSizeStringIEC(int64(node.Used), 2))
				fmt.Printf("      "+i18n.G("Total: %v")+"\n", units.GetByteSizeStringIEC(int64(node.Total), 2))
			}
		}

		fmt.Printf("  "+i18n.G("Free: %v")+"\n", units.GetByteSizeStringIEC(int64(resources.Memory.Total-resources.Memory.Used), 2))
		fmt.Printf("  "+i18n.G("Used: %v")+"\n", units.GetByteSizeStringIEC(int64(resources.Memory.Used), 2))
		fmt.Printf("  "+i18n.G("Total: %v")+"\n", units.GetByteSizeStringIEC(int64(resources.Memory.Total), 2))

		// GPUs
		if len(resources.GPU.Cards) == 1 {
			fmt.Printf("\n" + i18n.G("GPU:") + "\n")
			c.renderGPU(resources.GPU.Cards[0], "  ", true)
		} else if len(resources.GPU.Cards) > 1 {
			fmt.Printf("\n" + i18n.G("GPUs:") + "\n")
			for id, gpu := range resources.GPU.Cards {
				fmt.Printf("  "+i18n.G("Card %d:")+"\n", id)
				c.renderGPU(gpu, "    ", true)
			}
		}

		// Network interfaces
		if len(resources.Network.Cards) == 1 {
			fmt.Printf("\n" + i18n.G("NIC:") + "\n")
			c.renderNIC(resources.Network.Cards[0], "  ", true)
		} else if len(resources.Network.Cards) > 1 {
			fmt.Printf("\n" + i18n.G("NICs:") + "\n")
			for id, nic := range resources.Network.Cards {
				fmt.Printf("  "+i18n.G("Card %d:")+"\n", id)
				c.renderNIC(nic, "    ", true)
			}
		}

		// Storage
		if len(resources.Storage.Disks) == 1 {
			fmt.Printf("\n" + i18n.G("Disk:") + "\n")
			c.renderDisk(resources.Storage.Disks[0], "  ", true)
		} else if len(resources.Storage.Disks) > 1 {
			fmt.Printf("\n" + i18n.G("Disks:") + "\n")
			for id, nic := range resources.Storage.Disks {
				fmt.Printf("  "+i18n.G("Disk %d:")+"\n", id)
				c.renderDisk(nic, "    ", true)
			}
		}

		return nil
	}

	serverStatus, _, err := d.GetServer()
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&serverStatus)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

func (c *cmdInfo) instanceInfo(d incus.InstanceServer, remote config.Remote, name string, showLog bool) error {
	// Quick checks.
	if c.flagTarget != "" {
		return fmt.Errorf(i18n.G("--target cannot be used with instances"))
	}

	// Get the full instance data.
	inst, _, err := d.GetInstanceFull(name)
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G("Name: %s")+"\n", inst.Name)

	fmt.Printf(i18n.G("Status: %s")+"\n", strings.ToUpper(inst.Status))

	if inst.Type == "" {
		inst.Type = "container"
	}

	if inst.Ephemeral {
		fmt.Printf(i18n.G("Type: %s (ephemeral)")+"\n", inst.Type)
	} else {
		fmt.Printf(i18n.G("Type: %s")+"\n", inst.Type)
	}

	fmt.Printf(i18n.G("Architecture: %s")+"\n", inst.Architecture)

	if inst.Location != "" && d.IsClustered() {
		fmt.Printf(i18n.G("Location: %s")+"\n", inst.Location)
	}

	if inst.State.Pid != 0 {
		fmt.Printf(i18n.G("PID: %d")+"\n", inst.State.Pid)
	}

	if !inst.CreatedAt.IsZero() {
		fmt.Printf(i18n.G("Created: %s")+"\n", inst.CreatedAt.Local().Format(dateLayout))
	}

	if !inst.LastUsedAt.IsZero() {
		fmt.Printf(i18n.G("Last Used: %s")+"\n", inst.LastUsedAt.Local().Format(dateLayout))
	}

	if inst.State.Pid != 0 {
		fmt.Println("\n" + i18n.G("Resources:"))
		// Processes
		fmt.Printf("  "+i18n.G("Processes: %d")+"\n", inst.State.Processes)

		// Disk usage
		diskInfo := ""
		if inst.State.Disk != nil {
			for entry, disk := range inst.State.Disk {
				if disk.Usage != 0 {
					diskInfo += fmt.Sprintf("    %s: %s\n", entry, units.GetByteSizeStringIEC(disk.Usage, 2))
				}
			}
		}

		if diskInfo != "" {
			fmt.Printf("  %s\n", i18n.G("Disk usage:"))
			fmt.Print(diskInfo)
		}

		// CPU usage
		cpuInfo := ""
		if inst.State.CPU.Usage != 0 {
			cpuInfo += fmt.Sprintf("    %s: %v\n", i18n.G("CPU usage (in seconds)"), inst.State.CPU.Usage/1000000000)
		}

		if cpuInfo != "" {
			fmt.Printf("  %s\n", i18n.G("CPU usage:"))
			fmt.Print(cpuInfo)
		}

		// Memory usage
		memoryInfo := ""
		if inst.State.Memory.Usage != 0 {
			memoryInfo += fmt.Sprintf("    %s: %s\n", i18n.G("Memory (current)"), units.GetByteSizeStringIEC(inst.State.Memory.Usage, 2))
		}

		if inst.State.Memory.UsagePeak != 0 {
			memoryInfo += fmt.Sprintf("    %s: %s\n", i18n.G("Memory (peak)"), units.GetByteSizeStringIEC(inst.State.Memory.UsagePeak, 2))
		}

		if inst.State.Memory.SwapUsage != 0 {
			memoryInfo += fmt.Sprintf("    %s: %s\n", i18n.G("Swap (current)"), units.GetByteSizeStringIEC(inst.State.Memory.SwapUsage, 2))
		}

		if inst.State.Memory.SwapUsagePeak != 0 {
			memoryInfo += fmt.Sprintf("    %s: %s\n", i18n.G("Swap (peak)"), units.GetByteSizeStringIEC(inst.State.Memory.SwapUsagePeak, 2))
		}

		if memoryInfo != "" {
			fmt.Printf("  %s\n", i18n.G("Memory usage:"))
			fmt.Print(memoryInfo)
		}

		// Network usage and IP info
		networkInfo := ""
		if inst.State.Network != nil {
			for netName, net := range inst.State.Network {
				networkInfo += fmt.Sprintf("    %s:\n", netName)
				networkInfo += fmt.Sprintf("      %s: %s\n", i18n.G("Type"), net.Type)
				networkInfo += fmt.Sprintf("      %s: %s\n", i18n.G("State"), strings.ToUpper(net.State))
				if net.HostName != "" {
					networkInfo += fmt.Sprintf("      %s: %s\n", i18n.G("Host interface"), net.HostName)
				}

				if net.Hwaddr != "" {
					networkInfo += fmt.Sprintf("      %s: %s\n", i18n.G("MAC address"), net.Hwaddr)
				}

				if net.Mtu != 0 {
					networkInfo += fmt.Sprintf("      %s: %d\n", i18n.G("MTU"), net.Mtu)
				}

				networkInfo += fmt.Sprintf("      %s: %s\n", i18n.G("Bytes received"), units.GetByteSizeString(net.Counters.BytesReceived, 2))
				networkInfo += fmt.Sprintf("      %s: %s\n", i18n.G("Bytes sent"), units.GetByteSizeString(net.Counters.BytesSent, 2))
				networkInfo += fmt.Sprintf("      %s: %d\n", i18n.G("Packets received"), net.Counters.PacketsReceived)
				networkInfo += fmt.Sprintf("      %s: %d\n", i18n.G("Packets sent"), net.Counters.PacketsSent)

				networkInfo += fmt.Sprintf("      %s:\n", i18n.G("IP addresses"))

				for _, addr := range net.Addresses {
					if addr.Family == "inet" {
						networkInfo += fmt.Sprintf("        %s:  %s/%s (%s)\n", addr.Family, addr.Address, addr.Netmask, addr.Scope)
					} else {
						networkInfo += fmt.Sprintf("        %s: %s/%s (%s)\n", addr.Family, addr.Address, addr.Netmask, addr.Scope)
					}
				}
			}
		}

		if networkInfo != "" {
			fmt.Printf("  %s\n", i18n.G("Network usage:"))
			fmt.Print(networkInfo)
		}
	}

	// List snapshots
	firstSnapshot := true
	if len(inst.Snapshots) > 0 {
		snapData := [][]string{}

		for _, snap := range inst.Snapshots {
			if firstSnapshot {
				fmt.Println("\n" + i18n.G("Snapshots:"))
			}

			var row []string

			fields := strings.Split(snap.Name, instance.SnapshotDelimiter)
			row = append(row, fields[len(fields)-1])

			if !snap.CreatedAt.IsZero() {
				row = append(row, snap.CreatedAt.Local().Format(dateLayout))
			} else {
				row = append(row, " ")
			}

			if !snap.ExpiresAt.IsZero() {
				row = append(row, snap.ExpiresAt.Local().Format(dateLayout))
			} else {
				row = append(row, " ")
			}

			if snap.Stateful {
				row = append(row, "YES")
			} else {
				row = append(row, "NO")
			}

			firstSnapshot = false
			snapData = append(snapData, row)
		}

		snapHeader := []string{
			i18n.G("Name"),
			i18n.G("Taken at"),
			i18n.G("Expires at"),
			i18n.G("Stateful"),
		}

		_ = cli.RenderTable(cli.TableFormatTable, snapHeader, snapData, inst.Snapshots)
	}

	// List backups
	firstBackup := true
	if len(inst.Backups) > 0 {
		backupData := [][]string{}

		for _, backup := range inst.Backups {
			if firstBackup {
				fmt.Println("\n" + i18n.G("Backups:"))
			}

			var row []string
			row = append(row, backup.Name)

			if !backup.CreatedAt.IsZero() {
				row = append(row, backup.CreatedAt.Local().Format(dateLayout))
			} else {
				row = append(row, " ")
			}

			if !backup.ExpiresAt.IsZero() {
				row = append(row, backup.ExpiresAt.Local().Format(dateLayout))
			} else {
				row = append(row, " ")
			}

			if backup.InstanceOnly {
				row = append(row, "YES")
			} else {
				row = append(row, "NO")
			}

			if backup.OptimizedStorage {
				row = append(row, "YES")
			} else {
				row = append(row, "NO")
			}

			firstBackup = false
			backupData = append(backupData, row)
		}

		backupHeader := []string{
			i18n.G("Name"),
			i18n.G("Taken at"),
			i18n.G("Expires at"),
			i18n.G("Instance Only"),
			i18n.G("Optimized Storage"),
		}

		_ = cli.RenderTable(cli.TableFormatTable, backupHeader, backupData, inst.Backups)
	}

	if showLog {
		var log io.Reader
		if inst.Type == "container" {
			log, err = d.GetInstanceLogfile(name, "lxc.log")
			if err != nil {
				return err
			}
		} else if inst.Type == "virtual-machine" {
			log, err = d.GetInstanceLogfile(name, "qemu.log")
			if err != nil {
				return err
			}
		} else {
			return fmt.Errorf(i18n.G("Unsupported instance type: %s"), inst.Type)
		}

		stuff, err := io.ReadAll(log)
		if err != nil {
			return err
		}

		fmt.Printf("\n"+i18n.G("Log:")+"\n\n%s\n", string(stuff))
	}

	return nil
}
