package main

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// linuxChecks: what the Landlock and seccomp rules must refuse on Linux,
// beyond the checks every system shares.
func linuxChecks(check func(string, bool, error), read func(string) error) {
	home, _ := os.UserHomeDir()
	for _, p := range []string{".gnupg", ".aws", ".mozilla", ".docker"} {
		check("read ~/"+p, false, read(filepath.Join(home, p)))
	}
	ppid := os.Getppid()
	check("read the launcher's environment (/proc)", false, read("/proc/"+strconv.Itoa(ppid)+"/environ"))
	check("read another program's memory map (/proc/1)", false, read("/proc/1/maps"))
	check("signal the launcher", false, syscall.Kill(ppid, 0))
	check("list your other terminals (/dev/pts)", false, read("/dev/pts"))
	check("send UDP (DNS, could carry data out)", false, udpSend())
	check("open a netlink socket", false, sock(unix.AF_NETLINK, unix.SOCK_RAW))
	check("open a Unix socket (SSH agent, D-Bus, X11)", false, sock(unix.AF_UNIX, unix.SOCK_STREAM))
	check("type into the terminal (TIOCSTI)", false, tiocsti())
}

func udpSend() error {
	c, err := net.DialTimeout("udp", "1.1.1.1:53", time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.Write([]byte{0})
	return err
}

func sock(domain, typ int) error {
	fd, err := unix.Socket(domain, typ, 0)
	if err != nil {
		return err
	}
	unix.Close(fd)
	return nil
}

// tiocsti pushes a harmless NUL into the terminal's input: nil if it went.
func tiocsti() error {
	b := byte(0)
	_, _, e := unix.Syscall(unix.SYS_IOCTL, os.Stdin.Fd(), unix.TIOCSTI, uintptr(unsafe.Pointer(&b)))
	if e != 0 {
		return e
	}
	return nil
}
