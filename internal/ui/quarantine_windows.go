package ui

import "os"

// markDownloaded adds the Mark of the Web (zone 3 = internet), so Windows
// warns before running a file saved from a chat.
func markDownloaded(path string) error {
	return os.WriteFile(path+":Zone.Identifier", []byte("[ZoneTransfer]\r\nZoneId=3\r\n"), 0o600)
}
