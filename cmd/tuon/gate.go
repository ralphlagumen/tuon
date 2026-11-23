package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ralph/tuon/internal/netgate"
	"github.com/ralph/tuon/internal/paths"
	"github.com/ralph/tuon/internal/safefile"
)

// gate is the running network gate for one launch: the app reaches Meta
// only through it (internal/netgate). Its log is in ~/Library/Caches/tuon,
// which the sandboxed app can neither read nor write.
type gate struct {
	g      *netgate.Gate
	l      net.Listener
	port   int
	events *os.File // the app's end of the refusals pipe (fd 7)
	w      *os.File
	start  time.Time
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
	gt.g.OnRefuse = func(target string) {
		gt.log("refused " + target)
		_ = w.SetWriteDeadline(time.Now().Add(time.Second)) // an app that stops reading can't stall us
		_, _ = fmt.Fprintln(w, target)
	}
	go gt.g.Serve(l)
	return gt, nil
}

// env: what the app needs to find the gate and its refusals pipe.
func (gt *gate) env() []string {
	return []string{netgate.Env + "=" + gt.l.Addr().String(), netgate.EventsFDEnv + "=7"}
}

// stop closes the gate, records what went through, and tells you (after
// the app has given the terminal back) if anything was refused.
func (gt *gate) stop() {
	gt.l.Close()
	gt.w.Close()
	gt.events.Close()
	refused := gt.g.Refused()
	gt.log(fmt.Sprintf("run %s–%s allowed: %s; refused: %s", gt.start.Format(time.RFC3339), time.Now().Format("15:04:05"),
		strings.Join(gt.g.AllowedHosts(), ", "), strings.Join(refused, ", ")))
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
