//go:build darwin || linux

package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
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

// Esc pressed while scroll reports keep coming reaches the app on its own,
// after them: glued to the next report it read as alt+Esc and the report
// as text ("[<65;104;52M" in the box, 2026-11-12).
func TestRelayEscAmidScrollReports(t *testing.T) {
	for name, glued := range map[string]bool{"own read": false, "same read": true} {
		t.Run(name, func(t *testing.T) {
			ctl, app, err := openPty()
			if err != nil {
				t.Skip("no pty:", err)
			}
			defer ctl.Close()
			defer app.Close()
			if _, err := term.MakeRaw(int(app.Fd())); err != nil {
				t.Fatal(err)
			}
			inR, inW, _ := os.Pipe()
			defer inW.Close()
			devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
			oldStdout := os.Stdout
			os.Stdout = devnull
			defer func() { os.Stdout = oldStdout }()
			go relay(int(inR.Fd()), int(ctl.Fd()), make(chan os.Signal), func() {}, make(chan error, 1))

			var got []byte
			reading := make(chan struct{})
			go func() { // poll: a terminal file has no read deadlines
				defer close(reading)
				b := make([]byte, 4096)
				deadline := time.Now().Add(1500 * time.Millisecond)
				for time.Now().Before(deadline) {
					fds := []unix.PollFd{{Fd: int32(app.Fd()), Events: unix.POLLIN}}
					if k, _ := unix.Poll(fds, 50); k > 0 {
						n, _ := unix.Read(int(app.Fd()), b)
						if n > 0 {
							got = append(got, b[:n]...)
						}
					}
				}
			}()
			report := []byte("\x1b[<65;104;52M")
			for i := 0; i < 30; i++ { // a scroll coasting, every 5 ms
				if i == 10 {
					if glued {
						_, _ = inW.Write(append([]byte{0x1b}, report...))
						time.Sleep(5 * time.Millisecond)
						continue
					}
					_, _ = inW.Write([]byte{0x1b})
				}
				_, _ = inW.Write(report)
				time.Sleep(5 * time.Millisecond)
			}
			<-reading
			s := string(got)
			if strings.Contains(s, "\x1b\x1b[") {
				t.Fatalf("Esc glued to a report: %q", s)
			}
			lone := strings.Count(s, "\x1b") - strings.Count(s, "\x1b[")
			if lone != 1 {
				t.Fatalf("want exactly one lone Esc, got %d: %q", lone, s)
			}
			if !strings.HasSuffix(s, "\x1b") || strings.Count(s, "\x1b[<65") != 30 {
				t.Fatalf("Esc should come after all 30 reports: %q", s)
			}
		})
	}
}

// TestRelayFiltersOutput: what the app draws reaches your terminal, but a
// code that reaches past the sandbox (a link, a title, a kitty file read)
// doesn't (internal/termfilter).
func TestRelayFiltersOutput(t *testing.T) {
	ctl, app, err := openPty()
	if err != nil {
		t.Skip("no pty:", err)
	}
	if _, err := term.MakeRaw(int(app.Fd())); err != nil {
		t.Fatal(err)
	}
	inR, _, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = outW
	defer func() { os.Stdout = oldStdout }()
	go relay(int(inR.Fd()), int(ctl.Fd()), make(chan os.Signal), func() {}, make(chan error, 1))

	_, _ = app.Write([]byte("\x1b[1mhi\x1b[0m\x1b]8;;ssh://evil\x07link\x1b]8;;\x07\x1b]0;fake\x07\x1b_Ga=T,t=f;L2V0Yy9wYXNzd2Q=\x1b\\\x1b]11;?\x07END"))
	var got []byte
	buf := make([]byte, 4096)
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(string(got), "END") && time.Now().Before(deadline) {
		_ = outR.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, _ := outR.Read(buf)
		got = append(got, buf[:n]...)
	}
	if want := "\x1b[1mhi\x1b[0mlink\x1b]11;?\x07END"; string(got) != want {
		t.Fatalf("terminal got %q, want %q", got, want)
	}
}
