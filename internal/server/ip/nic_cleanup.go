package ip

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"github.com/google/uuid"
	"github.com/vishvananda/netlink"
)

// NICLinkCleanup identifies a host allocation in its original kernel namespace.
// Participating NIC writers never move a host end into another namespace.
// Privileged external writers must not forge allocation aliases/MACs or race RTNL effects.
type NICLinkCleanup struct {
	Version         int
	BootID          string
	NamespaceDevice uint64
	NamespaceInode  uint64
	Index           int
	Name            string
	Kind            string
	HardwareAddr    string
	Alias           string
}

var nicLinkMutex sync.Mutex

type nicLinkCleanupOps struct {
	root    func() (string, uint64, uint64, error)
	byName  func(string) (netlink.Link, error)
	byIndex func(int) (netlink.Link, error)
	list    func() ([]netlink.Link, error)
	alias   func(netlink.Link, string) error
	remove  func(netlink.Link) error
}

// CurrentBootID returns the running kernel's boot identity.
func CurrentBootID() (string, error) {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(boot)), nil
}

// NICClaimFromEarlierBoot reports whether a recorded host-link claim belongs to a positively
// different kernel boot. A reboot has already discarded that boot's links, aliases and VF settings.
func NICClaimFromEarlierBoot(p NICLinkCleanup) (bool, error) {
	if p.BootID == "" {
		return false, nil
	}

	boot, err := CurrentBootID()
	if err != nil {
		return false, err
	}

	if boot == p.BootID {
		return false, nil
	}

	id, err := uuid.Parse(boot)
	if err != nil || id == uuid.Nil || id.String() != boot {
		return false, errors.New("Current kernel boot identity is invalid")
	}

	return true, nil
}

func nicLinkRoot() (string, uint64, uint64, error) {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", 0, 0, err
	}

	var stat syscall.Stat_t
	err = syscall.Stat("/proc/thread-self/ns/net", &stat)
	return strings.TrimSpace(string(boot)), uint64(stat.Dev), stat.Ino, err
}

func nicLinkOps() nicLinkCleanupOps {
	return nicLinkCleanupOps{root: nicLinkRoot, byName: netlink.LinkByName, byIndex: netlink.LinkByIndex, list: netlink.LinkList, alias: netlink.LinkSetAlias, remove: netlink.LinkDel}
}

// Validate checks the recorded original host-link identity.
func (p NICLinkCleanup) Validate() error {
	id, err := uuid.Parse(p.BootID)
	if err != nil || id == uuid.Nil || id.String() != p.BootID || p.Version != 1 || p.NamespaceInode == 0 || p.Index <= 0 || p.Name == "" || p.Kind == "" || p.HardwareAddr == "" {
		return errors.New("Original NIC kernel identity is incomplete")
	}

	generation := strings.TrimPrefix(p.Alias, "incus-ovn-")
	id, err = uuid.Parse(generation)
	if err != nil || id == uuid.Nil || id.String() != generation || p.Alias != "incus-ovn-"+generation {
		return errors.New("Original NIC kernel allocation generation is invalid")
	}

	return nil
}

// CaptureNICLinkCleanup claims only the actual present virtual host allocation.
// Legacy adoption requires the caller's rooted OVS/device association first.
func CaptureNICLinkCleanup(name string, kind string, expected *NICLinkCleanup) (NICLinkCleanup, error) {
	nicLinkMutex.Lock()
	defer nicLinkMutex.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return captureNICLinkCleanup(nicLinkOps(), name, kind, expected)
}

func captureNICLinkCleanup(ops nicLinkCleanupOps, name string, kind string, expected *NICLinkCleanup) (NICLinkCleanup, error) {
	boot, dev, ino, err := ops.root()
	if err != nil {
		return NICLinkCleanup{}, err
	}

	link, err := ops.byName(name)
	if err != nil {
		return NICLinkCleanup{}, err
	}

	if link.Type() != kind || kind == "" {
		return NICLinkCleanup{}, errors.New("Original NIC virtual interface type changed")
	}

	attrs := link.Attrs()
	plan := NICLinkCleanup{Version: 1, BootID: boot, NamespaceDevice: dev, NamespaceInode: ino, Index: attrs.Index, Name: attrs.Name, Kind: link.Type(), HardwareAddr: attrs.HardwareAddr.String(), Alias: attrs.Alias}
	if expected != nil {
		if expected.Index == 0 {
			err = expected.validateIntent()
			if err != nil {
				return NICLinkCleanup{}, err
			}

			if expected.BootID != plan.BootID || expected.NamespaceDevice != plan.NamespaceDevice || expected.NamespaceInode != plan.NamespaceInode || expected.Name != plan.Name || expected.Kind != plan.Kind || (expected.HardwareAddr != "" && expected.HardwareAddr != plan.HardwareAddr) {
				return NICLinkCleanup{}, errors.New("Original virtual intent does not own the present allocation")
			}

			if expected.Alias != plan.Alias {
				if plan.Alias != "" || expected.HardwareAddr == "" {
					return NICLinkCleanup{}, errors.New("Original virtual intent does not own the present allocation")
				}
				// Veth create ignores alias; its persisted unpredictable host MAC binds this allocation.
				prior := plan
				plan.Alias = expected.Alias
				err = plan.Validate()
				if err != nil {
					return NICLinkCleanup{}, err
				}

				current, err := ops.byIndex(plan.Index)
				if err != nil || !prior.matches(current) {
					return NICLinkCleanup{}, errors.Join(err, errors.New("Original virtual intent changed before alias publication"))
				}

				aliasErr := ops.alias(current, plan.Alias)
				current, err = ops.byIndex(plan.Index)
				if err != nil || !plan.matches(current) {
					return NICLinkCleanup{}, errors.Join(aliasErr, err, errors.New("Original virtual intent alias was not acknowledged"))
				}
			}

			return plan, plan.Validate()
		}

		if *expected != plan {
			return NICLinkCleanup{}, errors.New("Original NIC kernel allocation changed")
		}

		return plan, plan.Validate()
	}

	if attrs.Alias != "" {
		return NICLinkCleanup{}, errors.New("Cannot adopt a NIC with an existing allocation alias")
	}

	plan.Alias = "incus-ovn-" + uuid.NewString()
	err = plan.Validate()
	if err != nil {
		return NICLinkCleanup{}, err
	}

	err = ops.alias(link, plan.Alias)
	if err != nil {
		return NICLinkCleanup{}, err
	}

	link, err = ops.byIndex(plan.Index)
	if err != nil {
		return NICLinkCleanup{}, err
	}

	if !plan.matches(link) {
		return NICLinkCleanup{}, errors.New("NIC allocation changed while claiming kernel identity")
	}

	return plan, nil
}

func (p NICLinkCleanup) matches(link netlink.Link) bool {
	a := link.Attrs()
	return a.Index == p.Index && a.Name == p.Name && a.Alias == p.Alias && link.Type() == p.Kind && a.HardwareAddr.String() == p.HardwareAddr
}

// ApplyNICLinkCleanup deletes by original numeric identity and verifies absence.
// A lost delete reply is reconciled by querying the original namespace/alias.
func ApplyNICLinkCleanup(plan NICLinkCleanup) error {
	nicLinkMutex.Lock()
	defer nicLinkMutex.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return applyNICLinkCleanup(nicLinkOps(), plan)
}

// NICLinkAbsent confirms that neither the original alias nor its numeric identity remains.
func NICLinkAbsent(plan NICLinkCleanup) error {
	nicLinkMutex.Lock()
	defer nicLinkMutex.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return nicLinkAbsent(nicLinkOps(), plan)
}

// VerifyNICVirtualLinkCleanup validates the original allocation or its guarded absence.
func VerifyNICVirtualLinkCleanup(plan NICLinkCleanup) error {
	nicLinkMutex.Lock()
	defer nicLinkMutex.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	_, err := verifyNICVirtualLinkCleanup(nicLinkOps(), plan)
	return err
}

func applyNICLinkCleanup(ops nicLinkCleanupOps, p NICLinkCleanup) error {
	if p.Index == 0 {
		return applyNICLinkIntent(ops, p)
	}

	link, err := verifyNICVirtualLinkCleanup(ops, p)
	if err != nil || link == nil {
		return err
	}

	deleteErr := ops.remove(link)
	verifyErr := nicLinkAbsent(ops, p)
	if verifyErr == nil {
		return nil
	}

	return errors.Join(deleteErr, fmt.Errorf("Original NIC host cleanup was not acknowledged: %w", verifyErr))
}

func verifyNICVirtualLinkCleanup(ops nicLinkCleanupOps, p NICLinkCleanup) (netlink.Link, error) {
	if p.Kind != "veth" && p.Kind != "tuntap" {
		return nil, errors.New("Cannot delete a physical allocation as a virtual NIC")
	}

	err := p.Validate()
	if err != nil {
		return nil, err
	}

	boot, dev, ino, err := ops.root()
	if err != nil {
		return nil, err
	}

	if boot != p.BootID {
		// A positively different kernel boot cannot retain a veth/tap allocation.
		// Preserve all current-boot replacements without issuing a destructive effect.
		id, err := uuid.Parse(boot)
		if err != nil || id == uuid.Nil || id.String() != boot {
			return nil, errors.New("Current kernel boot identity is invalid")
		}

		return nil, nil
	}

	if dev != p.NamespaceDevice || ino != p.NamespaceInode {
		return nil, errors.New("Original NIC kernel namespace changed within the same boot")
	}

	link, err := ops.byIndex(p.Index)
	if err != nil {
		var absent netlink.LinkNotFoundError
		if !errors.As(err, &absent) {
			return nil, err
		}

		return nil, nicLinkAbsent(ops, p)
	}

	if !p.matches(link) {
		return nil, errors.New("Original NIC kernel allocation replaced or changed; preserving current interface")
	}

	return link, nil
}

func nicLinkAbsent(ops nicLinkCleanupOps, p NICLinkCleanup) error {
	boot, _, _, err := ops.root()
	if err != nil {
		return err
	}

	if boot != p.BootID {
		// A positively different kernel boot cannot retain a veth/tap allocation; its numeric
		// identities are routinely reused after a reboot.
		id, err := uuid.Parse(boot)
		if err != nil || id == uuid.Nil || id.String() != boot {
			return errors.New("Current kernel boot identity is invalid")
		}

		return nil
	}

	links, err := ops.list()
	if err != nil {
		return err
	}

	for _, link := range links {
		if link.Attrs().Alias == p.Alias {
			return errors.New("Original NIC allocation remains in the source namespace")
		}

		if link.Attrs().Index == p.Index {
			return errors.New("Original NIC numeric identity is still present or reused")
		}
	}

	return nil
}

// VerifyNICLinkCleanup proves a still-present original physical representor.
func VerifyNICLinkCleanup(p NICLinkCleanup) error {
	nicLinkMutex.Lock()
	defer nicLinkMutex.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ops := nicLinkOps()
	err := p.Validate()
	if err != nil {
		return err
	}

	boot, dev, ino, err := ops.root()
	if err != nil {
		return err
	}

	if boot != p.BootID || dev != p.NamespaceDevice || ino != p.NamespaceInode {
		return errors.New("Original physical NIC boot/namespace changed")
	}

	link, err := ops.byIndex(p.Index)
	if err != nil {
		return err
	}

	if !p.matches(link) {
		return errors.New("Original physical NIC allocation changed")
	}

	return nil
}

// CaptureNICPhysicalLinkCleanup claims a verified free VF's representor.
// A nonempty alias may be replaced only after an exact completed receipt check.
func CaptureNICPhysicalLinkCleanup(name string, expected *NICLinkCleanup, reuse bool) (NICLinkCleanup, error) {
	nicLinkMutex.Lock()
	defer nicLinkMutex.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ops := nicLinkOps()
	link, err := ops.byName(name)
	if err != nil {
		return NICLinkCleanup{}, err
	}

	if expected == nil && link.Attrs().Alias != "" && reuse {
		prior := link.Attrs().Alias
		ops.alias = func(link netlink.Link, alias string) error {
			current, err := netlink.LinkByIndex(link.Attrs().Index)
			if err != nil {
				return err
			}

			if current.Attrs().Alias != prior {
				return errors.New("Original physical allocation changed before reuse")
			}

			return netlink.LinkSetAlias(link, alias)
		}

		ops.byName = func(name string) (netlink.Link, error) {
			current, err := netlink.LinkByName(name)
			if err != nil {
				return nil, err
			}

			if current.Attrs().Alias != prior {
				return nil, errors.New("Original physical allocation changed before reuse")
			}

			current.Attrs().Alias = ""
			return current, nil
		}
	}

	return captureNICLinkCleanup(ops, name, link.Type(), expected)
}

// ReleaseNICPhysicalLinkCleanup clears only an unpublished original marker.
// Callers must positively restore the original VF before releasing this claim.
func ReleaseNICPhysicalLinkCleanup(p NICLinkCleanup) error {
	nicLinkMutex.Lock()
	defer nicLinkMutex.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return releaseNICPhysicalLinkCleanup(nicLinkOps(), p)
}

func releaseNICPhysicalLinkCleanup(ops nicLinkCleanupOps, p NICLinkCleanup) error {
	err := p.Validate()
	if err != nil {
		return err
	}

	boot, dev, ino, err := ops.root()
	if err != nil {
		return err
	}

	if boot != p.BootID || dev != p.NamespaceDevice || ino != p.NamespaceInode {
		return errors.New("Original physical marker root changed")
	}

	link, err := ops.byIndex(p.Index)
	if err != nil {
		return err
	}

	if !p.matches(link) {
		return errors.New("Original physical marker changed before release")
	}

	clearErr := ops.alias(link, "")
	current, err := ops.byIndex(p.Index)
	if err != nil {
		return errors.Join(clearErr, err)
	}

	p.Alias = ""
	if !p.matches(current) {
		return errors.Join(clearErr, errors.New("Original physical marker release was not acknowledged"))
	}

	return nil
}

func (p NICLinkCleanup) validateIntent() error {
	if p.Index != 0 || (p.Kind != "veth" && p.Kind != "tuntap") {
		return errors.New("Virtual NIC allocation intent is invalid")
	}

	if p.HardwareAddr != "" {
		address, err := net.ParseMAC(p.HardwareAddr)
		if err != nil || p.Kind != "veth" || len(address) != 6 || address[0]&3 != 2 || address.String() != p.HardwareAddr {
			return errors.New("Virtual NIC intent host MAC is invalid")
		}
	}

	check := p
	check.Index = 1
	check.HardwareAddr = "intent"
	return check.Validate()
}

// NewNICLinkCleanupIntent persists an original generation before exclusive creation.
func NewNICLinkCleanupIntent(name, kind string) (NICLinkCleanup, error) {
	nicLinkMutex.Lock()
	defer nicLinkMutex.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	boot, dev, ino, err := nicLinkRoot()
	if err != nil {
		return NICLinkCleanup{}, err
	}

	p := NICLinkCleanup{Version: 1, BootID: boot, NamespaceDevice: dev, NamespaceInode: ino, Name: name, Kind: kind, Alias: "incus-ovn-" + uuid.NewString()}
	if kind == "veth" {
		address := make(net.HardwareAddr, 6)
		_, err = rand.Read(address)
		if err != nil {
			return NICLinkCleanup{}, err
		}

		address[0] = (address[0] & 0xfc) | 2
		p.HardwareAddr = address.String()
	}

	return p, p.validateIntent()
}

// CreateNICLinkCleanup serializes exclusive creation and captures its own marker.
func CreateNICLinkCleanup(intent NICLinkCleanup, create func() error) (NICLinkCleanup, error) {
	nicLinkMutex.Lock()
	defer nicLinkMutex.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return createNICLinkCleanup(nicLinkOps(), intent, create)
}

func createNICLinkCleanup(ops nicLinkCleanupOps, intent NICLinkCleanup, create func() error) (NICLinkCleanup, error) {
	err := intent.validateIntent()
	if err != nil {
		return intent, err
	}

	boot, dev, ino, err := ops.root()
	if err != nil {
		return intent, err
	}

	if boot != intent.BootID || dev != intent.NamespaceDevice || ino != intent.NamespaceInode {
		return intent, errors.New("Virtual NIC creation intent root changed")
	}

	_, err = ops.byName(intent.Name)
	var absent netlink.LinkNotFoundError
	if !errors.As(err, &absent) {
		return intent, errors.Join(err, errors.New("Virtual NIC creation name is already occupied or unproven"))
	}

	createErr := create()
	plan, captureErr := captureNICLinkCleanup(ops, intent.Name, intent.Kind, &intent)
	if captureErr != nil {
		return intent, errors.Join(createErr, captureErr)
	}

	return plan, createErr
}

func applyNICLinkIntent(ops nicLinkCleanupOps, p NICLinkCleanup) error {
	err := p.validateIntent()
	if err != nil {
		return err
	}

	boot, dev, ino, err := ops.root()
	if err != nil {
		return err
	}

	if boot != p.BootID {
		id, err := uuid.Parse(boot)
		if err != nil || id == uuid.Nil || id.String() != boot {
			return errors.New("Current virtual intent boot identity is invalid")
		}

		return nil
	}

	if dev != p.NamespaceDevice || ino != p.NamespaceInode {
		return errors.New("Original virtual intent namespace changed")
	}

	link, err := ops.byName(p.Name)
	var absent netlink.LinkNotFoundError
	if errors.As(err, &absent) {
		links, listErr := ops.list()
		if listErr != nil {
			return listErr
		}

		for _, link := range links {
			if link.Attrs().Alias == p.Alias {
				return errors.New("Original virtual intent allocation was moved or renamed")
			}
		}

		return nil
	}

	if err != nil {
		return err
	}

	if link.Attrs().Alias != p.Alias && (link.Attrs().Alias != "" || p.HardwareAddr == "") {
		return errors.New("Virtual intent creation is ambiguous or replaced; preserving current interface")
	}

	plan, err := captureNICLinkCleanup(ops, p.Name, p.Kind, &p)
	if err != nil {
		return err
	}

	return applyNICLinkCleanup(ops, plan)
}
