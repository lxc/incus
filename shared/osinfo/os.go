package osinfo

import (
	"strings"
)

// OSType represents an OS type (windows, linux, macos, bsd).
type OSType string

const (
	// Windows represents the Windows OS type.
	Windows OSType = "windows"
	// Linux represents the Linux OS type.
	Linux OSType = "linux"
	// FreeBSD represents the FreeBSD OS type.
	FreeBSD OSType = "freebsd"
	// MacOS represents the macOS OS type.
	MacOS OSType = "macos"
	// UnknownOS represents all other OS types.
	UnknownOS OSType = "unknown"
)

// Distro represents an OS distribution.
type Distro string

const (
	// AlmaLinux represents an Alma Linux distribution.
	AlmaLinux Distro = "alma"
	// AmazonLinux represents an Amazon Linux distribution.
	AmazonLinux Distro = "amazon"
	// ArchLinux represents an Arch Linux distribution.
	ArchLinux Distro = "arch"
	// CentOSLinux represents a CentOS Linux distribution.
	CentOSLinux Distro = "centos"
	// DebianLinux represents a Debian Linux distribution.
	DebianLinux Distro = "debian"
	// FedoraLinux represents a Fedora Linux distribution.
	FedoraLinux Distro = "fedora"
	// OracleLinux represents an Oracle Linux distribution.
	OracleLinux Distro = "oracle"
	// RedHatLinux represents a RedHat Enterprise Linux distribution.
	RedHatLinux Distro = "rhel"
	// RockyLinux represents a Rocky Linux distribution.
	RockyLinux Distro = "rocky"
	// SUSELinux represents OpenSUSE/SLES Linux distributions.
	SUSELinux Distro = "suse"
	// UbuntuLinux represents an Ubuntu Linux distribution.
	UbuntuLinux Distro = "ubuntu"

	// OtherDistro represents all other distributions, or OSTypes without a distribution concept.
	OtherDistro Distro = "other"
)

// IsRHELDerivative returns whether the given distro is a RHEL derivative.
func (d Distro) IsRHELDerivative() bool {
	switch d {
	case CentOSLinux, OracleLinux, RedHatLinux, RockyLinux, FedoraLinux, AmazonLinux, AlmaLinux:
		return true
	default:
		return false
	}
}

// DetermineOSDetails returns the OS type, distribution, and version from the given image.os and image.release values.
func DetermineOSDetails(imageOS, imageRelease string) (OSType, Distro, string) {
	osType, distro := DetermineOS(imageOS)
	switch osType {
	case Linux:
		return osType, distro, DetermineLinuxDistroVersion(distro, imageRelease)
	case Windows:
		version, _ := ToWindowsVersion(imageRelease)
		return osType, distro, version
	}

	return osType, distro, ""
}

// DetermineOS returns the OS type from the image.os value.
func DetermineOS(imageOS string) (OSType, Distro) {
	imageOS = strings.ToLower(imageOS)
	matches := func(names ...string) bool {
		for _, name := range names {
			if strings.Contains(imageOS, name) {
				return true
			}
		}

		return false
	}

	if matches("windows") {
		return Windows, OtherDistro
	}

	if matches("darwin", "macos", "mac os") {
		return MacOS, OtherDistro
	}

	if matches("freebsd", "opnsense", "pfsense") {
		return FreeBSD, OtherDistro
	}

	// If we're sure it's linux, return it directly.
	distro := DetermineLinuxDistro(imageOS)
	if distro != OtherDistro {
		return Linux, distro
	}

	return UnknownOS, OtherDistro
}
