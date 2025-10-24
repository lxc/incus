package osarch

import (
	"fmt"
	"slices"
)

// nolint:revive
const (
	ARCH_UNKNOWN                     = 0
	ARCH_32BIT_INTEL_X86             = 1
	ARCH_64BIT_INTEL_X86             = 2
	ARCH_32BIT_ARMV7_LITTLE_ENDIAN   = 3
	ARCH_64BIT_ARMV8_LITTLE_ENDIAN   = 4
	ARCH_32BIT_POWERPC_BIG_ENDIAN    = 5
	ARCH_64BIT_POWERPC_BIG_ENDIAN    = 6
	ARCH_64BIT_POWERPC_LITTLE_ENDIAN = 7
	ARCH_64BIT_S390_BIG_ENDIAN       = 8
	ARCH_32BIT_MIPS                  = 9
	ARCH_64BIT_MIPS                  = 10
	ARCH_32BIT_RISCV_LITTLE_ENDIAN   = 11
	ARCH_64BIT_RISCV_LITTLE_ENDIAN   = 12
	ARCH_32BIT_ARMV6_LITTLE_ENDIAN   = 13
	ARCH_32BIT_ARMV8_LITTLE_ENDIAN   = 14
	ARCH_64BIT_LOONGARCH             = 15
)

var architectureNames = map[int]string{
	ARCH_32BIT_INTEL_X86:             "i686",
	ARCH_64BIT_INTEL_X86:             "x86_64",
	ARCH_32BIT_ARMV6_LITTLE_ENDIAN:   "armv6l",
	ARCH_32BIT_ARMV7_LITTLE_ENDIAN:   "armv7l",
	ARCH_32BIT_ARMV8_LITTLE_ENDIAN:   "armv8l",
	ARCH_64BIT_ARMV8_LITTLE_ENDIAN:   "aarch64",
	ARCH_32BIT_POWERPC_BIG_ENDIAN:    "ppc",
	ARCH_64BIT_POWERPC_BIG_ENDIAN:    "ppc64",
	ARCH_64BIT_POWERPC_LITTLE_ENDIAN: "ppc64le",
	ARCH_64BIT_S390_BIG_ENDIAN:       "s390x",
	ARCH_32BIT_MIPS:                  "mips",
	ARCH_64BIT_MIPS:                  "mips64",
	ARCH_32BIT_RISCV_LITTLE_ENDIAN:   "riscv32",
	ARCH_64BIT_RISCV_LITTLE_ENDIAN:   "riscv64",
	ARCH_64BIT_LOONGARCH:             "loongarch64",
}

var architectureAliases = map[int][]string{
	ARCH_32BIT_INTEL_X86:             {"i386", "i586", "386", "x86", "generic_32"},
	ARCH_64BIT_INTEL_X86:             {"amd64", "generic_64", "x86_64_v1", "x86_64_v2", "x86_64_v3", "x86_64_v4"},
	ARCH_32BIT_ARMV6_LITTLE_ENDIAN:   {"armel", "arm"},
	ARCH_32BIT_ARMV7_LITTLE_ENDIAN:   {"armhf", "armhfp", "armv7a_hardfp", "armv7", "armv7a_vfpv3_hardfp"},
	ARCH_32BIT_ARMV8_LITTLE_ENDIAN:   {},
	ARCH_64BIT_ARMV8_LITTLE_ENDIAN:   {"arm64", "arm64_generic"},
	ARCH_32BIT_POWERPC_BIG_ENDIAN:    {"powerpc"},
	ARCH_64BIT_POWERPC_BIG_ENDIAN:    {"powerpc64", "ppc64"},
	ARCH_64BIT_POWERPC_LITTLE_ENDIAN: {"ppc64el"},
	ARCH_32BIT_MIPS:                  {"mipsel", "mipsle"},
	ARCH_64BIT_MIPS:                  {"mips64el", "mips64le"},
	ARCH_32BIT_RISCV_LITTLE_ENDIAN:   {},
	ARCH_64BIT_RISCV_LITTLE_ENDIAN:   {},
	ARCH_64BIT_LOONGARCH:             {"loong64"},
}

var architecturePersonalities = map[int]string{
	ARCH_32BIT_INTEL_X86:             "linux32",
	ARCH_64BIT_INTEL_X86:             "linux64",
	ARCH_32BIT_ARMV6_LITTLE_ENDIAN:   "linux32",
	ARCH_32BIT_ARMV7_LITTLE_ENDIAN:   "linux32",
	ARCH_32BIT_ARMV8_LITTLE_ENDIAN:   "linux32",
	ARCH_64BIT_ARMV8_LITTLE_ENDIAN:   "linux64",
	ARCH_32BIT_POWERPC_BIG_ENDIAN:    "linux32",
	ARCH_64BIT_POWERPC_BIG_ENDIAN:    "linux64",
	ARCH_64BIT_POWERPC_LITTLE_ENDIAN: "linux64",
	ARCH_64BIT_S390_BIG_ENDIAN:       "linux64",
	ARCH_32BIT_MIPS:                  "linux32",
	ARCH_64BIT_MIPS:                  "linux64",
	ARCH_32BIT_RISCV_LITTLE_ENDIAN:   "linux32",
	ARCH_64BIT_RISCV_LITTLE_ENDIAN:   "linux64",
	ARCH_64BIT_LOONGARCH:             "linux64",
}

var architectureSupportedPersonalities = map[int][]int{
	ARCH_32BIT_INTEL_X86:             {},
	ARCH_64BIT_INTEL_X86:             {ARCH_32BIT_INTEL_X86},
	ARCH_32BIT_ARMV6_LITTLE_ENDIAN:   {},
	ARCH_32BIT_ARMV7_LITTLE_ENDIAN:   {ARCH_32BIT_ARMV6_LITTLE_ENDIAN},
	ARCH_32BIT_ARMV8_LITTLE_ENDIAN:   {ARCH_32BIT_ARMV6_LITTLE_ENDIAN, ARCH_32BIT_ARMV7_LITTLE_ENDIAN},
	ARCH_64BIT_ARMV8_LITTLE_ENDIAN:   {ARCH_32BIT_ARMV6_LITTLE_ENDIAN, ARCH_32BIT_ARMV7_LITTLE_ENDIAN, ARCH_32BIT_ARMV8_LITTLE_ENDIAN},
	ARCH_32BIT_POWERPC_BIG_ENDIAN:    {},
	ARCH_64BIT_POWERPC_BIG_ENDIAN:    {ARCH_32BIT_POWERPC_BIG_ENDIAN},
	ARCH_64BIT_POWERPC_LITTLE_ENDIAN: {},
	ARCH_64BIT_S390_BIG_ENDIAN:       {},
	ARCH_32BIT_MIPS:                  {},
	ARCH_64BIT_MIPS:                  {ARCH_32BIT_MIPS},
	ARCH_32BIT_RISCV_LITTLE_ENDIAN:   {},
	ARCH_64BIT_RISCV_LITTLE_ENDIAN:   {},
	ARCH_64BIT_LOONGARCH:             {},
}

// ArchitectureDefault is the fallback architecture when the local architecture can't be properly detected.
const ArchitectureDefault = "x86_64"

// ArchitectureName converts an architecture ID to its name.
func ArchitectureName(arch int) (string, error) {
	archName, exists := architectureNames[arch]
	if exists {
		return archName, nil
	}

	return "unknown", fmt.Errorf("Architecture isn't supported: %d", arch)
}

// ArchitectureID converts an architecture name to its ID.
func ArchitectureID(arch string) (int, error) {
	for archID, archName := range architectureNames {
		if archName == arch {
			return archID, nil
		}
	}

	for archID, archAliases := range architectureAliases {
		if slices.Contains(archAliases, arch) {
			return archID, nil
		}
	}

	return ARCH_UNKNOWN, fmt.Errorf("Architecture isn't supported: %s", arch)
}

// ArchitecturePersonality returns the kernel personality name for the architecture.
func ArchitecturePersonality(arch int) (string, error) {
	archPersonality, exists := architecturePersonalities[arch]
	if exists {
		return archPersonality, nil
	}

	return "", fmt.Errorf("Architecture isn't supported: %d", arch)
}

// ArchitecturePersonalities returns the list of personalities for the provided architecture.
func ArchitecturePersonalities(arch int) ([]int, error) {
	personalities, exists := architectureSupportedPersonalities[arch]
	if exists {
		return personalities, nil
	}

	return []int{}, fmt.Errorf("Architecture isn't supported: %d", arch)
}

// ArchitectureGetLocalID returns the local hardware architecture ID.
func ArchitectureGetLocalID() (int, error) {
	name, err := ArchitectureGetLocal()
	if err != nil {
		return -1, err
	}

	id, err := ArchitectureID(name)
	if err != nil {
		return -1, err
	}

	return id, nil
}

// SupportedArchitectures returns the list of all supported architectures.
func SupportedArchitectures() []string {
	result := []string{}
	for _, archName := range architectureNames {
		result = append(result, archName)
	}

	return result
}
