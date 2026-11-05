package vault

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Helper runs tuon-keyhelper, the small Swift program that uses the Mac's
// Secure Enclave (Go can't reach CryptoKit directly).
type Helper struct{ Path string }

// FindHelper finds tuon-keyhelper next to tuon, and only there: never on
// PATH, where another program could put its own. A helper that other users
// can change isn't used. It returns nil where there's no Secure Enclave
// support (not macOS, or not built).
func FindHelper() *Helper {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			p := filepath.Join(filepath.Dir(exe), "tuon-keyhelper")
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o022 == 0 {
				return &Helper{Path: p}
			}
		}
	}
	return nil
}

func (h *Helper) run(stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.Command(h.Path, args...)
	var out, errb bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(stdin), &out, &errb
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee) && ee.ExitCode() == 2:
		return nil, ErrNotConfirmed
	case errors.As(err, &ee) && ee.ExitCode() == 3:
		return nil, errors.New("this Mac has no Secure Enclave")
	case err != nil:
		return nil, fmt.Errorf("key helper: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// Create makes a new Secure Enclave key (no prompt).
func (h *Helper) Create() (key, pub []byte, err error) {
	b, err := h.run(nil, "create")
	if err != nil {
		return nil, nil, err
	}
	var r struct{ Key, Pub string }
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, nil, err
	}
	if key, err = unb64(r.Key); err != nil {
		return nil, nil, err
	}
	pub, err = unb64(r.Pub)
	return key, pub, err
}

// Derive runs the key agreement inside the chip; macOS asks for Touch ID
// or the Mac password, with the helper's own fixed text.
func (h *Helper) Derive(key, peer []byte) ([]byte, error) {
	in, _ := json.Marshal(map[string]string{"key": b64(key), "peer": b64(peer)})
	b, err := h.run(in, "derive")
	if err != nil {
		return nil, err
	}
	var r struct{ Secret string }
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return unb64(r.Secret)
}

// Check asks the owner to confirm (Touch ID or Mac password), for the idle
// lock: the key stays in memory, this only proves who's at the keyboard.
func (h *Helper) Check() error {
	_, err := h.run(nil, "check")
	return err
}
