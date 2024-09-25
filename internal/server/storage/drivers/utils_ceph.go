package drivers

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/lxc/incus/v6/shared/api"
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

// CephMonitors gets the mon-host field for the relevant cluster and extracts the list of addresses and ports.
func CephMonitors(cluster string) ([]string, error) {
	// Open the CEPH configuration.
	cephConf, err := os.Open(fmt.Sprintf("/etc/ceph/%s.conf", cluster))
	if err != nil {
		return nil, fmt.Errorf("Failed to open %q: %w", fmt.Sprintf("/etc/ceph/%s.conf", cluster), err)
	}

	// Locate the mon-host key and its values.
	cephMon := []string{}
	scan := bufio.NewScanner(cephConf)
	for scan.Scan() {
		line := scan.Text()
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "mon_host") || strings.HasPrefix(line, "mon-host") || strings.HasPrefix(line, "mon host") {
			fields := strings.SplitN(line, "=", 2)
			if len(fields) < 2 {
				continue
			}

			// Parsing mon_host is quite tricky.
			// It supports a space separate list of comma separated lists of:
			//  - DNS names
			//  - IPv4 addresses
			//  - IPv6 addresses (square brackets)
			//  - Optional version indicator
			//  - Optional port numbers
			//  - Optional data (after / separator)
			//  - Tuples of addresses with all the above still applying inside the tuple
			//
			// As this function is primarily used for cephfs which
			// doesn't take the version indication, trailing bits or supports those
			// tuples, all of those effectively get stripped away to get a clean
			// address list (with ports).
			entries := strings.Split(fields[1], " ")
			for _, entry := range entries {
				servers := strings.Split(entry, ",")
				for _, server := range servers {
					// Trim leading/trailing spaces.
					server = strings.TrimSpace(server)

					// Trim leading protocol version.
					server = strings.TrimPrefix(server, "v1:")
					server = strings.TrimPrefix(server, "v2:")
					server = strings.TrimPrefix(server, "[v1:")
					server = strings.TrimPrefix(server, "[v2:")

					// Trim trailing divider.
					server = strings.Split(server, "/")[0]

					// Handle end of nested blocks.
					server = strings.ReplaceAll(server, "]]", "]")
					if !strings.HasPrefix(server, "[") {
						server = strings.TrimSuffix(server, "]")
					}

					// Trim any spaces.
					server = strings.TrimSpace(server)

					// If nothing left, skip.
					if server == "" {
						continue
					}

					// Append the default v1 port if none are present.
					if !strings.HasSuffix(server, ":6789") && !strings.HasSuffix(server, ":3300") {
						server += ":6789"
					}

					cephMon = append(cephMon, strings.TrimSpace(server))
				}
			}
		}
	}

	if len(cephMon) == 0 {
		return nil, fmt.Errorf("Couldn't find a CEPH mon")
	}

	return cephMon, nil
}

func getCephKeyFromFile(path string) (string, error) {
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

// CephKeyring gets the key for a particular Ceph cluster and client name.
func CephKeyring(cluster string, client string) (string, error) {
	var cephSecret string
	cephConfigPath := fmt.Sprintf("/etc/ceph/%v.conf", cluster)

	keyringPathFull := fmt.Sprintf("/etc/ceph/%v.client.%v.keyring", cluster, client)
	keyringPathCluster := fmt.Sprintf("/etc/ceph/%v.keyring", cluster)
	keyringPathGlobal := "/etc/ceph/keyring"
	keyringPathGlobalBin := "/etc/ceph/keyring.bin"

	if util.PathExists(keyringPathFull) {
		return getCephKeyFromFile(keyringPathFull)
	} else if util.PathExists(keyringPathCluster) {
		return getCephKeyFromFile(keyringPathCluster)
	} else if util.PathExists(keyringPathGlobal) {
		return getCephKeyFromFile(keyringPathGlobal)
	} else if util.PathExists(keyringPathGlobalBin) {
		return getCephKeyFromFile(keyringPathGlobalBin)
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
					return getCephKeyFromFile(strings.TrimSpace(fields[1]))
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
