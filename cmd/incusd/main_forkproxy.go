package main

/*
#include "config.h"

#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <sched.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/epoll.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

#include "incus.h"
#include "macro.h"
#include "memory_utils.h"
#include "process_utils.h"

int whoami = -ESRCH;

#define FORKPROXY_CHILD 1
#define FORKPROXY_PARENT 0
#define FORKPROXY_UDS_SOCK_FD_NUM 200

static int switch_uid_gid(uint32_t uid, uint32_t gid)
{
	if (setgid((gid_t)gid) < 0)
		return -1;

	if (setuid((uid_t)uid) < 0)
		return -1;

	return 0;
}

static int lxc_epoll_wait_nointr(int epfd, struct epoll_event* events,
				 int maxevents, int timeout)
{
	int ret;
again:
	ret = epoll_wait(epfd, events, maxevents, timeout);
	if (ret < 0 && errno == EINTR)
		goto again;
	return ret;
}

static void *async_wait_kludge(void *args)
{
	pid_t pid = PTR_TO_INT(args);

	wait_for_pid(pid);
	return NULL;
}

#define LISTEN_NEEDS_MNTNS 1U
#define CONNECT_NEEDS_MNTNS 2U

void forkproxy(void)
{
	unsigned int needs_mntns = 0;
	int connect_pid, connect_pidfd, listen_pid, listen_pidfd;
	size_t unix_prefix_len = sizeof("unix:") - 1;
	ssize_t ret;
	pid_t pid;
	char *connect_addr, *cur, *listen_addr;
	int sk_fds[2] = {-EBADF, -EBADF};

	// Get the pid
	cur = advance_arg(false);
	if (cur == NULL ||
	    (strcmp(cur, "--help") == 0 || strcmp(cur, "--version") == 0 ||
	     strcmp(cur, "-h") == 0))
		_exit(EXIT_FAILURE);

	listen_pid = atoi(advance_arg(true));
	listen_pidfd = atoi(advance_arg(true));
	listen_addr = advance_arg(true);
	connect_pid = atoi(advance_arg(true));
	connect_pidfd = atoi(advance_arg(true));
	connect_addr = advance_arg(true);

	if (strncmp(listen_addr, "udp:", sizeof("udp:") - 1) == 0 &&
	    strncmp(connect_addr, "udp:", sizeof("udp:") - 1) != 0) {
		    fprintf(stderr, "Error: Proxying from udp to non-udp protocol is not supported\n");
		    _exit(EXIT_FAILURE);
	}

	// We only need to attach to the mount namespace for
	// non-abstract unix sockets.
	if ((strncmp(listen_addr, "unix:", unix_prefix_len) == 0) &&
	    (listen_addr[unix_prefix_len] != '@'))
		    needs_mntns |= LISTEN_NEEDS_MNTNS;

	if ((strncmp(connect_addr, "unix:", unix_prefix_len) == 0) &&
	    (connect_addr[unix_prefix_len] != '@'))
		    needs_mntns |= CONNECT_NEEDS_MNTNS;

	ret = socketpair(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0, sk_fds);
	if (ret < 0) {
		fprintf(stderr,
			"%s - Failed to create anonymous unix socket pair\n",
			strerror(errno));
		_exit(EXIT_FAILURE);
	}

	pid = fork();
	if (pid < 0) {
		fprintf(stderr, "%s - Failed to create new process\n",
			strerror(errno));
		_exit(EXIT_FAILURE);
	}

	if (pid == 0) {
		int listen_nsfd;

		whoami = FORKPROXY_CHILD;

		ret = close(sk_fds[0]);
		if (ret < 0)
			fprintf(stderr, "%s - Failed to close fd %d\n",
				strerror(errno), sk_fds[0]);

		listen_nsfd = pidfd_nsfd(listen_pidfd, listen_pid);
		if (listen_nsfd < 0) {
			fprintf(stderr, "Error: %m - Failed to safely open namespace file descriptor based on pidfd %d\n", listen_pidfd);
			_exit(EXIT_FAILURE);
		}

		// Attach to the user namespace of the listener
		attach_userns_fd(listen_nsfd);

		// Attach to the network namespace of the listener
		if (!change_namespaces(listen_pidfd, listen_nsfd, CLONE_NEWNET)) {
			fprintf(stderr, "Error: %m - Failed setns to listener network namespace\n");
			_exit(EXIT_FAILURE);
		}

		if ((needs_mntns & LISTEN_NEEDS_MNTNS) && !change_namespaces(listen_pidfd, listen_nsfd, CLONE_NEWNS)) {
			fprintf(stderr, "Error: %m - Failed setns to listener mount namespace\n");
			_exit(EXIT_FAILURE);
		}

		close_prot_errno_disarm(listen_nsfd);
		close_prot_errno_disarm(listen_pidfd);

		ret = dup3(sk_fds[1], FORKPROXY_UDS_SOCK_FD_NUM, O_CLOEXEC);
		if (ret < 0) {
			fprintf(stderr,
				"%s - Failed to duplicate fd %d to fd 200\n",
				strerror(errno), sk_fds[1]);
			_exit(EXIT_FAILURE);
		}

		ret = close(sk_fds[1]);
		if (ret < 0)
			fprintf(stderr, "%s - Failed to close fd %d\n",
				strerror(errno), sk_fds[1]);
	} else {
		pthread_t thread;
		int connect_nsfd;

		whoami = FORKPROXY_PARENT;

		ret = close(sk_fds[1]);
		if (ret < 0)
			fprintf(stderr, "%s - Failed to close fd %d\n",
				strerror(errno), sk_fds[1]);

		connect_nsfd = pidfd_nsfd(connect_pidfd, connect_pid);
		if (connect_nsfd < 0) {
			fprintf(stderr, "Error: %m - Failed to safely open namespace file descriptor based on pidfd %d\n", connect_pidfd);
			_exit(EXIT_FAILURE);
		}

		// Attach to the user namespace of the connector
		attach_userns_fd(connect_nsfd);

		// Attach to the network namespace of the connector
		if (!change_namespaces(connect_pidfd, connect_nsfd, CLONE_NEWNET)) {
			fprintf(stderr, "Error: %m - Failed setns to connector network namespace\n");
			_exit(EXIT_FAILURE);
		}

		// Attach to the mount namespace of the connector
		if ((needs_mntns & CONNECT_NEEDS_MNTNS) && !change_namespaces(connect_pidfd, connect_nsfd, CLONE_NEWNS)) {
			fprintf(stderr, "Error: %m - Failed setns to connector mount namespace\n");
			_exit(EXIT_FAILURE);
		}

		close_prot_errno_disarm(connect_nsfd);
		close_prot_errno_disarm(connect_pidfd);

		ret = dup3(sk_fds[0], FORKPROXY_UDS_SOCK_FD_NUM, O_CLOEXEC);
		if (ret < 0) {
			fprintf(stderr,
				"%s - Failed to duplicate fd %d to fd 200\n",
				strerror(errno), sk_fds[1]);
			_exit(EXIT_FAILURE);
		}

		ret = close(sk_fds[0]);
		if (ret < 0)
			fprintf(stderr, "%s - Failed to close fd %d\n",
				strerror(errno), sk_fds[0]);

		// Usually we should wait for the child process somewhere here.
		// But we cannot really do this. The listener file descriptors
		// are retrieved in the go runtime but at that point we have
		// already double-fork()ed to daemonize ourselves and so we
		// can't wait on the child anymore after we received the
		// listener fds. On the other hand, if we wait on the child
		// here we wait on the child before the receive. However, if we
		// do this then we can end up in a situation where the socket
		// send buffer is full and we need to retrieve some file
		// descriptors first before we can go on sending more. But this
		// won't be possible because we're waiting before the call to
		// receive the file descriptor in the go runtime. Luckily, we
		// can just rely on init doing it's job and reaping the zombie
		// process. So, technically unsatisfying but pragmatically
		// correct.

		// Create detached waiting thread after all namespace
		// interactions have concluded since some of them require
		// single-threadedness.
		if (pthread_create(&thread, NULL, async_wait_kludge, INT_TO_PTR(pid)) ||
		    pthread_detach(thread)) {
			fprintf(stderr, "%m - Failed to create detached thread\n");
			_exit(EXIT_FAILURE);
		}
	}
}
*/
import "C"

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/internal/linux"
	"github.com/lxc/incus/internal/netutils"
	"github.com/lxc/incus/internal/server/daemon"
	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/network"
	_ "github.com/lxc/incus/shared/cgo" // Used by cgo
)

const forkproxyUDSSockFDNum int = C.FORKPROXY_UDS_SOCK_FD_NUM

type cmdForkproxy struct {
	global *cmdGlobal
}

// UDP session tracking (map "client tuple" to udp session)
var udpSessions = map[string]*udpSession{}
var udpSessionsLock sync.Mutex

type udpSession struct {
	client    net.Addr
	target    net.Conn
	timer     *time.Timer
	timerLock sync.Mutex
}

func (c *cmdForkproxy) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkproxy <listen PID> <listen PidFd> <listen address> <connect PID> <connect PidFd> <connect address> <log path> <pid path> <listen gid> <listen uid> <listen mode> <security gid> <security uid>"
	cmd.Short = "Setup network connection proxying"
	cmd.Long = `Description:
  Setup network connection proxying

  This internal command will spawn a new proxy process for a particular
  container, connecting one side to the host and the other to the
  container.
`
	cmd.Args = cobra.ExactArgs(12)
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func rearmUDPFd(epFd C.int, connFd C.int) {
	var ev C.struct_epoll_event
	ev.events = C.EPOLLIN | C.EPOLLONESHOT
	*(*C.int)(unsafe.Pointer(uintptr(unsafe.Pointer(&ev)) + unsafe.Sizeof(ev.events))) = connFd
	ret := C.epoll_ctl(epFd, C.EPOLL_CTL_MOD, connFd, &ev)
	if ret < 0 {
		fmt.Println("Error: Failed to add listener fd to epoll instance")
	}
}

func listenerInstance(epFd C.int, lAddr *deviceConfig.ProxyAddress, cAddr *deviceConfig.ProxyAddress, connFd C.int, lStruct *lStruct, proxy bool) error {
	// Single or multiple port -> single port
	connectAddr := cAddr.Address
	if cAddr.ConnType != "unix" {
		connectPort := cAddr.Ports[0]
		if lAddr.ConnType != "unix" && cAddr.ConnType != "unix" && len(cAddr.Ports) > 1 {
			// multiple port -> multiple port
			connectPort = cAddr.Ports[(*lStruct).lAddrIndex]
		}

		connectAddr = net.JoinHostPort(cAddr.Address, fmt.Sprintf("%d", connectPort))
	}

	if lAddr.ConnType == "udp" {
		// This only handles udp <-> udp. The C constructor will have verified this before
		go func() {
			srcConn, err := net.FileConn((*lStruct).f)
			if err != nil {
				fmt.Printf("Warning: Failed to re-assemble listener: %v\n", err)
				rearmUDPFd(epFd, connFd)
				return
			}

			dstConn, err := net.Dial(cAddr.ConnType, connectAddr)
			if err != nil {
				fmt.Printf("Warning: Failed to connect to target: %v\n", err)
				rearmUDPFd(epFd, connFd)
				return
			}

			genericRelay(srcConn, dstConn, true)
			rearmUDPFd(epFd, connFd)
		}()

		return nil
	}

	// Accept a new client
	listener := (*lStruct).lConn
	srcConn, err := (*listener).Accept()
	if err != nil {
		fmt.Printf("Warning: Failed to accept new connection: %v\n", err)
		return err
	}

	dstConn, err := net.Dial(cAddr.ConnType, connectAddr)
	if err != nil {
		_ = srcConn.Close()
		fmt.Printf("Warning: Failed to connect to target: %v\n", err)
		return err
	}

	if proxy && cAddr.ConnType == "tcp" {
		if lAddr.ConnType == "unix" {
			_, _ = dstConn.Write([]byte("PROXY UNKNOWN\r\n"))
		} else {
			cHost, cPort, err := net.SplitHostPort(srcConn.RemoteAddr().String())
			if err != nil {
				return err
			}

			dHost, dPort, err := net.SplitHostPort(srcConn.LocalAddr().String())
			if err != nil {
				return err
			}

			proto := srcConn.LocalAddr().Network()
			proto = strings.ToUpper(proto)
			if strings.Contains(cHost, ":") {
				proto = fmt.Sprintf("%s6", proto)
			} else {
				proto = fmt.Sprintf("%s4", proto)
			}

			_, _ = dstConn.Write([]byte(fmt.Sprintf("PROXY %s %s %s %s %s\r\n", proto, cHost, dHost, cPort, dPort)))
		}
	}

	if cAddr.ConnType == "unix" && lAddr.ConnType == "unix" {
		// Handle OOB if both src and dst are using unix sockets
		go unixRelay(srcConn, dstConn)
	} else {
		go genericRelay(srcConn, dstConn, false)
	}

	return nil
}

type lStruct struct {
	f          *os.File
	lConn      *net.Listener
	lAddrIndex int
}

func (c *cmdForkproxy) Run(cmd *cobra.Command, args []string) error {
	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	// Quick checks.
	if len(args) != 12 {
		_ = cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return fmt.Errorf("Missing required arguments")
	}

	// Check where we are in initialization
	if C.whoami != C.FORKPROXY_PARENT && C.whoami != C.FORKPROXY_CHILD {
		return fmt.Errorf("Failed to call forkproxy constructor")
	}

	listenAddr := args[2]
	lAddr, err := network.ProxyParseAddr(listenAddr)
	if err != nil {
		return err
	}

	connectAddr := args[5]
	cAddr, err := network.ProxyParseAddr(connectAddr)
	if err != nil {
		return err
	}

	if (lAddr.ConnType == "udp" || lAddr.ConnType == "tcp") && cAddr.ConnType == "udp" || cAddr.ConnType == "tcp" {
		err := fmt.Errorf("Invalid port range")
		if len(lAddr.Ports) > 1 && len(cAddr.Ports) > 1 && (len(cAddr.Ports) != len(lAddr.Ports)) {
			fmt.Println(err)
			return err
		} else if len(lAddr.Ports) == 1 && len(cAddr.Ports) > 1 {
			fmt.Println(err)
			return err
		}
	}

	if C.whoami == C.FORKPROXY_CHILD {
		defer func() { _ = unix.Close(forkproxyUDSSockFDNum) }()

		if lAddr.ConnType == "unix" && !lAddr.Abstract {
			err := os.Remove(lAddr.Address)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
		}

		var listenAddresses []string
		if lAddr.ConnType == "unix" {
			listenAddresses = []string{lAddr.Address}
		} else {
			listenPortCount := len(lAddr.Ports)
			listenAddresses = make([]string, 0, listenPortCount)

			for i := 0; i < listenPortCount; i++ {
				listenAddresses = append(listenAddresses, net.JoinHostPort(lAddr.Address, fmt.Sprintf("%d", lAddr.Ports[i])))
			}
		}

		for _, listenAddress := range listenAddresses {
			file, err := getListenerFile(lAddr.ConnType, listenAddress)
			if err != nil {
				return err
			}

		sAgain:
			err = netutils.AbstractUnixSendFd(forkproxyUDSSockFDNum, int(file.Fd()))
			if err != nil {
				errno, ok := linux.GetErrno(err)
				if ok && (errno == unix.EAGAIN) {
					goto sAgain
				}

				break
			}

			_ = file.Close()
		}

		if lAddr.ConnType == "unix" && !lAddr.Abstract {
			var err error

			listenAddrGID := -1
			if args[6] != "" {
				listenAddrGID, err = strconv.Atoi(args[6])
				if err != nil {
					return err
				}
			}

			listenAddrUID := -1
			if args[7] != "" {
				listenAddrUID, err = strconv.Atoi(args[7])
				if err != nil {
					return err
				}
			}

			if listenAddrGID != -1 || listenAddrUID != -1 {
				err = os.Chown(lAddr.Address, listenAddrUID, listenAddrGID)
				if err != nil {
					return err
				}
			}

			var listenAddrMode os.FileMode
			if args[8] != "" {
				tmp, err := strconv.ParseUint(args[8], 8, 0)
				if err != nil {
					return err
				}

				listenAddrMode = os.FileMode(tmp)
				err = os.Chmod(lAddr.Address, listenAddrMode)
				if err != nil {
					return err
				}
			}
		}

		return err
	}

	addrRecvCount := 1
	if lAddr.ConnType != "unix" {
		addrRecvCount = len(lAddr.Ports)
	}

	files := []*os.File{}
	for i := 0; i < addrRecvCount; i++ {
	rAgain:
		f, err := netutils.AbstractUnixReceiveFd(forkproxyUDSSockFDNum, netutils.UnixFdsAcceptExact)
		if err != nil {
			errno, ok := linux.GetErrno(err)
			if ok && (errno == unix.EAGAIN) {
				goto rAgain
			}

			fmt.Printf("Error: Failed to receive fd from listener process: %v\n", err)
			_ = unix.Close(forkproxyUDSSockFDNum)
			return err
		}

		if f == nil {
			fmt.Println("Error: Failed to receive fd from listener process")
			_ = unix.Close(forkproxyUDSSockFDNum)
			return err
		}

		files = append(files, f)
	}

	_ = unix.Close(forkproxyUDSSockFDNum)

	var listenerMap map[int]*lStruct

	isUDPListener := lAddr.ConnType == "udp"
	listenerMap = make(map[int]*lStruct, addrRecvCount)
	if isUDPListener {
		for i, f := range files {
			listenerMap[int(f.Fd())] = &lStruct{
				f:          f,
				lAddrIndex: i,
			}
		}
	} else {
		for i, f := range files {
			listener, err := net.FileListener(f)
			if err != nil {
				fmt.Printf("Error: Failed to re-assemble listener: %v\n", err)
				return err
			}

			listenerMap[int(f.Fd())] = &lStruct{
				lConn:      &listener,
				lAddrIndex: i,
			}
		}
	}

	// Drop privilege if requested
	gid := uint64(0)
	if args[9] != "" {
		gid, err = strconv.ParseUint(args[9], 10, 32)
		if err != nil {
			return err
		}
	}

	uid := uint64(0)
	if args[10] != "" {
		uid, err = strconv.ParseUint(args[10], 10, 32)
		if err != nil {
			return err
		}
	}

	if uid != 0 || gid != 0 {
		ret := C.switch_uid_gid(C.uint32_t(uid), C.uint32_t(gid))
		if ret < 0 {
			return fmt.Errorf("Failed to switch to uid %d and gid %d", uid, gid)
		}
	}

	// Handle SIGTERM which is sent when the proxy is to be removed
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, unix.SIGTERM)

	if lAddr.ConnType == "unix" && !lAddr.Abstract {
		defer func() { _ = os.Remove(lAddr.Address) }()
	}

	epFd := C.epoll_create1(C.EPOLL_CLOEXEC)
	if epFd < 0 {
		return fmt.Errorf("Failed to create new epoll instance")
	}

	// Wait for SIGTERM and close the listener in order to exit the loop below
	self := unix.Getpid()
	go func() {
		<-sigs
		for _, f := range files {
			C.epoll_ctl(epFd, C.EPOLL_CTL_DEL, C.int(f.Fd()), nil)
			_ = f.Close()
		}

		_ = unix.Close(int(epFd))

		if !isUDPListener {
			for _, l := range listenerMap {
				conn := (*l).lConn
				_ = (*conn).Close()
			}
		}

		_ = unix.Kill(self, unix.SIGKILL)
	}()
	defer func() { _ = unix.Kill(self, unix.SIGTERM) }()

	for _, f := range files {
		var ev C.struct_epoll_event
		ev.events = C.EPOLLIN
		if isUDPListener {
			ev.events |= C.EPOLLONESHOT
		}

		*(*C.int)(unsafe.Pointer(&ev.data)) = C.int(f.Fd())
		ret := C.epoll_ctl(epFd, C.EPOLL_CTL_ADD, C.int(f.Fd()), &ev)
		if ret < 0 {
			return fmt.Errorf("Error: Failed to add listener fd to epoll instance")
		}
	}

	// This line is used by the daemon to check forkproxy has started OK.
	fmt.Println("Status: Started")

	for {
		var events [10]C.struct_epoll_event

		nfds := C.lxc_epoll_wait_nointr(epFd, &events[0], 10, -1)
		if nfds < 0 {
			fmt.Println("Error: Failed to wait on epoll instance")
			break
		}

		for i := C.int(0); i < nfds; i++ {
			curFd := *(*C.int)(unsafe.Pointer(&events[i].data))
			srcConn, ok := listenerMap[int(curFd)]
			if !ok {
				continue
			}

			err := listenerInstance(epFd, lAddr, cAddr, curFd, srcConn, args[11] == "true")
			if err != nil {
				fmt.Printf("Warning: Failed to prepare new listener instance: %v\n", err)
			}
		}
	}

	fmt.Println("Status: Stopping proxy")
	return nil
}

func proxyCopy(dst net.Conn, src net.Conn) error {
	var err error

	// Attempt casting to UDP connections
	srcUdp, srcIsUdp := src.(*net.UDPConn)
	dstUdp, dstIsUdp := dst.(*net.UDPConn)

	buf := make([]byte, 32*1024)
	for {
	rAgain:
		var nr int
		var er error

		if srcIsUdp && srcUdp.RemoteAddr() == nil {
			var addr net.Addr
			nr, addr, er = srcUdp.ReadFrom(buf)
			if er == nil {
				// Look for existing UDP session
				udpSessionsLock.Lock()
				us, ok := udpSessions[addr.String()]
				udpSessionsLock.Unlock()

				if !ok {
					dc, err := net.Dial(dst.RemoteAddr().Network(), dst.RemoteAddr().String())
					if err != nil {
						return err
					}

					us = &udpSession{
						client: addr,
						target: dc,
					}

					udpSessionsLock.Lock()
					udpSessions[addr.String()] = us
					udpSessionsLock.Unlock()

					go func() { _ = proxyCopy(src, dc) }()
					us.timer = time.AfterFunc(30*time.Minute, func() {
						_ = us.target.Close()

						udpSessionsLock.Lock()
						delete(udpSessions, addr.String())
						udpSessionsLock.Unlock()
					})
				}

				us.timerLock.Lock()
				us.timer.Reset(30 * time.Minute)
				us.timerLock.Unlock()

				dst = us.target
				dstUdp, dstIsUdp = dst.(*net.UDPConn)
			}
		} else {
			nr, er = src.Read(buf)
		}

		// keep retrying on EAGAIN
		errno, ok := linux.GetErrno(er)
		if ok && (errno == unix.EAGAIN) {
			goto rAgain
		}

		if nr > 0 {
		wAgain:
			var nw int
			var ew error

			if dstIsUdp && dstUdp.RemoteAddr() == nil {
				var us *udpSession

				udpSessionsLock.Lock()
				for _, v := range udpSessions {
					if v.target.LocalAddr() == src.LocalAddr() {
						us = v
						break
					}
				}
				udpSessionsLock.Unlock()

				if us == nil {
					return fmt.Errorf("Connection expired")
				}

				us.timerLock.Lock()
				us.timer.Reset(30 * time.Minute)
				us.timerLock.Unlock()

				nw, ew = dstUdp.WriteTo(buf[0:nr], us.client)
			} else {
				nw, ew = dst.Write(buf[0:nr])
			}

			// keep retrying on EAGAIN
			errno, ok := linux.GetErrno(ew)
			if ok && (errno == unix.EAGAIN) {
				goto wAgain
			}

			if ew != nil {
				err = ew
				break
			}

			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}

			break
		}
	}

	return err
}

func genericRelay(dst net.Conn, src net.Conn, timeout bool) {
	relayer := func(src net.Conn, dst net.Conn, ch chan error) {
		ch <- proxyCopy(src, dst)
		close(ch)
	}

	chSend := make(chan error)
	chRecv := make(chan error)

	go relayer(src, dst, chRecv)

	_, isUDP := dst.(*net.UDPConn)
	if !isUDP {
		go relayer(dst, src, chSend)
	}

	select {
	case errSnd := <-chSend:
		if daemon.Debug && errSnd != nil {
			fmt.Printf("Warning: Error while sending data: %v\n", errSnd)
		}

	case errRcv := <-chRecv:
		if daemon.Debug && errRcv != nil {
			fmt.Printf("Warning: Error while reading data: %v\n", errRcv)
		}
	}

	_ = src.Close()
	_ = dst.Close()

	// Empty the channels
	if !isUDP {
		<-chSend
	}
	<-chRecv
}

func unixRelayer(src *net.UnixConn, dst *net.UnixConn, ch chan error) {
	dataBuf := make([]byte, 4096)
	oobBuf := make([]byte, 4096)

	for {
		// Read from the source
	readAgain:
		sData, sOob, _, _, err := src.ReadMsgUnix(dataBuf, oobBuf)
		if err != nil {
			errno, ok := linux.GetErrno(err)
			if ok && errno == unix.EAGAIN {
				goto readAgain
			}

			ch <- err
			return
		}

		var fds []int
		if sOob > 0 {
			entries, err := unix.ParseSocketControlMessage(oobBuf[:sOob])
			if err != nil {
				ch <- err
				return
			}

			for _, msg := range entries {
				fds, err = unix.ParseUnixRights(&msg)
				if err != nil {
					ch <- err
					return
				}
			}
		}

		// Send to the destination
	writeAgain:
		tData, tOob, err := dst.WriteMsgUnix(dataBuf[:sData], oobBuf[:sOob], nil)
		if err != nil {
			errno, ok := linux.GetErrno(err)
			if ok && errno == unix.EAGAIN {
				goto writeAgain
			}

			ch <- err
			return
		}

		if sData != tData || sOob != tOob {
			ch <- fmt.Errorf("Lost oob data during transfer")
			return
		}

		// Close those fds we received
		for _, fd := range fds {
			err := unix.Close(fd)
			if err != nil {
				ch <- err
				return
			}
		}
	}
}

func unixRelay(dst io.ReadWriteCloser, src io.ReadWriteCloser) {
	chSend := make(chan error)
	go unixRelayer(dst.(*net.UnixConn), src.(*net.UnixConn), chSend)

	chRecv := make(chan error)
	go unixRelayer(src.(*net.UnixConn), dst.(*net.UnixConn), chRecv)

	select {
	case errSnd := <-chSend:
		if daemon.Debug && errSnd != nil {
			fmt.Printf("Warning: Error while sending data: %v\n", errSnd)
		}

	case errRcv := <-chRecv:
		if daemon.Debug && errRcv != nil {
			fmt.Printf("Warning: Error while reading data: %v\n", errRcv)
		}
	}

	_ = src.Close()
	_ = dst.Close()

	// Empty the channels
	<-chSend
	<-chRecv
}

func tryListen(protocol string, addr string) (net.Listener, error) {
	var listener net.Listener
	var err error

	for i := 0; i < 10; i++ {
		listener, err = net.Listen(protocol, addr)
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		return nil, err
	}

	return listener, nil
}

func tryListenUDP(protocol string, addr string) (*os.File, error) {
	var UDPConn *net.UDPConn
	var err error

	udpAddr, err := net.ResolveUDPAddr(protocol, addr)
	if err != nil {
		return nil, err
	}

	for i := 0; i < 10; i++ {
		UDPConn, err = net.ListenUDP(protocol, udpAddr)
		if err == nil {
			file, err := UDPConn.File()
			_ = UDPConn.Close()
			return file, err
		}

		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		return nil, err
	}

	if UDPConn == nil {
		return nil, fmt.Errorf("Failed to setup UDP listener")
	}

	file, err := UDPConn.File()
	_ = UDPConn.Close()
	return file, err
}

func getListenerFile(protocol string, addr string) (*os.File, error) {
	if protocol == "udp" {
		return tryListenUDP("udp", addr)
	}

	listener, err := tryListen(protocol, addr)
	if err != nil {
		return nil, fmt.Errorf("Failed to listen on %s: %w", addr, err)
	}

	var file *os.File
	switch l := listener.(type) {
	case *net.TCPListener:
		file, err = l.File()
	case *net.UnixListener:
		file, err = l.File()
	default:
		return nil, fmt.Errorf("Could not get listener file: invalid listener type")
	}

	if err != nil {
		return nil, fmt.Errorf("Failed to get file from listener: %w", err)
	}

	return file, nil
}
