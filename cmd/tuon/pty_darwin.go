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

	"github.com/ralph/tuon/internal/paths"
	"github.com/ralph/tuon/internal/sandbox"
	"github.com/ralph/tuon/internal/seq"
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
//
// The two directions run apart (keys on their own goroutine): in one loop,
// a write to your terminal that waited (Terminal.app busy drawing) stopped
// the keys being read, Terminal.app then waited to hand over more keys
// (mouse reports while scrolling fast) and stopped taking output: all three
// stuck for good (2026-11-12, sampled: launcher and app both in write).
func relay(in, ctl int, winch chan os.Signal, resize func(), done chan error) {
	out := int(os.Stdout.Fd())
	_ = unix.SetNonblock(ctl, true) // the keys side must be able to give up
	stop := make(chan struct{})
	keysDone := make(chan struct{})
	trace := seq.OpenTrace(paths.Dir(), "launcher") // TUON_KEYTRACE=1
	go func() { relayKeys(in, ctl, stop, trace); close(keysDone) }()
	defer func() { close(stop); <-keysDone }()

	buf := make([]byte, 32<<10)
	exited := false
	for {
		select {
		case <-winch:
			resize()
		case err := <-done:
			done <- err // for the caller
			exited = true
		case <-keysDone:
			keysDone = nil // keyboard gone: keep showing output until the app exits
		default:
		}
		fds := []unix.PollFd{{Fd: int32(ctl), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, 100)
		if err != nil && err != unix.EINTR {
			return
		}
		if n == 0 {
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
			if (err != nil && err != unix.EAGAIN) || m == 0 {
				if exited {
					return
				}
				// the app closed its terminal: wait for it to exit
				time.Sleep(20 * time.Millisecond)
			}
		}
	}
}

// relayKeys copies keys to the app until stop. A read never ends inside an
// escape sequence or character (seq.CompleteLen); an unfinished tail waits
// at most holdFor (a lone Esc is a real key), timed from when it was held,
// so it goes on time whatever the app is doing (audit A13-11).
func relayKeys(in, ctl int, stop chan struct{}, trace *seq.Trace) {
	const holdFor = 25 * time.Millisecond
	// escWaitMax: an Esc set aside during a scroll goes after this at most
	const escWaitMax = 750 * time.Millisecond
	buf := make([]byte, 32<<10)
	var held []byte
	var heldAt time.Time
	// escAside: an Esc pressed while scroll reports kept coming. Sent with
	// the next report it reads as alt+Esc and the report as typed text
	// ("[<65;104;52M" in the box), so it waits until the keys go quiet and
	// is then sent on its own.
	var escAside bool
	var asideAt time.Time
	for {
		select {
		case <-stop:
			return
		default:
		}
		if len(held) > 0 && time.Since(heldAt) >= holdFor {
			if !writeKeysT(trace, ctl, held, stop) {
				return
			}
			held = held[:0]
		}
		if escAside && time.Since(asideAt) >= escWaitMax {
			if !writeKeysT(trace, ctl, []byte{0x1b}, stop) {
				return
			}
			escAside = false
		}
		wait := 100
		if len(held) > 0 {
			wait = max(1, int((holdFor-time.Since(heldAt))/time.Millisecond)+1)
		}
		if escAside {
			wait = min(wait, int(holdFor/time.Millisecond))
		}
		fds := []unix.PollFd{{Fd: int32(in), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, wait)
		if err != nil && err != unix.EINTR {
			return
		}
		if n == 0 && escAside && len(held) == 0 { // quiet: now the Esc is a key of its own
			if !writeKeysT(trace, ctl, []byte{0x1b}, stop) {
				return
			}
			escAside = false
			continue
		}
		if n == 0 || fds[0].Revents&unix.POLLIN == 0 {
			if n > 0 && fds[0].Revents&(unix.POLLHUP|unix.POLLERR) != 0 {
				return
			}
			continue
		}
		m, err := unix.Read(in, buf)
		if m > 0 {
			trace.Bytes("in", buf[:m])
		}
		if m <= 0 {
			if err != nil && err != unix.EINTR && err != unix.EAGAIN {
				return
			}
			continue
		}
		if len(held) == 1 && held[0] == 0x1b && buf[0] == 0x1b && !escAside {
			// a lone Esc, then straight away another sequence: set it aside
			held, escAside, asideAt = held[:0], true, time.Now()
		}
		data := append(held, buf[:m]...)
		k := seq.CompleteLen(data)
		if out, found := dropGluedEsc(data[:k]); found { // the same, inside one read
			if !escAside {
				escAside, asideAt = true, time.Now()
			}
			data, k = append(out, data[k:]...), len(out)
		}
		if !writeKeysT(trace, ctl, data[:k], stop) {
			return
		}
		if len(held) == 0 || k > 0 { // a new hold starts its own clock
			heldAt = time.Now()
		}
		held = append(held[:0:0], data[k:]...)
	}
}

// dropGluedEsc takes out each lone Esc directly followed by another escape
// sequence (ESC ESC [ or ESC ESC O): an Esc key pressed amid mouse reports.
func dropGluedEsc(b []byte) ([]byte, bool) {
	var out []byte
	found := false
	for i := 0; i < len(b); i++ {
		if b[i] == 0x1b && i+2 < len(b) && b[i+1] == 0x1b && (b[i+2] == '[' || b[i+2] == 'O') {
			if !found {
				out = append(out, b[:i]...)
				found = true
			}
			continue
		}
		if found {
			out = append(out, b[i])
		}
	}
	if !found {
		return b, false
	}
	return out, true
}

// writeKeysT: writeKeys, recorded in the key trace.
func writeKeysT(trace *seq.Trace, fd int, b []byte, stop chan struct{}) bool {
	trace.Bytes("to-app", b)
	return writeKeys(fd, b, stop)
}

// writeKeys writes to the app's (non-blocking) terminal, waiting while it
// is full, but never past stop. false: stopped or broken.
func writeKeys(fd int, b []byte, stop chan struct{}) bool {
	for len(b) > 0 {
		n, err := unix.Write(fd, b)
		if n > 0 {
			b = b[n:]
			continue
		}
		switch err {
		case unix.EINTR:
			continue
		case unix.EAGAIN:
			select {
			case <-stop:
				return false
			default:
			}
			fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
			_, _ = unix.Poll(fds, 100)
		default:
			return false
		}
	}
	return true
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
