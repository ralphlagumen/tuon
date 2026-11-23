package main

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

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
