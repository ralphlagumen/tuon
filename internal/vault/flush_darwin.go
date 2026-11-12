package vault

import "golang.org/x/sys/unix"

// fread is FREAD from <sys/fcntl.h>: TIOCFLUSH's "input side" flag.
const fread = 0x1

// flushInput drops typed-but-unread input (TIOCFLUSH, read side).
func flushInput(fd int) { _ = unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, fread) }
