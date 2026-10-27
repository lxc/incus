package drivers

import (
	"fmt"
)

func Example_lvm_parseLogicalVolumeName() {
	d := &lvm{}
	d.name = "pool"

	type testVol struct {
		lvName string
		parent Volume
	}

	parentCT := Volume{
		contentType: ContentTypeFS,
		volType:     VolumeTypeContainer,
		name:        "proj_testct-with-hyphens",
	}

	parentVM := Volume{
		contentType: ContentTypeBlock,
		volType:     VolumeTypeVM,
		name:        "proj_testvm-with-hyphens",
	}

	custVol := Volume{
		contentType: ContentTypeFS,
		volType:     VolumeTypeCustom,
		name:        "proj_testvol-with-hyphens.block", // .block ending doesn't indicate a block vol.
	}

	tests := []testVol{
		// Test container snapshots.
		{parent: parentCT, lvName: "containers_proj_testct--with--hyphens"},
		{parent: parentCT, lvName: "containers_proj_testct--with--hyphens-snap1--with--hyphens"},
		{parent: parentCT, lvName: "containers_proj_testct--with--hyphens-snap1--with--hyphens.block"},
		// Test container with name containing snapshot prefix.
		{parent: parentCT, lvName: "containers_proj_testct--with--hyphens--snap0"},
		// Test virtual machine snapshots.
		{parent: parentVM, lvName: "virtual-machines_proj_testvm--with--hyphens.block"},
		{parent: parentVM, lvName: "virtual-machines_proj_testvm--with--hyphens-snap1--with--hyphens.block"},
		{parent: parentVM, lvName: "virtual-machines_proj_testvm--with--hyphens-snap1--with--hyphens.block.block"},
		// Test custom volume filesystem snapshots.
		{parent: custVol, lvName: "custom_proj_testvol--with--hyphens.block"},
		{parent: custVol, lvName: "custom_proj_testvol--with--hyphens.block-snap1--with--hyphens.block"},
	}

	for _, test := range tests {
		snapName := d.parseLogicalVolumeSnapshot(test.parent, test.lvName)
		if snapName == "" {
			fmt.Printf("%s: Unrecognised\n", test.lvName)
		} else {
			fmt.Printf("%s: %s\n", test.lvName, snapName)
		}
	}

	// Output: containers_proj_testct--with--hyphens: Unrecognised
	// containers_proj_testct--with--hyphens-snap1--with--hyphens: snap1-with-hyphens
	// containers_proj_testct--with--hyphens-snap1--with--hyphens.block: snap1-with-hyphens.block
	// containers_proj_testct--with--hyphens--snap0: Unrecognised
	// virtual-machines_proj_testvm--with--hyphens.block: Unrecognised
	// virtual-machines_proj_testvm--with--hyphens-snap1--with--hyphens.block: snap1-with-hyphens
	// virtual-machines_proj_testvm--with--hyphens-snap1--with--hyphens.block.block: snap1-with-hyphens.block
	// custom_proj_testvol--with--hyphens.block: Unrecognised
	// custom_proj_testvol--with--hyphens.block-snap1--with--hyphens.block: snap1-with-hyphens.block
}
