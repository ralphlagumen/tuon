package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// On Linux the sandbox is Landlock (files, TCP ports, Unix sockets, UDP,
// signals, as far as the kernel supports) plus a seccomp filter (no
// sockets but TCP over IPv4/IPv6, no typing into the terminal). Both are
// set by the launcher in a tiny "enter" step: it re-runs itself with
// EnterArg, restricts itself, then execs the chat app, so the rules are in
// force before any library's init code runs (SANDBOX-LINUX, audit 14 SC12).

const gatePortEnv = "TUON_SANDBOX_GATE_PORT"

// Command builds the sandboxed chat app: the launcher itself, entering the
// sandbox, then the app. nil and the reason when there's no sandbox.
func Command(app string, args []string, gatePort int) (*exec.Cmd, string) {
	if Disabled() {
		return nil, "sandbox off (TUON_NO_SANDBOX)"
	}
	abi := landlockABI()
	if abi < 1 {
		return nil, "sandbox unavailable: this Linux has no Landlock (needs 5.13 or later, enabled)"
	}
	self, err := os.Executable()
	if err != nil {
		return nil, "sandbox unavailable: " + err.Error()
	}
	tmp, err := TempBase()
	if err != nil {
		return nil, "sandbox unavailable: " + err.Error()
	}
	cmd := exec.Command(self, append([]string{EnterArg, app}, args...)...)
	cmd.Env = append(AppEnv(os.Environ()), envMark+"=1", "TMPDIR="+tmp, gatePortEnv+"="+strconv.Itoa(gatePort))
	if note := partialNote(abi); note != "" {
		cmd.Env = append(cmd.Env, NoteEnv+"="+note)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd, ""
}

// partialNote says what an older kernel can't enforce.
func partialNote(abi int) string {
	switch {
	case abi < 4:
		return "sandbox partial: this Linux can't limit network connections (needs 6.7+), so the app isn't held to Meta"
	case seccompArch() == 0:
		return "sandbox partial: no socket filter on " + runtime.GOARCH
	}
	return ""
}

// TempBase is the chat app's temp folder (its TMPDIR): private to you,
// the only temp folder the app may write. $XDG_RUNTIME_DIR/tuon-tmp, or
// /tmp/tuon-<uid> without one.
func TempBase() (string, error) {
	base := filepath.Join(os.TempDir(), "tuon-"+strconv.Itoa(os.Getuid()))
	if x := os.Getenv("XDG_RUNTIME_DIR"); x != "" && filepath.IsAbs(x) {
		base = filepath.Join(x, "tuon-tmp")
	}
	if err := os.Mkdir(base, 0o700); err != nil && !os.IsExist(err) {
		return "", err
	}
	st, err := os.Lstat(base)
	if err != nil {
		return "", err
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !st.IsDir() || !ok || int(sys.Uid) != os.Getuid() || st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s isn't a private folder of yours", base)
	}
	return base, nil
}

// Enter is the launcher's enter step (os.Args = launcher, EnterArg, app,
// args…): restrict this process, then become the app. It never returns;
// any failure stops here, never runs the app unrestricted.
func Enter(app string, args []string) {
	if err := enter(app, args); err != nil {
		fmt.Fprintln(os.Stderr, "tuon: couldn't start the sandbox:", err)
		os.Exit(1)
	}
}

func enter(app string, args []string) error {
	runtime.LockOSThread() // the rules are per thread: set them on the one that execs
	gatePort, _ := strconv.Atoi(os.Getenv(gatePortEnv))
	os.Unsetenv(gatePortEnv)
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("no_new_privs: %w", err)
	}
	if err := landlock(rules(home, os.Getenv("TMPDIR"), app, gatePort)); err != nil {
		return fmt.Errorf("landlock: %w", err)
	}
	if err := seccompFilter(); err != nil {
		return fmt.Errorf("seccomp: %w", err)
	}
	return unix.Exec(app, append([]string{app}, args...), os.Environ())
}

// fsRule: what may be done under one path.
type fsRule struct {
	path   string
	access uint64
}

// policy is the whole Landlock ruleset.
type policy struct {
	fs       []fsRule
	tcpPorts []uint16 // the only ports the app may connect to; nil with gatePort 0 = no TCP
}

const (
	fsRead  = unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR
	fsExec  = fsRead | unix.LANDLOCK_ACCESS_FS_EXECUTE
	fsWrite = fsRead | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_TRUNCATE |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REFER
	fsDev = unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
)

// rules: the same reach as the macOS profile. Reads: system files, tuon's
// folder, its program folder, the attachment folders; nothing else in
// your home. Writes: tuon's folder and the app's own temp folder. Runs:
// only the app (and the system's shared libraries it may load). Network:
// only the launcher's gate.
func rules(home, tmp, app string, gatePort int) policy {
	var p policy
	add := func(access uint64, paths ...string) {
		for _, path := range paths {
			p.fs = append(p.fs, fsRule{path, access})
		}
	}
	add(fsRead, "/etc", "/usr", "/bin", "/sbin", "/opt", "/sys/fs/cgroup", "/sys/devices/system/cpu", "/proc/self")
	add(fsExec, "/lib", "/lib64", "/lib32", "/usr/lib", "/usr/lib64", "/usr/libexec", "/nix/store") // loaders, libraries
	// its program folder may be read; only the app itself run (not tuon,
	// the launcher, which sits next to it)
	add(fsRead, filepath.Dir(app))
	add(fsExec, app)
	if real, err := filepath.EvalSymlinks(app); err == nil {
		add(fsRead, filepath.Dir(real))
		add(fsExec, real)
	}
	add(fsWrite, filepath.Join(home, ".config", "tuon"))
	if tmp != "" {
		add(fsWrite, tmp)
	}
	for _, f := range ReadableFolders {
		add(fsRead, filepath.Join(home, f))
	}
	add(unix.LANDLOCK_ACCESS_FS_READ_FILE, "/dev/urandom", "/dev/random", "/dev/zero")
	add(fsDev, "/dev/null", "/dev/tty")
	// the terminal tuon runs in, and only that one (not your other terminals)
	for fd := 0; fd <= 2; fd++ {
		if t, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(fd)); err == nil && filepath.Dir(t) == "/dev/pts" {
			add(fsDev, t)
		}
	}
	if gatePort > 0 && gatePort < 1<<16 {
		p.tcpPorts = []uint16{uint16(gatePort)}
	}
	return p
}
