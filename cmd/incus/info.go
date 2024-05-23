package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/v6/client"
	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/shared/api"
	config "github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/lxc/incus/v6/shared/units"
)

type cmdInfo struct {
	global *cmdGlobal

	flagShowAccess bool
	flagShowLog    bool
	flagResources  bool
	flagTarget     string
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
	cmd.Flags().BoolVar(&c.flagShowAccess, "show-access", false, i18n.G("Show the instance's access list"))
	cmd.Flags().BoolVar(&c.flagShowLog, "show-log", false, i18n.G("Show the instance's recent log entries"))
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

	if c.flagShowAccess {
		access, err := d.GetInstanceAccess(cName)
		if err != nil {
			return err
		}

		data, err := yaml.Marshal(access)
		if err != nil {
			return err
		}

		fmt.Printf("%s", data)

		return nil
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

func (c *cmdInfo) renderUSB(usb api.ResourcesUSBDevice, prefix string) {
	fmt.Printf(prefix+i18n.G("Vendor: %v")+"\n", usb.Vendor)
	fmt.Printf(prefix+i18n.G("Vendor ID: %v")+"\n", usb.VendorID)
	fmt.Printf(prefix+i18n.G("Product: %v")+"\n", usb.Product)
	fmt.Printf(prefix+i18n.G("Product ID: %v")+"\n", usb.ProductID)
	fmt.Printf(prefix+i18n.G("Bus Address: %v")+"\n", usb.BusAddress)
	fmt.Printf(prefix+i18n.G("Device Address: %v")+"\n", usb.DeviceAddress)
	if len(usb.Serial) > 0 {
		fmt.Printf(prefix+i18n.G("Serial Number: %v")+"\n", usb.Serial)
	}
}

func (c *cmdInfo) renderPCI(pci api.ResourcesPCIDevice, prefix string) {
	fmt.Printf(prefix+i18n.G("Address: %v")+"\n", pci.PCIAddress)
	fmt.Printf(prefix+i18n.G("Vendor: %v")+"\n", pci.Vendor)
	fmt.Printf(prefix+i18n.G("Vendor ID: %v")+"\n", pci.VendorID)
	fmt.Printf(prefix+i18n.G("Product: %v")+"\n", pci.Product)
	fmt.Printf(prefix+i18n.G("Product ID: %v")+"\n", pci.ProductID)
	fmt.Printf(prefix+i18n.G("NUMA node: %v")+"\n", pci.NUMANode)
	fmt.Printf(prefix+i18n.G("IOMMU group: %v")+"\n", pci.IOMMUGroup)
	fmt.Printf(prefix+i18n.G("Driver: %v")+"\n", pci.Driver)
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

		// System
		fmt.Printf(i18n.G("System:") + "\n")
		if resources.System.UUID != "" {
			fmt.Printf("  "+i18n.G("UUID: %v")+"\n", resources.System.UUID)
		}

		if resources.System.Vendor != "" {
			fmt.Printf("  "+i18n.G("Vendor: %v")+"\n", resources.System.Vendor)
		}

		if resources.System.Product != "" {
			fmt.Printf("  "+i18n.G("Product: %v")+"\n", resources.System.Product)
		}

		if resources.System.Family != "" {
			fmt.Printf("  "+i18n.G("Family: %v")+"\n", resources.System.Family)
		}

		if resources.System.Version != "" {
			fmt.Printf("  "+i18n.G("Version: %v")+"\n", resources.System.Version)
		}

		if resources.System.Sku != "" {
			fmt.Printf("  "+i18n.G("SKU: %v")+"\n", resources.System.Sku)
		}

		if resources.System.Serial != "" {
			fmt.Printf("  "+i18n.G("Serial: %v")+"\n", resources.System.Serial)
		}

		if resources.System.Type != "" {
			fmt.Printf("  "+i18n.G("Type: %s")+"\n", resources.System.Type)
		}

		// System: Chassis
		if resources.System.Chassis != nil {
			fmt.Printf(i18n.G("  Chassis:") + "\n")
			if resources.System.Chassis.Vendor != "" {
				fmt.Printf("      "+i18n.G("Vendor: %s")+"\n", resources.System.Chassis.Vendor)
			}

			if resources.System.Chassis.Type != "" {
				fmt.Printf("      "+i18n.G("Type: %s")+"\n", resources.System.Chassis.Type)
			}

			if resources.System.Chassis.Version != "" {
				fmt.Printf("      "+i18n.G("Version: %s")+"\n", resources.System.Chassis.Version)
			}

			if resources.System.Chassis.Serial != "" {
				fmt.Printf("      "+i18n.G("Serial: %s")+"\n", resources.System.Chassis.Serial)
			}
		}

		// System: Motherboard
		if resources.System.Motherboard != nil {
			fmt.Printf(i18n.G("  Motherboard:") + "\n")
			if resources.System.Motherboard.Vendor != "" {
				fmt.Printf("      "+i18n.G("Vendor: %s")+"\n", resources.System.Motherboard.Vendor)
			}

			if resources.System.Motherboard.Product != "" {
				fmt.Printf("      "+i18n.G("Product: %s")+"\n", resources.System.Motherboard.Product)
			}

			if resources.System.Motherboard.Serial != "" {
				fmt.Printf("      "+i18n.G("Serial: %s")+"\n", resources.System.Motherboard.Serial)
			}

			if resources.System.Motherboard.Version != "" {
				fmt.Printf("      "+i18n.G("Version: %s")+"\n", resources.System.Motherboard.Version)
			}
		}

		// System: Firmware
		if resources.System.Firmware != nil {
			fmt.Printf(i18n.G("  Firmware:") + "\n")
			if resources.System.Firmware.Vendor != "" {
				fmt.Printf("      "+i18n.G("Vendor: %s")+"\n", resources.System.Firmware.Vendor)
			}

			if resources.System.Firmware.Version != "" {
				fmt.Printf("      "+i18n.G("Version: %s")+"\n", resources.System.Firmware.Version)
			}

			if resources.System.Firmware.Date != "" {
				fmt.Printf("      "+i18n.G("Date: %s")+"\n", resources.System.Firmware.Date)
			}
		}

		// Load
		fmt.Printf("\n" + i18n.G("Load:") + "\n")
		if resources.Load.Processes > 0 {
			fmt.Printf("  "+i18n.G("Processes: %d")+"\n", resources.Load.Processes)
			fmt.Printf("  "+i18n.G("Average: %.2f %.2f %.2f")+"\n", resources.Load.Average1Min, resources.Load.Average5Min, resources.Load.Average10Min)
		}

		// CPU
		if len(resources.CPU.Sockets) == 1 {
			fmt.Printf("\n" + i18n.G("CPU:") + "\n")
			fmt.Printf("  "+i18n.G("Architecture: %s")+"\n", resources.CPU.Architecture)
			c.renderCPU(resources.CPU.Sockets[0], "  ")
		} else if len(resources.CPU.Sockets) > 1 {
			fmt.Printf(i18n.G("CPUs:") + "\n")
			fmt.Printf("  "+i18n.G("Architecture: %s")+"\n", resources.CPU.Architecture)
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

		// USB
		if len(resources.USB.Devices) == 1 {
			fmt.Printf("\n" + i18n.G("USB device:") + "\n")
			c.renderUSB(resources.USB.Devices[0], "  ")
		} else if len(resources.USB.Devices) > 1 {
			fmt.Printf("\n" + i18n.G("USB devices:") + "\n")
			for id, usb := range resources.USB.Devices {
				fmt.Printf("  "+i18n.G("Device %d:")+"\n", id)
				c.renderUSB(usb, "    ")
			}
		}

		// PCI
		if len(resources.PCI.Devices) == 1 {
			fmt.Printf("\n" + i18n.G("PCI device:") + "\n")
			c.renderPCI(resources.PCI.Devices[0], "  ")
		} else if len(resources.PCI.Devices) > 1 {
			fmt.Printf("\n" + i18n.G("PCI devices:") + "\n")
			for id, pci := range resources.PCI.Devices {
				fmt.Printf("  "+i18n.G("Device %d:")+"\n", id)
				c.renderPCI(pci, "    ")
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
		if !inst.State.StartedAt.IsZero() {
			fmt.Printf(i18n.G("Started: %s")+"\n", inst.State.StartedAt.Local().Format(dateLayout))
		}

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
			network := inst.State.Network

			netNames := make([]string, 0, len(network))
			for netName := range network {
				netNames = append(netNames, netName)
			}

			sort.Strings(netNames)

			for _, netName := range netNames {
				networkInfo += fmt.Sprintf("    %s:\n", netName)
				networkInfo += fmt.Sprintf("      %s: %s\n", i18n.G("Type"), network[netName].Type)
				networkInfo += fmt.Sprintf("      %s: %s\n", i18n.G("State"), strings.ToUpper(network[netName].State))
				if network[netName].HostName != "" {
					networkInfo += fmt.Sprintf("      %s: %s\n", i18n.G("Host interface"), network[netName].HostName)
				}

				if network[netName].Hwaddr != "" {
					networkInfo += fmt.Sprintf("      %s: %s\n", i18n.G("MAC address"), network[netName].Hwaddr)
				}

				if network[netName].Mtu != 0 {
					networkInfo += fmt.Sprintf("      %s: %d\n", i18n.G("MTU"), network[netName].Mtu)
				}

				networkInfo += fmt.Sprintf("      %s: %s\n", i18n.G("Bytes received"), units.GetByteSizeString(network[netName].Counters.BytesReceived, 2))
				networkInfo += fmt.Sprintf("      %s: %s\n", i18n.G("Bytes sent"), units.GetByteSizeString(network[netName].Counters.BytesSent, 2))
				networkInfo += fmt.Sprintf("      %s: %d\n", i18n.G("Packets received"), network[netName].Counters.PacketsReceived)
				networkInfo += fmt.Sprintf("      %s: %d\n", i18n.G("Packets sent"), network[netName].Counters.PacketsSent)

				networkInfo += fmt.Sprintf("      %s:\n", i18n.G("IP addresses"))

				for _, addr := range network[netName].Addresses {
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
