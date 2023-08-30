package simplestreams

import (
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/osarch"
)

var nativeName, _ = osarch.ArchitectureGetLocal()

type sortedImages []api.Image

func (a sortedImages) Len() int {
	return len(a)
}

func (a sortedImages) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a sortedImages) Less(i, j int) bool {
	// When sorting images, group by:
	// - Operating system (os)
	// - Release (release)
	// - Variant (variant)
	// - Serial number / date (serial)
	// - Architecture (architecture)
	for _, prop := range []string{"os", "release", "variant", "serial", "architecture"} {
		if a[i].Properties[prop] == a[j].Properties[prop] {
			continue
		}

		if a[i].Properties[prop] == "" {
			return false
		}

		if a[i].Properties[prop] == "" {
			return true
		}

		if prop == "serial" {
			return a[i].Properties[prop] > a[j].Properties[prop]
		}

		return a[i].Properties[prop] < a[j].Properties[prop]
	}

	if a[i].Properties["type"] != a[j].Properties["type"] {
		iScore := 0
		jScore := 0

		// Image types in order of preference.
		for score, pref := range []string{"squashfs", "root.tar.xz", "disk-kvm.img", "uefi1.img", "disk1.img"} {
			if a[i].Properties["type"] == pref {
				iScore = score
			}

			if a[j].Properties["type"] == pref {
				jScore = score
			}
		}

		return iScore < jScore
	}

	return false
}

type sortedAliases []extendedAlias

func (a sortedAliases) Len() int {
	return len(a)
}

func (a sortedAliases) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a sortedAliases) Less(i, j int) bool {
	// Check functions.
	isNative := func(arch string) bool {
		return nativeName == arch
	}

	isPersonality := func(arch string) bool {
		archID, err := osarch.ArchitectureId(nativeName)
		if err != nil {
			return false
		}

		personalities, err := osarch.ArchitecturePersonalities(archID)
		if err != nil {
			return false
		}

		for _, personality := range personalities {
			personalityName, err := osarch.ArchitectureName(personality)
			if err != nil {
				return false
			}

			if personalityName == arch {
				return true
			}
		}

		return false
	}

	// Same thing.
	if a[i].Architecture == a[j].Architecture {
		return false
	}

	// Look for native.
	if isNative(a[i].Architecture) {
		return true
	}

	// Look for personality.
	if isPersonality(a[i].Architecture) && !isNative(a[j].Architecture) {
		return true
	}

	return false
}
