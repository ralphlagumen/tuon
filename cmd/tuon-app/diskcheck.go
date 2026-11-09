package main

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// diskWarning: tuon's data is only protected on a stolen, switched-off
// laptop if the disk is encrypted. On macOS, say so when FileVault is off.
func diskWarning() string {
	if runtime.GOOS != "darwin" {
		return "" // no reliable check without admin rights elsewhere
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "fdesetup", "status").Output()
	if err != nil || strings.Contains(string(out), "FileVault is On") {
		return ""
	}
	return "FileVault is off: anyone with this Mac can read your chats. Turn it on in System Settings, Privacy & Security."
}
