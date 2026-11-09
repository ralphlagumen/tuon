// Package opener lets the sandboxed chat app ask the launcher for the few
// things that must happen outside the sandbox on macOS: opening a web
// link, showing photos in Preview, showing a saved file in Finder, and
// reading an image or file from the clipboard. The app can't start `open`
// or `osascript` itself, because both can ask macOS to start any program,
// and what macOS starts runs outside the sandbox.
//
// The app writes one JSON request per line on fd 5 and reads one JSON reply
// per line on fd 6. The launcher checks every request before acting: only
// http(s) links, only image files in tuon's photo folder (opened in
// Preview, never by their default app), only files in Downloads (shown,
// never opened). No dependencies, so the launcher stays small.
package opener

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
)

// Env names the app's request pipe (fd 5; replies come on fd 6).
const Env = "TUON_OPENER_FD"

// The requests.
const (
	OpURL       = "url"        // Args[0]: an http(s) link
	OpPhotos    = "photos"     // Args: photo files in $TMPDIR/tuon
	OpReveal    = "reveal"     // Args[0]: a file in ~/Downloads
	OpClipImage = "clip-image" // reply Data: PNG bytes on the clipboard
	OpClipFile  = "clip-file"  // reply Text: path of a file copied in Finder
)

// Request is one ask from the app.
type Request struct {
	Op   string   `json:"op"`
	Args []string `json:"args,omitempty"`
}

// Reply is the launcher's answer.
type Reply struct {
	Err  string `json:"err,omitempty"`
	Text string `json:"text,omitempty"`
	Data []byte `json:"data,omitempty"`
}

// maxLine bounds one message (a large pasted image, base64).
const maxLine = 64 << 20

// Client is the app's side.
type Client struct {
	mu sync.Mutex
	w  *os.File
	r  *bufio.Reader
}

var (
	once   sync.Once
	client *Client
)

// Connect returns the client when the launcher handed over the pipes, nil
// otherwise (no sandbox: the app opens things itself).
func Connect() *Client {
	once.Do(func() {
		if os.Getenv(Env) != "5" {
			return
		}
		os.Unsetenv(Env)
		client = &Client{w: os.NewFile(5, "tuon-opener"), r: bufio.NewReaderSize(os.NewFile(6, "tuon-opener-reply"), 1<<16)}
	})
	return client
}

// Do sends a request and waits for the reply.
func (c *Client) Do(op string, args ...string) (Reply, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, _ := json.Marshal(Request{Op: op, Args: args})
	if _, err := c.w.Write(append(b, '\n')); err != nil {
		return Reply{}, err
	}
	line, err := readLine(c.r)
	if err != nil {
		return Reply{}, err
	}
	var rep Reply
	if err := json.Unmarshal(line, &rep); err != nil {
		return Reply{}, err
	}
	if rep.Err != "" {
		return rep, errors.New(rep.Err)
	}
	return rep, nil
}

// Serve answers requests from r on w with handle until r closes.
func Serve(r io.Reader, w io.Writer, handle func(Request) Reply) {
	br := bufio.NewReaderSize(r, 1<<16)
	for {
		line, err := readLine(br)
		if err != nil {
			return
		}
		var req Request
		var rep Reply
		if json.Unmarshal(line, &req) != nil {
			rep = Reply{Err: "bad request"}
		} else {
			rep = handle(req)
		}
		b, _ := json.Marshal(rep)
		if _, err := w.Write(append(b, '\n')); err != nil {
			return
		}
	}
}

func readLine(r *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		out = append(out, chunk...)
		if len(out) > maxLine {
			return nil, errors.New("message too long")
		}
		if !isPrefix {
			return out, nil
		}
	}
}
