package edk2

import (
	"os"
	"path/filepath"

	"github.com/lxc/incus/v6/shared/osarch"
)

// FirmwarePair represents a combination of firmware code (Code) and storage (Vars).
type FirmwarePair struct {
	Code string
	Vars string
}

// Installation represents a set of available firmware at a given location on the system.
type Installation struct {
	Path  string
	Usage map[FirmwareUsage][]FirmwarePair
}

// FirmwareUsage represents the situation in which a given firmware file will be used.
type FirmwareUsage int

const (
	// GENERIC is a generic EDK2 firmware.
	GENERIC FirmwareUsage = iota

	// SECUREBOOT is a UEFI Secure Boot enabled firmware.
	SECUREBOOT

	// CSM is a firmware with the UEFI Compatibility Support Module enabled to boot BIOS-only operating systems.
	CSM
)

var architectureInstallations = map[int][]Installation{
	osarch.ARCH_64BIT_INTEL_X86: {{
		Path: "/usr/share/OVMF",
		Usage: map[FirmwareUsage][]FirmwarePair{
			GENERIC: {
				{Code: "OVMF_CODE.4MB.fd", Vars: "OVMF_VARS.4MB.fd"},
				{Code: "OVMF_CODE_4M.fd", Vars: "OVMF_VARS_4M.fd"},
				{Code: "OVMF_CODE.4m.fd", Vars: "OVMF_VARS.4m.fd"},
				{Code: "OVMF_CODE.2MB.fd", Vars: "OVMF_VARS.2MB.fd"},
				{Code: "OVMF_CODE.fd", Vars: "OVMF_VARS.fd"},
				{Code: "OVMF_CODE.fd", Vars: "qemu.nvram"},
			},
			SECUREBOOT: {
				{Code: "OVMF_CODE.4MB.fd", Vars: "OVMF_VARS.4MB.ms.fd"},
				{Code: "OVMF_CODE_4M.ms.fd", Vars: "OVMF_VARS_4M.ms.fd"},
				{Code: "OVMF_CODE_4M.secboot.fd", Vars: "OVMF_VARS_4M.secboot.fd"},
				{Code: "OVMF_CODE.secboot.4m.fd", Vars: "OVMF_VARS.4m.fd"},
				{Code: "OVMF_CODE.secboot.fd", Vars: "OVMF_VARS.secboot.fd"},
				{Code: "OVMF_CODE.secboot.fd", Vars: "OVMF_VARS.fd"},
				{Code: "OVMF_CODE.2MB.fd", Vars: "OVMF_VARS.2MB.ms.fd"},
				{Code: "OVMF_CODE.fd", Vars: "OVMF_VARS.ms.fd"},
				{Code: "OVMF_CODE.fd", Vars: "qemu.nvram"},
			},
			CSM: {
				{Code: "seabios.bin", Vars: "seabios.bin"},
				{Code: "OVMF_CODE.4MB.CSM.fd", Vars: "OVMF_VARS.4MB.CSM.fd"},
				{Code: "OVMF_CODE.csm.4m.fd", Vars: "OVMF_VARS.4m.fd"},
				{Code: "OVMF_CODE.2MB.CSM.fd", Vars: "OVMF_VARS.2MB.CSM.fd"},
				{Code: "OVMF_CODE.CSM.fd", Vars: "OVMF_VARS.CSM.fd"},
				{Code: "OVMF_CODE.csm.fd", Vars: "OVMF_VARS.fd"},
			},
		},
	}, {
		Path: "/usr/share/qemu",
		Usage: map[FirmwareUsage][]FirmwarePair{
			GENERIC: {
				{Code: "ovmf-x86_64-4m-code.bin", Vars: "ovmf-x86_64-4m-vars.bin"},
				{Code: "ovmf-x86_64.bin", Vars: "ovmf-x86_64-code.bin"},
			},
			SECUREBOOT: {
				{Code: "ovmf-x86_64-ms-4m-vars.bin", Vars: "ovmf-x86_64-ms-4m-code.bin"},
				{Code: "ovmf-x86_64-ms-code.bin", Vars: "ovmf-x86_64-ms-vars.bin"},
			},
		},
	}},
	osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN: {{
		Path: "/usr/share/AAVMF",
		Usage: map[FirmwareUsage][]FirmwarePair{
			GENERIC: {
				{Code: "AAVMF_CODE.fd", Vars: "AAVMF_VARS.fd"},
				{Code: "OVMF_CODE.4MB.fd", Vars: "OVMF_VARS.4MB.fd"},
				{Code: "OVMF_CODE_4M.fd", Vars: "OVMF_VARS_4M.fd"},
				{Code: "OVMF_CODE.4m.fd", Vars: "OVMF_VARS.4m.fd"},
				{Code: "OVMF_CODE.2MB.fd", Vars: "OVMF_VARS.2MB.fd"},
				{Code: "OVMF_CODE.fd", Vars: "OVMF_VARS.fd"},
				{Code: "OVMF_CODE.fd", Vars: "qemu.nvram"},
			},
			SECUREBOOT: {
				{Code: "AAVMF_CODE.ms.fd", Vars: "AAVMF_VARS.ms.fd"},
				{Code: "OVMF_CODE.4MB.fd", Vars: "OVMF_VARS.4MB.ms.fd"},
				{Code: "OVMF_CODE_4M.ms.fd", Vars: "OVMF_VARS_4M.ms.fd"},
				{Code: "OVMF_CODE_4M.secboot.fd", Vars: "OVMF_VARS_4M.secboot.fd"},
				{Code: "OVMF_CODE.secboot.4m.fd", Vars: "OVMF_VARS.4m.fd"},
				{Code: "OVMF_CODE.secboot.fd", Vars: "OVMF_VARS.secboot.fd"},
				{Code: "OVMF_CODE.secboot.fd", Vars: "OVMF_VARS.fd"},
				{Code: "OVMF_CODE.2MB.fd", Vars: "OVMF_VARS.2MB.ms.fd"},
				{Code: "OVMF_CODE.fd", Vars: "OVMF_VARS.ms.fd"},
				{Code: "OVMF_CODE.fd", Vars: "qemu.nvram"},
			},
		},
	}},
}

// GetArchitectureInstallations returns an array of installations for a specific host architecture.
func GetArchitectureInstallations(hostArch int) []Installation {
	installations, found := architectureInstallations[hostArch]
	if found {
		return installations
	}

	return []Installation{}
}

// GetAchitectureFirmwarePairs creates an array of FirmwarePair for a
// specific host architecture. If the environment variable INCUS_EDK2_PATH
// has been set it will override the default installation path when
// constructing Code & Vars paths.
func GetAchitectureFirmwarePairs(hostArch int) []FirmwarePair {
	firmwares := make([]FirmwarePair, 0)

	for _, usage := range []FirmwareUsage{GENERIC, SECUREBOOT, CSM} {
		firmwares = append(firmwares, GetArchitectureFirmwarePairsForUsage(hostArch, usage)...)
	}

	return firmwares
}

// GetArchitectureFirmwarePairsForUsage creates an array of FirmwarePair
// for a specific host architecture and usage combination. If the
// environment variable INCUS_EDK2_PATH has been set it will override the
// default installation path when constructing Code & Vars paths.
func GetArchitectureFirmwarePairsForUsage(hostArch int, usage FirmwareUsage) []FirmwarePair {
	firmwares := make([]FirmwarePair, 0)

	for _, installation := range GetArchitectureInstallations(hostArch) {
		usage, found := installation.Usage[usage]
		if found {
			for _, firmwarePair := range usage {
				searchPath := installation.Path

				if GetenvEdk2Path() != "" {
					searchPath = GetenvEdk2Path()
				}

				firmwares = append(firmwares, FirmwarePair{
					Code: filepath.Join(searchPath, firmwarePair.Code),
					Vars: filepath.Join(searchPath, firmwarePair.Vars),
				})
			}
		}
	}

	return firmwares
}

// GetenvEdk2Path returns the environment variable for overriding the path to use for EDK2 installations.
func GetenvEdk2Path() string {
	return os.Getenv("INCUS_EDK2_PATH")
}
