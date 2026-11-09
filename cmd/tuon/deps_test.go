package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestLauncherStaysSmall: tuon runs outside the sandbox, so it may only link
// the standard library, three golang.org/x modules and tuon's own small
// packages. Anything else (even a new indirect import) fails here: its
// init() would run unsandboxed, next to the vault's key.
func TestLauncherStaysSmall(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skip("go list unavailable:", err)
	}
	allowed := []string{
		"golang.org/x/crypto/", "golang.org/x/sys/", "golang.org/x/term",
		"github.com/ralph/tuon/cmd/tuon",
		"github.com/ralph/tuon/internal/browserlogin", "github.com/ralph/tuon/internal/opener",
		"github.com/ralph/tuon/internal/paths", "github.com/ralph/tuon/internal/sandbox",
		"github.com/ralph/tuon/internal/safefile", "github.com/ralph/tuon/internal/vault",
	}
	for _, pkg := range strings.Fields(string(out)) {
		if !strings.Contains(strings.SplitN(pkg, "/", 2)[0], ".") {
			continue // the standard library (no dot in the first element)
		}
		ok := false
		for _, a := range allowed {
			if pkg == a || strings.HasPrefix(pkg, a) && (strings.HasSuffix(a, "/") || strings.HasPrefix(pkg, a+"/")) {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("the launcher links %s, which isn't on its list", pkg)
		}
	}
}
