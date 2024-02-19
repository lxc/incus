package main

import (
	"crypto/tls"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/lxc/incus/internal/linux"
	"github.com/lxc/incus/internal/revert"
	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/ip"
	"github.com/lxc/incus/internal/server/util"
	"github.com/lxc/incus/shared/logger"
	localtls "github.com/lxc/incus/shared/tls"
)

// A variation of the standard tls.Listener that supports atomically swapping
// the underlying TLS configuration. Requests served before the swap will
// continue using the old configuration.
type networkListener struct {
	net.Listener
	mu     sync.RWMutex
	config *tls.Config
}

func networkTLSListener(inner net.Listener, config *tls.Config) *networkListener {
	listener := &networkListener{
		Listener: inner,
		config:   config,
	}

	return listener
}

// Accept waits for and returns the next incoming TLS connection then use the
// current TLS configuration to handle it.
func (l *networkListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	return tls.Server(c, l.config), nil
}

func serverTLSConfig() (*tls.Config, error) {
	certInfo, err := localtls.KeyPairAndCA(".", "agent", localtls.CertServer, false)
	if err != nil {
		return nil, err
	}

	tlsConfig := util.ServerTLSConfig(certInfo)
	return tlsConfig, nil
}

// reconfigureNetworkInterfaces checks for the existence of files under NICConfigDir in the config share.
// Each file is named <device>.json and contains the Device Name, NIC Name, MTU and MAC address.
func reconfigureNetworkInterfaces() {
	nicDirEntries, err := os.ReadDir(deviceConfig.NICConfigDir)
	if err != nil {
		// Abort if configuration folder does not exist (nothing to do), otherwise log and return.
		if os.IsNotExist(err) {
			return
		}

		logger.Error("Could not read network interface configuration directory", logger.Ctx{"err": err})
		return
	}

	// Attempt to load the virtio_net driver in case it's not be loaded yet.
	_ = linux.LoadModule("virtio_net")

	// nicData is a map of MAC address to NICConfig.
	nicData := make(map[string]deviceConfig.NICConfig, len(nicDirEntries))

	for _, f := range nicDirEntries {
		nicBytes, err := os.ReadFile(filepath.Join(deviceConfig.NICConfigDir, f.Name()))
		if err != nil {
			logger.Error("Could not read network interface configuration file", logger.Ctx{"err": err})
		}

		var conf deviceConfig.NICConfig
		err = json.Unmarshal(nicBytes, &conf)
		if err != nil {
			logger.Error("Could not parse network interface configuration file", logger.Ctx{"err": err})
			return
		}

		if conf.MACAddress != "" {
			nicData[conf.MACAddress] = conf
		}
	}

	// configureNIC applies any config specified for the interface based on its current MAC address.
	configureNIC := func(currentNIC net.Interface) error {
		revert := revert.New()
		defer revert.Fail()

		// Look for a NIC config entry for this interface based on its MAC address.
		nic, ok := nicData[currentNIC.HardwareAddr.String()]
		if !ok {
			return nil
		}

		var changeName, changeMTU bool
		if nic.NICName != "" && currentNIC.Name != nic.NICName {
			changeName = true
		}

		if nic.MTU > 0 && currentNIC.MTU != int(nic.MTU) {
			changeMTU = true
		}

		if !changeName && !changeMTU {
			return nil // Nothing to do.
		}

		link := ip.Link{
			Name: currentNIC.Name,
			MTU:  uint32(currentNIC.MTU),
		}

		err := link.SetDown()
		if err != nil {
			return err
		}

		revert.Add(func() {
			_ = link.SetUp()
		})

		// Apply the name from the NIC config if needed.
		if changeName {
			err = link.SetName(nic.NICName)
			if err != nil {
				return err
			}

			revert.Add(func() {
				err := link.SetName(currentNIC.Name)
				if err != nil {
					return
				}

				link.Name = currentNIC.Name
			})

			link.Name = nic.NICName
		}

		// Apply the MTU from the NIC config if needed.
		if changeMTU {
			err = link.SetMTU(nic.MTU)
			if err != nil {
				return err
			}

			link.MTU = nic.MTU

			revert.Add(func() {
				err := link.SetMTU(uint32(currentNIC.MTU))
				if err != nil {
					return
				}

				link.MTU = uint32(currentNIC.MTU)
			})
		}

		err = link.SetUp()
		if err != nil {
			return err
		}

		revert.Success()
		return nil
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		logger.Error("Unable to read network interfaces", logger.Ctx{"err": err})
	}

	for _, iface := range ifaces {
		err = configureNIC(iface)
		if err != nil {
			logger.Error("Unable to reconfigure network interface", logger.Ctx{"interface": iface.Name, "err": err})
		}
	}
}
