package ui

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	termimg "github.com/blacktop/go-termimg"
	"golang.org/x/image/draw"
)

// Real photos in terminals that speak the kitty graphics protocol
// (Ghostty, kitty, WezTerm). Elsewhere Tuon falls back to pixel blocks.
//
// How it fits a full-screen TUI: the photo is sent to the terminal once as
// a PNG file path (tiny message), with a "virtual placement" of N x M cells.
// The screen then contains placeholder characters (U+10EEEE) colored with
// the image ID. The terminal draws the photo wherever those characters are,
// so photos scroll and redraw like ordinary text.

const kittyMaxSide = 1600 // px; plenty for a sharp 360p+ view

var kittyMode = detectKitty()

// RealPhotos reports whether this terminal can show real photos.
func RealPhotos() bool { return kittyMode }

func detectKitty() bool {
	switch os.Getenv("TUON_IMAGES") {
	case "kitty":
		return true
	case "blocks":
		return false
	}
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return true
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "ghostty", "WezTerm":
		return true
	}
	t := os.Getenv("TERM")
	return t == "xterm-ghostty" || t == "xterm-kitty"
}

type kittyImages struct {
	out  io.Writer
	dir  string
	next uint32
	ids  map[string]uint32 // ref|cols|rows -> image ID
	pngs map[string]string // ref -> cached PNG path
}

func newKittyImages(out io.Writer, dir string) *kittyImages {
	return &kittyImages{out: out, dir: dir, next: 1, ids: map[string]uint32{}, pngs: map[string]string{}}
}

// place makes sure the photo is sent and placed at cols x rows cells, and
// returns the placeholder lines to draw.
func (k *kittyImages) place(ref string, img image.Image, cols, rows int) ([]string, error) {
	if cols < 1 || rows < 1 {
		return nil, nil
	}
	key := fmt.Sprintf("%s|%d|%d", ref, cols, rows)
	id, ok := k.ids[key]
	if !ok {
		path, err := k.pngFor(ref, img)
		if err != nil {
			return nil, err
		}
		id = k.next
		k.next++
		// transmit by file path, then a virtual placement of cols x rows
		seq := fmt.Sprintf("\x1b_Ga=t,f=100,t=f,i=%d,q=2;%s\x1b\\", id, base64.StdEncoding.EncodeToString([]byte(path))) +
			fmt.Sprintf("\x1b_Ga=p,U=1,i=%d,c=%d,r=%d,q=2\x1b\\", id, cols, rows)
		if _, err := io.WriteString(k.out, seq); err != nil {
			return nil, err
		}
		k.ids[key] = id
	}
	return placeholderLines(id, cols, rows), nil
}

// pngFor writes a downsized PNG of the photo once and returns its path.
func (k *kittyImages) pngFor(ref string, img image.Image) (string, error) {
	if p, ok := k.pngs[ref]; ok {
		return p, nil
	}
	if err := os.MkdirAll(k.dir, 0o700); err != nil {
		return "", err
	}
	sum := sha1.Sum([]byte(ref))
	path := filepath.Join(k.dir, hex.EncodeToString(sum[:8])+".png")
	if _, err := os.Stat(path); err != nil {
		b := img.Bounds()
		w, h := b.Dx(), b.Dy()
		if s := max(w, h); s > kittyMaxSide {
			w, h = w*kittyMaxSide/s, h*kittyMaxSide/s
		}
		dst := image.NewRGBA(image.Rect(0, 0, max(1, w), max(1, h)))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return "", err
		}
		err = png.Encode(f, dst)
		f.Close()
		if err != nil {
			return "", err
		}
	}
	k.pngs[ref] = path
	return path, nil
}

// clearAll removes our photos from the terminal's memory (on quit).
func (k *kittyImages) clearAll() {
	if len(k.ids) > 0 {
		_, _ = io.WriteString(k.out, "\x1b_Ga=d,d=A,q=2\x1b\\")
	}
}

// placeholderLines: each row starts with a placeholder carrying its row and
// column-0 marks; the rest of the row inherits them. The foreground color
// carries the image ID.
func placeholderLines(id uint32, cols, rows int) []string {
	color := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", (id>>16)&0xFF, (id>>8)&0xFF, id&0xFF)
	rest := strings.Repeat(termimg.PLACEHOLDER_CHAR, cols-1)
	lines := make([]string, rows)
	for r := 0; r < rows; r++ {
		lines[r] = color + termimg.CreatePlaceholder(uint16(r), 0, byte(id>>24)) + rest + "\x1b[39m"
	}
	return lines
}

// kittyBox picks a cell box for a photo that keeps its shape, assuming
// cells are about twice as tall as wide.
func kittyBox(img image.Image, maxCols, maxRows int) (int, int) {
	b := img.Bounds()
	iw, ih := float64(b.Dx()), float64(b.Dy())
	if iw == 0 || ih == 0 || maxCols < 1 || maxRows < 1 {
		return 0, 0
	}
	cols := maxCols
	rows := int(float64(cols)*ih/iw/2 + 0.5)
	if rows > maxRows {
		rows = maxRows
		cols = int(float64(rows)*2*iw/ih + 0.5)
	}
	return max(1, min(cols, maxCols)), max(1, rows)
}
