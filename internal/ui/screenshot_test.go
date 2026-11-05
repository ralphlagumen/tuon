package ui

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// TestScreenshot renders the real View (with colors) to a PNG, so the
// design can be reviewed without a terminal.
//
//	TUON_PREVIEW=bin/preview go test ./internal/ui -run TestScreenshot
func TestScreenshot(t *testing.T) {
	dir := os.Getenv("TUON_PREVIEW")
	if dir == "" {
		t.Skip("set TUON_PREVIEW=<dir>")
	}
	dir, _ = filepath.Abs(filepath.Join("..", "..", dir))
	lipgloss.SetColorProfile(termenv.TrueColor)
	trueColor = true

	m, _, _ := newTestModel(t, 130, 36)
	m = seed(m)
	m = key(m, "3")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	img := image.NewRGBA(image.Rect(0, 0, 60, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 60; x++ {
			img.Set(x, y, color.RGBA{uint8(80 + x*2), uint8(120 + y), uint8(160 - y), 255})
		}
	}
	next, _ = m.Update(mediaMsg{ref: "img1", raw: []byte{1}, img: img})
	m = next.(Model)
	m = key(m, "up")
	m = key(m, "up")
	m = key(m, "up")
	writePNG(t, filepath.Join(dir, "screen-chat.png"), rasterScreen(m.View()))

	m = key(m, "r")
	writePNG(t, filepath.Join(dir, "screen-react.png"), rasterScreen(m.View()))
	m = key(m, "esc")
	m = key(m, "esc")
	m = key(m, "esc")
	writePNG(t, filepath.Join(dir, "screen-list.png"), rasterScreen(m.View()))
}

var sgrAny = regexp.MustCompile(`^\x1b\[([0-9;]*)m`)

// rasterScreen draws ANSI text with a 7x13 bitmap font (ASCII only;
// other glyphs show as a small box, blocks are drawn as blocks).
func rasterScreen(s string) image.Image {
	const cw, ch = 7, 14
	lines := strings.Split(s, "\n")
	cols := 0
	for _, l := range lines {
		cols = max(cols, len([]rune(stripSGR(l))))
	}
	img := image.NewRGBA(image.Rect(0, 0, cols*cw, len(lines)*ch))
	xdraw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{0, 0, 0, 255}), image.Point{}, xdraw.Src)
	def := color.RGBA{200, 200, 200, 255}
	for row, l := range lines {
		fg, bg := def, color.RGBA{0, 0, 0, 255}
		bold := false
		col := 0
		for len(l) > 0 {
			if m := sgrAny.FindStringSubmatch(l); m != nil {
				l = l[len(m[0]):]
				parts := strings.Split(m[1], ";")
				for i := 0; i < len(parts); i++ {
					switch parts[i] {
					case "", "0":
						fg, bg, bold = def, color.RGBA{0, 0, 0, 255}, false
					case "1":
						bold = true
					case "38", "48":
						if i+4 < len(parts)+0 && parts[i+1] == "2" {
							r, _ := strconv.Atoi(parts[i+2])
							g, _ := strconv.Atoi(parts[i+3])
							b, _ := strconv.Atoi(parts[i+4])
							c := color.RGBA{uint8(r), uint8(g), uint8(b), 255}
							if parts[i] == "38" {
								fg = c
							} else {
								bg = c
							}
							i += 4
						}
					}
				}
				continue
			}
			r := []rune(l)[0]
			l = l[len(string(r)):]
			x0, y0 := col*cw, row*ch
			for y := 0; y < ch; y++ {
				for x := 0; x < cw; x++ {
					img.SetRGBA(x0+x, y0+y, bg)
				}
			}
			switch {
			case r == '▀':
				for y := 0; y < ch/2; y++ {
					for x := 0; x < cw; x++ {
						img.SetRGBA(x0+x, y0+y, fg)
					}
				}
			case r == '▎' || r == '│':
				for y := 0; y < ch; y++ {
					img.SetRGBA(x0+1, y0+y, fg)
					if r == '▎' {
						img.SetRGBA(x0+2, y0+y, fg)
					}
				}
			case r == '─' || r == '┬' || r == '┴' || r == '┼':
				for x := 0; x < cw; x++ {
					img.SetRGBA(x0+x, y0+ch/2, fg)
				}
			case r < 128 && r > ' ':
				d := font.Drawer{Dst: img, Src: image.NewUniform(fg), Face: basicfont.Face7x13, Dot: fixed.P(x0, y0+11)}
				d.DrawString(string(r))
				if bold {
					d.Dot = fixed.P(x0+1, y0+11)
					d.DrawString(string(r))
				}
			case r >= 128:
				for x := 1; x < cw-1; x++ { // non-ASCII glyph placeholder
					img.SetRGBA(x0+x, y0+3, fg)
					img.SetRGBA(x0+x, y0+10, fg)
				}
			}
			col++
		}
	}
	return img
}
