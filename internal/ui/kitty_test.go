package ui

import (
	"bytes"
	"encoding/base64"
	"image"
	"strings"
	"testing"

	termimg "github.com/blacktop/go-termimg"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestKittyPlaceholdersAreExactWidth(t *testing.T) {
	lines := placeholderLines(7, 30, 4)
	if len(lines) != 4 {
		t.Fatalf("rows = %d", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != 30 {
			t.Fatalf("row %d width = %d, want 30", i, w)
		}
		if !strings.HasPrefix(l, "\x1b[38;2;0;0;7m") || !strings.Contains(l, termimg.PLACEHOLDER_CHAR) {
			t.Fatalf("row %d not colored with image id: %q", i, l)
		}
	}
}

func TestKittySendsOnce(t *testing.T) {
	var out bytes.Buffer
	k := newKittyImages(&out)
	img := image.NewRGBA(image.Rect(0, 0, 3000, 2000))

	if _, err := k.place("ref1", img, 40, 13); err != nil {
		t.Fatal(err)
	}
	first := out.String()
	if !strings.Contains(first, "a=t,f=100,t=d,i=1") || !strings.Contains(first, "a=p,U=1,i=1,c=40,r=13") {
		t.Fatalf("transmit/placement missing: %q", first)
	}
	// the photo travels as chunked data (no file): reassemble and decode it
	var b64 strings.Builder
	for _, seq := range strings.Split(first, "\x1b\\") {
		if !strings.HasPrefix(seq, "\x1b_G") || strings.Contains(seq, "a=p") {
			continue
		}
		chunk := seq[strings.Index(seq, ";")+1:]
		if len(chunk) > 4096 {
			t.Fatalf("chunk of %d bytes, protocol limit is 4096", len(chunk))
		}
		b64.WriteString(chunk)
	}
	if !strings.Contains(first, "m=0;") {
		t.Fatal("no final chunk (m=0)")
	}
	data, err := base64.StdEncoding.DecodeString(b64.String())
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width != kittyMaxSide {
		t.Fatalf("png %v width %d, want %d", err, cfg.Width, kittyMaxSide)
	}

	out.Reset()
	if _, err := k.place("ref1", img, 40, 13); err != nil || out.Len() != 0 {
		t.Fatalf("second place re-sent the photo (%d bytes)", out.Len())
	}
	if _, err := k.place("ref1", img, 100, 33); err != nil || !strings.Contains(out.String(), "i=2") {
		t.Fatalf("new size should get its own placement: %q", out.String())
	}
}

func TestKittyChatView(t *testing.T) {
	m, _, _ := newTestModel(t, 120, 40)
	var out bytes.Buffer
	m.kitty = newKittyImages(&out)
	m = seed(m)
	m = key(m, "3")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	next, _ = m.Update(mediaMsg{ref: "img1", raw: []byte{1}, img: image.NewRGBA(image.Rect(0, 0, 800, 600))})
	m = next.(Model)

	view := m.View()
	if !strings.Contains(view, termimg.PLACEHOLDER_CHAR) {
		t.Fatal("chat view should draw the photo with placeholders")
	}
	if !strings.Contains(out.String(), "a=p,U=1") {
		t.Fatal("photo was not sent to the terminal")
	}
	for i, l := range strings.Split(view, "\n") {
		if ansi.StringWidth(l) > m.w {
			t.Fatalf("line %d too wide: %d > %d", i, ansi.StringWidth(l), m.w)
		}
	}
	m = key(m, "up") // select the newest message (the photo)
	m = key(m, "o")
	if v := m.View(); !strings.Contains(v, "800×600 original") || !strings.Contains(v, termimg.PLACEHOLDER_CHAR) {
		t.Fatalf("full view:\n%s", ansi.Strip(v))
	}
}
