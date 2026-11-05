package store

import (
	"bytes"
	"os"
)

func benchOpen(path string) (*Store, error) {
	if os.Getenv("TUON_BENCH_KEY") != "" {
		return OpenEncrypted(path, bytes.Repeat([]byte{7}, 32))
	}
	return OpenPath(path)
}
