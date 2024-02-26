package instance

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/flosch/pongo2"
	"github.com/google/uuid"
	liblxc "github.com/lxc/go-lxc"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/internal/instance"
	internalInstance "github.com/lxc/incus/internal/instance"
	"github.com/lxc/incus/internal/migration"
	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/backup"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/instance/operationlock"
	"github.com/lxc/incus/internal/server/seccomp"
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/internal/server/sys"
	localUtil "github.com/lxc/incus/internal/server/util"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/idmap"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/osarch"
	"github.com/lxc/incus/shared/util"
	"github.com/lxc/incus/shared/validate"
)

// ValidDevices is linked from instance/drivers.validDevices to validate device config.
var ValidDevices func(state *state.State, p api.Project, instanceType instancetype.Type, localDevices deviceConfig.Devices, expandedDevices deviceConfig.Devices) error

// Load is linked from instance/drivers.load to allow different instance types to be loaded.
var Load func(s *state.State, args db.InstanceArgs, p api.Project) (Instance, error)

// Create is linked from instance/drivers.create to allow difference instance types to be created.
// Returns a revert fail function that can be used to undo this function if a subsequent step fails.
var Create func(s *state.State, args db.InstanceArgs, p api.Project) (Instance, revert.Hook, error)

func exclusiveConfigKeys(key1 string, key2 string, config map[string]string) (val string, ok bool, err error) {
	if config[key1] != "" && config[key2] != "" {
		return "", false, fmt.Errorf("Mutually exclusive keys %s and %s are set", key1, key2)
	}

	val, ok = config[key1]
	if ok {
		return
	}

	val, ok = config[key2]
	if ok {
		return
	}

	return "", false, nil
}

// ValidConfig validates an instance's config.
func ValidConfig(sysOS *sys.OS, config map[string]string, expanded bool, instanceType instancetype.Type) error {
	if config == nil {
		return nil
	}

	for k, v := range config {
		if instanceType == instancetype.Any && !expanded && strings.HasPrefix(k, instance.ConfigVolatilePrefix) {
			return fmt.Errorf("Volatile keys can only be set on instances")
		}

		if instanceType == instancetype.Any && !expanded && strings.HasPrefix(k, "image.") {
			return fmt.Errorf("Image keys can only be set on instances")
		}

		err := validConfigKey(sysOS, k, v, instanceType)
		if err != nil {
			return err
		}
	}

	_, rawSeccomp := config["raw.seccomp"]
	_, isAllow, err := exclusiveConfigKeys("security.syscalls.allow", "security.syscalls.whitelist", config)
	if err != nil {
		return err
	}

	_, isDeny, err := exclusiveConfigKeys("security.syscalls.deny", "security.syscalls.blacklist", config)
	if err != nil {
		return err
	}

	val, _, err := exclusiveConfigKeys("security.syscalls.deny_default", "security.syscalls.blacklist_default", config)
	if err != nil {
		return err
	}

	isDenyDefault := util.IsTrue(val)

	val, _, err = exclusiveConfigKeys("security.syscalls.deny_compat", "security.syscalls.blacklist_compat", config)
	if err != nil {
		return err
	}

	isDenyCompat := util.IsTrue(val)

	if rawSeccomp && (isAllow || isDeny || isDenyDefault || isDenyCompat) {
		return fmt.Errorf("raw.seccomp is mutually exclusive with security.syscalls*")
	}

	if isAllow && (isDeny || isDenyDefault || isDenyCompat) {
		return fmt.Errorf("security.syscalls.allow is mutually exclusive with security.syscalls.deny*")
	}

	_, err = seccomp.SyscallInterceptMountFilter(config)
	if err != nil {
		return err
	}

	if expanded && (util.IsFalseOrEmpty(config["security.privileged"])) && sysOS.IdmapSet == nil {
		return fmt.Errorf("No uid/gid allocation configured. In this mode, only privileged containers are supported")
	}

	if util.IsTrue(config["security.privileged"]) && util.IsTrue(config["nvidia.runtime"]) {
		return fmt.Errorf("nvidia.runtime is incompatible with privileged containers")
	}

	return nil
}

func validConfigKey(os *sys.OS, key string, value string, instanceType instancetype.Type) error {
	f, err := instance.ConfigKeyChecker(key, instanceType.ToAPI())
	if err != nil {
		return err
	}

	if err = f(value); err != nil {
		return err
	}

	if key == "raw.lxc" {
		return lxcValidConfig(value)
	}

	if key == "security.syscalls.deny_compat" || key == "security.syscalls.blacklist_compat" {
		for _, arch := range os.Architectures {
			if arch == osarch.ARCH_64BIT_INTEL_X86 ||
				arch == osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN ||
				arch == osarch.ARCH_64BIT_POWERPC_BIG_ENDIAN {
				return nil
			}
		}
		return fmt.Errorf("%s isn't supported on this architecture", key)
	}

	return nil
}

func lxcParseRawLXC(line string) (string, string, error) {
	// Ignore empty lines
	if len(line) == 0 {
		return "", "", nil
	}

	// Skip space {"\t", " "}
	line = strings.TrimLeft(line, "\t ")

	// Ignore comments
	if strings.HasPrefix(line, "#") {
		return "", "", nil
	}

	// Ensure the format is valid
	membs := strings.SplitN(line, "=", 2)
	if len(membs) != 2 {
		return "", "", fmt.Errorf("Invalid raw.lxc line: %s", line)
	}

	key := strings.ToLower(strings.Trim(membs[0], " \t"))
	val := strings.Trim(membs[1], " \t")
	return key, val, nil
}

func lxcValidConfig(rawLxc string) error {
	for _, line := range strings.Split(rawLxc, "\n") {
		key, _, err := lxcParseRawLXC(line)
		if err != nil {
			return err
		}

		if key == "" {
			continue
		}

		// block some keys
		if key == "lxc.logfile" || key == "lxc.log.file" {
			return fmt.Errorf("Setting lxc.logfile is not allowed")
		}

		if key == "lxc.syslog" || key == "lxc.log.syslog" {
			return fmt.Errorf("Setting lxc.log.syslog is not allowed")
		}

		if key == "lxc.ephemeral" {
			return fmt.Errorf("Setting lxc.ephemeral is not allowed")
		}

		if strings.HasPrefix(key, "lxc.prlimit.") {
			return fmt.Errorf(`Process limits should be set via ` +
				`"limits.kernel.[limit name]" and not ` +
				`directly via "lxc.prlimit.[limit name]"`)
		}

		networkKeyPrefix := "lxc.net."
		if !liblxc.RuntimeLiblxcVersionAtLeast(liblxc.Version(), 2, 1, 0) {
			networkKeyPrefix = "lxc.network."
		}

		if strings.HasPrefix(key, networkKeyPrefix) {
			fields := strings.Split(key, ".")

			if !liblxc.RuntimeLiblxcVersionAtLeast(liblxc.Version(), 2, 1, 0) {
				// lxc.network.X.ipv4 or lxc.network.X.ipv6
				if len(fields) == 4 && slices.Contains([]string{"ipv4", "ipv6"}, fields[3]) {
					continue
				}

				// lxc.network.X.ipv4.gateway or lxc.network.X.ipv6.gateway
				if len(fields) == 5 && slices.Contains([]string{"ipv4", "ipv6"}, fields[3]) && fields[4] == "gateway" {
					continue
				}
			} else {
				// lxc.net.X.ipv4.address or lxc.net.X.ipv6.address
				if len(fields) == 5 && slices.Contains([]string{"ipv4", "ipv6"}, fields[3]) && fields[4] == "address" {
					continue
				}

				// lxc.net.X.ipv4.gateway or lxc.net.X.ipv6.gateway
				if len(fields) == 5 && slices.Contains([]string{"ipv4", "ipv6"}, fields[3]) && fields[4] == "gateway" {
					continue
				}
			}

			return fmt.Errorf("Only interface-specific ipv4/ipv6 %s keys are allowed", networkKeyPrefix)
		}
	}

	return nil
}

// AllowedUnprivilegedOnlyMap checks that root user is not mapped into instance.
func AllowedUnprivilegedOnlyMap(rawIdmap string) error {
	rawMaps, err := idmap.NewSetFromIncusIDMap(rawIdmap)
	if err != nil {
		return err
	}

	for _, ent := range rawMaps.Entries {
		if ent.HostID == 0 {
			return fmt.Errorf("Cannot map root user into container as the server was configured to only allow unprivileged containers")
		}
	}

	return nil
}

// LoadByID loads an instance by ID.
func LoadByID(s *state.State, id int) (Instance, error) {
	var project string
	var name string

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Get the DB record
		project, name, err = tx.GetInstanceProjectAndName(ctx, id)
		if err != nil {
			return fmt.Errorf("Failed getting instance project and name: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return LoadByProjectAndName(s, project, name)
}

// LoadInstanceDatabaseObject loads a db.Instance object, accounting for snapshots.
func LoadInstanceDatabaseObject(ctx context.Context, tx *db.ClusterTx, project, name string) (*cluster.Instance, error) {
	var container *cluster.Instance
	var err error

	if strings.Contains(name, internalInstance.SnapshotDelimiter) {
		parts := strings.SplitN(name, internalInstance.SnapshotDelimiter, 2)
		instanceName := parts[0]
		snapshotName := parts[1]

		instance, err := cluster.GetInstance(ctx, tx.Tx(), project, instanceName)
		if err != nil {
			return nil, fmt.Errorf("Failed to fetch instance %q in project %q: %w", name, project, err)
		}

		snapshot, err := cluster.GetInstanceSnapshot(ctx, tx.Tx(), project, instanceName, snapshotName)
		if err != nil {
			return nil, fmt.Errorf("Failed to fetch snapshot %q of instance %q in project %q: %w", snapshotName, instanceName, project, err)
		}

		c := snapshot.ToInstance(instance.Name, instance.Node, instance.Type, instance.Architecture)
		container = &c
	} else {
		container, err = cluster.GetInstance(ctx, tx.Tx(), project, name)
		if err != nil {
			return nil, fmt.Errorf("Failed to fetch instance %q in project %q: %w", name, project, err)
		}
	}

	return container, nil
}

// LoadByProjectAndName loads an instance by project and name.
func LoadByProjectAndName(s *state.State, projectName string, instanceName string) (Instance, error) {
	// Get the DB record
	var args db.InstanceArgs
	var p *api.Project
	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		proj, err := cluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return err
		}

		p, err = proj.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		inst, err := LoadInstanceDatabaseObject(ctx, tx, projectName, instanceName)
		if err != nil {
			return err
		}

		instArgs, err := tx.InstancesToInstanceArgs(ctx, true, *inst)
		if err != nil {
			return err
		}

		args = instArgs[inst.ID]

		return nil
	})
	if err != nil {
		return nil, err
	}

	inst, err := Load(s, args, *p)
	if err != nil {
		return nil, fmt.Errorf("Failed to load instance: %w", err)
	}

	return inst, nil
}

// LoadNodeAll loads all instances on this server.
func LoadNodeAll(s *state.State, instanceType instancetype.Type) ([]Instance, error) {
	var err error
	var instances []Instance

	filter := cluster.InstanceFilter{Type: instanceType.Filter()}
	if s.ServerName != "" {
		filter.Node = &s.ServerName
	}

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.InstanceList(ctx, func(dbInst db.InstanceArgs, p api.Project) error {
			inst, err := Load(s, dbInst, p)
			if err != nil {
				return fmt.Errorf("Failed loading instance %q in project %q: %w", dbInst.Name, dbInst.Project, err)
			}

			instances = append(instances, inst)

			return nil
		}, filter)
	})
	if err != nil {
		return nil, err
	}

	return instances, nil
}

// LoadFromBackup loads from a mounted instance's backup file.
// If applyProfiles is false, then the profiles property will be cleared to prevent profile enrichment from DB.
// Then the expanded config and expanded devices from the backup file will be applied to the local config and
// local devices respectively. This is done to allow an expanded instance to be returned without needing the DB.
func LoadFromBackup(s *state.State, projectName string, instancePath string, applyProfiles bool) (Instance, error) {
	var inst Instance

	backupYamlPath := filepath.Join(instancePath, "backup.yaml")
	backupConf, err := backup.ParseConfigYamlFile(backupYamlPath)
	if err != nil {
		return nil, fmt.Errorf("Failed parsing instance backup file from %q: %w", backupYamlPath, err)
	}

	instDBArgs, err := backup.ConfigToInstanceDBArgs(s, backupConf, projectName, applyProfiles)
	if err != nil {
		return nil, err
	}

	if !applyProfiles {
		// Stop instance.Load() from expanding profile config from DB, and apply expanded config from
		// backup file to local config. This way we can still see the devices even if DB not available.
		instDBArgs.Config = backupConf.Container.ExpandedConfig
		instDBArgs.Devices = deviceConfig.NewDevices(backupConf.Container.ExpandedDevices)
	}

	var p *api.Project
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		proj, err := cluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return err
		}

		p, err = proj.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	inst, err = Load(s, *instDBArgs, *p)
	if err != nil {
		return nil, fmt.Errorf("Failed loading instance from backup file %q: %w", backupYamlPath, err)
	}

	return inst, nil
}

// DeviceNextInterfaceHWAddr generates a random MAC address.
func DeviceNextInterfaceHWAddr() (string, error) {
	// Generate a new random MAC address using the usual prefix
	ret := bytes.Buffer{}
	for _, c := range "00:16:3e:xx:xx:xx" {
		if c == 'x' {
			c, err := rand.Int(rand.Reader, big.NewInt(16))
			if err != nil {
				return "", err
			}

			ret.WriteString(fmt.Sprintf("%x", c.Int64()))
		} else {
			ret.WriteString(string(c))
		}
	}

	return ret.String(), nil
}

// BackupLoadByName load an instance backup from the database.
func BackupLoadByName(s *state.State, project, name string) (*backup.InstanceBackup, error) {
	var args db.InstanceBackup

	// Get the backup database record
	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		args, err = tx.GetInstanceBackup(ctx, project, name)
		return err
	})
	if err != nil {
		return nil, err
	}

	// Load the instance it belongs to
	instance, err := LoadByID(s, args.InstanceID)
	if err != nil {
		return nil, err
	}

	return backup.NewInstanceBackup(s, instance, args.ID, name, args.CreationDate, args.ExpiryDate, args.InstanceOnly, args.OptimizedStorage), nil
}

// ResolveImage takes an instance source and returns a hash suitable for instance creation or download.
func ResolveImage(ctx context.Context, tx *db.ClusterTx, projectName string, source api.InstanceSource) (string, error) {
	if source.Fingerprint != "" {
		return source.Fingerprint, nil
	}

	if source.Alias != "" {
		if source.Server != "" {
			return source.Alias, nil
		}

		_, alias, err := tx.GetImageAlias(ctx, projectName, source.Alias, true)
		if err != nil {
			return "", err
		}

		return alias.Target, nil
	}

	if source.Properties != nil {
		if source.Server != "" {
			return "", fmt.Errorf("Property match is only supported for local images")
		}

		hashes, err := tx.GetImagesFingerprints(ctx, projectName, false)
		if err != nil {
			return "", err
		}

		var image *api.Image
		for _, imageHash := range hashes {
			_, img, err := tx.GetImageByFingerprintPrefix(ctx, imageHash, cluster.ImageFilter{Project: &projectName})
			if err != nil {
				continue
			}

			if image != nil && img.CreatedAt.Before(image.CreatedAt) {
				continue
			}

			match := true
			for key, value := range source.Properties {
				if img.Properties[key] != value {
					match = false
					break
				}
			}

			if !match {
				continue
			}

			image = img
		}

		if image != nil {
			return image.Fingerprint, nil
		}

		return "", fmt.Errorf("No matching image could be found")
	}

	return "", fmt.Errorf("Must specify one of alias, fingerprint or properties for init from image")
}

// SuitableArchitectures returns a slice of architecture ids based on an instance create request.
//
// An empty list indicates that the request may be handled by any architecture.
// A nil list indicates that we can't tell at this stage, typically for private images.
func SuitableArchitectures(ctx context.Context, s *state.State, tx *db.ClusterTx, projectName string, sourceInst *cluster.Instance, sourceImageRef string, req api.InstancesPost) ([]int, error) {
	// Handle cases where the architecture is already provided.
	if slices.Contains([]string{"migration", "none"}, req.Source.Type) && req.Architecture != "" {
		id, err := osarch.ArchitectureId(req.Architecture)
		if err != nil {
			return nil, err
		}

		return []int{id}, nil
	}

	// For migration, an architecture must be specified in the req.
	if req.Source.Type == "migration" && req.Architecture == "" {
		return nil, api.StatusErrorf(http.StatusBadRequest, "An architecture must be specified in migration requests")
	}

	// For none, allow any architecture.
	if req.Source.Type == "none" {
		return []int{}, nil
	}

	// For copy, always use the source architecture.
	if req.Source.Type == "copy" {
		return []int{sourceInst.Architecture}, nil
	}

	// For image, things get a bit more complicated.
	if req.Source.Type == "image" {
		// Handle local images.
		if req.Source.Server == "" {
			_, img, err := tx.GetImageByFingerprintPrefix(ctx, sourceImageRef, cluster.ImageFilter{Project: &projectName})
			if err != nil {
				return nil, err
			}

			id, err := osarch.ArchitectureId(img.Architecture)
			if err != nil {
				return nil, err
			}

			return []int{id}, nil
		}

		// Handle remote images.
		if req.Source.Server != "" {
			if req.Source.Secret != "" {
				// We can't retrieve a private image, defer to later processing.
				return nil, nil
			}

			var err error
			var remote incus.ImageServer
			if slices.Contains([]string{"", "incus", "lxd"}, req.Source.Protocol) {
				// Remote image server.
				remote, err = incus.ConnectPublicIncus(req.Source.Server, &incus.ConnectionArgs{
					TLSServerCert: req.Source.Certificate,
					UserAgent:     version.UserAgent,
					Proxy:         s.Proxy,
					CachePath:     s.OS.CacheDir,
					CacheExpiry:   time.Hour,
				})
				if err != nil {
					return nil, err
				}
			} else if req.Source.Protocol == "simplestreams" {
				// Remote simplestreams image server.
				remote, err = incus.ConnectSimpleStreams(req.Source.Server, &incus.ConnectionArgs{
					TLSServerCert: req.Source.Certificate,
					UserAgent:     version.UserAgent,
					Proxy:         s.Proxy,
					CachePath:     s.OS.CacheDir,
					CacheExpiry:   time.Hour,
				})
				if err != nil {
					return nil, err
				}
			} else {
				return nil, api.StatusErrorf(http.StatusBadRequest, "Unsupported remote image server protocol %q", req.Source.Protocol)
			}

			// Look for a matching alias.
			entries, err := remote.GetImageAliasArchitectures(string(req.Type), sourceImageRef)
			if err != nil {
				// Look for a matching image by fingerprint.
				img, _, err := remote.GetImage(sourceImageRef)
				if err != nil {
					return nil, err
				}

				id, err := osarch.ArchitectureId(img.Architecture)
				if err != nil {
					return nil, err
				}

				return []int{id}, nil
			}

			architectures := []int{}
			for arch := range entries {
				id, err := osarch.ArchitectureId(arch)
				if err != nil {
					return nil, err
				}

				architectures = append(architectures, id)
			}

			return architectures, nil
		}
	}

	// No other known types
	return nil, api.StatusErrorf(http.StatusBadRequest, "Unknown instance source type %q", req.Source.Type)
}

// ValidName validates an instance name. There are different validation rules for instance snapshot names
// so it takes an argument indicating whether the name is to be used for a snapshot or not.
func ValidName(instanceName string, isSnapshot bool) error {
	if isSnapshot {
		parentName, snapshotName, _ := api.GetParentAndSnapshotName(instanceName)
		err := validate.IsHostname(parentName)
		if err != nil {
			return fmt.Errorf("Invalid instance name: %w", err)
		}

		// Snapshot part is more flexible, but doesn't allow space or / character.
		if strings.ContainsAny(snapshotName, " /") {
			return fmt.Errorf("Invalid instance snapshot name: Cannot contain space or / characters")
		}
	} else {
		if strings.Contains(instanceName, internalInstance.SnapshotDelimiter) {
			return fmt.Errorf("The character %q is reserved for snapshots", internalInstance.SnapshotDelimiter)
		}

		err := validate.IsHostname(instanceName)
		if err != nil {
			return fmt.Errorf("Invalid instance name: %w", err)
		}
	}

	return nil
}

// CreateInternal creates an instance record and storage volume record in the database and sets up devices.
// Accepts a reverter that revert steps this function does will be added to. It is up to the caller to
// call the revert's Fail() or Success() function as needed.
// Returns the created instance, along with a "create" operation lock that needs to be marked as Done once the
// instance is fully completed, and a revert fail function that can be used to undo this function if a subsequent
// step fails.
func CreateInternal(s *state.State, args db.InstanceArgs, clearLogDir bool, checkArchitecture bool) (Instance, *operationlock.InstanceOperation, revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	// Check instance type requested is supported by this machine.
	err := s.InstanceTypes[args.Type]
	if err != nil {
		return nil, nil, nil, fmt.Errorf("Instance type %q is not supported on this server: %w", args.Type, err)
	}

	// Set default values.
	if args.Project == "" {
		args.Project = api.ProjectDefaultName
	}

	if args.Profiles == nil {
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			args.Profiles, err = tx.GetProfiles(ctx, args.Project, []string{"default"})

			return err
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("Failed to get default profile for new instance")
		}
	}

	if args.Config == nil {
		args.Config = map[string]string{}
	}

	if args.BaseImage != "" {
		args.Config["volatile.base_image"] = args.BaseImage
	}

	if args.Config["volatile.uuid"] == "" {
		args.Config["volatile.uuid"] = uuid.New().String()
	}

	args.Config["volatile.uuid.generation"] = args.Config["volatile.uuid"]

	if args.Devices == nil {
		args.Devices = deviceConfig.Devices{}
	}

	if args.Architecture == 0 {
		args.Architecture = s.OS.Architectures[0]
	}

	err = ValidName(args.Name, args.Snapshot)
	if err != nil {
		return nil, nil, nil, err
	}

	if !args.Snapshot {
		// Unset expiry date since instances don't expire.
		args.ExpiryDate = time.Time{}

		// Generate a cloud-init instance-id if not provided.
		//
		// This is generated here rather than in startCommon as only new
		// instances or those which get modified should receive an instance-id.
		// Existing instances will keep using their instance name as instance-id to
		// avoid triggering cloud-init on upgrade.
		if args.Config["volatile.cloud-init.instance-id"] == "" {
			args.Config["volatile.cloud-init.instance-id"] = uuid.New().String()
		}
	}

	// Validate instance config.
	err = ValidConfig(s.OS, args.Config, false, args.Type)
	if err != nil {
		return nil, nil, nil, err
	}

	// Leave validating devices to Create function call below.

	// Validate architecture.
	_, err = osarch.ArchitectureName(args.Architecture)
	if err != nil {
		return nil, nil, nil, err
	}

	if checkArchitecture && !slices.Contains(s.OS.Architectures, args.Architecture) {
		return nil, nil, nil, fmt.Errorf("Requested architecture isn't supported by this host")
	}

	var profiles []string

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Validate profiles.
		profiles, err = tx.GetProfileNames(ctx, args.Project)

		return err
	})
	if err != nil {
		return nil, nil, nil, err
	}

	checkedProfiles := map[string]bool{}
	for _, profile := range args.Profiles {
		if !slices.Contains(profiles, profile.Name) {
			return nil, nil, nil, fmt.Errorf("Requested profile %q doesn't exist", profile.Name)
		}

		if checkedProfiles[profile.Name] {
			return nil, nil, nil, fmt.Errorf("Duplicate profile found in request")
		}

		checkedProfiles[profile.Name] = true
	}

	if args.CreationDate.IsZero() {
		args.CreationDate = time.Now().UTC()
	}

	if args.LastUsedDate.IsZero() {
		args.LastUsedDate = time.Unix(0, 0).UTC()
	}

	// Prevent concurrent create requests for same instance.
	op, err := operationlock.Create(args.Project, args.Name, operationlock.ActionCreate, false, false)
	if err != nil {
		return nil, nil, nil, err
	}

	revert.Add(func() { op.Done(err) })

	var dbInst cluster.Instance
	var p *api.Project

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		proj, err := cluster.GetProject(ctx, tx.Tx(), args.Project)
		if err != nil {
			return err
		}

		p, err = proj.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		devices, err := cluster.APIToDevices(args.Devices.CloneNative())
		if err != nil {
			return err
		}

		if args.Snapshot {
			parts := strings.SplitN(args.Name, internalInstance.SnapshotDelimiter, 2)
			instanceName := parts[0]
			snapshotName := parts[1]
			instance, err := cluster.GetInstance(ctx, tx.Tx(), args.Project, instanceName)
			if err != nil {
				return fmt.Errorf("Get instance %q in project %q", instanceName, args.Project)
			}

			snapshot := cluster.InstanceSnapshot{
				Project:      args.Project,
				Instance:     instanceName,
				Name:         snapshotName,
				CreationDate: args.CreationDate,
				Stateful:     args.Stateful,
				Description:  args.Description,
				ExpiryDate:   sql.NullTime{Time: args.ExpiryDate, Valid: true},
			}

			id, err := cluster.CreateInstanceSnapshot(ctx, tx.Tx(), snapshot)
			if err != nil {
				return fmt.Errorf("Add snapshot info to the database: %w", err)
			}

			err = cluster.CreateInstanceSnapshotConfig(ctx, tx.Tx(), id, args.Config)
			if err != nil {
				return err
			}

			err = cluster.CreateInstanceSnapshotDevices(ctx, tx.Tx(), id, devices)
			if err != nil {
				return err
			}

			// Read back the snapshot, to get ID and creation time.
			s, err := cluster.GetInstanceSnapshot(ctx, tx.Tx(), args.Project, instanceName, snapshotName)
			if err != nil {
				return fmt.Errorf("Fetch created snapshot from the database: %w", err)
			}

			dbInst = s.ToInstance(instance.Name, instance.Node, instance.Type, instance.Architecture)

			newArgs, err := tx.InstancesToInstanceArgs(ctx, false, dbInst)
			if err != nil {
				return err
			}

			// Populate profile info that was already loaded.
			newInstArgs := newArgs[dbInst.ID]
			newInstArgs.Profiles = args.Profiles
			args = newInstArgs

			return nil
		}

		// Create the instance entry.
		dbInst = cluster.Instance{
			Project:      args.Project,
			Name:         args.Name,
			Node:         s.ServerName,
			Type:         args.Type,
			Snapshot:     args.Snapshot,
			Architecture: args.Architecture,
			Ephemeral:    args.Ephemeral,
			CreationDate: args.CreationDate,
			Stateful:     args.Stateful,
			LastUseDate:  sql.NullTime{Time: args.LastUsedDate, Valid: true},
			Description:  args.Description,
			ExpiryDate:   sql.NullTime{Time: args.ExpiryDate, Valid: true},
		}

		instanceID, err := cluster.CreateInstance(ctx, tx.Tx(), dbInst)
		if err != nil {
			return fmt.Errorf("Add instance info to the database: %w", err)
		}

		err = cluster.CreateInstanceDevices(ctx, tx.Tx(), instanceID, devices)
		if err != nil {
			return err
		}

		err = cluster.CreateInstanceConfig(ctx, tx.Tx(), instanceID, args.Config)
		if err != nil {
			return err
		}

		profileNames := make([]string, 0, len(args.Profiles))
		for _, profile := range args.Profiles {
			profileNames = append(profileNames, profile.Name)
		}

		err = cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(instanceID), dbInst.Project, profileNames)
		if err != nil {
			return err
		}

		// Read back the instance, to get ID and creation time.
		dbRow, err := cluster.GetInstance(ctx, tx.Tx(), args.Project, args.Name)
		if err != nil {
			return fmt.Errorf("Fetch created instance from the database: %w", err)
		}

		dbInst = *dbRow

		if dbInst.ID < 1 {
			return fmt.Errorf("Unexpected instance database ID %d: %w", dbInst.ID, err)
		}

		newArgs, err := tx.InstancesToInstanceArgs(ctx, false, dbInst)
		if err != nil {
			return err
		}

		// Populate profile info that was already loaded.
		newInstArgs := newArgs[dbInst.ID]
		newInstArgs.Profiles = args.Profiles
		args = newInstArgs

		return nil
	})
	if err != nil {
		if err == db.ErrAlreadyDefined {
			thing := "Instance"
			if internalInstance.IsSnapshot(args.Name) {
				thing = "Snapshot"
			}

			return nil, nil, nil, fmt.Errorf("%s %q already exists", thing, args.Name)
		}

		return nil, nil, nil, err
	}

	revert.Add(func() {
		_ = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.DeleteInstance(ctx, dbInst.Project, dbInst.Name)
		})
	})
	inst, cleanup, err := Create(s, args, *p)
	if err != nil {
		logger.Error("Failed initialising instance", logger.Ctx{"project": args.Project, "instance": args.Name, "type": args.Type, "err": err})
		return nil, nil, nil, fmt.Errorf("Failed initialising instance: %w", err)
	}

	revert.Add(cleanup)

	// Wipe any existing log for this instance name.
	if clearLogDir {
		_ = os.RemoveAll(inst.LogPath())
		_ = os.RemoveAll(inst.RunPath())
	}

	cleanup = revert.Clone().Fail
	revert.Success()
	return inst, op, cleanup, err
}

// NextSnapshotName finds the next snapshot for an instance.
func NextSnapshotName(s *state.State, inst Instance, defaultPattern string) (string, error) {
	var err error

	pattern := inst.ExpandedConfig()["snapshots.pattern"]
	if pattern == "" {
		pattern = defaultPattern
	}

	pattern, err = internalUtil.RenderTemplate(pattern, pongo2.Context{
		"creation_date": time.Now(),
	})
	if err != nil {
		return "", err
	}

	count := strings.Count(pattern, "%d")
	if count > 1 {
		return "", fmt.Errorf("Snapshot pattern may contain '%%d' only once")
	} else if count == 1 {
		var i int

		_ = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			i = tx.GetNextInstanceSnapshotIndex(ctx, inst.Project().Name, inst.Name(), pattern)

			return nil
		})

		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	snapshotExists := false

	snapshots, err := inst.Snapshots()
	if err != nil {
		return "", err
	}

	for _, snap := range snapshots {
		_, snapOnlyName, _ := api.GetParentAndSnapshotName(snap.Name())
		if snapOnlyName == pattern {
			snapshotExists = true
			break
		}
	}

	// Append '-0', '-1', etc. if the actual pattern/snapshot name already exists
	if snapshotExists {
		pattern = fmt.Sprintf("%s-%%d", pattern)

		var i int

		_ = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			i = tx.GetNextInstanceSnapshotIndex(ctx, inst.Project().Name, inst.Name(), pattern)

			return nil
		})
		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	return pattern, nil
}

// temporaryName returns the temporary instance name using a stable random generator.
// The returned string is a valid DNS name.
func temporaryName(instUUID string) (string, error) {
	r, err := localUtil.GetStableRandomGenerator(instUUID)
	if err != nil {
		return "", err
	}

	// The longest temporary name is move-of-18446744073709551615 which has a length
	// of 30 characters since 18446744073709551615 is the biggest value for an uint64.
	// The prefix is attached to have a valid DNS name that doesn't start with numbers.
	return fmt.Sprintf("move-of-%d", r.Uint64()), nil
}

// MoveTemporaryName returns a name derived from the instance's volatile.uuid, to use when moving an instance
// across pools or cluster members which can be used for the naming the temporary copy before deleting the original
// instance and renaming the copy to the original name.
// If volatile.uuid is not set, a new UUID is generated and stored in the instance's config.
func MoveTemporaryName(inst Instance) (string, error) {
	instUUID := inst.LocalConfig()["volatile.uuid"]
	if instUUID == "" {
		instUUID = uuid.New().String()
		err := inst.VolatileSet(map[string]string{"volatile.uuid": instUUID})
		if err != nil {
			return "", fmt.Errorf("Failed setting volatile.uuid to %s: %w", instUUID, err)
		}
	}

	return temporaryName(instUUID)
}

// IsSameLogicalInstance returns true if the supplied Instance and db.Instance have the same project and name or
// if they have the same volatile.uuid values.
func IsSameLogicalInstance(inst Instance, dbInst *db.InstanceArgs) bool {
	// Instance name is unique within a project.
	if dbInst.Project == inst.Project().Name && dbInst.Name == inst.Name() {
		return true
	}

	// Don't trigger duplicate resource errors for temporary copies.
	if dbInst.Config["volatile.uuid"] == inst.LocalConfig()["volatile.uuid"] {
		// Accommodate moving instances between storage pools.
		// Check temporary copy against source.
		tempName, err := temporaryName(inst.LocalConfig()["volatile.uuid"])
		if err != nil {
			return false
		}

		if dbInst.Name == tempName {
			return true
		}

		// Check source against temporary copy.
		tempName, err = temporaryName(dbInst.Config["volatile.uuid"])
		if err != nil {
			return false
		}

		if inst.Name() == tempName {
			return true
		}

		// Accommodate moving instances between projects.
		if dbInst.Project != inst.Project().Name {
			return true
		}
	}

	return false
}

// SnapshotToProtobuf converts a snapshot record to a migration snapshot record.
func SnapshotToProtobuf(snap *api.InstanceSnapshot) *migration.Snapshot {
	config := make([]*migration.Config, 0, len(snap.Config))
	for k, v := range snap.Config {
		kCopy := string(k)
		vCopy := string(v)
		config = append(config, &migration.Config{Key: &kCopy, Value: &vCopy})
	}

	devices := make([]*migration.Device, 0, len(snap.Devices))
	for name, d := range snap.Devices {
		props := make([]*migration.Config, 0, len(snap.Devices))
		for k, v := range d {
			// Local loop vars.
			kCopy := string(k)
			vCopy := string(v)
			props = append(props, &migration.Config{Key: &kCopy, Value: &vCopy})
		}

		nameCopy := name // Local loop var.
		devices = append(devices, &migration.Device{Name: &nameCopy, Config: props})
	}

	isEphemeral := snap.Ephemeral
	archID, _ := osarch.ArchitectureId(snap.Architecture)
	arch := int32(archID)
	stateful := snap.Stateful
	creationDate := snap.CreatedAt.UTC().Unix()
	lastUsedDate := snap.LastUsedAt.UTC().Unix()
	expiryDate := snap.ExpiresAt.UTC().Unix()

	return &migration.Snapshot{
		Name:         &snap.Name,
		LocalConfig:  config,
		Profiles:     snap.Profiles,
		Ephemeral:    &isEphemeral,
		LocalDevices: devices,
		Architecture: &arch,
		Stateful:     &stateful,
		CreationDate: &creationDate,
		LastUsedDate: &lastUsedDate,
		ExpiryDate:   &expiryDate,
	}
}

// SnapshotProtobufToInstanceArgs converts a migration snapshot record to DB instance record format.
func SnapshotProtobufToInstanceArgs(s *state.State, inst Instance, snap *migration.Snapshot) (*db.InstanceArgs, error) {
	snapConfig := snap.GetLocalConfig()
	config := make(map[string]string, len(snapConfig))
	for _, ent := range snapConfig {
		config[ent.GetKey()] = ent.GetValue()
	}

	snapDevices := snap.GetLocalDevices()
	devices := make(deviceConfig.Devices, len(snapDevices))
	for _, ent := range snap.GetLocalDevices() {
		entConfig := ent.GetConfig()
		props := make(map[string]string, len(entConfig))
		for _, prop := range entConfig {
			props[prop.GetKey()] = prop.GetValue()
		}

		devices[ent.GetName()] = props
	}

	var profiles []api.Profile

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		profiles, err = tx.GetProfiles(ctx, inst.Project().Name, snap.Profiles)

		return err
	})
	if err != nil {
		return nil, err
	}

	args := db.InstanceArgs{
		Architecture: int(snap.GetArchitecture()),
		Config:       config,
		Type:         inst.Type(),
		Snapshot:     true,
		Devices:      devices,
		Ephemeral:    snap.GetEphemeral(),
		Name:         inst.Name() + internalInstance.SnapshotDelimiter + snap.GetName(),
		Profiles:     profiles,
		Stateful:     snap.GetStateful(),
		Project:      inst.Project().Name,
	}

	if snap.GetCreationDate() != 0 {
		args.CreationDate = time.Unix(snap.GetCreationDate(), 0)
	}

	if snap.GetLastUsedDate() != 0 {
		args.LastUsedDate = time.Unix(snap.GetLastUsedDate(), 0)
	}

	if snap.GetExpiryDate() != 0 {
		args.ExpiryDate = time.Unix(snap.GetExpiryDate(), 0)
	}

	return &args, nil
}
