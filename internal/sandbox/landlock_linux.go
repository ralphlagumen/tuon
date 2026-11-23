package sandbox

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// landlockABI is the kernel's Landlock version (0: none).
func landlockABI() int {
	v, _, e := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if e != 0 {
		return 0
	}
	return int(v)
}

// The rights each version added; a kernel refuses rights it doesn't know,
// so only the known ones are asked for. Rights not listed by a newer kernel
// (and not asked for) stay allowed: the seccomp filter covers sockets.
func fsRights(abi int) uint64 {
	r := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR | unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
	if abi >= 2 {
		r |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		r |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		r |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	if supported(unix.LANDLOCK_ACCESS_FS_RESOLVE_UNIX, 0, 0) {
		r |= unix.LANDLOCK_ACCESS_FS_RESOLVE_UNIX // connecting to Unix sockets by path: none allowed
	}
	return r
}

func netRights(abi int) uint64 {
	if abi < 4 {
		return 0
	}
	r := uint64(unix.LANDLOCK_ACCESS_NET_BIND_TCP | unix.LANDLOCK_ACCESS_NET_CONNECT_TCP)
	if udp := uint64(unix.LANDLOCK_ACCESS_NET_BIND_UDP | unix.LANDLOCK_ACCESS_NET_CONNECT_SEND_UDP); supported(0, udp, 0) {
		r |= udp
	}
	return r
}

func scopes(abi int) uint64 {
	if abi < 6 {
		return 0
	}
	return unix.LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET | unix.LANDLOCK_SCOPE_SIGNAL
}

// rulesetAttr: the first three fields of struct landlock_ruleset_attr
// (what kernels with network rules and scopes understand).
type rulesetAttr struct {
	fs, net, scoped uint64
}

type netPortAttr struct {
	allowed, port uint64
}

const ruleNetPort = 2 // LANDLOCK_RULE_NET_PORT

// supported asks the kernel whether it knows these rights.
func supported(fs, net, scoped uint64) bool {
	if fs == 0 {
		fs = unix.LANDLOCK_ACCESS_FS_READ_FILE
	}
	a := rulesetAttr{fs, net, scoped}
	fd, _, e := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&a)), unsafe.Sizeof(a), 0)
	if e != 0 {
		return false
	}
	unix.Close(int(fd))
	return true
}

// landlock applies p to this thread (and what it execs).
func landlock(p policy) error {
	abi := landlockABI()
	if abi < 1 {
		return errNoLandlock
	}
	handled := rulesetAttr{fsRights(abi), netRights(abi), scopes(abi)}
	r, _, e := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&handled)), unsafe.Sizeof(handled), 0)
	if e != 0 {
		return e
	}
	ruleset := int(r)
	defer unix.Close(ruleset)
	for _, rule := range p.fs {
		access := rule.access & handled.fs
		f, err := os.OpenFile(rule.path, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			continue // not on this system
		}
		st, err := f.Stat()
		if err == nil && !st.IsDir() {
			access &= fileRights // rights about a folder's entries can't go on a file
		}
		attr := unix.LandlockPathBeneathAttr{Allowed_access: access, Parent_fd: int32(f.Fd())}
		_, _, e := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&attr)), 0, 0, 0)
		f.Close()
		if e != 0 {
			return e
		}
	}
	if handled.net&unix.LANDLOCK_ACCESS_NET_CONNECT_TCP != 0 {
		for _, port := range p.tcpPorts {
			attr := netPortAttr{unix.LANDLOCK_ACCESS_NET_CONNECT_TCP, uint64(port)}
			_, _, e := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), ruleNetPort, uintptr(unsafe.Pointer(&attr)), 0, 0, 0)
			if e != 0 {
				return e
			}
		}
	}
	if _, _, e := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(ruleset), 0, 0); e != 0 {
		return e
	}
	return nil
}

// fileRights: the rights that make sense on a single file.
const fileRights = unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_READ_FILE |
	unix.LANDLOCK_ACCESS_FS_TRUNCATE | unix.LANDLOCK_ACCESS_FS_IOCTL_DEV

var errNoLandlock = errors.New("not supported by this kernel")
