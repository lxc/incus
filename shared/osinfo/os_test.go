package osinfo

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOSInfo_DetermineOSDetails(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		imageOS      string
		imageRelease string

		expectedOS      OSType
		expectedDistro  Distro
		expectedVersion string
	}{
		{
			name:            "debian 13",
			imageOS:         "Debian",
			imageRelease:    "13 (serial)",
			expectedOS:      Linux,
			expectedDistro:  DebianLinux,
			expectedVersion: "13",
		},
		{
			name:            "debian trixie",
			imageOS:         "Debian",
			imageRelease:    "trixie (serial)",
			expectedOS:      Linux,
			expectedDistro:  DebianLinux,
			expectedVersion: "13",
		},
		{
			name:            "debian unknown",
			imageOS:         "Debian",
			imageRelease:    "foo (serial)",
			expectedOS:      Linux,
			expectedDistro:  DebianLinux,
			expectedVersion: "",
		},
		{
			name:            "ubuntu 26.04",
			imageOS:         "Ubuntu",
			imageRelease:    "26.04 (serial)",
			expectedOS:      Linux,
			expectedDistro:  UbuntuLinux,
			expectedVersion: "26.04",
		},
		{
			name:            "ubuntu resolute",
			imageOS:         "Ubuntu",
			imageRelease:    "resolute (serial)",
			expectedOS:      Linux,
			expectedDistro:  UbuntuLinux,
			expectedVersion: "26.04",
		},
		{
			name:            "ubuntu unknown",
			imageOS:         "Ubuntu",
			imageRelease:    "foo (serial)",
			expectedOS:      Linux,
			expectedDistro:  UbuntuLinux,
			expectedVersion: "",
		},
		{
			name:            "rhel 10",
			imageOS:         "RHEL",
			imageRelease:    "10.1 (serial)",
			expectedOS:      Linux,
			expectedDistro:  RedHatLinux,
			expectedVersion: "10",
		},
		{
			name:            "red hat enterprise linux 10",
			imageOS:         "Red Hat Enterprise Linux",
			imageRelease:    "10.1 (serial)",
			expectedOS:      Linux,
			expectedDistro:  RedHatLinux,
			expectedVersion: "10",
		},
		{
			name:            "red hat 10",
			imageOS:         "Red Hat",
			imageRelease:    "10.1 (serial)",
			expectedOS:      Linux,
			expectedDistro:  RedHatLinux,
			expectedVersion: "10",
		},
		{
			name:            "redhat enterprise linux 10",
			imageOS:         "RedHat Enterprise Linux",
			imageRelease:    "10.1 (serial)",
			expectedOS:      Linux,
			expectedDistro:  RedHatLinux,
			expectedVersion: "10",
		},
		{
			name:            "redhat 10",
			imageOS:         "RedHat",
			imageRelease:    "10.1 (serial)",
			expectedOS:      Linux,
			expectedDistro:  RedHatLinux,
			expectedVersion: "10",
		},
		{
			name:            "red-hat enterprise linux 10",
			imageOS:         "Red-Hat Enterprise Linux",
			imageRelease:    "10.1 (serial)",
			expectedOS:      Linux,
			expectedDistro:  RedHatLinux,
			expectedVersion: "10",
		},
		{
			name:            "red-hat 10",
			imageOS:         "Red-Hat",
			imageRelease:    "10.1 (serial)",
			expectedOS:      Linux,
			expectedDistro:  RedHatLinux,
			expectedVersion: "10",
		},
		{
			name:            "red-hat unknown",
			imageOS:         "Red-Hat",
			imageRelease:    "foo (serial)",
			expectedOS:      Linux,
			expectedDistro:  RedHatLinux,
			expectedVersion: "",
		},
		{
			name:            "centos 10-stream",
			imageOS:         "CentOS",
			imageRelease:    "10-Stream (serial)",
			expectedOS:      Linux,
			expectedDistro:  CentOSLinux,
			expectedVersion: "10",
		},
		{
			name:            "centos 10.1-stream",
			imageOS:         "CentOS",
			imageRelease:    "10.1-Stream (serial)",
			expectedOS:      Linux,
			expectedDistro:  CentOSLinux,
			expectedVersion: "10",
		},
		{
			name:            "centos 10.1",
			imageOS:         "CentOS",
			imageRelease:    "10.1 (serial)",
			expectedOS:      Linux,
			expectedDistro:  CentOSLinux,
			expectedVersion: "10",
		},
		{
			name:            "amazon linux 2023",
			imageOS:         "Amazon",
			imageRelease:    "2023 (serial)",
			expectedOS:      Linux,
			expectedDistro:  AmazonLinux,
			expectedVersion: "2023",
		},
		{
			name:            "suse tumbleweed",
			imageOS:         "OpenSUSE",
			imageRelease:    "1234 tumbleweed 1234 (serial)",
			expectedOS:      Linux,
			expectedDistro:  SUSELinux,
			expectedVersion: "",
		},
		{
			name:            "suse 16.0",
			imageOS:         "OpenSUSE",
			imageRelease:    "16.0 (serial)",
			expectedOS:      Linux,
			expectedDistro:  SUSELinux,
			expectedVersion: "16",
		},
		{
			name:            "sles 16.0",
			imageOS:         "SLES",
			imageRelease:    "16.0 (serial)",
			expectedOS:      Linux,
			expectedDistro:  SUSELinux,
			expectedVersion: "16",
		},
		{
			name:            "suse linux enterprise server 16.0",
			imageOS:         "suse linux enterprise server",
			imageRelease:    "16.0 (serial)",
			expectedOS:      Linux,
			expectedDistro:  SUSELinux,
			expectedVersion: "16",
		},
		{
			name:            "windows xp",
			imageOS:         "Windows",
			imageRelease:    "prefix XP suffix",
			expectedOS:      Windows,
			expectedDistro:  OtherDistro,
			expectedVersion: "XP",
		},
		{
			name:            "windows 7",
			imageOS:         "Windows",
			imageRelease:    "prefix 7 suffix",
			expectedOS:      Windows,
			expectedDistro:  OtherDistro,
			expectedVersion: "7",
		},
		{
			name:            "windows 8",
			imageOS:         "Windows",
			imageRelease:    "prefix 8 suffix",
			expectedOS:      Windows,
			expectedDistro:  OtherDistro,
			expectedVersion: "8",
		},
		{
			name:            "windows 8.1",
			imageOS:         "Windows",
			imageRelease:    "prefix 8.1 suffix",
			expectedOS:      Windows,
			expectedDistro:  OtherDistro,
			expectedVersion: "8.1",
		},
		{
			name:            "windows 10",
			imageOS:         "Windows",
			imageRelease:    "prefix 10 suffix",
			expectedOS:      Windows,
			expectedDistro:  OtherDistro,
			expectedVersion: "10",
		},
		{
			name:            "windows 11",
			imageOS:         "Windows",
			imageRelease:    "prefix 11 suffix",
			expectedOS:      Windows,
			expectedDistro:  OtherDistro,
			expectedVersion: "11",
		},
		{
			name:            "windows Server 2008",
			imageOS:         "Windows",
			imageRelease:    "prefix Server 2008 suffix",
			expectedOS:      Windows,
			expectedDistro:  OtherDistro,
			expectedVersion: "Server 2008",
		},
		{
			name:            "windows Server 2008 R2",
			imageOS:         "Windows",
			imageRelease:    "prefix Server 2008 R2 suffix",
			expectedOS:      Windows,
			expectedDistro:  OtherDistro,
			expectedVersion: "Server 2008 R2",
		},
		{
			name:            "windows Server R2 2008",
			imageOS:         "Windows",
			imageRelease:    "prefix Server R2 2008 suffix",
			expectedOS:      Windows,
			expectedDistro:  OtherDistro,
			expectedVersion: "Server 2008 R2",
		},
		{
			name:            "windows Server R2 2008 (case insensitive)",
			imageOS:         "Windows",
			imageRelease:    "prefix server r2 2008 suffix",
			expectedOS:      Windows,
			expectedDistro:  OtherDistro,
			expectedVersion: "Server 2008 R2",
		},
		{
			name:            "freebsd",
			imageOS:         "FreeBSD",
			imageRelease:    "",
			expectedOS:      FreeBSD,
			expectedDistro:  OtherDistro,
			expectedVersion: "",
		},
		{
			name:            "macos",
			imageOS:         "macOS",
			imageRelease:    "",
			expectedOS:      MacOS,
			expectedDistro:  OtherDistro,
			expectedVersion: "",
		},
		{
			name:            "other",
			imageOS:         "",
			imageRelease:    "",
			expectedOS:      UnknownOS,
			expectedDistro:  OtherDistro,
			expectedVersion: "",
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			t.Logf("\n\nTEST %02d: %s\n\n", i, tc.name)

			osType, distro, version := DetermineOSDetails(tc.imageOS, tc.imageRelease)

			require.Equal(t, tc.expectedOS, osType)
			require.Equal(t, tc.expectedDistro, distro)
			require.Equal(t, tc.expectedVersion, version)
		})
	}
}

func TestOSInfo_GetOSQemuCompatibility(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		osType  OSType
		distro  Distro
		version string

		expectedSCSISupport bool
		expectedNetSupport  bool
		cpuFlags            []string
	}{
		{
			name:                "ubuntu - full support with version",
			osType:              Linux,
			distro:              UbuntuLinux,
			version:             "17.04",
			expectedSCSISupport: true,
			expectedNetSupport:  true,
			cpuFlags:            nil,
		},
		{
			name:                "ubuntu - full support without version",
			osType:              Linux,
			distro:              UbuntuLinux,
			version:             "",
			expectedSCSISupport: true,
			expectedNetSupport:  true,
			cpuFlags:            nil,
		},
		{
			name:                "ubuntu - missing INVPCID support < 17",
			osType:              Linux,
			distro:              UbuntuLinux,
			version:             "16.04",
			expectedSCSISupport: true,
			expectedNetSupport:  true,
			cpuFlags:            []string{"-invpcid"},
		},
		{
			name:                "debian - full support with version",
			osType:              Linux,
			distro:              DebianLinux,
			version:             "10",
			expectedSCSISupport: true,
			expectedNetSupport:  true,
			cpuFlags:            nil,
		},
		{
			name:                "debian - missing INVPCID support < 10 but > 5",
			osType:              Linux,
			distro:              DebianLinux,
			version:             "9",
			expectedSCSISupport: true,
			expectedNetSupport:  true,
			cpuFlags:            []string{"-invpcid"},
		},
		{
			name:                "debian - missing vionet, vioscsi support < 6",
			osType:              Linux,
			distro:              DebianLinux,
			version:             "5",
			expectedSCSISupport: false,
			expectedNetSupport:  false,
			cpuFlags:            nil,
		},
		{
			name:                "suse - full support with version",
			osType:              Linux,
			distro:              SUSELinux,
			version:             "11",
			expectedSCSISupport: true,
			expectedNetSupport:  true,
			cpuFlags:            nil,
		},
		{
			name:                "suse - full support without version",
			osType:              Linux,
			distro:              SUSELinux,
			version:             "",
			expectedSCSISupport: true,
			expectedNetSupport:  true,
			cpuFlags:            nil,
		},
		{
			name:                "suse - missing vionet support < 11",
			osType:              Linux,
			distro:              SUSELinux,
			version:             "10",
			expectedSCSISupport: true,
			expectedNetSupport:  false,
			cpuFlags:            nil,
		},
		{
			name:                "suse - missing vionet and vioscsi support < 10",
			osType:              Linux,
			distro:              SUSELinux,
			version:             "9",
			expectedSCSISupport: false,
			expectedNetSupport:  false,
			cpuFlags:            nil,
		},
		{
			name:                "rhel - full support with version",
			osType:              Linux,
			distro:              RedHatLinux,
			version:             "7",
			expectedSCSISupport: true,
			expectedNetSupport:  true,
			cpuFlags:            nil,
		},
		{
			name:                "rhel - full support without version",
			osType:              Linux,
			distro:              RedHatLinux,
			version:             "",
			expectedSCSISupport: true,
			expectedNetSupport:  true,
			cpuFlags:            nil,
		},
		{
			name:                "rhel - missing vionet support < 7",
			osType:              Linux,
			distro:              RedHatLinux,
			version:             "6",
			expectedSCSISupport: true,
			expectedNetSupport:  false,
			cpuFlags:            nil,
		},
		{
			name:                "rhel - missing vionet and vioscsi support < 6",
			osType:              Linux,
			distro:              RedHatLinux,
			version:             "5",
			expectedSCSISupport: false,
			expectedNetSupport:  false,
			cpuFlags:            nil,
		},
		{
			name:                "oracle - full support with version",
			osType:              Linux,
			distro:              OracleLinux,
			version:             "7",
			expectedSCSISupport: true,
			expectedNetSupport:  true,
			cpuFlags:            nil,
		},
		{
			name:                "oracle - missing SMAP and vionet support < 7",
			osType:              Linux,
			distro:              OracleLinux,
			version:             "6",
			expectedSCSISupport: true,
			expectedNetSupport:  false,
			cpuFlags:            []string{"-smap"},
		},
		{
			name:                "windows - full support with version",
			osType:              Windows,
			distro:              OtherDistro,
			version:             "10",
			expectedSCSISupport: true,
			expectedNetSupport:  true,
			cpuFlags:            nil,
		},
		{
			name:                "windows - full support without version",
			osType:              Windows,
			distro:              OtherDistro,
			version:             "",
			expectedSCSISupport: true,
			expectedNetSupport:  true,
			cpuFlags:            nil,
		},
		{
			name:                "windows - missing vioscsi and vionet support on XP",
			osType:              Windows,
			distro:              OtherDistro,
			version:             "XP",
			expectedSCSISupport: false,
			expectedNetSupport:  false,
			cpuFlags:            nil,
		},
		{
			name:                "windows - missing vioscsi and vionet support on Server 2003",
			osType:              Windows,
			distro:              OtherDistro,
			version:             "Server 2003",
			expectedSCSISupport: false,
			expectedNetSupport:  false,
			cpuFlags:            nil,
		},
		{
			name:                "other - full support",
			osType:              UnknownOS,
			distro:              OtherDistro,
			version:             "",
			expectedSCSISupport: true,
			expectedNetSupport:  true,
			cpuFlags:            nil,
		},
		{
			name:                "empty - full support",
			osType:              "",
			distro:              "",
			version:             "",
			expectedSCSISupport: true,
			expectedNetSupport:  true,
			cpuFlags:            nil,
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			t.Logf("\n\nTEST %02d: %s\n\n", i, tc.name)

			vioSCSI, vioNet, smap := GetOSQemuCompatibility(tc.osType, tc.distro, tc.version)

			require.Equal(t, tc.expectedSCSISupport, vioSCSI)
			require.Equal(t, tc.expectedNetSupport, vioNet)
			require.Equal(t, tc.cpuFlags, smap)
		})
	}
}
