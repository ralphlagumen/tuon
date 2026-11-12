package main

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// openTestPty: a pseudo-terminal pair, like the launcher's openPty.
func openTestPty() (ctl, app *os.File, err error) {
	ctl, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	for _, req := range []uint{unix.TIOCPTYGRANT, unix.TIOCPTYUNLK} {
		if _, _, e := unix.Syscall(unix.SYS_IOCTL, ctl.Fd(), uintptr(req), 0); e != 0 {
			ctl.Close()
			return nil, nil, e
		}
	}
	var name [128]byte
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, ctl.Fd(), uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&name[0]))); e != 0 {
		ctl.Close()
		return nil, nil, e
	}
	n := 0
	for n < len(name) && name[n] != 0 {
		n++
	}
	app, err = os.OpenFile(string(name[:n]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		ctl.Close()
	}
	return ctl, app, err
}

// A lone Esc that ends a full read is handed over at once, not held until
// the next key (it looked like a freeze when a fast scroll stopped).
func TestTtyInLoneEscAtEndOfFullRead(t *testing.T) {
	ctl, app, err := openTestPty()
	if err != nil {
		t.Skip("no pty:", err)
	}
	defer ctl.Close()
	defer app.Close()
	if _, err := term.MakeRaw(int(app.Fd())); err != nil {
		t.Fatal(err)
	}
	in := &ttyIn{File: app}
	_, _ = ctl.Write([]byte(strings.Repeat("a", 255) + "\x1b"))
	time.Sleep(20 * time.Millisecond)

	p := make([]byte, 256) // Bubble Tea's read size
	n, err := in.Read(p)
	if err != nil || n != 255 {
		t.Fatalf("first read = %d, %v; want the 255 letters (Esc kept back)", n, err)
	}
	got := make(chan string, 1)
	go func() {
		n, _ := in.Read(p)
		got <- string(p[:n])
	}()
	select {
	case s := <-got:
		if s != "\x1b" {
			t.Fatalf("second read = %q, want the Esc", s)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("the Esc was held until another key")
	}
}
