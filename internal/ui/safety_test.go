package ui

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestOnlyWebLinksOpen(t *testing.T) {
	for _, bad := range []string{
		"file:///Applications/Calculator.app",
		"javascript:alert(1)",
		"smb://evil/share",
		"tel:123",
		"-a Terminal",
		"https://",
	} {
		if _, err := safeWebURL(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	for _, good := range []string{"https://www.reddit.com/r/x", "http://example.com/a?b=c&d=e"} {
		if _, err := safeWebURL(good); err != nil {
			t.Errorf("%q should open: %v", good, err)
		}
	}
}

func TestHugeImageRefused(t *testing.T) {
	// a real, small PNG header that claims 20000 x 20000 pixels
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1)))
	b := buf.Bytes()
	// patch width/height in the IHDR chunk (bytes 16..23)
	copy(b[16:24], []byte{0, 0, 0x4e, 0x20, 0, 0, 0x4e, 0x20})
	binary.BigEndian.PutUint32(b[29:33], crc32.ChecksumIEEE(b[12:29])) // keep the chunk valid
	if _, err := decodeImage(b); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected a size refusal, got %v", err)
	}
}
