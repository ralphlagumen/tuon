//go:build !windows

package main

import (
	"os"
	"os/signal"
	"runtime"
	"syscall"
)

// dumpStacksOnSignal: `kill -USR1 <tuon-app pid>` writes every goroutine's
// stack to tuon.log, without stopping tuon. For diagnosing a freeze: the
// stacks hold function names and file lines, never message text.
func dumpStacksOnSignal() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGUSR1)
	go func() {
		for range sigs {
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			if l, err := openLog(); err == nil {
				l.Warn().Str("stacks", string(buf[:n])).Msg("goroutine dump (SIGUSR1)")
			}
		}
	}()
}
