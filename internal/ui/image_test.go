package ui

import (
	"image"
	"image/png"
	"os"
	"regexp"
	"testing"
)

func TestTermSafe(t *testing.T) {
	cases := map[string]string{
		"Cubes❄️💙":   "Cubes❄ 💙", // narrow symbol + marker -> symbol + space
		"✨ ICE":      "✨ ICE",    // no marker, untouched
		"ok ❤️":      "ok ❤ ",
		"plain text": "plain text",
	}
	for in, want := range cases {
		if got := termSafe(in); got != want {
			t.Errorf("termSafe(%q) = %q, want %q", in, got, want)
		}
	}
}

var sgr = regexp.MustCompile(`^\x1b\[(38|48);2;(\d+);(\d+);(\d+)m`)

func stripSGR(s string) string {
	return regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(s, "")
}

func writePNG(t *testing.T, path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
