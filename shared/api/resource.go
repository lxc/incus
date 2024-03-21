package api

// Resources represents the system hardware resources
//
// swagger:model
//
// API extension: resources.
type Resources struct {
	// CPU information
	CPU ResourcesCPU `json:"cpu" yaml:"cpu"`

	// Memory information
	Memory ResourcesMemory `json:"memory" yaml:"memory"`

	// GPU devices
	//
	// API extension: resources_gpu
	GPU ResourcesGPU `json:"gpu" yaml:"gpu"`

	// Network devices
	//
	// API extension: resources_v2
	Network ResourcesNetwork `json:"network" yaml:"network"`

	// Storage devices
	//
	// API extension: resources_v2
	Storage ResourcesStorage `json:"storage" yaml:"storage"`

	// USB devices
	//
	// API extension: resources_usb_pci
	USB ResourcesUSB `json:"usb" yaml:"usb"`

	// PCI devices
	//
	// API extension: resources_usb_pci
	PCI ResourcesPCI `json:"pci" yaml:"pci"`

	// System information
	//
	// API extension: resources_system
	System ResourcesSystem `json:"system" yaml:"system"`
}

// ResourcesCPU represents the cpu resources available on the system
//
// swagger:model
//
// API extension: resources.
type ResourcesCPU struct {
	// Architecture name
	// Example: x86_64
	//
	// API extension: resources_v2
	Architecture string `json:"architecture" yaml:"architecture"`

	// List of CPU sockets
	Sockets []ResourcesCPUSocket `json:"sockets" yaml:"sockets"`

	// Total number of CPU threads (from all sockets and cores)
	// Example: 1
	Total uint64 `json:"total" yaml:"total"`
}

// ResourcesCPUSocket represents a CPU socket on the system
//
// swagger:model
//
// API extension: resources_v2.
type ResourcesCPUSocket struct {
	// Product name
	// Example: Intel(R) Core(TM) i5-7300U CPU @ 2.60GHz
	Name string `json:"name,omitempty" yaml:"name,omitempty"`

	// Vendor name
	// Example: GenuineIntel
	Vendor string `json:"vendor,omitempty" yaml:"vendor,omitempty"`

	// Socket number
	// Example: 0
	Socket uint64 `json:"socket" yaml:"socket"`

	// List of CPU caches
	Cache []ResourcesCPUCache `json:"cache,omitempty" yaml:"cache,omitempty"`

	// List of CPU cores
	Cores []ResourcesCPUCore `json:"cores" yaml:"cores"`

	// Current CPU frequency (Mhz)
	// Example: 3499
	Frequency uint64 `json:"frequency,omitempty" yaml:"frequency,omitempty"`

	// Minimum CPU frequency (Mhz)
	// Example: 400
	FrequencyMinimum uint64 `json:"frequency_minimum,omitempty" yaml:"frequency_minimum,omitempty"`

	// Maximum CPU frequency (Mhz)
	// Example: 3500
	FrequencyTurbo uint64 `json:"frequency_turbo,omitempty" yaml:"frequency_turbo,omitempty"`
}

// ResourcesCPUCache represents a CPU cache
//
// swagger:model
//
// API extension: resources_v2.
type ResourcesCPUCache struct {
	// Cache level (usually a number from 1 to 3)
	// Example: 1
	Level uint64 `json:"level" yaml:"level"`

	// Type of cache (Data, Instruction, Unified, ...)
	// Example: Data
	Type string `json:"type" yaml:"type"`

	// Size of the cache (in bytes)
	// Example: 32768
	Size uint64 `json:"size" yaml:"size"`
}

// ResourcesCPUCore represents a CPU core on the system
//
// swagger:model
//
// API extension: resources_v2.
type ResourcesCPUCore struct {
	// Core identifier within the socket
	// Example: 0
	Core uint64 `json:"core" yaml:"core"`

	// What die the CPU is a part of (for chiplet designs)
	// Example: 0
	//
	// API extension: resources_cpu_core_die
	Die uint64 `json:"die" yaml:"die"`

	// List of threads
	Threads []ResourcesCPUThread `json:"threads" yaml:"threads"`

	// Current frequency
	// Example: 3500
	Frequency uint64 `json:"frequency,omitempty" yaml:"frequency,omitempty"`
}

// ResourcesCPUThread represents a CPU thread on the system
//
// swagger:model
//
// API extension: resources_v2.
type ResourcesCPUThread struct {
	// Thread ID (used for CPU pinning)
	// Example: 0
	ID int64 `json:"id" yaml:"id"`

	// NUMA node the thread is a part of
	// Example: 0
	NUMANode uint64 `json:"numa_node" yaml:"numa_node"`

	// Thread identifier within the core
	// Example: 0
	Thread uint64 `json:"thread" yaml:"thread"`

	// Whether the thread is online (enabled)
	// Example: true
	Online bool `json:"online" yaml:"online"`

	// Whether the thread has been isolated (outside of normal scheduling)
	// Example: false
	//
	// API extension: resource_cpu_isolated
	Isolated bool `json:"isolated" yaml:"isolated"`
}

// ResourcesGPU represents the GPU resources available on the system
//
// swagger:model
//
// API extension: resources_gpu.
type ResourcesGPU struct {
	// List of GPUs
	Cards []ResourcesGPUCard `json:"cards" yaml:"cards"`

	// Total number of GPUs
	// Example: 1
	Total uint64 `json:"total" yaml:"total"`
}

// ResourcesGPUCard represents a GPU card on the system
//
// swagger:model
//
// API extension: resources_v2.
type ResourcesGPUCard struct {
	// Kernel driver currently associated with the GPU
	// Example: i915
	Driver string `json:"driver,omitempty" yaml:"driver,omitempty"`

	// Version of the kernel driver
	// Example: 5.8.0-36-generic
	DriverVersion string `json:"driver_version,omitempty" yaml:"driver_version,omitempty"`

	// DRM information (if card is in used by the host)
	DRM *ResourcesGPUCardDRM `json:"drm,omitempty" yaml:"drm,omitempty"`

	// SRIOV information (when supported by the card)
	SRIOV *ResourcesGPUCardSRIOV `json:"sriov,omitempty" yaml:"sriov,omitempty"`

	// NVIDIA specific information
	Nvidia *ResourcesGPUCardNvidia `json:"nvidia,omitempty" yaml:"nvidia,omitempty"`

	// Map of available mediated device profiles
	// Example: null
	//
	// API extension: resources_gpu_mdev
	Mdev map[string]ResourcesGPUCardMdev `json:"mdev,omitempty" yaml:"mdev,omitempty"`

	// NUMA node the GPU is a part of
	// Example: 0
	NUMANode uint64 `json:"numa_node" yaml:"numa_node"`

	// PCI address
	// Example: 0000:00:02.0
	PCIAddress string `json:"pci_address,omitempty" yaml:"pci_address,omitempty"`

	// Name of the vendor
	// Example: Intel Corporation
	Vendor string `json:"vendor,omitempty" yaml:"vendor,omitempty"`

	// PCI ID of the vendor
	// Example: 8086
	VendorID string `json:"vendor_id,omitempty" yaml:"vendor_id,omitempty"`

	// Name of the product
	// Example: HD Graphics 620
	Product string `json:"product,omitempty" yaml:"product,omitempty"`

	// PCI ID of the product
	// Example: 5916
	ProductID string `json:"product_id,omitempty" yaml:"product_id,omitempty"`

	// USB address (for USB cards)
	// Example: 2:7
	//
	// API extension: resources_gpu_usb
	USBAddress string `json:"usb_address,omitempty" yaml:"usb_address,omitempty"`
}

// ResourcesGPUCardDRM represents the Linux DRM configuration of the GPU
//
// swagger:model
//
// API extension: resources_v2.
type ResourcesGPUCardDRM struct {
	// DRM card ID
	// Example: 0
	ID uint64 `json:"id" yaml:"id"`

	// Card device name
	// Example: card0
	CardName string `json:"card_name" yaml:"card_name"`

	// Card device number
	// Example: 226:0
	CardDevice string `json:"card_device" yaml:"card_device"`

	// Control device name
	// Example: controlD64
	ControlName string `json:"control_name,omitempty" yaml:"control_name,omitempty"`

	// Control device number
	// Example: 226:0
	ControlDevice string `json:"control_device,omitempty" yaml:"control_device,omitempty"`

	// Render device name
	// Example: renderD128
	RenderName string `json:"render_name,omitempty" yaml:"render_name,omitempty"`

	// Render device number
	// Example: 226:128
	RenderDevice string `json:"render_device,omitempty" yaml:"render_device,omitempty"`
}

// ResourcesGPUCardSRIOV represents the SRIOV configuration of the GPU
//
// swagger:model
//
// API extension: resources_v2.
type ResourcesGPUCardSRIOV struct {
	// Number of VFs currently configured
	// Example: 0
	CurrentVFs uint64 `json:"current_vfs" yaml:"current_vfs"`

	// Maximum number of supported VFs
	// Example: 0
	MaximumVFs uint64 `json:"maximum_vfs" yaml:"maximum_vfs"`

	// List of VFs (as additional GPU devices)
	// Example: null
	VFs []ResourcesGPUCard `json:"vfs" yaml:"vfs"`
}

// ResourcesGPUCardNvidia represents additional information for NVIDIA GPUs
//
// swagger:model
//
// API extension: resources_gpu.
type ResourcesGPUCardNvidia struct {
	// Version of the CUDA API
	// Example: 11.0
	CUDAVersion string `json:"cuda_version,omitempty" yaml:"cuda_version,omitempty"`

	// Version of the NVRM (usually driver version)
	// Example: 450.102.04
	NVRMVersion string `json:"nvrm_version,omitempty" yaml:"nvrm_version,omitempty"`

	// Brand name
	// Example: GeForce
	Brand string `json:"brand" yaml:"brand"`

	// Model name
	// Example: GeForce GT 730
	Model string `json:"model" yaml:"model"`

	// GPU UUID
	// Example: GPU-6ddadebd-dafe-2db9-f10f-125719770fd3
	UUID string `json:"uuid,omitempty" yaml:"uuid,omitempty"`

	// Architecture (generation)
	// Example: 3.5
	Architecture string `json:"architecture,omitempty" yaml:"architecture,omitempty"`

	// Card device name
	// Example: nvidia0
	//
	// API extension: resources_v2
	CardName string `json:"card_name" yaml:"card_name"`

	// Card device number
	// Example: 195:0
	//
	// API extension: resources_v2
	CardDevice string `json:"card_device" yaml:"card_device"`
}

// ResourcesGPUCardMdev represents the mediated devices configuration of the GPU
//
// swagger:model
//
// API extension: resources_gpu_mdev.
type ResourcesGPUCardMdev struct {
	// The mechanism used by this device
	// Example: vfio-pci
	API string `json:"api" yaml:"api"`

	// Number of available devices of this profile
	// Example: 2
	Available uint64 `json:"available" yaml:"available"`

	// Profile name
	// Example: i915-GVTg_V5_8
	Name string `json:"name,omitempty" yaml:"name,omitempty"`

	// Profile description
	// Example: low_gm_size: 128MB\nhigh_gm_size: 512MB\nfence: 4\nresolution: 1920x1200\nweight: 4
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// List of active devices (UUIDs)
	// Example: ["42200aac-0977-495c-8c9e-6c51b9092a01", "b4950c00-1437-41d9-88f6-28d61cf9b9ef"]
	Devices []string `json:"devices" yaml:"devices"`
}

// ResourcesNetwork represents the network cards available on the system
//
// swagger:model
//
// API extension: resources_v2.
type ResourcesNetwork struct {
	// List of network cards
	Cards []ResourcesNetworkCard `json:"cards" yaml:"cards"`

	// Total number of network cards
	// Example: 1
	Total uint64 `json:"total" yaml:"total"`
}

// ResourcesNetworkCard represents a network card on the system
//
// swagger:model
//
// API extension: resources_v2.
type ResourcesNetworkCard struct {
	// Kernel driver currently associated with the card
	// Example: atlantic
	Driver string `json:"driver,omitempty" yaml:"driver,omitempty"`

	// Version of the kernel driver
	// Example: 5.8.0-36-generic
	DriverVersion string `json:"driver_version,omitempty" yaml:"driver_version,omitempty"`

	// List of ports on the card
	Ports []ResourcesNetworkCardPort `json:"ports,omitempty" yaml:"ports,omitempty"`

	// SRIOV information (when supported by the card)
	SRIOV *ResourcesNetworkCardSRIOV `json:"sriov,omitempty" yaml:"sriov,omitempty"`

	// vDPA information (when supported by the card)
	//
	// API extension: ovn_nic_acceleration_vdpa
	VDPA *ResourcesNetworkCardVDPA `json:"vdpa,omitempty" yaml:"vdpa,omitempty"`

	// NUMA node the card is a part of
	// Example: 0
	NUMANode uint64 `json:"numa_node" yaml:"numa_node"`

	// PCI address (for PCI cards)
	// Example: 0000:0d:00.0
	PCIAddress string `json:"pci_address,omitempty" yaml:"pci_address,omitempty"`

	// Name of the vendor
	// Example: Aquantia Corp.
	Vendor string `json:"vendor,omitempty" yaml:"vendor,omitempty"`

	// PCI ID of the vendor
	// Example: 1d6a
	VendorID string `json:"vendor_id,omitempty" yaml:"vendor_id,omitempty"`

	// Name of the product
	// Example: AQC107 NBase-T/IEEE
	Product string `json:"product,omitempty" yaml:"product,omitempty"`

	// PCI ID of the product
	// Example: 87b1
	ProductID string `json:"product_id,omitempty" yaml:"product_id,omitempty"`

	// Current firmware version
	// Example: 3.1.100
	//
	// API extension: resources_network_firmware
	FirmwareVersion string `json:"firmware_version,omitempty" yaml:"firmware_version,omitempty"`

	// USB address (for USB cards)
	// Example: 2:7
	//
	// API extension: resources_network_usb
	USBAddress string `json:"usb_address,omitempty" yaml:"usb_address,omitempty"`
}

// ResourcesNetworkCardPort represents a network port on the system
//
// swagger:model
//
// API extension: resources_v2.
type ResourcesNetworkCardPort struct {
	// Port identifier (interface name)
	// Example: eth0
	ID string `json:"id" yaml:"id"`

	// MAC address
	// Example: 00:23:a4:01:01:6f
	Address string `json:"address,omitempty" yaml:"address,omitempty"`

	// Port number
	// Example: 0
	Port uint64 `json:"port" yaml:"port"`

	// Transport protocol
	// Example: ethernet
	Protocol string `json:"protocol" yaml:"protocol"`

	// List of supported modes
	// Example: ["100baseT/Full", "1000baseT/Full", "2500baseT/Full", "5000baseT/Full", "10000baseT/Full"]
	SupportedModes []string `json:"supported_modes,omitempty" yaml:"supported_modes,omitempty"`

	// List of supported port types
	// Example: ["twisted pair"]
	SupportedPorts []string `json:"supported_ports,omitempty" yaml:"supported_ports,omitempty"`

	// Current port type
	// Example: twisted pair
	PortType string `json:"port_type,omitempty" yaml:"port_type,omitempty"`

	// Type of transceiver used
	// Example: internal
	TransceiverType string `json:"transceiver_type,omitempty" yaml:"transceiver_type,omitempty"`

	// Whether auto negotiation is used
	// Example: true
	AutoNegotiation bool `json:"auto_negotiation" yaml:"auto_negotiation"`

	// Whether a link was detected
	// Example: true
	LinkDetected bool `json:"link_detected" yaml:"link_detected"`

	// Current speed (Mbit/s)
	// Example: 10000
	LinkSpeed uint64 `json:"link_speed,omitempty" yaml:"link_speed,omitempty"`

	// Duplex type
	// Example: full
	LinkDuplex string `json:"link_duplex,omitempty" yaml:"link_duplex,omitempty"`

	// Additional information for infiniband devices
	//
	// API extension: resources_infiniband
	Infiniband *ResourcesNetworkCardPortInfiniband `json:"infiniband,omitempty" yaml:"infiniband,omitempty"`
}

// ResourcesNetworkCardPortInfiniband represents the Linux Infiniband configuration for the port
//
// swagger:model
//
// API extension: resources_infiniband.
type ResourcesNetworkCardPortInfiniband struct {
	// ISSM device name
	// Example: issm0
	IsSMName string `json:"issm_name,omitempty" yaml:"issm_name,omitempty"`

	// ISSM device number
	// Example: 231:64
	IsSMDevice string `json:"issm_device,omitempty" yaml:"issm_device,omitempty"`

	// MAD device name
	// Example: umad0
	MADName string `json:"mad_name,omitempty" yaml:"mad_name,omitempty"`

	// MAD device number
	// Example: 231:0
	MADDevice string `json:"mad_device,omitempty" yaml:"mad_device,omitempty"`

	// Verb device name
	// Example: uverbs0
	VerbName string `json:"verb_name,omitempty" yaml:"verb_name,omitempty"`

	// Verb device number
	// Example: 231:192
	VerbDevice string `json:"verb_device,omitempty" yaml:"verb_device,omitempty"`
}

// ResourcesNetworkCardSRIOV represents the SRIOV configuration of the network card
//
// swagger:model
//
// API extension: resources_v2.
type ResourcesNetworkCardSRIOV struct {
	// Number of VFs currently configured
	// Example: 0
	CurrentVFs uint64 `json:"current_vfs" yaml:"current_vfs"`

	// Maximum number of supported VFs
	// Example: 0
	MaximumVFs uint64 `json:"maximum_vfs" yaml:"maximum_vfs"`

	// List of VFs (as additional Network devices)
	// Example: null
	VFs []ResourcesNetworkCard `json:"vfs" yaml:"vfs"`
}

// ResourceNetworkCardVDPA represents the VDPA configuration of the network card
//
// swagger:model
//
// API extension: ovn_nic_acceleration_vdpa.
type ResourcesNetworkCardVDPA struct {
	// Name of the VDPA device
	Name string `json:"name" yaml:"name"`

	// Device identifier of the VDPA device
	Device string `json:"device" yaml:"device"`
}

// ResourcesStorage represents the local storage
//
// swagger:model
//
// API extension: resources_v2.
type ResourcesStorage struct {
	// List of disks
	Disks []ResourcesStorageDisk `json:"disks" yaml:"disks"`

	// Total number of partitions
	// Example: 1
	Total uint64 `json:"total" yaml:"total"`
}

// ResourcesStorageDisk represents a disk
//
// swagger:model
//
// API extension: resources_v2.
type ResourcesStorageDisk struct {
	// ID of the disk (device name)
	// Example: nvme0n1
	ID string `json:"id" yaml:"id"`

	// Device number
	// Example: 259:0
	Device string `json:"device" yaml:"device"`

	// Disk model name
	// Example: INTEL SSDPEKKW256G7
	Model string `json:"model,omitempty" yaml:"model,omitempty"`

	// Storage type
	// Example: nvme
	Type string `json:"type,omitempty" yaml:"type,omitempty"`

	// Whether the disk is read-only
	// Example: false
	ReadOnly bool `json:"read_only" yaml:"read_only"`

	// Total size of the disk (bytes)
	// Example: 256060514304
	Size uint64 `json:"size" yaml:"size"`

	// Whether the disk is removable (hot-plug)
	// Example: false
	Removable bool `json:"removable" yaml:"removable"`

	// WWN identifier
	// Example: eui.0000000001000000e4d25cafae2e4c00
	WWN string `json:"wwn,omitempty" yaml:"wwn,omitempty"`

	// NUMA node the disk is a part of
	// Example: 0
	NUMANode uint64 `json:"numa_node" yaml:"numa_node"`

	// Device by-path identifier
	// Example: pci-0000:05:00.0-nvme-1
	//
	// API extension: resources_disk_sata
	DevicePath string `json:"device_path,omitempty" yaml:"device_path,omitempty"`

	// Block size
	// Example: 512
	//
	// API extension: resources_disk_sata
	BlockSize uint64 `json:"block_size" yaml:"block_size"`

	// Current firmware version
	// Example: PSF121C
	//
	// API extension: resources_disk_sata
	FirmwareVersion string `json:"firmware_version,omitempty" yaml:"firmware_version,omitempty"`

	// Rotation speed (RPM)
	// Example: 0
	//
	// API extension: resources_disk_sata
	RPM uint64 `json:"rpm" yaml:"rpm"`

	// Serial number
	// Example: BTPY63440ARH256D
	//
	// API extension: resources_disk_sata
	Serial string `json:"serial,omitempty" yaml:"serial,omitempty"`

	// Device by-id identifier
	// Example: nvme-eui.0000000001000000e4d25cafae2e4c00
	//
	// API extension: resources_disk_id
	DeviceID string `json:"device_id" yaml:"device_id"`

	// List of partitions
	Partitions []ResourcesStorageDiskPartition `json:"partitions" yaml:"partitions"`

	// PCI address
	// Example: 0000:05:00.0
	//
	// API extension: resources_disk_address
	PCIAddress string `json:"pci_address,omitempty" yaml:"pci_address,omitempty"`

	// USB address
	// Example: 3:5
	//
	// API extension: resources_disk_address
	USBAddress string `json:"usb_address,omitempty" yaml:"usb_address,omitempty"`
}

// ResourcesStorageDiskPartition represents a partition on a disk
//
// swagger:model
//
// API extension: resources_v2.
type ResourcesStorageDiskPartition struct {
	// ID of the partition (device name)
	// Example: nvme0n1p1
	ID string `json:"id" yaml:"id"`

	// Device number
	// Example: 259:1
	Device string `json:"device" yaml:"device"`

	// Whether the partition is read-only
	// Example: false
	ReadOnly bool `json:"read_only" yaml:"read_only"`

	// Size of the partition (bytes)
	// Example: 254933278208
	Size uint64 `json:"size" yaml:"size"`

	// Partition number
	// Example: 1
	Partition uint64 `json:"partition" yaml:"partition"`
}

// ResourcesMemory represents the memory resources available on the system
//
// swagger:model
//
// API extension: resources.
type ResourcesMemory struct {
	// List of NUMA memory nodes
	// Example: null
	//
	// API extension: resources_v2
	Nodes []ResourcesMemoryNode `json:"nodes,omitempty" yaml:"nodes,omitempty"`

	// Total of memory huge pages (bytes)
	// Example: 429284917248
	HugepagesTotal uint64 `json:"hugepages_total" yaml:"hugepages_total"`

	// Used memory huge pages (bytes)
	// Example: 429284917248
	HugepagesUsed uint64 `json:"hugepages_used" yaml:"hugepages_used"`

	// Size of memory huge pages (bytes)
	// Example: 2097152
	HugepagesSize uint64 `json:"hugepages_size" yaml:"hugepages_size"`

	// Used system memory (bytes)
	// Example: 557450502144
	Used uint64 `json:"used" yaml:"used"`

	// Total system memory (bytes)
	// Example: 687194767360
	Total uint64 `json:"total" yaml:"total"`
}

// ResourcesMemoryNode represents the node-specific memory resources available on the system
//
// swagger:model
//
// API extension: resources_v2.
type ResourcesMemoryNode struct {
	// NUMA node identifier
	// Example: 0
	NUMANode uint64 `json:"numa_node" yaml:"numa_node"`

	// Used memory huge pages (bytes)
	// Example: 214536552448
	HugepagesUsed uint64 `json:"hugepages_used" yaml:"hugepages_used"`

	// Total of memory huge pages (bytes)
	// Example: 214536552448
	HugepagesTotal uint64 `json:"hugepages_total" yaml:"hugepages_total"`

	// Used system memory (bytes)
	// Example: 264880439296
	Used uint64 `json:"used" yaml:"used"`

	// Total system memory (bytes)
	// Example: 343597383680
	Total uint64 `json:"total" yaml:"total"`
}

// ResourcesStoragePool represents the resources available to a given storage pool
//
// swagger:model
//
// API extension: resources.
type ResourcesStoragePool struct {
	// Disk space usage
	Space ResourcesStoragePoolSpace `json:"space,omitempty" yaml:"space,omitempty"`

	// DIsk inode usage
	Inodes ResourcesStoragePoolInodes `json:"inodes,omitempty" yaml:"inodes,omitempty"`
}

// ResourcesStoragePoolSpace represents the space available to a given storage pool
//
// swagger:model
//
// API extension: resources.
type ResourcesStoragePoolSpace struct {
	// Used disk space (bytes)
	// Example: 343537419776
	Used uint64 `json:"used,omitempty" yaml:"used,omitempty"`

	// Total disk space (bytes)
	// Example: 420100937728
	Total uint64 `json:"total" yaml:"total"`
}

// ResourcesStoragePoolInodes represents the inodes available to a given storage pool
//
// swagger:model
//
// API extension: resources.
type ResourcesStoragePoolInodes struct {
	// Used inodes
	// Example: 23937695
	Used uint64 `json:"used" yaml:"used"`

	// Total inodes
	// Example: 30709993797
	Total uint64 `json:"total" yaml:"total"`
}

// ResourcesUSB represents the USB devices available on the system
//
// swagger:model
//
// API extension: resources_usb_pci.
type ResourcesUSB struct {
	// List of USB devices
	Devices []ResourcesUSBDevice `json:"devices" yaml:"devices"`

	// Total number of USB devices
	// Example: 1
	Total uint64 `json:"total" yaml:"total"`
}

// ResourcesUSBDevice represents a USB device
//
// swagger:model
//
// API extension: resources_usb_pci.
type ResourcesUSBDevice struct {
	// USB address (bus)
	// Example: 1
	BusAddress uint64 `json:"bus_address" yaml:"bus_address"`

	// USB address (device)
	// Example: 3
	DeviceAddress uint64 `json:"device_address" yaml:"device_address"`

	// USB serial number
	// Example: DAE005fp
	//
	// API extension: device_usb_serial.
	Serial string `json:"serial" yaml:"serial"`

	// List of USB interfaces
	Interfaces []ResourcesUSBDeviceInterface `json:"interfaces" yaml:"interfaces"`

	// Name of the vendor
	// Example: ATEN International Co., Ltd
	Vendor string `json:"vendor" yaml:"vendor"`

	// USB ID of the vendor
	// Example: 0557
	VendorID string `json:"vendor_id" yaml:"vendor_id"`

	// Name of the product
	// Example: Hermon USB hidmouse Device
	Product string `json:"product" yaml:"product"`

	// USB ID of the product
	// Example: 2221
	ProductID string `json:"product_id" yaml:"product_id"`

	// Transfer speed (Mbit/s)
	// Example: 12
	Speed float64 `json:"speed" yaml:"speed"`
}

// ResourcesUSBDeviceInterface represents a USB device interface
//
// swagger:model
//
// API extension: resources_usb_pci.
type ResourcesUSBDeviceInterface struct {
	// Class of USB interface
	// Example: Human Interface Device
	Class string `json:"class" yaml:"class"`

	// ID of the USB interface class
	// Example: 3
	ClassID uint64 `json:"class_id" yaml:"class_id"`

	// Kernel driver currently associated with the device
	// Example: usbhid
	Driver string `json:"driver" yaml:"driver"`

	// Version of the kernel driver
	// Example: 5.8.0-36-generic
	DriverVersion string `json:"driver_version" yaml:"driver_version"`

	// Interface number
	// Example: 0
	Number uint64 `json:"number" yaml:"number"`

	// Sub class of the interface
	// Example: Boot Interface Subclass
	SubClass string `json:"subclass" yaml:"subclass"`

	// ID of the USB interface sub class
	// Example: 1
	SubClassID uint64 `json:"subclass_id" yaml:"subclass_id"`
}

// ResourcesPCI represents the PCI devices available on the system
//
// swagger:model
//
// API extension: resources_usb_pci.
type ResourcesPCI struct {
	// List of PCI devices
	Devices []ResourcesPCIDevice `json:"devices" yaml:"devices"`

	// Total number of PCI devices
	// Example: 1
	Total uint64 `json:"total" yaml:"total"`
}

// ResourcesPCIDevice represents a PCI device
//
// swagger:model
//
// API extension: resources_usb_pci.
type ResourcesPCIDevice struct {
	// Kernel driver currently associated with the GPU
	// Example: mgag200
	Driver string `json:"driver" yaml:"driver"`

	// Version of the kernel driver
	// Example: 5.8.0-36-generic
	DriverVersion string `json:"driver_version" yaml:"driver_version"`

	// NUMA node the card is a part of
	// Example: 0
	NUMANode uint64 `json:"numa_node" yaml:"numa_node"`

	// PCI address
	// Example: 0000:07:03.0
	PCIAddress string `json:"pci_address" yaml:"pci_address"`

	// Name of the vendor
	// Example: Matrox Electronics Systems Ltd.
	Vendor string `json:"vendor" yaml:"vendor"`

	// PCI ID of the vendor
	// Example: 102b
	VendorID string `json:"vendor_id" yaml:"vendor_id"`

	// Name of the product
	// Example: MGA G200eW WPCM450
	Product string `json:"product" yaml:"product"`

	// PCI ID of the product
	// Example: 0532
	ProductID string `json:"product_id" yaml:"product_id"`

	// IOMMU group number
	// Example: 20
	//
	// API extension: resources_pci_iommu
	IOMMUGroup uint64 `json:"iommu_group" yaml:"iommu_group"`

	// Vital Product Data
	// Example:
	//
	// API extension: resources_pci_vpd
	VPD ResourcesPCIVPD `json:"vpd" yaml:"vpd"`
}

// ResourcesPCIVPD represents VPD entries for a device
//
// swagger:model
//
// API extension: resources_pci_vpd.
type ResourcesPCIVPD struct {
	// Hardware provided product name.
	// Example: HP Ethernet 1Gb 4-port 331i Adapter
	ProductName string `json:"product_name,omitempty" yaml:"product_name,omitempty"`

	// Vendor provided key/value pairs.
	// Example: {"EC": ""A-5545", "MN": "103C", "V0": "5W PCIeGen2"}
	Entries map[string]string `json:"entries,omitempty" yaml:"entries,omitempty"`
}

// ResourcesSystem represents the system
//
// swagger:model
//
// API extension: resources_system.
type ResourcesSystem struct {
	// System UUID
	// Example: 7fa1c0cc-2271-11b2-a85c-aab32a05d71a
	UUID string `json:"uuid" yaml:"uuid"`

	// System vendor
	// Example: LENOVO
	Vendor string `json:"vendor" yaml:"vendor"`

	// System model
	// Example: 20HRCTO1WW
	Product string `json:"product" yaml:"product"`

	// System family
	// Example: ThinkPad X1 Carbon 5th
	Family string `json:"family" yaml:"family"`

	// System version
	// Example: ThinkPad X1 Carbon 5th
	Version string `json:"version" yaml:"version"`

	// System nanufacturer SKU
	// LENOVO_MT_20HR_BU_Think_FM_ThinkPad X1 Carbon 5th
	Sku string `json:"sku" yaml:"sku"`

	// System serial number
	// Example: PY3DD4X9
	Serial string `json:"serial" yaml:"serial"`

	// System type (unknown, physical, virtual-machine, container, ...)
	// Example: physical
	Type string `json:"type" yaml:"type"`

	// Firmware details
	Firmware *ResourcesSystemFirmware `json:"firmware" yaml:"firmware"`

	// Chassis details
	Chassis *ResourcesSystemChassis `json:"chassis" yaml:"chassis"`

	// Motherboard details
	Motherboard *ResourcesSystemMotherboard `json:"motherboard" yaml:"motherboard"`
}

// ResourcesSystemFirmware represents the system firmware
//
// swagger:model
//
// API extension: resources_system.
type ResourcesSystemFirmware struct {
	// Firmware vendor
	// Example: Lenovo
	Vendor string `json:"vendor" yaml:"vendor"`

	// Firmware build date
	// Example: 10/14/2020
	Date string `json:"date" yaml:"date"`

	// Firmware version
	// Example: N1MET64W (1.49)
	Version string `json:"version" yaml:"version"`
}

// ResourcesSystemChassis represents the system chassis
//
// swagger:model
//
// API extension: resources_system.
type ResourcesSystemChassis struct {
	// Chassis vendor
	// Example: Lenovo
	Vendor string `json:"vendor" yaml:"vendor"`

	// Chassis type
	// Example: Notebook
	Type string `json:"type" yaml:"type"`

	// Chassis serial number
	// Example: PY3DD4X9
	Serial string `json:"serial" yaml:"serial"`

	// Chassis version/revision
	// Example: None
	Version string `json:"version" yaml:"version"`
}

// ResourcesSystemMotherboard represents the motherboard
//
// swagger:model
//
// API extension: resources_system.
type ResourcesSystemMotherboard struct {
	// Motherboard vendor
	// Example: Lenovo
	Vendor string `json:"vendor" yaml:"vendor"`

	// Motherboard model
	// Example: 20HRCTO1WW
	Product string `json:"product" yaml:"product"`

	// Motherboard serial number
	// Example: L3CF4FX003A
	Serial string `json:"serial" yaml:"serial"`

	// Motherboard version/revision
	// Example: None
	Version string `json:"version" yaml:"version"`
}
