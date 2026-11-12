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

// The start of a mouse report kept from a full read waits for its rest even
// when it comes late (a busy moment), instead of going alone as an Esc key.
func TestTtyInTailWaitsForSlowRest(t *testing.T) {
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
	p := make([]byte, 256)
	if n, _ := in.Read(p); n != 255 {
		t.Fatalf("first read = %d", n)
	}
	go func() {
		time.Sleep(80 * time.Millisecond) // the rest comes late
		_, _ = ctl.Write([]byte("[<65;133;59M"))
	}()
	n, _ := in.Read(p)
	if got := string(p[:n]); got != "\x1b[<65;133;59M" {
		t.Fatalf("second read = %q, want the whole report", got)
	}
}

func TestEscLastMovesEveryGluedEsc(t *testing.T) {
	r := "\x1b[<65;127;46M"
	for in, want := range map[string]string{
		"\x1b" + r + r:                       r + r + "\x1b",
		r + "\x1b" + r + "\x1b" + r:          r + r + r + "\x1b\x1b",
		"\x1b" + r + "\x1bOA":                r + "\x1bOA\x1b",
		r + r:                                r + r,
		"\x1b\x1b":                           "\x1b\x1b", // alt+esc stays
		"ab\x1b" + r + strings.Repeat(r, 15): "ab" + r + strings.Repeat(r, 15) + "\x1b",
	} {
		b := []byte(in)
		escLast(b)
		if string(b) != want {
			t.Errorf("escLast(%q) = %q, want %q", in, b, want)
		}
	}
}
