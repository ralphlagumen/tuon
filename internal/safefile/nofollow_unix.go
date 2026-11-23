//go:build unix

package safefile

import "syscall"

// noFollow: open fails on a link instead of following it.
const noFollow = syscall.O_NOFOLLOW
