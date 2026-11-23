//go:build !darwin

package main

// markDownloaded: no system-wide "downloaded" mark off macOS. The file is
// saved 0600, so it can't be run without someone making it executable.
func markDownloaded(string) error { return nil }
