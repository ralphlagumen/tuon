//go:build !linux

package main

func linuxChecks(func(string, bool, error), func(string) error) {}
