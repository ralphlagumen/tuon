package main

import (
	"io"
	"os"
)

// appInput: Windows reads console input differently; plain stdin.
func appInput() io.Reader { return os.Stdin }
