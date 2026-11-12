package seq

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Trace records terminal input (Masked) with times, when TUON_KEYTRACE=1:
// for finding where an escape sequence gets split. Off: a nil *Trace, and
// every method does nothing.
type Trace struct {
	mu    sync.Mutex
	f     *os.File
	start time.Time
}

// OpenTrace opens dir/keytrace-<name>.log if TUON_KEYTRACE=1.
func OpenTrace(dir, name string) *Trace {
	if os.Getenv("TUON_KEYTRACE") != "1" {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(dir, "keytrace-"+name+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil
	}
	t := &Trace{f: f, start: time.Now()}
	t.Note("start " + time.Now().Format(time.RFC3339))
	return t
}

// Bytes logs one chunk: what (e.g. "in", "to-app", "read") and its bytes.
func (t *Trace) Bytes(what string, b []byte) {
	if t == nil || len(b) == 0 {
		return
	}
	t.Note(fmt.Sprintf("%-7s %4d  %s", what, len(b), Masked(b)))
}

// Note logs a line with the time since start.
func (t *Trace) Note(s string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	fmt.Fprintf(t.f, "%9.3f ms  %s\n", float64(time.Since(t.start).Microseconds())/1000, s)
}
