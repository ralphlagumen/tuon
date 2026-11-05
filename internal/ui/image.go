package ui

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Photos: decoded safely here, then drawn as real photos where the terminal
// can (kitty.go), or opened in the system viewer.

var trueColor = detectTrueColor()

func detectTrueColor() bool {
	ct := os.Getenv("COLORTERM")
	if ct == "truecolor" || ct == "24bit" {
		return true
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app", "WezTerm", "ghostty", "vscode", "Hyper":
		return true
	}
	return strings.Contains(os.Getenv("TERM"), "kitty") || strings.Contains(os.Getenv("TERM"), "ghostty")
}

// maxPixels refuses images that claim absurd sizes: a tiny file can say it
// is 100,000 x 100,000 and eat all memory when decoded.
const maxPixels = 50_000_000

func decodeImage(b []byte) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("photo is too large to show (%dx%d)", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	// big photos are shrunk on arrival: nothing on screen needs more, and a
	// decoded 4000px photo alone would take 60 MB
	const maxSide = 1600
	bd := img.Bounds()
	if s := max(bd.Dx(), bd.Dy()); s > maxSide {
		dst := image.NewRGBA(image.Rect(0, 0, max(1, bd.Dx()*maxSide/s), max(1, bd.Dy()*maxSide/s)))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, bd, draw.Src, nil)
		return dst, nil
	}
	return img, nil
}

// to256rgb maps a color to the xterm 6x6x6 cube or the gray ramp, for
// terminals without 24-bit color.
func to256rgb(r, g, b int) int {
	if max(r, g, b)-min(r, g, b) < 12 { // gray
		if r < 8 {
			return 16
		}
		if r > 238 {
			return 231
		}
		return 232 + (r-8)*24/231
	}
	q := func(v int) int { return (v*5 + 127) / 255 }
	return 16 + 36*q(r) + 6*q(g) + q(b)
}
