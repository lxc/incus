package maas

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/juju/gomaasapi"

	"github.com/cyphar/incus/incus/project"
	"github.com/cyphar/incus/shared/api"
)

// Instance is a MAAS specific instance interface.
// This is used rather than instance.Instance to avoid import loops.
type Instance interface {
	Name() string
	Project() api.Project
}

// Controller represents a MAAS server's machine functions.
type Controller struct {
	url string

	srv     gomaasapi.Controller
	srvRaw  gomaasapi.Client
	machine gomaasapi.Machine
}

// ContainerInterface represents a MAAS connected network interface on the container.
type ContainerInterface struct {
	Name       string
	MACAddress string
	Subnets    []ContainerInterfaceSubnet
}

// ContainerInterfaceSubnet represents an interface's subscription to a MAAS subnet.
type ContainerInterfaceSubnet struct {
	Name    string
	Address string
}

func parseInterfaces(interfaces []ContainerInterface) (map[string]ContainerInterface, error) {
	// Quick checks.
	if len(interfaces) == 0 {
		return nil, fmt.Errorf("At least one interface must be provided")
	}

	// Parse the MACs and interfaces
	macInterfaces := map[string]ContainerInterface{}
	for _, iface := range interfaces {
		_, ok := macInterfaces[iface.MACAddress]
		if ok {
			return nil, fmt.Errorf("MAAS doesn't allow duplicate MAC addresses")
		}

		if iface.MACAddress == "" {
			return nil, fmt.Errorf("Interfaces must have a MAC address")
		}

		if len(iface.Subnets) == 0 {
			return nil, fmt.Errorf("Interfaces must have at least one subnet")
		}

		macInterfaces[iface.MACAddress] = iface
	}

	return macInterfaces, nil
}

// NewController returns a new Controller using the specific MAAS server and machine.
func NewController(url string, key string, machine string) (*Controller, error) {
	baseURL := fmt.Sprintf("%s/api/2.0/", url)

	// Connect to MAAS
	srv, err := gomaasapi.NewController(gomaasapi.ControllerArgs{
		BaseURL: baseURL,
		APIKey:  key,
	})
	if err != nil {
		// Juju errors aren't user-friendly, try to extract what actually happened
		if !strings.Contains(err.Error(), "unsupported version") {
			return nil, err
		}

		return nil, fmt.Errorf("Unable to connect MAAS at '%s': %v", baseURL,
			strings.Split(strings.Split(err.Error(), "unsupported version: ")[1], " (")[0])
	}

	srvRaw, err := gomaasapi.NewAuthenticatedClient(baseURL, key)
	if err != nil {
		return nil, err
	}

	// Find the right machine
	machines, err := srv.Machines(gomaasapi.MachinesArgs{Hostnames: []string{machine}})
	if err != nil {
		return nil, err
	}

	if len(machines) != 1 {
		return nil, fmt.Errorf("Couldn't find the specified machine: %s", machine)
	}

	// Setup the struct
	c := Controller{}
	c.srv = srv
	c.srvRaw = *srvRaw
	c.machine = machines[0]
	c.url = baseURL

	return &c, err
}

func (c *Controller) getDomain(inst Instance) string {
	fields := strings.Split(c.machine.FQDN(), ".")
	domain := strings.Join(fields[1:], ".")

	if inst.Project().Name == project.Default {
		return domain
	}

	return fmt.Sprintf("%s.%s", inst.Project().Name, domain)
}

func (c *Controller) getDevice(name string, domain string) (gomaasapi.Device, error) {
	devs, err := c.machine.Devices(gomaasapi.DevicesArgs{
		Hostname: []string{name},
		Domain:   domain,
	})
	if err != nil {
		return nil, err
	}

	if len(devs) != 1 {
		return nil, fmt.Errorf("Couldn't find the specified instance: %s", name)
	}

	return devs[0], nil
}

func (c *Controller) getSubnets() (map[string]gomaasapi.Subnet, error) {
	// Get all the spaces
	spaces, err := c.srv.Spaces()
	if err != nil {
		return nil, err
	}

	// Get all the subnets
	subnets := map[string]gomaasapi.Subnet{}
	for _, space := range spaces {
		for _, subnet := range space.Subnets() {
			subnets[subnet.Name()] = subnet
		}
	}

	return subnets, nil
}

// CreateContainer defines a new MAAS device for the controller.
func (c *Controller) CreateContainer(inst Instance, interfaces []ContainerInterface) error {
	// Parse the provided interfaces
	macInterfaces, err := parseInterfaces(interfaces)
	if err != nil {
		return err
	}

	// Get all the subnets
	subnets, err := c.getSubnets()
	if err != nil {
		return err
	}

	// Validation
	if len(interfaces) < 1 {
		return fmt.Errorf("Empty list of MAAS interface provided")
	}

	for _, iface := range interfaces {
		if len(iface.Subnets) < 1 {
			return fmt.Errorf("Bad subnet provided for interface '%s'", iface.Name)
		}

		for _, subnet := range iface.Subnets {
			_, ok := subnets[subnet.Name]
			if !ok {
				return fmt.Errorf("Subnet '%s' doesn't exist in MAAS", interfaces[0].Subnets[0].Name)
			}
		}
	}

	// Create the device and first interface
	device, err := c.machine.CreateDevice(gomaasapi.CreateMachineDeviceArgs{
		Hostname:      inst.Name(),
		Domain:        c.getDomain(inst),
		InterfaceName: interfaces[0].Name,
		MACAddress:    interfaces[0].MACAddress,
		VLAN:          subnets[interfaces[0].Subnets[0].Name].VLAN(),
	})
	if err != nil {
		return err
	}

	// Wipe the container entry if anything fails
	success := false
	defer func() {
		if success {
			return
		}

		_ = c.DeleteContainer(inst)
	}()

	// Create the rest of the interfaces
	for _, iface := range interfaces[1:] {
		_, err := device.CreateInterface(gomaasapi.CreateInterfaceArgs{
			Name:       iface.Name,
			MACAddress: iface.MACAddress,
			VLAN:       subnets[iface.Subnets[0].Name].VLAN(),
		})
		if err != nil {
			return err
		}
	}

	// Get a fresh copy of the device
	device, err = c.getDevice(inst.Name(), c.getDomain(inst))
	if err != nil {
		return err
	}

	// Setup the interfaces
	for _, entry := range device.InterfaceSet() {
		// Get our record
		iface, ok := macInterfaces[entry.MACAddress()]
		if !ok {
			return fmt.Errorf("MAAS created an interface with a bad MAC: %s", entry.MACAddress())
		}

		// Add the subnets
		for _, subnet := range iface.Subnets {
			err := entry.LinkSubnet(gomaasapi.LinkSubnetArgs{
				Mode:      gomaasapi.LinkModeStatic,
				Subnet:    subnets[subnet.Name],
				IPAddress: subnet.Address,
			})
			if err != nil {
				return err
			}
		}
	}

	success = true
	return nil
}

// DefinedContainer returns true if the container is defined in MAAS.
func (c *Controller) DefinedContainer(inst Instance) (bool, error) {
	devs, err := c.machine.Devices(gomaasapi.DevicesArgs{
		Hostname: []string{inst.Name()},
		Domain:   c.getDomain(inst),
	})
	if err != nil {
		return false, err
	}

	if len(devs) == 1 {
		return true, nil
	}

	return false, nil
}

// UpdateContainer updates the MAAS device's interfaces with the new provided state.
func (c *Controller) UpdateContainer(inst Instance, interfaces []ContainerInterface) error {
	// Parse the provided interfaces
	macInterfaces, err := parseInterfaces(interfaces)
	if err != nil {
		return err
	}

	// Get all the subnets
	subnets, err := c.getSubnets()
	if err != nil {
		return err
	}

	device, err := c.getDevice(inst.Name(), c.getDomain(inst))
	if err != nil {
		return err
	}

	// Validation
	if len(interfaces) < 1 {
		return fmt.Errorf("Empty list of MAAS interface provided")
	}

	for _, iface := range interfaces {
		if len(iface.Subnets) < 1 {
			return fmt.Errorf("Bad subnet provided for interface '%s'", iface.Name)
		}

		for _, subnet := range iface.Subnets {
			_, ok := subnets[subnet.Name]
			if !ok {
				return fmt.Errorf("Subnet '%s' doesn't exist in MAAS", interfaces[0].Subnets[0].Name)
			}
		}
	}

	// Iterate over existing interfaces, drop all removed ones and update existing ones
	existingInterfaces := map[string]gomaasapi.Interface{}
	for _, entry := range device.InterfaceSet() {
		// Check if the interface has been removed from the container
		iface, ok := macInterfaces[entry.MACAddress()]
		if !ok {
			// Delete the interface in MAAS
			err = entry.Delete()
			if err != nil {
				return err
			}

			continue
		}

		// Update the subnets
		existingSubnets := map[string]gomaasapi.Subnet{}
		for _, link := range entry.Links() {
			// Check if the MAAS subnet matches any of the container's
			found := false
			for _, subnet := range iface.Subnets {
				if subnet.Name == link.Subnet().Name() {
					if subnet.Address == "" || subnet.Address == link.IPAddress() {
						found = true
					}

					break
				}
			}

			// If no exact match could be found, remove it from MAAS
			if !found {
				err = entry.UnlinkSubnet(link.Subnet())
				if err != nil {
					return err
				}

				continue
			}

			// Record the existing up to date subnet
			existingSubnets[link.Subnet().Name()] = link.Subnet()
		}

		// Add any missing (or updated) subnet to MAAS
		for _, subnet := range iface.Subnets {
			// Check that it's not configured yet
			_, ok := existingSubnets[subnet.Name]
			if ok {
				continue
			}

			// Add the link
			err := entry.LinkSubnet(gomaasapi.LinkSubnetArgs{
				Mode:      gomaasapi.LinkModeStatic,
				Subnet:    subnets[subnet.Name],
				IPAddress: subnet.Address,
			})
			if err != nil {
				return err
			}
		}

		// Record the interface has being configured
		existingInterfaces[entry.MACAddress()] = entry
	}

	// Iterate over expected interfaces, add any missing one
	for _, iface := range macInterfaces {
		_, ok := existingInterfaces[iface.MACAddress]
		if ok {
			// We already have it so just move on
			continue
		}

		// Create the new interface
		entry, err := device.CreateInterface(gomaasapi.CreateInterfaceArgs{
			Name:       iface.Name,
			MACAddress: iface.MACAddress,
			VLAN:       subnets[iface.Subnets[0].Name].VLAN(),
		})
		if err != nil {
			return err
		}

		// Add the subnets
		for _, subnet := range iface.Subnets {
			err := entry.LinkSubnet(gomaasapi.LinkSubnetArgs{
				Mode:      gomaasapi.LinkModeStatic,
				Subnet:    subnets[subnet.Name],
				IPAddress: subnet.Address,
			})
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// RenameContainer renames the MAAS device for the container without releasing any allocation.
func (c *Controller) RenameContainer(inst Instance, newName string) error {
	device, err := c.getDevice(inst.Name(), c.getDomain(inst))
	if err != nil {
		return err
	}

	// FIXME: We should convince the Juju folks to implement an Update() method on Device
	uri, err := url.Parse(fmt.Sprintf("%s/devices/%s/", c.url, device.SystemID()))
	if err != nil {
		return err
	}

	values := url.Values{}
	values.Set("hostname", newName)

	_, err = c.srvRaw.Put(uri, values)
	if err != nil {
		return err
	}

	return nil
}

// DeleteContainer removes the MAAS device for the container.
func (c *Controller) DeleteContainer(inst Instance) error {
	device, err := c.getDevice(inst.Name(), c.getDomain(inst))
	if err != nil {
		return err
	}

	err = device.Delete()
	if err != nil {
		return err
	}

	return nil
}
