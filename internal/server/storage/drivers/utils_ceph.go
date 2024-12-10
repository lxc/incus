package drivers

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/subprocess"
)

// CephMonmap represents the json (partial) output of `ceph mon dump`.
type CephMonmap struct {
	Fsid string `json:"fsid"`
	Mons []struct {
		Name        string `json:"name"`
		PublicAddrs struct {
			Addrvec []struct {
				Type  string `json:"type"`
				Addr  string `json:"addr"`
				Nonce int    `json:"nonce"`
			} `json:"addrvec"`
		} `json:"public_addrs"`
		Addr       string `json:"addr"`
		PublicAddr string `json:"public_addr"`
		Priority   int    `json:"priority"`
		Weight     int    `json:"weight"`
	} `json:"mons"`
}

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
	ctx := logger.Ctx{"cluster": cluster}
	cephMon := []string{}

	// First try the ceph client admin tool
	jsonBlob, err := subprocess.RunCommand(
		"ceph", "mon", "dump",
		"--cluster", cluster,
		"--format", "json",
	)
	if err != nil {
		ctx["err"] = err
		logger.Warn("Failed to get monitors from ceph client", ctx)
	} else {
		// Extract monitor public addresses from json
		var monmap CephMonmap
		err = json.Unmarshal([]byte(jsonBlob), &monmap)
		if err != nil {
			ctx["err"] = err
			logger.Warn("Failed to decode json from 'ceph mon dump'", ctx)
		} else {
			for _, mon := range monmap.Mons {
				cephMon = append(cephMon, mon.PublicAddr)
			}
		}
	}

	// If that fails try ceph-conf
	if len(cephMon) == 0 {
		monBlob, err := subprocess.RunCommand(
			"ceph-conf", "mon_host",
			"--cluster", cluster,
		)
		if err != nil {
			logger.Warn("Failed to get monitors from ceph-conf", logger.Ctx{"cluster": cluster, "err": err})
		} else {
			// sadly ceph-conf doesn't give us a nice json blob like 'ceph mon dump'
			for _, mon := range strings.Split(monBlob, " ") {
				mon = strings.Trim(mon, "[]")
				for _, addr := range strings.Split(mon, ",") {
					addr = strings.Split(addr, ":")[1]
					addr = strings.Split(addr, "/")[0]
					cephMon = append(cephMon, addr)
				}
			}
		}
	}

	if len(cephMon) == 0 {
		return nil, fmt.Errorf("Failed to retrieve monitors for %q", cluster)
	}

	return cephMon, nil
}

// CephKeyring gets the key for a particular Ceph cluster and client name.
func CephKeyring(cluster string, client string) (string, error) {
	var cephSecret string

	cephSecret, err := subprocess.RunCommand(
		"ceph-conf", "key",
		"--cluster", cluster,
		"--name", "client."+client,
	)
	if err != nil {
		return "", fmt.Errorf("Failed to get key for 'client.%s' on %q", client, cluster)
	}

	return cephSecret, nil
}

// CephFSID returns the ceph fsid for a given cluster name
// requires that the ceph client is avaible on the host.
func CephFSID(cluster string) (string, error) {
	fsid, err := subprocess.RunCommand("ceph", "--cluster", cluster)
	if err != nil {
		return "", fmt.Errorf("Failed to get fsid for cluster %q: %w", cluster, err)
	}

	fsid = strings.TrimSpace(fsid)
	return fsid, nil
}

// CephMount attempts to mount a CephFS volume via the `ceph.mount` helper.
func CephMount(src string, dst string, options string) error {
	args := []string{
		src,
		dst,
	}

	if options != "" {
		args = append(args, "-o", options)
	}

	_, err := subprocess.RunCommand("mount.ceph", args...)
	return err
}
