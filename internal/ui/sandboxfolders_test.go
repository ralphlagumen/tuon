package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHomeSuggestionsInSandbox: the sandbox can't list your home folder,
// so "@~/" offers the folders it can read.
func TestHomeSuggestionsInSandbox(t *testing.T) {
	t.Setenv("TUON_SANDBOXED", "1")
	home, _ := os.UserHomeDir()
	var got []string
	for _, s := range pathSuggestions("~/") {
		got = append(got, s.Label)
	}
	if strings.Join(got, " ") != "~/Downloads/ ~/Desktop/ ~/Documents/ ~/Pictures/" {
		t.Fatalf("@~/ suggests %v", got)
	}
	if s := pathSuggestions("~/Do"); len(s) != 2 || s[0].Path != filepath.Join(home, "Downloads") {
		t.Fatalf("@~/Do suggests %v", s)
	}
}
