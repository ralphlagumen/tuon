package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/ralph/tuon/internal/sandbox"
)

// The chat app gets a terminal of its own (a pseudo-terminal), and the
// launcher relays keys and output between it and yours. Sharing your
// terminal would let the app type into your shell after tuon quits
// (ioctl TIOCSTI): with its own, anything it types lands in a terminal
// that closes with it. sudo does the same (use_pty).

// openPty returns a new pseudo-terminal's controller and the app's end.
func openPty() (ctl, app *os.File, err error) {
	ctl, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	fail := func(e error) (*os.File, *os.File, error) { ctl.Close(); return nil, nil, e }
	if err := ioctl(ctl.Fd(), unix.TIOCPTYGRANT, 0); err != nil {
		return fail(err)
	}
	if err := ioctl(ctl.Fd(), unix.TIOCPTYUNLK, 0); err != nil {
		return fail(err)
	}
	var name [128]byte
	if err := ioctl(ctl.Fd(), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))); err != nil {
		return fail(err)
	}
	n := 0
	for n < len(name) && name[n] != 0 {
		n++
	}
	app, err = os.OpenFile(string(name[:n]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return fail(err)
	}
	return ctl, app, nil
}

func ioctl(fd, req, arg uintptr) error {
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, fd, req, arg); e != 0 {
		return e
	}
	return nil
}

// runOnPty starts cmd on its own terminal and relays until it exits. ok is
// false when there's no terminal to relay (stdin isn't one): then the
// caller runs it the usual way.
func runOnPty(cmd *exec.Cmd, started func()) (code int, ok bool, err error) {
	in := int(os.Stdin.Fd())
	if !term.IsTerminal(in) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return 0, false, nil
	}
	ctl, app, err := openPty()
	if err != nil {
		return 0, false, nil // fall back rather than not start
	}
	defer ctl.Close()
	resize := func() {
		if ws, err := unix.IoctlGetWinsize(in, unix.TIOCGWINSZ); err == nil {
			_ = unix.IoctlSetWinsize(int(ctl.Fd()), unix.TIOCSWINSZ, ws)
		}
	}
	resize()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = app, app, app
	// sandbox-test checks the app's terminal isn't this one
	if st, err := os.Stdin.Stat(); err == nil {
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%d", sandbox.ParentTTYEnv, sys.Rdev))
		}
	}
	// its own session, with the new terminal as its controlling terminal
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	old, err := term.MakeRaw(in) // keys go through untouched; the app's terminal handles them
	if err != nil {
		app.Close()
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		cmd.SysProcAttr = nil
		return 0, false, nil
	}
	defer term.Restore(in, old)
	err = cmd.Start()
	started()
	// keep our copy of the app's end open until its output is read: on
	// macOS, the last close of that end throws away what's still unread
	defer app.Close()
	if err != nil {
		return 0, true, err
	}
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	relay(in, int(ctl.Fd()), winch, resize, done)
	err = <-done
	// anything the app left running in its session goes with it
	_ = unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), true, nil
	}
	return 0, true, err
}

// relay copies your keys to the app's terminal and its output to yours,
// with poll (no reader left behind once the app is gone, so the launcher
// can read the keyboard again afterwards).
func relay(in, ctl int, winch chan os.Signal, resize func(), done chan error) {
	out := int(os.Stdout.Fd())
	buf := make([]byte, 32<<10)
	var held []byte // an unfinished escape sequence or character (completeLen)
	exited := false
	for {
		select {
		case <-winch:
			resize()
		case err := <-done:
			done <- err // for the caller
			exited = true
		default:
		}
		fds := []unix.PollFd{{Fd: int32(ctl), Events: unix.POLLIN}}
		if !exited {
			fds = append(fds, unix.PollFd{Fd: int32(in), Events: unix.POLLIN})
		}
		wait := 100
		if len(held) > 0 {
			wait = 25 // the rest of a sequence comes at once; a lone Esc is a key
		}
		n, err := unix.Poll(fds, wait)
		if err != nil && err != unix.EINTR {
			return
		}
		if n == 0 {
			if len(held) > 0 {
				writeAll(ctl, held)
				held = held[:0]
			}
			if exited {
				return // its output is all drained
			}
			continue
		}
		if fds[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) != 0 {
			m, err := unix.Read(ctl, buf)
			if m > 0 {
				writeAll(out, buf[:m])
			}
			if err != nil || m == 0 {
				if exited {
					return
				}
				// the app closed its terminal: wait for it to exit
				time.Sleep(20 * time.Millisecond)
			}
		}
		if len(fds) > 1 && fds[1].Revents&unix.POLLIN != 0 {
			if m, err := unix.Read(in, buf); m > 0 {
				data := append(held, buf[:m]...)
				k := completeLen(data)
				writeAll(ctl, data[:k])
				held = append(held[:0:0], data[k:]...)
			} else if err != nil {
				return
			}
		}
	}
}

func writeAll(fd int, b []byte) {
	for len(b) > 0 {
		n, err := unix.Write(fd, b)
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		if err != nil || n <= 0 {
			return
		}
		b = b[n:]
	}
}
