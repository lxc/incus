package osinfo

import (
	"slices"
	"strconv"
	"strings"
)

// GetOSQemuCompatibility returns whether the given OS details support various modern qemu features.
func GetOSQemuCompatibility(osType OSType, distro Distro, distroVersion string) (bool, bool, []string) {
	// Assume full support by default.
	supportsVirtioSCSI := true
	supportsModernVirtioNet := true
	supportsSMAP := true
	supportsINVPCID := true

	switch osType {
	case FreeBSD:
	case MacOS:
	case Linux:
		var v int
		if distroVersion != "" {
			if distro == UbuntuLinux {
				distroVersion = strings.Split(distroVersion, ".")[0]
			}

			verInt, err := strconv.Atoi(distroVersion)
			if err == nil {
				v = verInt
			}
		}

		if v == 0 {
			return true, true, nil
		}

		switch distro {
		case UbuntuLinux:
			supportsINVPCID = v >= 17
		case SUSELinux:
			supportsVirtioSCSI = v >= 10
			supportsModernVirtioNet = v >= 11
		case DebianLinux:
			supportsVirtioSCSI = v >= 6
			supportsModernVirtioNet = v >= 6
			supportsINVPCID = v <= 5 || v >= 10
		default:
			if distro.IsRHELDerivative() {
				supportsVirtioSCSI = v >= 6
				supportsModernVirtioNet = v >= 7
				supportsSMAP = distro != OracleLinux || v >= 7 // oracle <=6 has issues with host CPU.
			}
		}
	case Windows:
		code, _ := MapWindowsVersionToAbbrev(distroVersion)
		if code != "" {
			supportsVirtioSCSI = !slices.Contains([]string{"2k3", "xp"}, code)
			supportsModernVirtioNet = !slices.Contains([]string{"2k3", "xp"}, code)
		}
	}

	var cpuFlags []string
	if !supportsINVPCID {
		cpuFlags = append(cpuFlags, "-invpcid")
	}

	if !supportsSMAP {
		cpuFlags = append(cpuFlags, "-smap")
	}

	return supportsVirtioSCSI, supportsModernVirtioNet, cpuFlags
}
