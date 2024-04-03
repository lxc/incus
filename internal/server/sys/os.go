//go:build linux && cgo && !agent

package sys

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lxc/incus/internal/linux"
	"github.com/lxc/incus/internal/server/cgroup"
	"github.com/lxc/incus/internal/server/db/cluster"
	localUtil "github.com/lxc/incus/internal/server/util"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/idmap"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/osarch"
)

// InotifyTargetInfo records the inotify information associated with a given
// inotify target.
type InotifyTargetInfo struct {
	Mask uint32
	Wd   int
	Path string
}

// InotifyInfo records the inotify information associated with a given
// inotify instance.
type InotifyInfo struct {
	Fd int
	sync.RWMutex
	Targets map[string]*InotifyTargetInfo
}

// OS is a high-level facade for accessing operating-system level functionalities.
type OS struct {
	// Directories
	CacheDir string // Cache directory (e.g. /var/cache/incus/).
	LogDir   string // Log directory (e.g. /var/log/incus/).
	RunDir   string // Runtime directory (e.g. /run/incus/).
	VarDir   string // Data directory (e.g. /var/lib/incus/).

	// Daemon environment
	Architectures   []int      // Cache of detected system architectures
	BackingFS       string     // Backing filesystem of $INCUS_DIR/containers
	ExecPath        string     // Absolute path to the daemon
	IdmapSet        *idmap.Set // Information about user/group ID mapping
	InotifyWatch    InotifyInfo
	LxcPath         string // Path to the $INCUS_DIR/containers directory
	MockMode        bool   // If true some APIs will be mocked (for testing)
	Nodev           bool
	RunningInUserNS bool

	// Privilege dropping
	UnprivUser  string
	UnprivUID   uint32
	UnprivGroup string
	UnprivGID   uint32

	// Apparmor features
	AppArmorAdmin     bool
	AppArmorAvailable bool
	AppArmorConfined  bool
	AppArmorStacked   bool
	AppArmorStacking  bool

	// Cgroup features
	CGInfo cgroup.Info

	// Kernel features
	CloseRange              bool // CloseRange indicates support for the close_range syscall.
	ContainerCoreScheduling bool // ContainerCoreScheduling indicates LXC and kernel support for core scheduling.
	CoreScheduling          bool // CoreScheduling indicates support for core scheduling syscalls.
	IdmappedMounts          bool // IdmappedMounts indicates kernel support for VFS idmap.
	NativeTerminals         bool // NativeTerminals indicates support for TIOGPTPEER ioctl.
	NetnsGetifaddrs         bool // NetnsGetifaddrs indicates support for NETLINK_GET_STRICT_CHK.
	PidFds                  bool // PidFds indicates support for PID fds.
	PidFdsThread            bool // PidFds indicates support for thread PID fds.
	PidFdSetns              bool // PidFdSetns indicates support for setns through PID fds.
	SeccompListenerAddfd    bool // SeccompListenerAddfd indicates support for passing new FD to process through seccomp notify.
	SeccompListener         bool // SeccompListener indicates support for seccomp notify.
	SeccompListenerContinue bool // SeccompListenerContinue indicates support continuing syscalls path for process through seccomp notify.
	UeventInjection         bool // UeventInjection indicates support for injecting uevents to a specific netns.
	UnprivBinfmt            bool // UnprivBinfmt indicates support for mounting binfmt_misc inside of a user namespace.
	VFS3Fscaps              bool // VFS3FScaps indicates support for v3 filesystem capacbilities.

	// LXC features
	LXCFeatures map[string]bool

	// OS info
	ReleaseInfo   map[string]string
	KernelVersion version.DottedVersion
	Uname         *linux.Utsname
	BootTime      time.Time
}

// DefaultOS returns a fresh uninitialized OS instance with default values.
func DefaultOS() *OS {
	newOS := &OS{
		CacheDir: internalUtil.CachePath(),
		LogDir:   internalUtil.LogPath(),
		RunDir:   internalUtil.RunPath(),
		VarDir:   internalUtil.VarPath(),
	}

	newOS.InotifyWatch.Fd = -1
	newOS.InotifyWatch.Targets = make(map[string]*InotifyTargetInfo)
	newOS.ReleaseInfo = make(map[string]string)
	return newOS
}

// Init our internal data structures.
func (s *OS) Init() ([]cluster.Warning, error) {
	var dbWarnings []cluster.Warning

	err := s.initDirs()
	if err != nil {
		return nil, err
	}

	s.Architectures, err = localUtil.GetArchitectures()
	if err != nil {
		return nil, err
	}

	s.LxcPath = filepath.Join(s.VarDir, "containers")

	s.BackingFS, err = linux.DetectFilesystem(s.LxcPath)
	if err != nil {
		logger.Error("Error detecting backing fs", logger.Ctx{"err": err})
	}

	// Detect if it is possible to run daemons as an unprivileged user and group.
	for _, userName := range []string{"incus", "nobody"} {
		u, err := user.Lookup(userName)
		if err != nil {
			continue
		}

		uid, err := strconv.ParseUint(u.Uid, 10, 32)
		if err != nil {
			return nil, err
		}

		s.UnprivUser = userName
		s.UnprivUID = uint32(uid)
		break
	}

	for _, groupName := range []string{"incus", "nogroup"} {
		g, err := user.LookupGroup(groupName)
		if err != nil {
			continue
		}

		gid, err := strconv.ParseUint(g.Gid, 10, 32)
		if err != nil {
			return nil, err
		}

		s.UnprivGroup = groupName
		s.UnprivGID = uint32(gid)
		break
	}

	s.IdmapSet = getIdmapset()
	s.ExecPath = localUtil.GetExecPath()
	s.RunningInUserNS = linux.RunningInUserNS()

	dbWarnings = s.initAppArmor()
	cgroup.Init()
	s.CGInfo = cgroup.GetInfo()

	// Fill in the OS release info.
	osInfo, err := osarch.GetLSBRelease()
	if err != nil {
		return nil, err
	}

	s.ReleaseInfo = osInfo

	uname, err := linux.Uname()
	if err != nil {
		return nil, err
	}

	s.Uname = uname

	kernelVersion, err := version.Parse(uname.Release)
	if err == nil {
		s.KernelVersion = *kernelVersion
	}

	// Fill in the boot time.
	out, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil, err
	}

	btime := int64(0)
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "btime ") {
			continue
		}

		fields := strings.Fields(line)
		btime, err = strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return nil, err
		}

		break
	}

	if btime > 0 {
		s.BootTime = time.Unix(btime, 0)
	}

	return dbWarnings, nil
}

// InitStorage initializes the storage layer after it has been mounted.
func (s *OS) InitStorage() error {
	return s.initStorageDirs()
}

// GetUnixSocket returns the full path to the unix.socket file that this daemon is listening on. Used by tests.
func (s *OS) GetUnixSocket() string {
	path := os.Getenv("INCUS_SOCKET")
	if path != "" {
		return path
	}

	return filepath.Join(s.VarDir, "unix.socket")
}

func getIdmapset() *idmap.Set {
	// Try getting the system map.
	idmapset, err := idmap.NewSetFromSystem("", "root")
	if err != nil && err != idmap.ErrSubidUnsupported {
		logger.Error("Unable to parse system idmap", logger.Ctx{"err": err})
		return nil
	}

	if idmapset != nil {
		logger.Info("System idmap (root user):")
		for _, entry := range idmapset.ToLXCString() {
			logger.Infof(" - %s", entry)
		}

		// Only keep the POSIX ranges.
		submap := idmapset.FilterPOSIX()

		if submap == nil {
			logger.Warn("No valid subuid/subgid map, only privileged containers will be functional")
			return nil
		}

		logger.Info("Selected idmap:")
		for _, entry := range submap.ToLXCString() {
			logger.Infof(" - %s", entry)
		}

		return submap
	}

	// Try getting the process map.
	idmapset, err = idmap.NewSetFromCurrentProcess()
	if err != nil {
		logger.Error("Unable to parse process idmap", logger.Ctx{"err": err})
		return nil
	}

	// Swap HostID for NSID and clear NSID (to turn into a usable map).
	for i, entry := range idmapset.Entries {
		idmapset.Entries[i].HostID = entry.NSID
		idmapset.Entries[i].NSID = 0
	}

	logger.Info("Current process idmap:")
	for _, entry := range idmapset.ToLXCString() {
		logger.Infof(" - %s", entry)
	}

	// Try splitting a larger chunk from the current map.
	submap, err := idmapset.Split(65536, 1000000000, 1000000, -1)
	if err != nil && err != idmap.ErrNoSuitableSubmap {
		logger.Error("Unable to split a submap", logger.Ctx{"err": err})
		return nil
	}

	if submap != nil {
		logger.Info("Selected idmap:")
		for _, entry := range submap.ToLXCString() {
			logger.Infof(" - %s", entry)
		}

		return submap
	}

	// Try splitting a smaller chunk from the current map.
	submap, err = idmapset.Split(65536, 1000000000, 65536, -1)
	if err != nil {
		if err == idmap.ErrNoSuitableSubmap {
			logger.Warn("Not enough uid/gid available, only privileged containers will be functional")
			return nil
		}

		logger.Error("Unable to split a submap", logger.Ctx{"err": err})
		return nil
	}

	logger.Info("Selected idmap:")
	for _, entry := range submap.ToLXCString() {
		logger.Infof(" - %s", entry)
	}

	return submap
}
