package ui

import (
	"github.com/ralph/tuon/internal/connector"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/ralph/tuon/internal/store"
)

// In Terminal.app "❤️" stays a color emoji: termSafe keeps it as "❤️"
// (2 cells, no escape codes) and the finished screen line gets the cursor
// codes. Elsewhere it becomes a text heart plus a space (also 2 cells).
func TestTermSafeHeart(t *testing.T) {
	defer func(v bool) { appleTerminal = v }(appleTerminal)
	for _, apple := range []bool{true, false} {
		appleTerminal = apple
		got := termSafe("love ❤️ it")
		if w := ansi.StringWidth(got); w != 10 {
			t.Errorf("apple=%v: width %d, want 10: %q", apple, w, got)
		}
		if strings.Contains(got, "\x1b") {
			t.Errorf("apple=%v: termSafe output must have no escape codes: %q", apple, got)
		}
		if hasVS := strings.Contains(got, "❤\uFE0F"); hasVS != apple {
			t.Errorf("apple=%v: color heart kept = %v: %q", apple, hasVS, got)
		}
		// safe to run twice (audit A13-2)
		if again := termSafe(got); again != got {
			t.Errorf("apple=%v: termSafe twice = %q, once = %q", apple, again, got)
		}
		// Messenger's bare ❤ reaction is a color heart too in Terminal.app
		if bare := termSafe("❤"); (bare == "❤\uFE0F") != apple {
			t.Errorf("apple=%v: bare heart %q", apple, bare)
		}
		// the line pass adds the cursor codes only in Terminal.app
		line := emojiCursor(got)
		if apple && line != "love "+emojiPre+"❤\uFE0F"+emojiPost+" it" {
			t.Errorf("apple: line = %q", line)
		}
		if !apple && line != got {
			t.Errorf("not apple: line changed: %q", line)
		}
		if s := emojiCursor(termSafe("😆 1️⃣")); strings.Contains(s, emojiPre) {
			t.Errorf("apple=%v: wide emoji / keycap got cursor codes: %q", apple, s)
		}
	}
}

// Audit A13-0: a truncated name full of hearts must not leave cursor steps
// behind. Every screen line has exactly one cursor step per heart drawn.
func TestEmojiCodesSurviveNoTruncation(t *testing.T) {
	defer func(v bool) { appleTerminal = v }(appleTerminal)
	appleTerminal = true
	m, _, _ := newTestModel(t, 100, 26)
	m = seed(m)
	next, _ := m.Update(EventsMsg([]any{
		connector.ChatEvent{Chat: connector.Chat{ID: "77", Name: strings.Repeat("❤", 80), Snippet: strings.Repeat("❤️", 60), LastActivity: time.Now()}},
	}))
	m = next.(Model)
	m = key(m, "3")
	for i, l := range strings.Split(m.View(), "\n") {
		hearts, steps := strings.Count(l, "❤\uFE0F"), strings.Count(l, emojiPost)
		if hearts != steps || strings.Count(l, emojiPre) != hearts {
			t.Fatalf("line %d: %d hearts, %d cursor steps, %d clears", i, hearts, steps, strings.Count(l, emojiPre))
		}
	}
	if !strings.Contains(m.View(), "❤\uFE0F") {
		t.Fatal("test needs hearts on screen")
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
