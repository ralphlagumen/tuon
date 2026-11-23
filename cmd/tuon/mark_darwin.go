package main

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// markDownloaded sets com.apple.quarantine (flags 0081 = "downloaded, not
// yet opened", what browsers set), so macOS warns before the file runs.
func markDownloaded(path string) error {
	return unix.Setxattr(path, "com.apple.quarantine", []byte(fmt.Sprintf("0081;%x;tuon;", time.Now().Unix())), 0)
}
