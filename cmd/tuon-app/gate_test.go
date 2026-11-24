package main

import "testing"

func TestReleaseName(t *testing.T) {
	for s, want := range map[string]bool{"v0.5.0": true, "v1.2": true, "v0.5.0\x1b[2J": false, "0.5.0": false, "v": false, "vX.Y": false} {
		if releaseName(s) != want {
			t.Errorf("%q: want %v", s, want)
		}
	}
}
