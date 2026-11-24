package main

import (
	"testing"
	"time"
)

func TestNewestTag(t *testing.T) {
	out := "a1\trefs/tags/v0.3.0\nb2\trefs/tags/v0.10.0\nc3\trefs/tags/v0.9.1\n" +
		"d4\trefs/tags/v9.9.9-evil\ne5\trefs/tags/v1.0.0;rm -rf\nf6\trefs/heads/v2.0.0\n"
	if got := newestTag(out); got != "v0.10.0" {
		t.Fatalf("newest %q, want v0.10.0 (odd names and branches ignored)", got)
	}
	if newestTag("") != "" {
		t.Fatal("no tags: nothing")
	}
}

func TestPendingUpdate(t *testing.T) {
	t.Setenv("TUON_NO_UPDATE_CHECK", "")
	old := version
	defer func() { version = old }()
	version = "v0.4.0"
	if pendingUpdate(updateState{latest: "v0.5.0"}) != "v0.5.0" {
		t.Error("newer release should be offered")
	}
	if pendingUpdate(updateState{latest: "v0.5.0", skip: "v0.5.0"}) != "" {
		t.Error("a skipped release isn't offered")
	}
	if pendingUpdate(updateState{latest: "v0.6.0", skip: "v0.5.0"}) != "v0.6.0" {
		t.Error("skipping one release doesn't skip the next")
	}
	if pendingUpdate(updateState{latest: "v0.4.0"}) != "" || pendingUpdate(updateState{latest: "v0.3.9"}) != "" {
		t.Error("same or older isn't an update")
	}
	t.Setenv("TUON_NO_UPDATE_CHECK", "1")
	if pendingUpdate(updateState{latest: "v0.5.0"}) != "" {
		t.Error("TUON_NO_UPDATE_CHECK turns it off")
	}
	t.Setenv("TUON_NO_UPDATE_CHECK", "")
	version = "dev"
	if pendingUpdate(updateState{latest: "v0.5.0"}) != "" {
		t.Error("source builds never nag")
	}
}

func TestUpdateStateKept(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", "")
	now := time.Unix(time.Now().Unix(), 0)
	saveState(updateState{checked: now, latest: "v0.5.0", skip: "v0.4.1"})
	st := loadState()
	if !st.checked.Equal(now) || st.latest != "v0.5.0" || st.skip != "v0.4.1" {
		t.Fatalf("got %+v", st)
	}
}
