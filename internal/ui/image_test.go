package ui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
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

func TestRenderImageShape(t *testing.T) {
	trueColor = true
	img := image.NewRGBA(image.Rect(0, 0, 400, 200)) // 2:1 landscape
	// square pixels: 40 px wide -> 20 px tall -> 10 rows
	if l := renderImage(img, 40, 40); len(l) != 10 {
		t.Fatalf("rows = %d, want 10", len(l))
	}
	tall := image.NewRGBA(image.Rect(0, 0, 100, 400)) // portrait, limited by rows
	if l := renderImage(tall, 80, 20); len(l) != 20 {
		t.Fatalf("portrait rows = %d, want 20", len(l))
	}
}

// TestPreview writes PNGs of what the terminal would show, for eyeballing.
//
//	TUON_PREVIEW=bin/preview go test ./internal/ui -run TestPreview
func TestPreview(t *testing.T) {
	dir := os.Getenv("TUON_PREVIEW")
	if dir == "" {
		t.Skip("set TUON_PREVIEW=<dir>")
	}
	dir, _ = filepath.Abs(filepath.Join("..", "..", dir))
	trueColor = true

	samples := map[string]image.Image{"text": textImage()}
	if b, err := os.ReadFile(filepath.Join(dir, "coast.jpg")); err == nil {
		if img, err := decodeImage(b); err == nil {
			samples["photo"] = img
		}
	}
	for name, img := range samples {
		for _, size := range []struct {
			tag        string
			cols, rows int
		}{{"thumb", 120, 60}, {"full", 358, 84}} {
			lines := renderImage(img, size.cols, size.rows)
			out := filepath.Join(dir, name+"-"+size.tag+".png")
			writePNG(t, out, rasterize(lines))
			t.Logf("%s: %d cols x %d rows -> %s", name+"-"+size.tag, len(lines[0]), len(lines), out)
		}
	}
}

// textImage mimics a chat screenshot: dark text on light background.
func textImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 360, 220))
	xdraw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{245, 245, 245, 255}), image.Point{}, xdraw.Src)
	d := font.Drawer{Dst: img, Src: image.NewUniform(color.RGBA{20, 20, 20, 255}), Face: basicfont.Face7x13}
	for i, s := range []string{
		"Meeting moved to 3pm tomorrow",
		"Bring the IEMOP report please",
		"Total: PHP 39.99 one-time",
		"Room 4B, 2nd floor",
		"ok see you there!",
	} {
		d.Dot = fixed.P(16, 30+i*38)
		d.DrawString(s)
	}
	for x := 0; x < 360; x++ { // a blue bar like a header
		for y := 0; y < 10; y++ {
			img.Set(x, y, color.RGBA{30, 90, 220, 255})
		}
	}
	return img
}

var sgr = regexp.MustCompile(`^\x1b\[(38|48);2;(\d+);(\d+);(\d+)m`)

// rasterize turns rendered lines back into pixels: each cell -> 8x16 px.
func rasterize(lines []string) image.Image {
	const cw, chh = 8, 16
	cols := 0
	for _, l := range lines {
		cols = max(cols, len([]rune(stripSGR(l))))
	}
	img := image.NewRGBA(image.Rect(0, 0, cols*cw, len(lines)*chh))
	for row, l := range lines {
		var fgc, bgc color.RGBA
		col := 0
		for len(l) > 0 {
			if m := sgr.FindStringSubmatch(l); m != nil {
				r, _ := strconv.Atoi(m[2])
				g, _ := strconv.Atoi(m[3])
				b, _ := strconv.Atoi(m[4])
				c := color.RGBA{uint8(r), uint8(g), uint8(b), 255}
				if m[1] == "38" {
					fgc = c
				} else {
					bgc = c
				}
				l = l[len(m[0]):]
				continue
			}
			if strings.HasPrefix(l, "\x1b[0m") {
				l = l[4:]
				continue
			}
			r := []rune(l)[0]
			l = l[len(string(r)):]
			for y := 0; y < chh; y++ {
				for x := 0; x < cw; x++ {
					c := bgc
					if r == '▀' && y < chh/2 {
						c = fgc
					}
					img.SetRGBA(col*cw+x, row*chh+y, c)
				}
			}
			col++
		}
	}
	return img
}

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
