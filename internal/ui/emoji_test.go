package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ralph/tuon/internal/store"
)

// In Terminal.app "❤️" stays a color emoji: cleared, drawn, then one cursor
// step, so Tuon counts the same 2 cells the terminal moves. Elsewhere it
// becomes a text heart plus a space (also 2 cells).
func TestTermSafeHeart(t *testing.T) {
	defer func(v bool) { appleTerminal = v }(appleTerminal)
	for _, apple := range []bool{true, false} {
		appleTerminal = apple
		got := termSafe("love ❤️ it")
		if w := ansi.StringWidth(got); w != 10 {
			t.Errorf("apple=%v: width %d, want 10: %q", apple, w, got)
		}
		hasVS := strings.Contains(got, "❤️")
		if hasVS != apple {
			t.Errorf("apple=%v: color heart kept = %v: %q", apple, hasVS, got)
		}
		if apple && got != "love "+emojiPre+"❤️"+emojiPost+" it" {
			t.Errorf("apple: %q", got)
		}
		// wrapping and truncating keep the escapes whole
		if w := ansi.StringWidth(ansi.Truncate(got, 7, "")); w != 7 {
			t.Errorf("apple=%v: truncated width %d", apple, w)
		}
		// Messenger's bare ❤ reaction is a color heart too in Terminal.app
		if bare := termSafe("❤"); (bare == emojiPre+"❤\uFE0F"+emojiPost) != apple || ansi.StringWidth(bare) != map[bool]int{true: 2, false: 1}[apple] {
			t.Errorf("apple=%v: bare heart %q", apple, bare)
		}
		// already-wide emoji and keycaps are untouched by this
		if s := termSafe("😆 1️⃣"); strings.Contains(s, emojiPre) {
			t.Errorf("apple=%v: %q", apple, s)
		}
	}
}

// Reactions: a count normally; on the picked message, who reacted.
func TestReactionChipNames(t *testing.T) {
	rs := []store.ReactionCount{
		{Emoji: "😆", Count: 3, Names: []string{"Ana Cruz", "Ben Ong", ""}},
		{Emoji: "❤️", Count: 1, Mine: true, Names: []string{"You"}},
	}
	plain := ansi.Strip(reactionChip(rs, false))
	if !strings.Contains(plain, "😆 3") || strings.Contains(plain, "Ana") || strings.Contains(plain, "•") {
		t.Errorf("chip = %q", plain)
	}
	who := ansi.Strip(reactionChip(rs, true))
	if !strings.Contains(who, "😆 Ana, Ben, 1 other") || !strings.Contains(who, "You") {
		t.Errorf("who = %q", who)
	}
}
