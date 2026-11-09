//go:build !windows

package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestTTYInWaitsForTheRestOfASequence(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	in := &ttyIn{File: r}
	buf := make([]byte, 256)

	_, _ = w.Write([]byte("\x1b[<65;8"))
	go func() { time.Sleep(5 * time.Millisecond); _, _ = w.Write([]byte("1;48M")) }()
	n, err := in.Read(buf)
	if err != nil || string(buf[:n]) != "\x1b[<65;81;48M" {
		t.Fatalf("got %q, %v; want the whole wheel report", buf[:n], err)
	}

	_, _ = w.Write([]byte("\x1b")) // a real Esc key: nothing follows
	start := time.Now()
	n, _ = in.Read(buf)
	if string(buf[:n]) != "\x1b" || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("lone Esc: got %q after %v", buf[:n], time.Since(start))
	}
}

// TestTTYInFullReadKeepsTail: a burst bigger than the reader's buffer must
// not hand over half a report (Bubble Tea types the rest as text).
func TestTTYInFullReadKeepsTail(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	in := &ttyIn{File: r}
	wheel := "\x1b[<65;103;58M" // 15 bytes: 256 isn't a multiple
	burst := strings.Repeat(wheel, 60)
	_, _ = w.Write([]byte(burst))
	var got strings.Builder
	buf := make([]byte, 256)
	for got.Len() < len(burst) {
		n, err := in.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 && buf[0] != 0x1b {
			t.Fatalf("a read started inside a report: %q", buf[:min(n, 20)])
		}
		if n > 0 && seqCut(buf[:n]) {
			t.Fatalf("a read ended inside a report: %q", buf[max(0, n-20):n])
		}
		got.Write(buf[:n])
	}
	if got.String() != burst {
		t.Fatal("bytes lost or reordered")
	}
}

func seqCut(b []byte) bool { return !strings.HasSuffix(string(b), "M") }
