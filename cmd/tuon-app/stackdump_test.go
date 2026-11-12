//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStackDumpOnUSR1(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, ".config", "tuon"), 0o700) // made at setup in real use
	dumpStacksOnSignal()
	_ = syscall.Kill(os.Getpid(), syscall.SIGUSR1)
	logPath := filepath.Join(home, ".config", "tuon", "tuon.log")
	for i := 0; i < 50; i++ {
		if b, _ := os.ReadFile(logPath); strings.Contains(string(b), "goroutine dump") && strings.Contains(string(b), "TestStackDumpOnUSR1") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no goroutine dump in tuon.log")
}
