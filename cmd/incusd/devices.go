package main

/*
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <stdio.h>
#include <linux/hidraw.h>

#include "../../shared/cgo/memory_utils.h"

#ifndef HIDIOCGRAWINFO
#define HIDIOCGRAWINFO _IOR('H', 0x03, struct hidraw_devinfo)
struct hidraw_devinfo {
	__u32 bustype;
	__s16 vendor;
	__s16 product;
};
#endif

static int get_hidraw_devinfo(int fd, struct hidraw_devinfo *info)
{
	int ret;

	ret = ioctl(fd, HIDIOCGRAWINFO, info);
	if (ret)
		return -1;

	return 0;
}

*/
import "C"

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/internal/server/cgroup"
	"github.com/lxc/incus/internal/server/device"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/resources"
	"github.com/lxc/incus/internal/server/state"
	_ "github.com/lxc/incus/shared/cgo" // Used by cgo
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
)

type deviceTaskCPU struct {
	id    int64
	strId string
	count *int
}

type deviceTaskCPUs []deviceTaskCPU

func (c deviceTaskCPUs) Len() int           { return len(c) }
func (c deviceTaskCPUs) Less(i, j int) bool { return *c[i].count < *c[j].count }
func (c deviceTaskCPUs) Swap(i, j int)      { c[i], c[j] = c[j], c[i] }

func deviceNetlinkListener() (chan []string, chan device.USBEvent, chan device.UnixHotplugEvent, error) {
	NETLINK_KOBJECT_UEVENT := 15
	UEVENT_BUFFER_SIZE := 2048

	fd, err := unix.Socket(
		unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC,
		NETLINK_KOBJECT_UEVENT,
	)
	if err != nil {
		return nil, nil, nil, err
	}

	nl := unix.SockaddrNetlink{
		Family: unix.AF_NETLINK,
		Pid:    uint32(os.Getpid()),
		Groups: 3,
	}

	err = unix.Bind(fd, &nl)
	if err != nil {
		return nil, nil, nil, err
	}

	chCPU := make(chan []string, 1)
	chUSB := make(chan device.USBEvent)
	chUnix := make(chan device.UnixHotplugEvent)

	go func(chCPU chan []string, chUSB chan device.USBEvent, chUnix chan device.UnixHotplugEvent) {
		b := make([]byte, UEVENT_BUFFER_SIZE*2)
		for {
			r, err := unix.Read(fd, b)
			if err != nil {
				continue
			}

			ueventBuf := make([]byte, r)
			copy(ueventBuf, b)

			udevEvent := false
			if strings.HasPrefix(string(ueventBuf), "libudev") {
				udevEvent = true
				// Skip the header that libudev prepends
				ueventBuf = ueventBuf[40 : len(ueventBuf)-1]
			}

			ueventLen := 0
			ueventParts := strings.Split(string(ueventBuf), "\x00")
			for i, part := range ueventParts {
				if strings.HasPrefix(part, "SEQNUM=") {
					ueventParts = append(ueventParts[:i], ueventParts[i+1:]...)
					break
				}
			}

			props := map[string]string{}
			for _, part := range ueventParts {
				// libudev string prefix distinguishes udev events from kernel uevents
				if strings.HasPrefix(part, "libudev") {
					udevEvent = true
					continue
				}

				ueventLen += len(part) + 1

				fields := strings.SplitN(part, "=", 2)
				if len(fields) != 2 {
					continue
				}

				props[fields[0]] = fields[1]
			}

			ueventLen--

			if udevEvent {
				// The kernel always prepends this and udev expects it.
				kernelPrefix := fmt.Sprintf("%s@%s", props["ACTION"], props["DEVPATH"])
				ueventParts = append([]string{kernelPrefix}, ueventParts...)
				ueventLen += len(kernelPrefix)
			}

			if props["SUBSYSTEM"] == "cpu" && !udevEvent {
				if props["DRIVER"] != "processor" {
					continue
				}

				if props["ACTION"] != "offline" && props["ACTION"] != "online" {
					continue
				}

				// As CPU re-balancing affects all containers, no need to queue them
				select {
				case chCPU <- []string{path.Base(props["DEVPATH"]), props["ACTION"]}:
				default:
					// Channel is full, drop the event
				}
			}

			if props["SUBSYSTEM"] == "net" && !udevEvent {
				if props["ACTION"] != "add" && props["ACTION"] != "removed" {
					continue
				}

				if !util.PathExists(fmt.Sprintf("/sys/class/net/%s", props["INTERFACE"])) {
					continue
				}
			}

			if props["SUBSYSTEM"] == "usb" && !udevEvent {
				parts := strings.Split(props["PRODUCT"], "/")
				if len(parts) < 2 {
					continue
				}

				major, ok := props["MAJOR"]
				if !ok {
					continue
				}

				minor, ok := props["MINOR"]
				if !ok {
					continue
				}

				devname, ok := props["DEVNAME"]
				if !ok {
					continue
				}

				busnum, ok := props["BUSNUM"]
				if !ok {
					continue
				}

				devnum, ok := props["DEVNUM"]
				if !ok {
					continue
				}

				zeroPad := func(s string, l int) string {
					return strings.Repeat("0", l-len(s)) + s
				}

				usb, err := device.USBNewEvent(
					props["ACTION"],
					/* udev doesn't zero pad these, while
					 * everything else does, so let's zero pad them
					 * for consistency
					 */
					zeroPad(parts[0], 4),
					zeroPad(parts[1], 4),
					major,
					minor,
					busnum,
					devnum,
					devname,
					ueventParts[:len(ueventParts)-1],
					ueventLen,
				)
				if err != nil {
					logger.Error("Error reading usb device", logger.Ctx{"err": err, "path": props["PHYSDEVPATH"]})
					continue
				}

				chUSB <- usb
			}

			// unix hotplug device events rely on information added by udev
			if udevEvent {
				action := props["ACTION"]
				if action != "add" && action != "remove" {
					continue
				}

				subsystem, ok := props["SUBSYSTEM"]
				if !ok {
					continue
				}

				devname, ok := props["DEVNAME"]
				if !ok {
					continue
				}

				major, ok := props["MAJOR"]
				if !ok {
					continue
				}

				minor, ok := props["MINOR"]
				if !ok {
					continue
				}

				vendor := ""
				product := ""
				if action == "add" {
					vendor, product, ok = ueventParseVendorProduct(props, subsystem, devname)
					if !ok {
						continue
					}
				}

				zeroPad := func(s string, l int) string {
					return strings.Repeat("0", l-len(s)) + s
				}

				// zeropad
				if len(vendor) < 4 {
					vendor = zeroPad(vendor, 4)
				}

				if len(product) < 4 {
					product = zeroPad(product, 4)
				}

				unix, err := device.UnixHotplugNewEvent(
					action,
					/* udev doesn't zero pad these, while
					 * everything else does, so let's zero pad them
					 * for consistency
					 */
					vendor,
					product,
					major,
					minor,
					subsystem,
					devname,
					ueventParts[:len(ueventParts)-1],
					ueventLen,
				)
				if err != nil {
					logger.Error("Error reading unix device", logger.Ctx{"err": err, "path": props["PHYSDEVPATH"]})
					continue
				}

				chUnix <- unix
			}
		}
	}(chCPU, chUSB, chUnix)

	return chCPU, chUSB, chUnix, nil
}

/*
 * fillFixedInstances fills the `fixedInstances` map with the instances that have been pinned to specific CPUs.
 * The `fixedInstances` map is a map of CPU IDs to a list of instances that have been pinned to that CPU.
 * The `targetCpuPool` is a list of CPU IDs that are available for pinning.
 * The `targetCpuNum` is the number of CPUs that are required for pinning.
 * The `loadBalancing` flag indicates whether the CPU pinning should be load balanced or not (e.g, NUMA placement when `limits.cpu` is a single number which means
 * a required number of vCPUs per instance that can be chosen within a CPU pool).
 */
func fillFixedInstances(fixedInstances map[int64][]instance.Instance, inst instance.Instance, effectiveCpus []int64, targetCpuPool []int64, targetCpuNum int, loadBalancing bool) {
	if len(targetCpuPool) < targetCpuNum {
		diffCount := len(targetCpuPool) - targetCpuNum
		logger.Warnf("%v CPUs have been required for pinning, but %v CPUs won't be allocated", len(targetCpuPool), -diffCount)
		targetCpuNum = len(targetCpuPool)
	}

	// If the `targetCpuPool` has been manually specified (explicit CPU IDs/ranges specified with `limits.cpu`)
	if len(targetCpuPool) == targetCpuNum && !loadBalancing {
		for _, nr := range targetCpuPool {
			if !util.ValueInSlice(nr, effectiveCpus) {
				continue
			}

			_, ok := fixedInstances[nr]
			if ok {
				fixedInstances[nr] = append(fixedInstances[nr], inst)
			} else {
				fixedInstances[nr] = []instance.Instance{inst}
			}
		}

		return
	}

	// If we need to load-balance the instance across the CPUs of `targetCpuPool` (e.g, NUMA placement),
	// the heuristic is to sort the `targetCpuPool` by usage (number of instances already pinned to each CPU)
	// and then assign the instance to the first `desiredCpuNum` least used CPUs.
	usage := map[int64]deviceTaskCPU{}
	for _, id := range targetCpuPool {
		cpu := deviceTaskCPU{}
		cpu.id = id
		cpu.strId = fmt.Sprintf("%d", id)

		count := 0
		_, ok := fixedInstances[id]
		if ok {
			count = len(fixedInstances[id])
		}

		cpu.count = &count
		usage[id] = cpu
	}

	sortedUsage := make(deviceTaskCPUs, 0)
	for _, value := range usage {
		sortedUsage = append(sortedUsage, value)
	}

	sort.Sort(sortedUsage)
	count := 0
	for _, cpu := range sortedUsage {
		if count == targetCpuNum {
			break
		}

		id := cpu.id
		_, ok := fixedInstances[id]
		if ok {
			fixedInstances[id] = append(fixedInstances[id], inst)
		} else {
			fixedInstances[id] = []instance.Instance{inst}
		}

		count++
	}
}

// deviceTaskBalance is used to balance the CPU load across containers running on a host.
// It first checks if CGroup support is available and returns if it isn't.
// It then retrieves the effective CPU list (the CPUs that are guaranteed to be online) and isolates any isolated CPUs.
// After that, it loads all instances of containers running on the node and iterates through them.
//
// For each container, it checks its CPU limits and determines whether it is pinned to specific CPUs or can use the load-balancing mechanism.
// If it is pinned, the function adds it to the fixedInstances map with the CPU numbers it is pinned to.
// If not, the container will be included in the load-balancing calculation,
// and the number of CPUs it can use is determined by taking the minimum of its assigned CPUs and the available CPUs. Note that if
// NUMA placement is enabled (`limits.cpu.nodes` is not empty), we apply a similar load-balancing logic to the `fixedInstances` map
// with a constraint being the number of vCPUs and the CPU pool being the CPUs pinned to a set of NUMA nodes.
//
// Next, the function balance the CPU usage by iterating over all the CPUs and dividing the containers into those that
// are pinned to a specific CPU and those that are load-balanced. For the pinned containers,
// it adds them to the pinning map with the CPU number it's pinned to.
// For the load-balanced containers, it sorts the available CPUs based on their usage count and assigns them to containers
// in ascending order until the required number of CPUs have been assigned.
// Finally, the pinning map is used to set the new CPU pinning for each container, updating it to the new balanced state.
//
// Overall, this function ensures that the CPU resources of the host are utilized effectively amongst all the containers running on it.
func deviceTaskBalance(s *state.State) {
	min := func(x, y int) int {
		if x < y {
			return x
		}

		return y
	}

	// Don't bother running when CGroup support isn't there
	if !s.OS.CGInfo.Supports(cgroup.CPUSet, nil) {
		return
	}

	// Get effective cpus list - those are all guaranteed to be online
	cg, err := cgroup.NewFileReadWriter(1, true)
	if err != nil {
		logger.Errorf("Unable to load cgroup writer: %v", err)
		return
	}

	effectiveCpus, err := cg.GetEffectiveCpuset()
	if err != nil {
		// Older kernel - use cpuset.cpus
		effectiveCpus, err = cg.GetCpuset()
		if err != nil {
			logger.Errorf("Error reading host's cpuset.cpus")
			return
		}
	}

	effectiveCpusInt, err := resources.ParseCpuset(effectiveCpus)
	if err != nil {
		logger.Errorf("Error parsing effective CPU set")
		return
	}

	isolatedCpusInt := resources.GetCPUIsolated()
	effectiveCpusSlice := []string{}
	for _, id := range effectiveCpusInt {
		if util.ValueInSlice(id, isolatedCpusInt) {
			continue
		}

		effectiveCpusSlice = append(effectiveCpusSlice, fmt.Sprintf("%d", id))
	}

	effectiveCpus = strings.Join(effectiveCpusSlice, ",")
	cpus, err := resources.ParseCpuset(effectiveCpus)
	if err != nil {
		logger.Error("Error parsing host's cpu set", logger.Ctx{"cpuset": effectiveCpus, "err": err})
		return
	}

	// Iterate through the instances
	instances, err := instance.LoadNodeAll(s, instancetype.Container)
	if err != nil {
		logger.Error("Problem loading instances list", logger.Ctx{"err": err})
		return
	}

	// Get CPU topology.
	cpusTopology, err := resources.GetCPU()
	if err != nil {
		logger.Errorf("Unable to load system CPUs information: %v", err)
		return
	}

	// Build a map of NUMA node to CPU threads.
	numaNodeToCPU := make(map[int64][]int64)
	for _, cpu := range cpusTopology.Sockets {
		for _, core := range cpu.Cores {
			for _, thread := range core.Threads {
				numaNodeToCPU[int64(thread.NUMANode)] = append(numaNodeToCPU[int64(thread.NUMANode)], thread.ID)
			}
		}
	}

	fixedInstances := map[int64][]instance.Instance{}
	balancedInstances := map[instance.Instance]int{}
	for _, c := range instances {
		conf := c.ExpandedConfig()
		cpuNodes := conf["limits.cpu.nodes"]
		var numaCpus []int64
		if cpuNodes != "" {
			numaNodeSet, err := resources.ParseNumaNodeSet(cpuNodes)
			if err != nil {
				logger.Error("Error parsing numa node set", logger.Ctx{"numaNodes": cpuNodes, "err": err})
				return
			}

			for _, numaNode := range numaNodeSet {
				numaCpus = append(numaCpus, numaNodeToCPU[numaNode]...)
			}
		}

		cpulimit, ok := conf["limits.cpu"]
		if !ok || cpulimit == "" {
			cpulimit = effectiveCpus
		}

		// Check that the container is running.
		// We use InitPID here rather than IsRunning because this task is triggered during the container's
		// onStart hook, which is during the time that the start lock is held, which causes IsRunning to
		// return false (because the container hasn't fully started yet) but it is sufficiently started to
		// have its cgroup CPU limits set.
		if c.InitPID() <= 0 {
			continue
		}

		count, err := strconv.Atoi(cpulimit)
		if err == nil {
			// Load-balance
			count = min(count, len(cpus))
			if len(numaCpus) > 0 {
				fillFixedInstances(fixedInstances, c, cpus, numaCpus, count, true)
			} else {
				balancedInstances[c] = count
			}
		} else {
			// Pinned
			containerCpus, err := resources.ParseCpuset(cpulimit)
			if err != nil {
				return
			}

			if len(numaCpus) > 0 {
				logger.Warnf("The pinned CPUs: %v, override the NUMA configuration with the CPUs: %v", containerCpus, numaCpus)
			}

			fillFixedInstances(fixedInstances, c, cpus, containerCpus, len(containerCpus), false)
		}
	}

	// Balance things
	pinning := map[instance.Instance][]string{}
	usage := map[int64]deviceTaskCPU{}

	for _, id := range cpus {
		cpu := deviceTaskCPU{}
		cpu.id = id
		cpu.strId = fmt.Sprintf("%d", id)
		count := 0
		cpu.count = &count

		usage[id] = cpu
	}

	for cpu, ctns := range fixedInstances {
		c, ok := usage[cpu]
		if !ok {
			logger.Errorf("Internal error: container using unavailable cpu")
			continue
		}

		id := c.strId
		for _, ctn := range ctns {
			_, ok := pinning[ctn]
			if ok {
				pinning[ctn] = append(pinning[ctn], id)
			} else {
				pinning[ctn] = []string{id}
			}
			*c.count += 1
		}
	}

	sortedUsage := make(deviceTaskCPUs, 0)
	for _, value := range usage {
		sortedUsage = append(sortedUsage, value)
	}

	for ctn, count := range balancedInstances {
		sort.Sort(sortedUsage)
		for _, cpu := range sortedUsage {
			if count == 0 {
				break
			}

			count -= 1

			id := cpu.strId
			_, ok := pinning[ctn]
			if ok {
				pinning[ctn] = append(pinning[ctn], id)
			} else {
				pinning[ctn] = []string{id}
			}
			*cpu.count += 1
		}
	}

	// Set the new pinning
	for ctn, set := range pinning {
		// Confirm the container didn't just stop
		if ctn.InitPID() <= 0 {
			continue
		}

		sort.Strings(set)
		cg, err := ctn.CGroup()
		if err != nil {
			logger.Error("balance: Unable to get cgroup struct", logger.Ctx{"name": ctn.Name(), "err": err, "value": strings.Join(set, ",")})
			continue
		}

		err = cg.SetCpuset(strings.Join(set, ","))
		if err != nil {
			logger.Error("balance: Unable to set cpuset", logger.Ctx{"name": ctn.Name(), "err": err, "value": strings.Join(set, ",")})
		}
	}
}

// deviceEventListener starts the event listener for resource scheduling.
// Accepts stateFunc which will be called each time it needs a fresh state.State.
func deviceEventListener(stateFunc func() *state.State) {
	chNetlinkCPU, chUSB, chUnix, err := deviceNetlinkListener()
	if err != nil {
		logger.Errorf("scheduler: Couldn't setup netlink listener: %v", err)
		return
	}

	for {
		select {
		case e := <-chNetlinkCPU:
			if len(e) != 2 {
				logger.Errorf("Scheduler: received an invalid cpu hotplug event")
				continue
			}

			s := stateFunc()

			if !s.OS.CGInfo.Supports(cgroup.CPUSet, nil) {
				continue
			}

			logger.Debugf("Scheduler: cpu: %s is now %s: re-balancing", e[0], e[1])
			deviceTaskBalance(s)

		case e := <-chUSB:
			device.USBRunHandlers(stateFunc(), &e)
		case e := <-chUnix:
			device.UnixHotplugRunHandlers(stateFunc(), &e)
		case e := <-cgroup.DeviceSchedRebalance:
			if len(e) != 3 {
				logger.Errorf("Scheduler: received an invalid rebalance event")
				continue
			}

			s := stateFunc()

			if !s.OS.CGInfo.Supports(cgroup.CPUSet, nil) {
				continue
			}

			logger.Debugf("Scheduler: %s %s %s: re-balancing", e[0], e[1], e[2])
			deviceTaskBalance(s)
		}
	}
}

// devicesRegister calls the Register() function on all supported devices so they receive events.
// This also has the effect of actively reconnecting to any running VM monitor sockets.
func devicesRegister(instances []instance.Instance) {
	logger.Debug("Registering running instances")

	for _, inst := range instances {
		if !inst.IsRunning() { // For VMs this will also trigger a connection to the QMP socket if running.
			continue
		}

		inst.RegisterDevices()
	}
}

func getHidrawDevInfo(fd int) (string, string, error) {
	info := C.struct_hidraw_devinfo{}
	ret, err := C.get_hidraw_devinfo(C.int(fd), &info)
	if ret != 0 {
		return "", "", err
	}

	return fmt.Sprintf("%04x", info.vendor), fmt.Sprintf("%04x", info.product), nil
}

func ueventParseVendorProduct(props map[string]string, subsystem string, devname string) (string, string, bool) {
	vendor, vendorOk := props["ID_VENDOR_ID"]
	product, productOk := props["ID_MODEL_ID"]

	if vendorOk && productOk {
		return vendor, product, true
	}

	if subsystem != "hidraw" {
		return "", "", false
	}

	if !filepath.IsAbs(devname) {
		return "", "", false
	}

	file, err := os.OpenFile(devname, os.O_RDWR, 0000)
	if err != nil {
		return "", "", false
	}

	defer func() { _ = file.Close() }()

	vendor, product, err = getHidrawDevInfo(int(file.Fd()))
	if err != nil {
		logger.Debugf("Failed to retrieve device info from hidraw device \"%s\"", devname)
		return "", "", false
	}

	return vendor, product, true
}
