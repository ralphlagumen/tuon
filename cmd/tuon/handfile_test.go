package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ralph/tuon/internal/opener"
)

func TestHandFile(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "report.pdf")
	_ = os.WriteFile(ok, []byte("%PDF-1.4"), 0o600)
	if rep := handFile(ok); rep.Err != "" || rep.Text != "report.pdf" || string(rep.Data) != "%PDF-1.4" {
		t.Fatalf("regular file: %+v", rep)
	}
	if rep := handFile(dir); !strings.Contains(rep.Err, "folder") {
		t.Fatalf("folder: %+v", rep)
	}
	big := filepath.Join(dir, "big.bin")
	f, _ := os.Create(big)
	_ = f.Truncate(opener.MaxFile + 1)
	f.Close()
	if rep := handFile(big); !strings.Contains(rep.Err, "25 MB") || rep.Data != nil {
		t.Fatalf("too big: %v (%d bytes)", rep.Err, len(rep.Data))
	}
	if rep := handFile("/dev/zero"); rep.Err == "" {
		t.Fatal("a device isn't a file")
	}
}
