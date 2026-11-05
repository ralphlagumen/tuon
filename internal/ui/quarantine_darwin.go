package ui

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// markDownloaded flags a file saved from a chat the way browsers do
// (com.apple.quarantine), so Gatekeeper warns before running it.
func markDownloaded(path string) error {
	// flags 0081 = "downloaded, not yet opened"; then time and the app that saved it
	v := fmt.Sprintf("0081;%x;tuon;", time.Now().Unix())
	return unix.Setxattr(path, "com.apple.quarantine", []byte(v), 0)
}
