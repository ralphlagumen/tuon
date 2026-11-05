package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Images are drawn with block characters in 24-bit color. Pixelated on
// purpose, but you can tell what the photo is.

var trueColor = detectTrueColor()

// thumbCache holds rendered thumbnails (photos never change). Only touched
// from Bubble Tea's update/view goroutine.
var thumbCache = map[string][]string{}

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

func decodeImage(b []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(b))
	return img, err
}

// renderImage fits img into at most cols x rows cells, keeping its shape.
//
// Look: square pixels with hard edges and true colors, like pixel-art
// portraits: blocky up close, clear from a normal distance.
// Each cell is the upper half block "▀" (foreground = top pixel,
// background = bottom pixel). A cell is about twice as tall as it is wide,
// so each pixel comes out square.
func renderImage(img image.Image, cols, rows int) []string {
	if img == nil || cols < 1 || rows < 1 {
		return nil
	}
	b := img.Bounds()
	iw, ih := float64(b.Dx()), float64(b.Dy())
	if iw == 0 || ih == 0 {
		return nil
	}
	scale := min(float64(cols)/iw, float64(rows*2)/ih)
	pw, ph := max(1, int(iw*scale+0.5)), max(2, int(ih*scale+0.5))
	pw = min(pw, cols)
	if ph%2 == 1 {
		ph++
	}

	// BiLinear averages every source pixel under each target pixel, so each
	// block gets the true color of its area (no noise, no blur).
	dst := image.NewRGBA(image.Rect(0, 0, pw, ph))
	draw.BiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)

	lines := make([]string, 0, ph/2)
	var sb strings.Builder
	for y := 0; y < ph; y += 2 {
		sb.Reset()
		var lastT, lastB color.RGBA
		for x := 0; x < pw; x++ {
			t, bt := dst.RGBAAt(x, y), dst.RGBAAt(x, y+1)
			if x == 0 || t != lastT {
				sb.WriteString(fg(t))
			}
			if x == 0 || bt != lastB {
				sb.WriteString(bg(bt))
			}
			lastT, lastB = t, bt
			sb.WriteString("▀")
		}
		sb.WriteString("\x1b[0m")
		lines = append(lines, sb.String())
	}
	return lines
}

func fg(c color.RGBA) string {
	if trueColor {
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.R, c.G, c.B)
	}
	return fmt.Sprintf("\x1b[38;5;%dm", to256(c))
}

func bg(c color.RGBA) string {
	if trueColor {
		return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", c.R, c.G, c.B)
	}
	return fmt.Sprintf("\x1b[48;5;%dm", to256(c))
}

// to256 maps a color to the xterm 6x6x6 cube or the gray ramp.
func to256(c color.RGBA) int { return to256rgb(int(c.R), int(c.G), int(c.B)) }

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
