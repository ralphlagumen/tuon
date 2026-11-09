//go:build !windows

package main

import (
	"os"
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
	in := ttyIn{r}
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
