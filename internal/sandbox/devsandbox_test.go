package sandbox

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDevSandboxHolds runs only under scripts/devsandbox (check.sh): it
// fails if dependency code running in tests could reach the SSH agent,
// home folder secrets, the internet, the repo or Go's normal build cache.
func TestDevSandboxHolds(t *testing.T) {
	mode := os.Getenv("TUON_DEVSANDBOX")
	if mode == "" {
		t.Skip("not under scripts/devsandbox")
	}
	if os.Getenv("SSH_AUTH_SOCK") != "" {
		t.Error("SSH_AUTH_SOCK reached the tests")
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{filepath.Join(home, ".ssh"), filepath.Join(home, "Documents"), filepath.Join(home, "Desktop")} {
		if _, err := os.ReadDir(p); err == nil {
			t.Errorf("read %s", p)
		}
	}
	if f, err := os.Create("../../devsandbox-probe"); err == nil { // the repo root
		f.Close()
		os.Remove(f.Name())
		t.Error("wrote into the repo")
	}
	normal := filepath.Join(home, "Library", "Caches", "go-build", "devsandbox-probe")
	if err := os.WriteFile(normal, nil, 0o600); err == nil {
		os.Remove(normal)
		t.Error("wrote Go's normal build cache")
	}
	// a Unix socket of our own: listening is fine, connecting is refused
	sock := filepath.Join(os.TempDir(), "devsandbox-probe.sock")
	os.Remove(sock)
	if l, err := net.Listen("unix", sock); err == nil {
		defer l.Close()
		defer os.Remove(sock)
		if c, err := net.DialTimeout("unix", sock, time.Second); err == nil {
			c.Close()
			t.Error("connected to a Unix socket")
		}
	}
	if mode == "offline" {
		if c, err := net.DialTimeout("tcp", "1.1.1.1:443", 3*time.Second); err == nil {
			c.Close()
			t.Error("reached the internet")
		}
	}
}
