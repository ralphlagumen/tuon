package main

import (
	"sync"

	"github.com/rs/zerolog"
)

// decoderNoise: warnings messagix's table decoder repeats for every chat
// row when Meta's format drifts from the library's (2026-11-12: Meta's
// thread rows moved columns 36+, which tuon doesn't read). They filled
// ~700 KB of tuon.log in 12 minutes and buried real warnings.
var decoderNoise = map[string]bool{
	"Failed to set int64":                        true,
	"Failed to set int":                          true,
	"Failed to set string":                       true,
	"Failed to set bool":                         true,
	"Failed to set float64":                      true,
	"Skipping dependency with no reference":      true,
	"Skipping dependency with unrecognized type": true,
	"Unknown dependency in sp":                   true,
}

// onceHook lets the first of each noisy warning through (so a format
// change still shows up once per run) and drops the repeats. Everything
// else passes untouched.
type onceHook struct{ seen sync.Map }

func (h *onceHook) Run(e *zerolog.Event, _ zerolog.Level, msg string) {
	if !decoderNoise[msg] {
		return
	}
	if _, dup := h.seen.LoadOrStore(msg, true); dup {
		e.Discard()
		return
	}
	e.Str("note", "repeats of this warning are not logged this run")
}
