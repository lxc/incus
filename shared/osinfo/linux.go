package osinfo

import (
	"regexp"
	"slices"
	"strings"
)

var ubuntuVersions = map[string]string{
	"warty":    "4.10",
	"hoary":    "5.04",
	"breezy":   "5.10",
	"dapper":   "6.06",
	"edgy":     "6.10",
	"feisty":   "7.04",
	"gutsy":    "7.10",
	"hardy":    "8.04",
	"intrepid": "8.10",
	"jaunty":   "9.04",
	"karmic":   "9.10",
	"lucid":    "10.04",
	"maverick": "10.10",
	"natty":    "11.04",
	"oneiric":  "11.10",
	"precise":  "12.04",
	"quantal":  "12.10",
	"raring":   "13.04",
	"saucy":    "13.10",
	"trusty":   "14.04",
	"utopic":   "14.10",
	"vivid":    "15.04",
	"wily":     "15.10",
	"xenial":   "16.04",
	"yakkety":  "16.10",
	"zesty":    "17.04",
	"artful":   "17.10",
	"bionic":   "18.04",
	"cosmic":   "18.10",
	"disco":    "19.04",
	"eoan":     "19.10",
	"focal":    "20.04",
	"groovy":   "20.10",
	"hirsute":  "21.04",
	"impish":   "21.10",
	"jammy":    "22.04",
	"kinetic":  "22.10",
	"lunar":    "23.04",
	"mantic":   "23.10",
	"noble":    "24.04",
	"oracular": "24.10",
	"plucky":   "25.04",
	"questing": "25.10",
	"resolute": "26.04",
	"stonking": "26.10",
}

var debianVersions = map[string]string{
	"etch":     "4",
	"lenny":    "5",
	"squeeze":  "6",
	"wheezy":   "7",
	"jessie":   "8",
	"stretch":  "9",
	"buster":   "10",
	"bullseye": "11",
	"bookworm": "12",
	"trixie":   "13",
	"forky":    "14",
	"duke":     "15",
}

// DetermineLinuxDistro returns the Linux distribution from the image.os value.
func DetermineLinuxDistro(imageOS string) Distro {
	containsAny := func(osName string, substrs ...string) bool {
		return slices.ContainsFunc(substrs, func(s string) bool { return strings.Contains(osName, s) })
	}

	distro := OtherDistro
	imageOS = strings.ToLower(imageOS)
	if strings.Contains(imageOS, "debian") {
		distro = DebianLinux
	} else if strings.Contains(imageOS, "ubuntu") {
		distro = UbuntuLinux
	} else if strings.Contains(imageOS, "oracle") {
		distro = OracleLinux
	} else if strings.Contains(imageOS, "rocky") {
		distro = RockyLinux
	} else if strings.Contains(imageOS, "amazon") {
		distro = AmazonLinux
	} else if strings.Contains(imageOS, "alma") {
		distro = AlmaLinux
	} else if strings.Contains(imageOS, "fedora") {
		distro = FedoraLinux
	} else if strings.Contains(imageOS, "centos") {
		distro = CentOSLinux
	} else if containsAny(imageOS, "rhel", "redhat", "red-hat", "red hat") {
		distro = RedHatLinux
	} else if containsAny(imageOS, "archlinux", "arch linux", "arch-linux") {
		distro = ArchLinux
	} else if containsAny(imageOS, "opensuse", "suse", "sles") {
		distro = SUSELinux
	}

	return distro
}

// DetermineLinuxDistroVersion returns the distribution release version from the image.release value.
func DetermineLinuxDistroVersion(distro Distro, release string) string {
	parseMajorVersion := func(version string) string {
		// Return the first number sequence.
		return regexp.MustCompile(`\d+`).FindString(version)
	}

	release = strings.ToLower(release)
	switch distro {
	case AlmaLinux,
		AmazonLinux,
		FedoraLinux,
		OracleLinux,
		RedHatLinux,
		RockyLinux:
		return parseMajorVersion(release)
	case SUSELinux:
		if !strings.Contains(release, "tumbleweed") {
			return parseMajorVersion(release)
		}

	case CentOSLinux:
		return parseMajorVersion(strings.Split(release, "-stream")[0])
	case DebianLinux:
		return determineDebianVersion(release)
	case UbuntuLinux:
		return determineUbuntuVersion(release)
	}

	return ""
}

func determineUbuntuVersion(version string) string {
	version = strings.ToLower(version)
	for k, v := range ubuntuVersions {
		if strings.Contains(version, k) {
			return v
		}
	}

	// Return the first nn.nn number sequence.
	return regexp.MustCompile(`\d{2}\.\d{2}`).FindString(version)
}

func determineDebianVersion(version string) string {
	version = strings.ToLower(version)
	for k, v := range debianVersions {
		if strings.Contains(version, k) {
			return v
		}
	}

	// Return the first number sequence.
	return regexp.MustCompile(`\d+`).FindString(version)
}
