//go:build linux && cgo && !agent

package cluster

import (
	"fmt"
)

// Code generation directives.
//
//go:generate -command mapper incus-generate db mapper -t devices.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e device objects
//go:generate mapper stmt -e device create struct=Device
//go:generate mapper stmt -e device delete
//
//go:generate mapper method -i -e device GetMany
//go:generate mapper method -i -e device Create struct=Device
//go:generate mapper method -i -e device Update struct=Device
//go:generate mapper method -i -e device DeleteMany

// DeviceType represents the types of supported devices.
type DeviceType int

// Device is a reference struct representing another entity's device.
type Device struct {
	ID          int
	ReferenceID int
	Name        string
	Type        DeviceType
	Config      map[string]string
}

// DeviceFilter specifies potential query parameter fields.
type DeviceFilter struct {
	Name   *string
	Type   *DeviceType
	Config *ConfigFilter
}

// Supported device types.
const (
	TypeNone        = DeviceType(0)
	TypeNIC         = DeviceType(1)
	TypeDisk        = DeviceType(2)
	TypeUnixChar    = DeviceType(3)
	TypeUnixBlock   = DeviceType(4)
	TypeUSB         = DeviceType(5)
	TypeGPU         = DeviceType(6)
	TypeInfiniband  = DeviceType(7)
	TypeProxy       = DeviceType(8)
	TypeUnixHotplug = DeviceType(9)
	TypeTPM         = DeviceType(10)
	TypePCI         = DeviceType(11)
)

func (t DeviceType) String() string {
	switch t {
	case TypeNone:
		return "none"
	case TypeNIC:
		return "nic"
	case TypeDisk:
		return "disk"
	case TypeUnixChar:
		return "unix-char"
	case TypeUnixBlock:
		return "unix-block"
	case TypeUSB:
		return "usb"
	case TypeGPU:
		return "gpu"
	case TypeInfiniband:
		return "infiniband"
	case TypeProxy:
		return "proxy"
	case TypeUnixHotplug:
		return "unix-hotplug"
	case TypeTPM:
		return "tpm"
	case TypePCI:
		return "pci"
	}

	return ""
}

// NewDeviceType determines the device type from the given string, if supported.
func NewDeviceType(t string) (DeviceType, error) {
	switch t {
	case "none":
		return TypeNone, nil
	case "nic":
		return TypeNIC, nil
	case "disk":
		return TypeDisk, nil
	case "unix-char":
		return TypeUnixChar, nil
	case "unix-block":
		return TypeUnixBlock, nil
	case "usb":
		return TypeUSB, nil
	case "gpu":
		return TypeGPU, nil
	case "infiniband":
		return TypeInfiniband, nil
	case "proxy":
		return TypeProxy, nil
	case "unix-hotplug":
		return TypeUnixHotplug, nil
	case "tpm":
		return TypeTPM, nil
	case "pci":
		return TypePCI, nil
	default:
		return -1, fmt.Errorf("Invalid device type %q", t)
	}
}

// DevicesToAPI takes a map of devices and converts them to API format.
func DevicesToAPI(devices map[string]Device) map[string]map[string]string {
	config := map[string]map[string]string{}
	for _, d := range devices {
		if d.Config == nil {
			d.Config = map[string]string{}
		}

		config[d.Name] = d.Config
		config[d.Name]["type"] = d.Type.String()
	}

	return config
}

// APIToDevices takes an API format devices map and converts it to a map of db.Device.
func APIToDevices(apiDevices map[string]map[string]string) (map[string]Device, error) {
	devices := map[string]Device{}
	for name, config := range apiDevices {
		newType, err := NewDeviceType(config["type"])
		if err != nil {
			return nil, err
		}

		device := Device{
			Name:   name,
			Type:   newType,
			Config: config,
		}

		devices[name] = device
	}

	return devices, nil
}
