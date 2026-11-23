package main

import "golang.org/x/sys/unix"

func downloadMark(path string) (string, error) {
	q := make([]byte, 64)
	n, err := unix.Getxattr(path, "com.apple.quarantine", q)
	if err != nil {
		return "", err
	}
	return string(q[:n]), nil
}
