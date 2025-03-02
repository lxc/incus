package drivers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
)

// CephGetRBDImageName returns the RBD image name as it is used in ceph.
// Example:
// A custom block volume named vol1 in project default will return custom_default_vol1.block.
func CephGetRBDImageName(vol Volume, snapName string, zombie bool) string {
	var out string
	parentName, snapshotName, isSnapshot := api.GetParentAndSnapshotName(vol.name)

	// Only use filesystem suffix on filesystem type image volumes (for all content types).
	if vol.volType == VolumeTypeImage || vol.volType == cephVolumeTypeZombieImage {
		parentName = fmt.Sprintf("%s_%s", parentName, vol.ConfigBlockFilesystem())
	}

	if vol.contentType == ContentTypeBlock {
		parentName = fmt.Sprintf("%s%s", parentName, cephBlockVolSuffix)
	} else if vol.contentType == ContentTypeISO {
		parentName = fmt.Sprintf("%s%s", parentName, cephISOVolSuffix)
	}

	// Use volume's type as storage volume prefix, unless there is an override in cephVolTypePrefixes.
	volumeTypePrefix := string(vol.volType)
	volumeTypePrefixOverride, foundOveride := cephVolTypePrefixes[vol.volType]
	if foundOveride {
		volumeTypePrefix = volumeTypePrefixOverride
	}

	if snapName != "" {
		// Always use the provided snapshot name if specified.
		out = fmt.Sprintf("%s_%s@%s", volumeTypePrefix, parentName, snapName)
	} else {
		if isSnapshot {
			// If volumeName is a snapshot (<vol>/<snap>) and snapName is not set,
			// assume that it's a normal snapshot (not a zombie) and prefix it with
			// "snapshot_".
			out = fmt.Sprintf("%s_%s@snapshot_%s", volumeTypePrefix, parentName, snapshotName)
		} else {
			out = fmt.Sprintf("%s_%s", volumeTypePrefix, parentName)
		}
	}

	// If the volume is to be in zombie state (i.e. not tracked in the database),
	// prefix the output with "zombie_".
	if zombie {
		out = fmt.Sprintf("zombie_%s", out)
	}

	return out
}

// CephBuildMount creates a mount string and option list from mount parameters.
func CephBuildMount(user string, key string, fsid string, monitors Monitors, fsName string, path string) (source string, options []string) {
	// Ceph mount paths must begin with a '/', if it doesn't (or is empty).
	// prefix it now. The leading '/' can be stripped out during option parsing.
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	msgrV2 := false
	monAddrs := monitors.V1
	if len(monitors.V2) > 0 {
		msgrV2 = true
		monAddrs = monitors.V2
	}

	// Build the source path.
	source = fmt.Sprintf("%s@%s.%s=%s", user, fsid, fsName, path)

	// Build the options list.
	options = []string{
		"mon_addr=" + strings.Join(monAddrs, "/"),
		"name=" + user,
	}

	// If key is blank assume cephx is disabled.
	if key != "" {
		options = append(options, "secret="+key)
	}

	// Pick connection mode.
	if msgrV2 {
		options = append(options, "ms_mode=prefer-crc")
	} else {
		options = append(options, "ms_mode=legacy")
	}

	return source, options
}

// callCeph makes a call to ceph with the given args.
func callCeph(args ...string) (string, error) {
	out, err := subprocess.RunCommand("ceph", args...)
	logger.Debug("callCeph", logger.Ctx{
		"cmd":  "ceph",
		"args": args,
		"err":  err,
		"out":  out,
	})
	return strings.TrimSpace(out), err
}

// callCephJSON makes a call to the `ceph` admin tool with the given args then parses the json output into `out`.
func callCephJSON(out any, args ...string) error {
	// Get as JSON format.
	args = append([]string{"--format", "json"}, args...)

	// Make the call.
	jsonOut, err := callCeph(args...)
	if err != nil {
		return err
	}

	// Parse the JSON.
	err = json.Unmarshal([]byte(jsonOut), &out)
	return err
}

// Monitors holds a list of ceph monitor addresses based on which protocol they expect.
type Monitors struct {
	V1 []string
	V2 []string
}

// CephMonitors returns a list of public monitor IP:ports for the given cluster.
func CephMonitors(cluster string) (Monitors, error) {
	// Get the monitor dump, there may be other better ways but this is quick and easy.
	monitors := struct {
		Mons []struct {
			PublicAddrs struct {
				Addrvec []struct {
					Type string `json:"type"`
					Addr string `json:"addr"`
				} `json:"addrvec"`
			} `json:"public_addrs"`
		} `json:"mons"`
	}{}

	err := callCephJSON(&monitors,
		"--cluster", cluster,
		"mon", "dump",
	)
	if err != nil {
		return Monitors{}, fmt.Errorf("Ceph mon dump for %q failed: %w", cluster, err)
	}

	// Loop through monitors then monitor addresses and add them to the list.
	var ep Monitors
	for _, mon := range monitors.Mons {
		for _, addr := range mon.PublicAddrs.Addrvec {
			if addr.Type == "v1" {
				ep.V1 = append(ep.V1, addr.Addr)
			} else if addr.Type == "v2" {
				ep.V2 = append(ep.V2, addr.Addr)
			} else {
				logger.Warnf("Unknown ceph monitor address type: %q:%q",
					addr.Type, addr.Addr,
				)
			}
		}
	}

	if len(ep.V2) == 0 {
		if len(ep.V1) == 0 {
			return Monitors{}, fmt.Errorf("No ceph monitors for %q", cluster)
		}

		logger.Warnf("Only found v1 monitors for ceph cluster %q", cluster)
	}

	return ep, nil
}

// CephKeyring retrieves the CephX key for the given entity.
func CephKeyring(cluster string, client string) (string, error) {
	// See if we can't find it from the filesystem directly (short path).
	value, err := cephKeyringFromFile(cluster, client)
	if err == nil {
		return value, nil
	}

	// If client isn't prefixed, prefix it with 'client.'.
	if !strings.Contains(client, ".") {
		client = "client." + client
	}

	// Check that cephx is enabled.
	authType, err := callCeph("--cluster", cluster, "config", "get", client, "auth_service_required")
	if err != nil {
		return "", fmt.Errorf("Failed to query ceph config for auth_service_required: %w", err)
	}

	if authType == "none" {
		logger.Infof("Ceph cluster %q has disabled cephx", cluster)
		return "", nil
	}

	// Call ceph auth get.
	key := struct {
		Key string `json:"key"`
	}{}
	err = callCephJSON(&key, "--cluster", cluster, "auth", "get-key", client)
	if err != nil {
		return "", fmt.Errorf("Failed to get keyring for %q on %q: %w", client, cluster, err)
	}

	return key.Key, nil
}

func cephGetKeyFromFile(path string) (string, error) {
	cephKeyring, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("Failed to open %q: %w", path, err)
	}

	// Locate the keyring entry and its value.
	var cephSecret string
	scan := bufio.NewScanner(cephKeyring)
	for scan.Scan() {
		line := scan.Text()
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "key") {
			fields := strings.SplitN(line, "=", 2)
			if len(fields) < 2 {
				continue
			}

			cephSecret = strings.TrimSpace(fields[1])
			break
		}
	}

	if cephSecret == "" {
		return "", fmt.Errorf("Couldn't find a keyring entry")
	}

	return cephSecret, nil
}

// cephKeyringFromFile gets the key for a particular Ceph cluster and client name.
func cephKeyringFromFile(cluster string, client string) (string, error) {
	var cephSecret string
	cephConfigPath := fmt.Sprintf("/etc/ceph/%v.conf", cluster)

	keyringPathFull := fmt.Sprintf("/etc/ceph/%v.client.%v.keyring", cluster, client)
	keyringPathCluster := fmt.Sprintf("/etc/ceph/%v.keyring", cluster)
	keyringPathGlobal := "/etc/ceph/keyring"
	keyringPathGlobalBin := "/etc/ceph/keyring.bin"

	if util.PathExists(keyringPathFull) {
		return cephGetKeyFromFile(keyringPathFull)
	} else if util.PathExists(keyringPathCluster) {
		return cephGetKeyFromFile(keyringPathCluster)
	} else if util.PathExists(keyringPathGlobal) {
		return cephGetKeyFromFile(keyringPathGlobal)
	} else if util.PathExists(keyringPathGlobalBin) {
		return cephGetKeyFromFile(keyringPathGlobalBin)
	} else if util.PathExists(cephConfigPath) {
		// Open the CEPH config file.
		cephConfig, err := os.Open(cephConfigPath)
		if err != nil {
			return "", fmt.Errorf("Failed to open %q: %w", cephConfigPath, err)
		}

		// Locate the keyring entry and its value.
		scan := bufio.NewScanner(cephConfig)
		for scan.Scan() {
			line := scan.Text()
			line = strings.TrimSpace(line)

			if line == "" {
				continue
			}

			if strings.HasPrefix(line, "key") {
				fields := strings.SplitN(line, "=", 2)
				if len(fields) < 2 {
					continue
				}

				// Check all key related config keys.
				switch strings.TrimSpace(fields[0]) {
				case "key":
					cephSecret = strings.TrimSpace(fields[1])
				case "keyfile":
					key, err := os.ReadFile(fields[1])
					if err != nil {
						return "", err
					}

					cephSecret = strings.TrimSpace(string(key))
				case "keyring":
					return cephGetKeyFromFile(strings.TrimSpace(fields[1]))
				}
			}

			if cephSecret != "" {
				break
			}
		}
	}

	if cephSecret == "" {
		return "", fmt.Errorf("Couldn't find a keyring entry")
	}

	return cephSecret, nil
}

// CephFsid retrieves the FSID for the given cluster.
func CephFsid(cluster string) (string, error) {
	// Call ceph fsid.
	fsid := struct {
		Fsid string `json:"fsid"`
	}{}

	err := callCephJSON(&fsid, "--cluster", cluster, "fsid")
	if err != nil {
		return "", fmt.Errorf("Couldn't get fsid for %q: %w", cluster, err)
	}

	return fsid.Fsid, nil
}
