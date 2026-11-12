package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

// The decoder's repeated warnings are logged once; other warnings, and
// tuon's own logger, are untouched.
func TestDecoderNoiseLoggedOnce(t *testing.T) {
	var buf bytes.Buffer
	base := zerolog.New(&buf)
	global := base.Hook(&onceHook{})
	for i := 0; i < 500; i++ {
		global.Warn().Str("field_name", "CannotUnsendReason").Msg("Failed to set int64")
		global.Warn().Msg("Skipping dependency with no reference")
	}
	global.Warn().Msg("Error decrypting message")
	global.Warn().Msg("Error decrypting message")
	base.Warn().Msg("Failed to set int64") // tuon's own logger: no hook

	out := buf.String()
	for msg, want := range map[string]int{
		`"Failed to set int64"`:                   2, // one through the hook, one from the plain logger
		`"Skipping dependency with no reference"`: 1,
		`"Error decrypting message"`:              2,
	} {
		if got := strings.Count(out, msg); got != want {
			t.Errorf("%s logged %d times, want %d", msg, got, want)
		}
	}
	if !strings.Contains(out, "repeats of this warning are not logged") {
		t.Error("the first one should say repeats are dropped")
	}
}
