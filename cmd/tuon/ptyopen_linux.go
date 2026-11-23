package main

import (
	"os"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPty returns a new pseudo-terminal's controller and the app's end.
func openPty() (ctl, app *os.File, err error) {
	ctl, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	fail := func(e error) (*os.File, *os.File, error) { ctl.Close(); return nil, nil, e }
	unlock := int32(0)
	if err := ioctl(ctl.Fd(), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); err != nil {
		return fail(err)
	}
	var n uint32
	if err := ioctl(ctl.Fd(), unix.TIOCGPTN, uintptr(unsafe.Pointer(&n))); err != nil {
		return fail(err)
	}
	app, err = os.OpenFile("/dev/pts/"+strconv.FormatUint(uint64(n), 10), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return fail(err)
	}
	return ctl, app, nil
}
