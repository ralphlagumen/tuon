package messenger

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/ralph/tuon/internal/connector"
)

func TestTroubleNoticeOncePerStreak(t *testing.T) {
	c := &Conn{log: zerolog.Nop(), events: make(chan any, 16), ctx: context.Background()}
	jayson := waTypes.JID{User: "100002876447027", Device: 56, Server: waTypes.MessengerServer}
	events := func() (n int) {
		for {
			select {
			case e := <-c.events:
				if k, ok := e.(connector.KeysTroubleEvent); ok && k.UserID == "100002876447027" {
					n++
				}
			default:
				return n
			}
		}
	}
	for _, id := range []string{"m1", "m2"} {
		for retry := 0; retry < 5; retry++ { // whatsmeow reports each failure per retry
			c.trackFailure(jayson, id)
		}
	}
	if n := events(); n != 0 {
		t.Fatalf("noticed after 2 messages: %d", n)
	}
	c.trackFailure(jayson, "m3")
	c.trackFailure(jayson, "m4")
	if n := events(); n != 1 {
		t.Fatalf("want exactly one notice at 3 failures, got %d", n)
	}
	c.decryptedFrom(jayson) // one works: streak over
	for _, id := range []string{"m5", "m6"} {
		c.trackFailure(jayson, id)
	}
	if n := events(); n != 0 {
		t.Fatal("a working message should reset the count")
	}
}
