package messenger

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-meta/pkg/messagix/table"
	waTypes "go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/ralph/tuon/internal/connector"
)

func TestTroubleNoticeOncePerStreak(t *testing.T) {
	c := &Conn{log: zerolog.Nop(), events: make(chan any, 16), ctx: context.Background()}
	jayson := waTypes.JID{User: "100002876447027", Device: 56, Server: waTypes.MessengerServer}
	recovered := 0
	events := func() (n int) {
		for {
			select {
			case e := <-c.events:
				if k, ok := e.(connector.KeysTroubleEvent); ok && k.UserID == "100002876447027" {
					if k.Failed == 0 {
						recovered++
					} else {
						n++
					}
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
	c.decryptedFrom(jayson) // one works: streak over, warning can go
	if events(); recovered != 1 {
		t.Fatalf("want one recovery notice, got %d", recovered)
	}
	c.decryptedFrom(jayson)
	if events(); recovered != 1 {
		t.Fatal("recovery noticed twice")
	}
	for _, id := range []string{"m5", "m6"} {
		c.trackFailure(jayson, id)
	}
	if n := events(); n != 0 {
		t.Fatal("a working message should reset the count")
	}
}

func TestFailedReactionMakesNoPlaceholder(t *testing.T) {
	c := &Conn{log: zerolog.Nop(), events: make(chan any, 16), ctx: context.Background(), threadType: map[int64]table.ThreadType{}}
	sender := waTypes.JID{User: "100002876447027", Device: 56, Server: waTypes.MessengerServer}
	chat := waTypes.JID{User: "100002876447027", Server: waTypes.MessengerServer}
	count := func() (msgs int) {
		for {
			select {
			case e := <-c.events:
				if _, ok := e.(connector.MessageEvent); ok {
					msgs++
				}
			default:
				return msgs
			}
		}
	}
	c.onUndecryptable(&events.UndecryptableMessage{Info: waTypes.MessageInfo{
		MessageSource: waTypes.MessageSource{Chat: chat, Sender: sender}, ID: "r1", Type: "reaction"}})
	if n := count(); n != 0 {
		t.Fatalf("a failed reaction made %d placeholder(s)", n)
	}
	c.onUndecryptable(&events.UndecryptableMessage{Info: waTypes.MessageInfo{
		MessageSource: waTypes.MessageSource{Chat: chat, Sender: sender}, ID: "m1", Type: "text"}})
	if n := count(); n != 1 {
		t.Fatalf("a failed message made %d placeholder(s), want 1", n)
	}
	if len(c.failed[sender.SignalAddress().String()]) != 1 {
		t.Fatal("only the message should count toward the warning")
	}
}
