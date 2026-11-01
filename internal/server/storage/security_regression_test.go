package storage

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/migration"
	localMigration "github.com/lxc/incus/v7/internal/server/migration"
)

func strPtr(s string) *string {
	return &s
}

// TestDependentVolumesAgainstOffer checks that a migration peer can't redefine the dependent volumes in its response (GHSA-q9fc-p923-6vp7).
func TestDependentVolumesAgainstOffer(t *testing.T) {
	offer := []*migration.DependentVolume{
		{
			Name:        strPtr("vol1"),
			Pool:        strPtr("pool1"),
			ContentType: strPtr("filesystem"),
			DeviceName:  strPtr("disk1"),
		},
	}

	err := ValidateDependentVolumesAgainstOffer(offer, offer)
	require.NoError(t, err)

	// A device that was never offered.
	err = ValidateDependentVolumesAgainstOffer(offer, []*migration.DependentVolume{
		{
			Name:        strPtr("vol2"),
			Pool:        strPtr("pool1"),
			ContentType: strPtr("filesystem"),
			DeviceName:  strPtr("disk2"),
		},
	})
	require.ErrorContains(t, err, "was not offered")

	// The offered device with a different volume name, pool or content type.
	for _, tampered := range []*migration.DependentVolume{
		{Name: strPtr("../../../root/evil"), Pool: strPtr("pool1"), ContentType: strPtr("filesystem"), DeviceName: strPtr("disk1")},
		{Name: strPtr("vol1"), Pool: strPtr("otherpool"), ContentType: strPtr("filesystem"), DeviceName: strPtr("disk1")},
		{Name: strPtr("vol1"), Pool: strPtr("pool1"), ContentType: strPtr("block"), DeviceName: strPtr("disk1")},
	} {
		err = ValidateDependentVolumesAgainstOffer(offer, []*migration.DependentVolume{tampered})
		require.ErrorContains(t, err, "doesn't match the offer")
	}
}

// TestDependentVolumeNilFields checks that a dependent volume with missing optional fields doesn't crash the daemon (GHSA-q9fc-p923-6vp7).
func TestDependentVolumeNilFields(t *testing.T) {
	require.NotPanics(t, func() {
		args := localMigration.ProtobufToDependentVolume(&migration.DependentVolume{}, localMigration.Type{}, nil)
		require.Empty(t, args.Name)
		require.Empty(t, args.Pool)
		require.Empty(t, args.DeviceName)
		require.Empty(t, args.ContentType)
	})
}
