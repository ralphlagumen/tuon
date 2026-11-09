package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWebLink(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://example.com/x": true, "http://example.com": true,
		"file:///etc/hosts": false, "-a Terminal": false, "javascript:alert(1)": false,
		"https://": false, "x-apple.systempreferences:": false, "/Applications/Calculator.app": false,
	} {
		if _, err := webLink(raw); (err == nil) != ok {
			t.Errorf("webLink(%q) ok=%v", raw, err == nil)
		}
	}
}

func TestPhotoFile(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	png := filepath.Join(dir, "a.png")
	_ = os.WriteFile(png, []byte("\x89PNG\r\n\x1a\n...."), 0o600)
	script := filepath.Join(dir, "b.png")
	_ = os.WriteFile(script, []byte("#!/bin/sh\necho hi\n"), 0o700)
	cmdFile := filepath.Join(dir, "c.command")
	_ = os.WriteFile(cmdFile, []byte("\x89PNG\r\n\x1a\n"), 0o700)
	outside := filepath.Join(t.TempDir(), "d.png")
	_ = os.WriteFile(outside, []byte("\x89PNG\r\n\x1a\n...."), 0o600)
	link := filepath.Join(dir, "e.png")
	_ = os.Symlink(outside, link)
	for p, ok := range map[string]bool{png: true, script: false, cmdFile: false, outside: false, link: false, dir: false, filepath.Join(dir, "missing.png"): false} {
		if _, err := photoFile(dir, p); (err == nil) != ok {
			t.Errorf("photoFile(%s) ok=%v: %v", filepath.Base(p), err == nil, err)
		}
	}
}

func TestWithin(t *testing.T) {
	for _, c := range []struct {
		dir, p string
		want   bool
	}{{"/a/b", "/a/b/c", true}, {"/a/b", "/a/b", false}, {"/a/b", "/a/bc", false}, {"/a/b", "/a/b/../c", false}, {"/a/b", "/x", false}} {
		if within(c.dir, filepath.Clean(c.p)) != c.want {
			t.Errorf("within(%s, %s) != %v", c.dir, c.p, c.want)
		}
	}
}
