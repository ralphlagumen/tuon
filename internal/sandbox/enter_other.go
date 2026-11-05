//go:build !darwin

package sandbox

// Enter: no sandbox on this system yet (Linux Landlock is planned).
func Enter() string { return "" }
