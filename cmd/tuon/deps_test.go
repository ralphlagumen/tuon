package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestLauncherStaysSmall: tuon runs outside the sandbox, so it must not
// link the chat app's code. A dependency linked here would run its init()
// unsandboxed, next to the vault's key.
func TestLauncherStaysSmall(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skip("go list unavailable:", err)
	}
	banned := []string{"mautrix", "whatsmeow", "libsignal", "sqlite", "keyring", "zerolog", "bubbletea",
		"/internal/store", "/internal/auth", "/internal/connector", "/internal/ui", "/internal/keystore"}
	for _, pkg := range strings.Fields(string(out)) {
		for _, b := range banned {
			if strings.Contains(pkg, b) {
				t.Errorf("the launcher links %s (matched %q)", pkg, b)
			}
		}
	}
}
