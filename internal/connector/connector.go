// Package connector defines what Tuon needs from a chat platform.
// Messenger is the first one; Slack and Google Chat plug in later.
package connector

import (
	"context"
	"time"
)

type Chat struct {
	ID           string
	Name         string // empty for 1:1 chats; UI falls back to the contact name
	Snippet      string
	LastActivity time.Time
	Unread       bool
	Group        bool
	Encrypted    bool
}

type MediaKind string

const (
	Photo   MediaKind = "photo"
	Video   MediaKind = "video"
	Voice   MediaKind = "voice"
	File    MediaKind = "file"
	Sticker MediaKind = "sticker"
)

type Media struct {
	Kind MediaKind
	// Ref is opaque to everything but the connector that made it
	// (a URL for plain chats, encoded keys for encrypted ones).
	Ref string
}

type Message struct {
	ID       string
	ChatID   string
	SenderID string
	Text     string
	Time     time.Time
	FromMe   bool
	Media    []Media
	// Pending: shown right away while sending; replaced when confirmed.
	Pending bool
	// ReplacesID: this confirmed message takes the place of that pending one.
	ReplacesID string
}

// Mention is a person tagged in an outgoing message. The text must contain
// "@"+Name where they are tagged.
type Mention struct {
	UserID string
	Name   string
}

// Attachment is a file to send.
type Attachment struct {
	Name string
	Mime string
	Data []byte
}

func (a Attachment) IsImage() bool {
	return len(a.Mime) > 6 && a.Mime[:6] == "image/" && a.Mime != "image/gif"
}

// Events sent on Connector.Events().
type (
	ChatEvent    struct{ Chat Chat }
	MessageEvent struct{ Message Message }
	ContactEvent struct{ ID, Name string }
	// MessageRemovedEvent drops a message (a send that failed).
	MessageRemovedEvent struct{ ID string }
	// ReactionEvent sets (or with Emoji == "" removes) one person's reaction.
	ReactionEvent struct {
		ChatID, MessageID, ActorID, Emoji string
	}
	// ParticipantEvent says someone is in a group chat (for @ suggestions).
	ParticipantEvent struct {
		ChatID, UserID, Nickname string
	}
	// StatusEvent reports connection state. Err != nil means something broke.
	StatusEvent struct {
		Text string
		Err  error
	}
)

type Connector interface {
	// Start logs in and connects. Data arrives on Events().
	Start(ctx context.Context) error
	Events() <-chan any
	SelfID() string
	// LoadOlder asks for messages before the given one; they arrive as MessageEvents.
	LoadOlder(ctx context.Context, chatID string, before time.Time, beforeID string) error
	Send(ctx context.Context, chatID, text string, mentions []Mention) error
	// SendFile sends one photo or file.
	SendFile(ctx context.Context, chatID string, file Attachment) error
	// React sets our reaction on a message; emoji "" removes it.
	// senderID is who wrote the message (encrypted chats need it).
	React(ctx context.Context, chatID, messageID, senderID, emoji string) error
	FetchMedia(ctx context.Context, m Media) ([]byte, error)
	Close()
}
