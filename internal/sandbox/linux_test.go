//go:build linux

package sandbox

import (
	"encoding/binary"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// runBPF evaluates a seccomp program on one call (a tiny classic-BPF
// interpreter: the instructions seccompProgram uses).
func runBPF(t *testing.T, prog []unix.SockFilter, arch, nr uint32, args ...uint64) uint32 {
	data := make([]byte, 64)
	binary.LittleEndian.PutUint32(data[0:], nr)
	binary.LittleEndian.PutUint32(data[4:], arch)
	for i, a := range args {
		binary.LittleEndian.PutUint64(data[16+8*i:], a)
	}
	var acc uint32
	for pc := 0; pc < len(prog); pc++ {
		in := prog[pc]
		switch in.Code {
		case unix.BPF_LD | unix.BPF_W | unix.BPF_ABS:
			acc = binary.LittleEndian.Uint32(data[in.K:])
		case unix.BPF_ALU | unix.BPF_AND | unix.BPF_K:
			acc &= in.K
		case unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, unix.BPF_JMP | unix.BPF_JGE | unix.BPF_K:
			hit := acc == in.K
			if in.Code&0xf0 == unix.BPF_JGE {
				hit = acc >= in.K
			}
			if hit {
				pc += int(in.Jt)
			} else {
				pc += int(in.Jf)
			}
		case unix.BPF_RET | unix.BPF_K:
			return in.K
		default:
			t.Fatalf("unknown instruction %#x at %d", in.Code, pc)
		}
	}
	t.Fatal("ran off the end")
	return 0
}

func TestSeccompProgram(t *testing.T) {
	allow, kill := uint32(unix.SECCOMP_RET_ALLOW), uint32(unix.SECCOMP_RET_KILL_PROCESS)
	deny := uint32(unix.SECCOMP_RET_ERRNO | uint32(unix.EACCES))
	for _, goarch := range []string{"amd64", "arm64"} {
		prog := seccompProgram(goarch)
		arch := map[string]uint32{"amd64": unix.AUDIT_ARCH_X86_64, "arm64": unix.AUDIT_ARCH_AARCH64}[goarch]
		socket, ioctl := uint64(sysSocket[goarch]), uint64(sysIoctl[goarch])
		cases := []struct {
			name string
			arch uint32
			nr   uint64
			args []uint64
			want uint32
		}{
			{"tcp4", arch, socket, []uint64{unix.AF_INET, unix.SOCK_STREAM | unix.SOCK_CLOEXEC | unix.SOCK_NONBLOCK}, allow},
			{"tcp6", arch, socket, []uint64{unix.AF_INET6, unix.SOCK_STREAM}, allow},
			{"udp", arch, socket, []uint64{unix.AF_INET, unix.SOCK_DGRAM}, deny},
			{"raw", arch, socket, []uint64{unix.AF_INET, unix.SOCK_RAW}, deny},
			{"unix", arch, socket, []uint64{unix.AF_UNIX, unix.SOCK_STREAM}, deny},
			{"netlink", arch, socket, []uint64{unix.AF_NETLINK, unix.SOCK_RAW}, deny},
			{"af_inet in high bits only", arch, socket, []uint64{1<<32 | unix.AF_UNIX, unix.SOCK_STREAM}, deny},
			{"tiocsti", arch, ioctl, []uint64{0, unix.TIOCSTI}, deny},
			{"tiocsti with high bits", arch, ioctl, []uint64{0, 1<<32 | unix.TIOCSTI}, deny},
			{"tioclinux", arch, ioctl, []uint64{0, unix.TIOCLINUX}, deny},
			{"tcgets", arch, ioctl, []uint64{0, unix.TCGETS}, allow},
			{"read", arch, 63, nil, allow},
			{"other arch", 0x40000003, socket, []uint64{unix.AF_UNIX}, kill},
		}
		if goarch == "amd64" {
			cases = append(cases, struct {
				name string
				arch uint32
				nr   uint64
				args []uint64
				want uint32
			}{"x32 socket", arch, 0x40000000 | socket, []uint64{unix.AF_UNIX}, kill})
		}
		for _, c := range cases {
			if got := runBPF(t, prog, c.arch, uint32(c.nr), c.args...); got != c.want {
				t.Errorf("%s %s: got %#x, want %#x", goarch, c.name, got, c.want)
			}
		}
	}
}

func TestLinuxRules(t *testing.T) {
	home := "/home/u"
	p := rules(home, "/run/user/1000/tuon-tmp", "/home/u/.local/bin/tuon-app", 4321)
	got := map[string]uint64{}
	for _, r := range p.fs {
		got[r.path] |= r.access
	}
	if got[filepath.Join(home, ".config", "tuon")]&unix.LANDLOCK_ACCESS_FS_WRITE_FILE == 0 {
		t.Error("tuon's folder must be writable")
	}
	if got["/run/user/1000/tuon-tmp"]&unix.LANDLOCK_ACCESS_FS_WRITE_FILE == 0 {
		t.Error("its temp folder must be writable")
	}
	if got["/home/u/.local/bin/tuon-app"]&unix.LANDLOCK_ACCESS_FS_EXECUTE == 0 {
		t.Error("the app must be runnable")
	}
	if got["/home/u/.local/bin"]&unix.LANDLOCK_ACCESS_FS_EXECUTE != 0 {
		t.Error("its folder holds the launcher: no running from there")
	}
	for path, a := range got {
		if path == home || path == filepath.Join(home, ".ssh") || path == "/" || path == "/home" || path == "/tmp" {
			t.Errorf("%s must not be in the rules (%#x)", path, a)
		}
		if a&unix.LANDLOCK_ACCESS_FS_WRITE_FILE != 0 && path != filepath.Join(home, ".config", "tuon") &&
			path != "/run/user/1000/tuon-tmp" && path != "/dev/null" && path != "/dev/tty" && filepath.Dir(path) != "/dev/pts" {
			t.Errorf("unexpected write access to %s", path)
		}
	}
	if len(p.tcpPorts) != 1 || p.tcpPorts[0] != 4321 {
		t.Errorf("tcp ports %v: only the gate", p.tcpPorts)
	}
}
