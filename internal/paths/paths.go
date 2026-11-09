// Package paths knows where tuon keeps its data and makes sure only one
// tuon uses it at a time. It has no dependencies, so the small launcher can
// use it without linking the database or Messenger code.
package paths

import (
	"errors"
	"os"
	"path/filepath"
)

// Dir is tuon's data folder, ~/.config/tuon.
func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "tuon")
}

// ErrRunning means another tuon already has the data folder open.
var ErrRunning = errors.New("tuon is already running in another window; use that one, or quit it first")
