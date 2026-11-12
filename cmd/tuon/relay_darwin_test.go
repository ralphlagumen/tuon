package main

import (
	"os"
	"testing"
	"time"

	"golang.org/x/term"
)

// A lone Esc typed while the app keeps drawing still reaches the app on
// its own, within the hold time (audit A13-11): it used to wait for a
// quiet moment and then merge with the next key as alt+key.
func TestRelayFlushesLoneEscWhileBusy(t *testing.T) {
	ctl, app, err := openPty()
	if err != nil {
		t.Skip("no pty:", err)
	}
	defer ctl.Close()
	defer app.Close()
	if _, err := term.MakeRaw(int(app.Fd())); err != nil { // bytes as they are, no line editing
		t.Fatal(err)
	}
	inR, inW, _ := os.Pipe()
	defer inW.Close()
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	oldStdout := os.Stdout
	os.Stdout = devnull // the relay copies the app's drawing to stdout
	defer func() { os.Stdout = oldStdout }()

	done := make(chan error, 1)
	go relay(int(inR.Fd()), int(ctl.Fd()), make(chan os.Signal), func() {}, done)

	stop := make(chan struct{})
	defer close(stop)
	go func() { // the app draws all the time
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = app.Write([]byte("frame "))
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	_, _ = inW.Write([]byte{0x1b})
	got := make(chan []byte, 1)
	go func() {
		b := make([]byte, 16)
		n, _ := app.Read(b)
		got <- b[:n]
	}()
	select {
	case b := <-got:
		if string(b) != "\x1b" {
			t.Fatalf("app got %q, want a lone Esc", b)
		}
		if d := time.Since(start); d > 150*time.Millisecond {
			t.Fatalf("Esc took %s", d)
		}
	case <-time.After(time.Second):
		t.Fatal("a lone Esc never reached the app while it was drawing")
	}
}

// Keys keep reaching the app while your terminal isn't taking output
// (Terminal.app busy): they used to share one loop, which deadlocked with
// Terminal.app during fast scrolling (2026-11-12).
func TestRelayKeysFlowWhileOutputBlocked(t *testing.T) {
	ctl, app, err := openPty()
	if err != nil {
		t.Skip("no pty:", err)
	}
	if _, err := term.MakeRaw(int(app.Fd())); err != nil {
		t.Fatal(err)
	}
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe() // "your terminal": nobody reads it, so it fills
	_ = outR
	oldStdout := os.Stdout
	os.Stdout = outW
	defer func() { os.Stdout = oldStdout }()

	go relay(int(inR.Fd()), int(ctl.Fd()), make(chan os.Signal), func() {}, make(chan error, 1))
	go func() { // the app draws far more than the terminal takes
		frame := make([]byte, 4096)
		for i := range frame {
			frame[i] = 'x'
		}
		for {
			if _, err := app.Write(frame); err != nil {
				return
			}
		}
	}()
	time.Sleep(300 * time.Millisecond) // the output pipe is full by now

	_, _ = inW.Write([]byte("k"))
	got := make(chan string, 1)
	go func() {
		b := make([]byte, 8)
		n, _ := app.Read(b)
		got <- string(b[:n])
	}()
	select {
	case s := <-got:
		if s != "k" {
			t.Fatalf("app got %q", s)
		}
	case <-time.After(time.Second):
		t.Fatal("a key never reached the app while the terminal wasn't taking output")
	}
}
