package migration

import (
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/lxc/incus/internal/migration"
	backupConfig "github.com/lxc/incus/internal/server/backup/config"
	"github.com/lxc/incus/internal/server/operations"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/ioprogress"
	"github.com/lxc/incus/shared/units"
)

// Info represents the index frame sent if supported.
type Info struct {
	Config *backupConfig.Config `json:"config,omitempty" yaml:"config,omitempty"` // Equivalent of backup.yaml but embedded in index.
}

// InfoResponse represents the response to the index frame sent if supported.
// Right now this doesn't contain anything useful, its just used to indicate receipt of the index header.
// But in the future the itention is to use it allow the target to send back additional information to the source
// about which frames (such as snapshots) it needs for the migration after having inspected the Info index header.
type InfoResponse struct {
	StatusCode int
	Error      string
	Refresh    *bool // This is used to let the source know whether to actually refresh a volume.
}

// Err returns the error of the response.
func (r *InfoResponse) Err() error {
	if r.StatusCode != http.StatusOK {
		return api.StatusErrorf(r.StatusCode, r.Error)
	}

	return nil
}

// Type represents the migration transport type. It indicates the method by which the migration can
// take place and what optional features are available.
type Type struct {
	FSType   migration.MigrationFSType // Transport mode selected.
	Features []string                  // Feature hints for selected FSType transport mode.
}

// VolumeSourceArgs represents the arguments needed to setup a volume migration source.
type VolumeSourceArgs struct {
	IndexHeaderVersion uint32
	Name               string
	Snapshots          []string
	MigrationType      Type
	TrackProgress      bool
	MultiSync          bool
	FinalSync          bool
	Data               any // Optional store to persist storage driver state between MultiSync phases.
	ContentType        string
	AllowInconsistent  bool
	Refresh            bool
	Info               *Info
	VolumeOnly         bool
	ClusterMove        bool
}

// VolumeTargetArgs represents the arguments needed to setup a volume migration sink.
type VolumeTargetArgs struct {
	IndexHeaderVersion    uint32
	Name                  string
	Description           string
	Config                map[string]string // Only used for custom volume migration.
	Snapshots             []string
	MigrationType         Type
	TrackProgress         bool
	Refresh               bool
	Live                  bool
	VolumeSize            int64
	ContentType           string
	VolumeOnly            bool
	ClusterMoveSourceName string
}

// TypesToHeader converts one or more Types to a MigrationHeader. It uses the first type argument
// supplied to indicate the preferred migration method and sets the MigrationHeader's Fs type
// to that. If the preferred type is ZFS then it will also set the header's optional ZfsFeatures.
// If the fallback Rsync type is present in any of the types even if it is not preferred, then its
// optional features are added to the header's RsyncFeatures, allowing for fallback negotiation to
// take place on the farside.
func TypesToHeader(types ...Type) *migration.MigrationHeader {
	missingFeature := false
	hasFeature := true
	var preferredType Type

	if len(types) > 0 {
		preferredType = types[0]
	}

	header := migration.MigrationHeader{Fs: &preferredType.FSType}

	// Add ZFS features if preferred type is ZFS.
	if preferredType.FSType == migration.MigrationFSType_ZFS {
		features := migration.ZfsFeatures{
			Compress: &missingFeature,
		}

		for _, feature := range preferredType.Features {
			if feature == "compress" {
				features.Compress = &hasFeature
			} else if feature == migration.ZFSFeatureMigrationHeader {
				features.MigrationHeader = &hasFeature
			} else if feature == migration.ZFSFeatureZvolFilesystems {
				features.HeaderZvols = &hasFeature
			}
		}

		header.ZfsFeatures = &features
	}

	// Add BTRFS features if preferred type is BTRFS.
	if preferredType.FSType == migration.MigrationFSType_BTRFS {
		features := migration.BtrfsFeatures{
			MigrationHeader:  &missingFeature,
			HeaderSubvolumes: &missingFeature,
		}

		for _, feature := range preferredType.Features {
			if feature == migration.BTRFSFeatureMigrationHeader {
				features.MigrationHeader = &hasFeature
			} else if feature == migration.BTRFSFeatureSubvolumes {
				features.HeaderSubvolumes = &hasFeature
			} else if feature == migration.BTRFSFeatureSubvolumeUUIDs {
				features.HeaderSubvolumeUuids = &hasFeature
			}
		}

		header.BtrfsFeatures = &features
	}

	// Check all the types for an Rsync method, if found add its features to the header's RsyncFeatures list.
	for _, t := range types {
		if t.FSType != migration.MigrationFSType_RSYNC && t.FSType != migration.MigrationFSType_BLOCK_AND_RSYNC {
			continue
		}

		features := migration.RsyncFeatures{
			Xattrs:        &missingFeature,
			Delete:        &missingFeature,
			Compress:      &missingFeature,
			Bidirectional: &missingFeature,
		}

		for _, feature := range t.Features {
			if feature == "xattrs" {
				features.Xattrs = &hasFeature
			} else if feature == "delete" {
				features.Delete = &hasFeature
			} else if feature == "compress" {
				features.Compress = &hasFeature
			} else if feature == "bidirectional" {
				features.Bidirectional = &hasFeature
			}
		}

		header.RsyncFeatures = &features
		break // Only use the first rsync transport type found to generate rsync features list.
	}

	return &header
}

// MatchTypes attempts to find matching migration transport types between an offered type sent from a remote
// source and the types supported by a local storage pool. If matches are found then one or more Types are
// returned containing the method and the matching optional features present in both. The function also takes a
// fallback type which is used as an additional offer type preference in case the preferred remote type is not
// compatible with the local type available. It is expected that both sides of the migration will support the
// fallback type for the volume's content type that is being migrated.
func MatchTypes(offer *migration.MigrationHeader, fallbackType migration.MigrationFSType, ourTypes []Type) ([]Type, error) {
	// Generate an offer types slice from the preferred type supplied from remote and the
	// fallback type supplied based on the content type of the transfer.
	offeredFSTypes := []migration.MigrationFSType{offer.GetFs(), fallbackType}

	matchedTypes := []Type{}

	// Find first matching type.
	for _, ourType := range ourTypes {
		for _, offerFSType := range offeredFSTypes {
			if offerFSType != ourType.FSType {
				continue // Not a match, try the next one.
			}

			// We got a match, now extract the relevant offered features.
			var offeredFeatures []string
			if offerFSType == migration.MigrationFSType_ZFS {
				offeredFeatures = offer.GetZfsFeaturesSlice()
			} else if offerFSType == migration.MigrationFSType_BTRFS {
				offeredFeatures = offer.GetBtrfsFeaturesSlice()
			} else if offerFSType == migration.MigrationFSType_RSYNC {
				offeredFeatures = offer.GetRsyncFeaturesSlice()
			}

			// Find common features in both our type and offered type.
			commonFeatures := []string{}
			for _, ourFeature := range ourType.Features {
				if slices.Contains(offeredFeatures, ourFeature) {
					commonFeatures = append(commonFeatures, ourFeature)
				}
			}

			if offer.GetRefresh() {
				// Optimized refresh with zfs only works if ZfsFeatureMigrationHeader is available.
				if ourType.FSType == migration.MigrationFSType_ZFS && !slices.Contains(commonFeatures, migration.ZFSFeatureMigrationHeader) {
					continue
				}

				// Optimized refresh with btrfs only works if BtrfsFeatureSubvolumeUUIDs is available.
				if ourType.FSType == migration.MigrationFSType_BTRFS && !slices.Contains(commonFeatures, migration.BTRFSFeatureSubvolumeUUIDs) {
					continue
				}
			}

			// Append type with combined features.
			matchedTypes = append(matchedTypes, Type{
				FSType:   ourType.FSType,
				Features: commonFeatures,
			})
		}
	}

	if len(matchedTypes) < 1 {
		// No matching transport type found, generate an error with offered types and our types.
		offeredTypeStrings := make([]string, 0, len(offeredFSTypes))
		for _, offerFSType := range offeredFSTypes {
			offeredTypeStrings = append(offeredTypeStrings, offerFSType.String())
		}

		ourTypeStrings := make([]string, 0, len(ourTypes))
		for _, ourType := range ourTypes {
			ourTypeStrings = append(ourTypeStrings, ourType.FSType.String())
		}

		return matchedTypes, fmt.Errorf("No matching migration types found. Offered types: %v, our types: %v", offeredTypeStrings, ourTypeStrings)
	}

	return matchedTypes, nil
}

func progressWrapperRender(op *operations.Operation, key string, description string, progressInt int64, speedInt int64) {
	meta := op.Metadata()
	if meta == nil {
		meta = make(map[string]any)
	}

	progress := fmt.Sprintf("%s (%s/s)", units.GetByteSizeString(progressInt, 2), units.GetByteSizeString(speedInt, 2))
	if description != "" {
		progress = fmt.Sprintf("%s: %s (%s/s)", description, units.GetByteSizeString(progressInt, 2), units.GetByteSizeString(speedInt, 2))
	}

	if meta[key] != progress {
		meta[key] = progress
		_ = op.UpdateMetadata(meta)
	}
}

// ProgressReader reports the read progress.
func ProgressReader(op *operations.Operation, key string, description string) func(io.ReadCloser) io.ReadCloser {
	return func(reader io.ReadCloser) io.ReadCloser {
		if op == nil {
			return reader
		}

		progress := func(progressInt int64, speedInt int64) {
			progressWrapperRender(op, key, description, progressInt, speedInt)
		}

		readPipe := &ioprogress.ProgressReader{
			ReadCloser: reader,
			Tracker: &ioprogress.ProgressTracker{
				Handler: progress,
			},
		}

		return readPipe
	}
}

// ProgressWriter reports the write progress.
func ProgressWriter(op *operations.Operation, key string, description string) func(io.WriteCloser) io.WriteCloser {
	return func(writer io.WriteCloser) io.WriteCloser {
		if op == nil {
			return writer
		}

		progress := func(progressInt int64, speedInt int64) {
			progressWrapperRender(op, key, description, progressInt, speedInt)
		}

		writePipe := &ioprogress.ProgressWriter{
			WriteCloser: writer,
			Tracker: &ioprogress.ProgressTracker{
				Handler: progress,
			},
		}

		return writePipe
	}
}

// ProgressTracker returns a migration I/O tracker.
func ProgressTracker(op *operations.Operation, key string, description string) *ioprogress.ProgressTracker {
	progress := func(progressInt int64, speedInt int64) {
		progressWrapperRender(op, key, description, progressInt, speedInt)
	}

	tracker := &ioprogress.ProgressTracker{
		Handler: progress,
	}

	return tracker
}
