//go:build !darwin && !windows

package ui

// markDownloaded: Linux has no standard "downloaded" flag that stops a file
// from running; saved files are written without the execute bit.
func markDownloaded(string) error { return nil }
