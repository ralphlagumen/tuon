package ui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"strings"

	termimg "github.com/blacktop/go-termimg"
	"golang.org/x/image/draw"
)

// Real photos in terminals that speak the kitty graphics protocol
// (Ghostty, kitty, WezTerm). Elsewhere photos are View photo buttons.
//
// How it fits a full-screen TUI: the photo is sent to the terminal once as
// PNG data in the escape sequence (never a file on disk), with a "virtual
// placement" of N x M cells.
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
	case "off", "blocks": // "blocks" kept for old setups
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
	next uint32
	ids  map[string]uint32 // ref|cols|rows -> image ID
}

// newKittyImages: photos go to the terminal as data inside the escape
// sequence, never through a file, so no readable copy lands on disk.
func newKittyImages(out io.Writer) *kittyImages {
	return &kittyImages{out: out, next: 1, ids: map[string]uint32{}}
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
		data, err := kittyPNG(img)
		if err != nil {
			return nil, err
		}
		id = k.next
		k.next++
		var b strings.Builder
		writeKittyData(&b, id, data)
		fmt.Fprintf(&b, "\x1b_Ga=p,U=1,i=%d,c=%d,r=%d,q=2\x1b\\", id, cols, rows) // virtual placement
		if _, err := io.WriteString(k.out, b.String()); err != nil {
			return nil, err
		}
		k.ids[key] = id
	}
	return placeholderLines(id, cols, rows), nil
}

// kittyPNG: a downsized PNG of the photo, in memory.
func kittyPNG(img image.Image) ([]byte, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if s := max(w, h); s > kittyMaxSide {
		w, h = w*kittyMaxSide/s, h*kittyMaxSide/s
	}
	dst := image.NewRGBA(image.Rect(0, 0, max(1, w), max(1, h)))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeKittyData transmits PNG data directly (t=d), in the 4096-byte
// base64 chunks the protocol requires.
func writeKittyData(b *strings.Builder, id uint32, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	for first := true; first || enc != ""; first = false {
		n := min(4096, len(enc))
		chunk := enc[:n]
		enc = enc[n:]
		more := 0
		if enc != "" {
			more = 1
		}
		if first {
			fmt.Fprintf(b, "\x1b_Ga=t,f=100,t=d,i=%d,q=2,m=%d;%s\x1b\\", id, more, chunk)
		} else {
			fmt.Fprintf(b, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}
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
