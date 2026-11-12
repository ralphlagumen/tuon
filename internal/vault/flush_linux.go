package vault

import "golang.org/x/sys/unix"

// flushInput drops typed-but-unread input (tcflush TCIFLUSH).
func flushInput(fd int) { _ = unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH) }
