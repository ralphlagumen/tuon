//go:build !darwin

package main

func downloadMark(string) (string, error) { return "", nil }
