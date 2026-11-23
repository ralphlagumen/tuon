package sandbox

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The seccomp filter: sockets only for TCP over IPv4/IPv6 (no Unix
// sockets, so no SSH agent, D-Bus or Docker even on kernels where
// Landlock can't refuse them; no UDP, netlink or raw sockets), and no
// TIOCSTI/TIOCLINUX, which would type commands into your terminal.
// Anything else (other architectures, 32-bit or x32 calls) is refused.

// seccompArch: the audit arch and syscall numbers for this build.
func seccompArch() uint32 {
	switch runtime.GOARCH {
	case "amd64":
		return unix.AUDIT_ARCH_X86_64
	case "arm64":
		return unix.AUDIT_ARCH_AARCH64
	}
	return 0
}

var sysSocket = map[string]uint32{"amd64": 41, "arm64": 198}
var sysIoctl = map[string]uint32{"amd64": 16, "arm64": 29}

// seccompProgram builds the BPF program for arch (nil: not supported).
func seccompProgram(goarch string) []unix.SockFilter {
	arch := map[string]uint32{"amd64": unix.AUDIT_ARCH_X86_64, "arm64": unix.AUDIT_ARCH_AARCH64}[goarch]
	if arch == 0 {
		return nil
	}
	const (
		ld  = unix.BPF_LD | unix.BPF_W | unix.BPF_ABS
		jeq = unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K
		jge = unix.BPF_JMP | unix.BPF_JGE | unix.BPF_K
		and = unix.BPF_ALU | unix.BPF_AND | unix.BPF_K
		ret = unix.BPF_RET | unix.BPF_K
		// struct seccomp_data: nr at 0, arch at 4, args[i] at 16+8i (low half, little-endian)
		offNr, offArch, offArg0, offArg1 = 0, 4, 16, 24
	)
	allow := uint32(unix.SECCOMP_RET_ALLOW)
	deny := uint32(unix.SECCOMP_RET_ERRNO | uint32(unix.EACCES))
	kill := uint32(unix.SECCOMP_RET_KILL_PROCESS)
	stmt := func(code uint16, k uint32) unix.SockFilter { return unix.SockFilter{Code: code, K: k} }
	jump := func(code uint16, k uint32, jt, jf uint8) unix.SockFilter {
		return unix.SockFilter{Code: code, K: k, Jt: jt, Jf: jf}
	}
	p := []unix.SockFilter{
		/* 0 */ stmt(ld, offArch),
		/* 1 */ jump(jeq, arch, 1, 0),
		/* 2 */ stmt(ret, kill),
		/* 3 */ stmt(ld, offNr),
	}
	if goarch == "amd64" { // x32 calls carry bit 30
		p = append(p, jump(jge, 0x40000000, 0, 1), stmt(ret, kill))
	}
	// offsets in the comments count from here
	p = append(p,
		/* +0 */ jump(jeq, sysSocket[goarch], 2, 0),
		/* +1 */ jump(jeq, sysIoctl[goarch], 9, 0),
		/* +2 */ stmt(ret, allow),
		// socket(domain, type, …): TCP over IPv4/IPv6 only
		/* +3 */
		stmt(ld, offArg0),
		/* +4 */ jump(jeq, unix.AF_INET, 1, 0),
		/* +5 */ jump(jeq, unix.AF_INET6, 0, 4),
		/* +6 */ stmt(ld, offArg1),
		/* +7 */ stmt(and, ^uint32(unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC)),
		/* +8 */ jump(jeq, unix.SOCK_STREAM, 0, 1),
		/* +9 */ stmt(ret, allow),
		/* +10 */ stmt(ret, deny),
		// ioctl(fd, request, …): the kernel reads request as 32 bits
		/* +11 */
		stmt(ld, offArg1),
		/* +12 */ jump(jeq, unix.TIOCSTI, 1, 0),
		/* +13 */ jump(jeq, unix.TIOCLINUX, 0, 1),
		/* +14 */ stmt(ret, deny),
		/* +15 */ stmt(ret, allow),
	)
	return p
}

// seccompFilter installs the filter on this thread (and what it execs).
func seccompFilter() error {
	prog := seccompProgram(runtime.GOARCH)
	if prog == nil {
		return nil // noted by partialNote
	}
	fprog := unix.SockFprog{Len: uint16(len(prog)), Filter: &prog[0]}
	_, _, e := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, 0, uintptr(unsafe.Pointer(&fprog)))
	if e != 0 {
		return e
	}
	return nil
}
