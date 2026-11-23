package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ralph/tuon/internal/netgate"
	"github.com/ralph/tuon/internal/paths"
	"github.com/ralph/tuon/internal/safefile"
)

// lastInput: when you last typed, clicked or scrolled (the key relay sets
// it). Upload while you've been away is what a quiet theft looks like.
var lastInput atomic.Int64

// termDropped: escape codes from the app the terminal filter dropped
// (internal/termfilter); tuon itself sends none of those.
var termDropped atomic.Int64

// gate is the running network gate for one launch: the app reaches Meta
// only through it (internal/netgate). Its log is in ~/Library/Caches/tuon,
// which the sandboxed app can neither read nor write.
type gate struct {
	g        *netgate.Gate
	stopOnce sync.Once
	done     chan struct{}
	mu       sync.Mutex
	alarms   []string
	l        net.Listener
	port     int
	events   *os.File // the app's end of the refusals pipe (fd 7)
	w        *os.File
	start    time.Time
}

func startGate() (*gate, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		l.Close()
		return nil, err
	}
	gt := &gate{g: netgate.New(), l: l, port: l.Addr().(*net.TCPAddr).Port, events: r, w: w, start: time.Now()}
	tell := func(line string) {
		gt.mu.Lock() // one line at a time on the pipe
		defer gt.mu.Unlock()
		_ = w.SetWriteDeadline(time.Now().Add(time.Second)) // an app that stops reading can't stall us
		_, _ = fmt.Fprintln(w, line)
	}
	gt.g.OnRefuse = func(target string) {
		gt.log("refused " + target)
		tell("refused " + target)
	}
	lastInput.Store(time.Now().UnixNano()) // starting tuon counts as being here
	gt.g.Idle = func() bool { return time.Since(time.Unix(0, lastInput.Load())) >= netgate.IdleAfter }
	gt.g.OnUnusual = func(msg string) {
		gt.log("UNUSUAL " + msg)
		gt.mu.Lock()
		gt.alarms = append(gt.alarms, msg)
		gt.mu.Unlock()
		tell("unusual " + msg)
	}
	gt.g.OnCert = func(host, root string) {
		msg := fmt.Sprintf("%s's certificate comes from %s, not Meta's authority (DigiCert): something may be intercepting tuon's connections", host, root)
		gt.log("CERT " + msg)
		gt.mu.Lock()
		gt.alarms = append(gt.alarms, msg)
		gt.mu.Unlock()
		tell("cert " + msg)
	}
	go gt.g.Serve(l)
	gt.done = make(chan struct{})
	go func() { // a line every 30 min, so a killed or crashed run still leaves numbers
		t := time.NewTicker(30 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				gt.log("running " + gt.traffic())
			case <-gt.done:
				return
			}
		}
	}()
	// closing the terminal (SIGHUP) or a kill (SIGTERM): record the run, then
	// go down the way the signal would have taken us anyway
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTERM)
	go func() {
		select {
		case s := <-sigs:
			gt.stop()
			signal.Reset(s)
			if self, err := os.FindProcess(os.Getpid()); err == nil {
				_ = self.Signal(s) // (builds on Windows too, where the gate isn't used)
			}
		case <-gt.done:
			signal.Stop(sigs)
		}
	}()
	return gt, nil
}

// traffic: this run's upload/download line for net.log.
func (gt *gate) traffic() string {
	up, down, idleUp, maxIdle := gt.g.Traffic()
	return fmt.Sprintf("up %s, down %s, up while away %s (most in %v: %s)",
		netgate.Size(up), netgate.Size(down), netgate.Size(idleUp), netgate.IdleWindow, netgate.Size(maxIdle))
}

// env: what the app needs to find the gate and its refusals pipe.
func (gt *gate) env() []string {
	return []string{netgate.Env + "=" + gt.l.Addr().String(), netgate.EventsFDEnv + "=7"}
}

// stop closes the gate, records what went through, and tells you (after
// the app has given the terminal back) if anything was refused.
func (gt *gate) stop() { gt.stopOnce.Do(gt.stopNow) }

func (gt *gate) stopNow() {
	close(gt.done)
	gt.l.Close()
	gt.w.Close()
	gt.events.Close()
	refused := gt.g.Refused()
	gt.log(fmt.Sprintf("run %s–%s %s; allowed: %s; refused: %s; certificates: %s; terminal codes dropped: %d",
		gt.start.Format(time.RFC3339), time.Now().Format("15:04:05"), gt.traffic(),
		strings.Join(gt.g.AllowedHosts(), ", "), strings.Join(refused, ", "), strings.Join(gt.g.Certs(), ", "), termDropped.Load()))
	gt.mu.Lock()
	alarms := gt.alarms
	gt.mu.Unlock()
	if len(alarms) > 0 {
		fmt.Fprintf(os.Stderr, "tuon: %s.\n  See %s. A proxy, VPN or security tool that inspects traffic does this; so does malware.\n  If you didn't send big files, check Messenger on your phone for messages you didn't send.\n",
			strings.Join(alarms, ";\n  "), gateLog())
	}
	if len(refused) > 0 {
		fmt.Fprintf(os.Stderr, "tuon: blocked %d connection target(s) that aren't Meta's servers: %s\n  (see %s; if this wasn't you testing, a library in tuon may be misbehaving)\n",
			len(refused), strings.Join(refused, ", "), gateLog())
	}
}

func gateLog() string { return filepath.Join(paths.WorkBase(), "net.log") }

func (gt *gate) log(line string) {
	base := paths.WorkBase()
	if base == "" || os.MkdirAll(base, 0o700) != nil {
		return
	}
	f, err := safefile.Append(gateLog(), 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), line)
}
