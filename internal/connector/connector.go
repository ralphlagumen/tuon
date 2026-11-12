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
	Link    MediaKind = "link" // a shared post or link card
	// Missing: an encrypted message arrived but couldn't be decrypted. It is
	// a placeholder that the real message (same ID) replaces when it loads.
	Missing MediaKind = "missing"
	// Unavailable: a Missing message the backup never had; see it on the phone.
	Unavailable MediaKind = "unavailable"
)

// MissingSnippet is the chat-list line for a message still being fetched.
const MissingSnippet = "New message, loading…"

type Media struct {
	Kind MediaKind
	// Ref is opaque to everything but the connector that made it
	// (a URL for plain chats, encoded keys for encrypted ones).
	Ref string `json:",omitempty"`
	// Name: a file's name, as the sender gave it (untrusted).
	Name string `json:",omitempty"`
	// For links: what the card says and where it goes.
	Title  string `json:",omitempty"`
	Source string `json:",omitempty"`
	URL    string `json:",omitempty"`
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
	// ReplyToID: the message this one replies to, if any.
	ReplyToID string
}

// ReplyRef points at the message being replied to.
type ReplyRef struct {
	ID, SenderID string
}

// Mention is a person tagged in an outgoing message. The text must contain
// "@"+Name where they are tagged.
type Mention struct {
	UserID string
	Name   string
	// Everyone tags the whole group ("@everyone"): UserID is the chat ID,
	// Members the people in it (encrypted groups tag each of them).
	Everyone bool
	Members  []string
}

// Person is someone (or, with Group, a group chat) found by a search.
type Person struct {
	ID, Name  string
	Group     bool // a group chat: ID is its chat ID
	Encrypted bool
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
	// ChatAliasEvent says chat From is the same conversation as chat To
	// (Messenger lists encrypted 1:1s under a thread number, while their
	// messages arrive under the person's ID).
	ChatAliasEvent struct{ From, To string }

	// KeysTroubleEvent: messages from one person's device keep failing to
	// decrypt live (they still arrive through the backup). Persistent cases
	// mean that phone holds outdated keys for tuon: `tuon relink` fixes it.
	// Failed == 0: that device's messages decrypt again.
	KeysTroubleEvent struct {
		UserID string
		Failed int
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
	// Send sends text; reply is nil unless it answers a specific message.
	Send(ctx context.Context, chatID, text string, mentions []Mention, reply *ReplyRef) error
	// SearchPeople asks the platform who matches a name.
	SearchPeople(ctx context.Context, query string) ([]Person, error)
	// LookupPeople asks for the names of people tuon has no name for;
	// they arrive as ContactEvents.
	LookupPeople(ctx context.Context, userIDs []string)
	// OpenDirect gets the 1:1 chat with a person ready to use (it may not be
	// in the chat list yet). The chat ID it returns is what Send takes.
	OpenDirect(ctx context.Context, userID string) (string, error)
	// KnowChat tells the connector what tuon already knows about a chat
	// (from earlier sessions), so sends take the right route.
	KnowChat(chat Chat)
	// SendFile sends one photo or file.
	SendFile(ctx context.Context, chatID string, file Attachment) error
	// React sets our reaction on a message; emoji "" removes it.
	// senderID is who wrote the message (encrypted chats need it).
	React(ctx context.Context, chatID, messageID, senderID, emoji string) error
	// MarkRead tells the platform you've read these messages (other
	// people's, in chatID), so the chat shows as read on your phone and the
	// web too. The senders then see you've seen them.
	MarkRead(ctx context.Context, chatID string, msgs []Message) error
	FetchMedia(ctx context.Context, m Media) ([]byte, error)
	Close()
}
