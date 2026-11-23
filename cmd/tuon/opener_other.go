//go:build !darwin && !linux

package main

import (
	"context"
	"errors"
)

// No sandbox here, so no launcher actions: the app does these itself.
var errNoOpener = errors.New("not on this system")

func sysOpenURL(context.Context, string) error      { return errNoOpener }
func sysOpenPhotos(context.Context, []string) error { return errNoOpener }
func sysReveal(context.Context, string) error       { return errNoOpener }
func sysClipImage(context.Context) []byte           { return nil }
func sysClipFile(context.Context) string            { return "" }
func sysPickFile(context.Context) string            { return "" }
func sysCopy(context.Context, string) error         { return errNoOpener }
func sysPaste(context.Context) string               { return "" }
func sysSound(string, int) error                    { return errNoOpener }
