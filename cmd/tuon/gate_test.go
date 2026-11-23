package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ralph/tuon/internal/netgate"
)

// TestGateWiring: idle follows your last input; an alarm reaches the app's
// pipe and net.log, which also gets the run's traffic line.
func TestGateWiring(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	gt, err := startGate()
	if err != nil {
		t.Fatal(err)
	}
	if gt.g.Idle() {
		t.Fatal("just started: not idle")
	}
	lastInput.Store(time.Now().Add(-netgate.IdleAfter - time.Second).UnixNano())
	if !gt.g.Idle() {
		t.Fatal("no input for IdleAfter: idle")
	}
	gt.g.OnUnusual("tuon uploaded 3.0 MB while you were away from the keyboard")
	line, err := bufio.NewReader(gt.events).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "unusual tuon uploaded 3.0 MB") {
		t.Fatalf("app got %q, %v", line, err)
	}
	gt.g.OnCert("gateway.facebook.com", "Totally Legit Inspection CA")
	line, err = bufio.NewReader(gt.events).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "cert gateway.facebook.com's certificate comes from Totally Legit Inspection CA") {
		t.Fatalf("app got %q, %v", line, err)
	}
	gt.stop()
	b, _ := os.ReadFile(gateLog())
	if !strings.Contains(string(b), "CERT gateway.facebook.com") {
		t.Fatalf("net.log has no CERT line:\n%s", b)
	}
	if !strings.Contains(string(b), "UNUSUAL tuon uploaded 3.0 MB") || !strings.Contains(string(b), "up 0 B, down 0 B, up while away 0 B") {
		t.Fatalf("net.log:\n%s", b)
	}
}
