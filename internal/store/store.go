// Package store keeps Tuon's local state in SQLite: chats, their space
// (work / personal / inbox), contacts, and a message cache.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ralph/tuon/internal/connector"
)

type Space string

// Folders are yours to add, rename, delete and reorder. Inbox is built in:
// it holds every chat you haven't put in a folder, and it's always last.
const (
	Work     Space = "Work" // starting folders
	Personal Space = "Personal"
	Inbox    Space = "inbox"
	// Unread isn't a folder: it shows unread chats from every folder.
	Unread Space = "\x00unread"
)

type Store struct {
	DB   *sql.DB
	Self string // our own user ID, left out of group names
	seal Cipher // encrypts sensitive columns; nil = plain (tests, before setup)
}

// Cipher encrypts single values for the database (the vault).
type Cipher interface {
	SealString(string) string
	OpenString(string) (string, error)
	IsSealed(string) bool
}

// SetCipher turns on encryption of message text and media, chat previews
// and drafts. Rows written before stay readable (and EncryptAll seals them).
func (s *Store) SetCipher(c Cipher) { s.seal = c }

func (s *Store) enc(v string) string {
	if s.seal == nil {
		return v
	}
	return s.seal.SealString(v)
}

func (s *Store) dec(v string) string {
	if s.seal == nil {
		return v
	}
	out, err := s.seal.OpenString(v)
	if err != nil {
		return "[can't decrypt]"
	}
	return out
}

type ChatRow struct {
	connector.Chat
	Display string // name to show: chat name, else contact name, else ID
	Space   Space
	Muted   bool
}

type MessageRow struct {
	connector.Message
	SenderName string
	Reactions  []ReactionCount
	MyReaction string
	ReplyTo    *Quote // what this message replies to, if it does
}

// Quote is the message a reply answers, shortened for display.
type Quote struct {
	Name, Text string
}

type ReactionCount struct {
	Emoji string
	Count int
	Mine  bool
}

// Person is someone you can @mention in a chat, or a search result
// (Group: a group chat not in the list yet).
type Person struct {
	ID, Name  string
	Group     bool
	Encrypted bool
}

const schema = `
CREATE TABLE IF NOT EXISTS tuon_chats (
	id        TEXT PRIMARY KEY,
	name      TEXT    NOT NULL DEFAULT '',
	snippet   TEXT    NOT NULL DEFAULT '',
	last_ts   INTEGER NOT NULL DEFAULT 0,
	unread    INTEGER NOT NULL DEFAULT 0,
	grp       INTEGER NOT NULL DEFAULT 0,
	encrypted INTEGER NOT NULL DEFAULT 0,
	space     TEXT    NOT NULL DEFAULT 'inbox',
	muted     INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS tuon_contacts (
	id   TEXT PRIMARY KEY,
	name TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS tuon_messages (
	id        TEXT PRIMARY KEY,
	chat_id   TEXT    NOT NULL,
	sender_id TEXT    NOT NULL,
	text      TEXT    NOT NULL,
	ts        INTEGER NOT NULL,
	from_me   INTEGER NOT NULL,
	media     TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS tuon_messages_chat ON tuon_messages(chat_id, ts);
CREATE TABLE IF NOT EXISTS tuon_reactions (
	message_id TEXT NOT NULL,
	actor_id   TEXT NOT NULL,
	chat_id    TEXT NOT NULL,
	emoji      TEXT NOT NULL,
	PRIMARY KEY (message_id, actor_id)
);
CREATE INDEX IF NOT EXISTS tuon_reactions_chat ON tuon_reactions(chat_id);
CREATE TABLE IF NOT EXISTS tuon_participants (
	chat_id  TEXT NOT NULL,
	user_id  TEXT NOT NULL,
	nickname TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (chat_id, user_id)
);
CREATE TABLE IF NOT EXISTS tuon_spaces (
	name TEXT PRIMARY KEY COLLATE NOCASE,
	pos  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS tuon_alias (
	from_id TEXT PRIMARY KEY,
	to_id   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS tuon_settings (
	k TEXT PRIMARY KEY,
	v TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS tuon_drafts (
	chat_id TEXT PRIMARY KEY,
	text    TEXT NOT NULL
);`

// Dir is ~/.config/tuon.
func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "tuon")
}

// Open opens ~/.config/tuon/tuon.db.
func Open() (*Store, error) {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return nil, err
	}
	return OpenPath(filepath.Join(Dir(), "tuon.db"))
}

func OpenPath(path string) (*Store, error) {
	// secure_delete: deleted or overwritten rows are zeroed on disk, so old
	// plain copies of encrypted text don't linger in free pages
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=secure_delete(1)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("db schema: %w", err)
	}
	// columns added after the first release (ignore "duplicate column")
	_, _ = db.Exec(`ALTER TABLE tuon_messages ADD COLUMN pending INTEGER NOT NULL DEFAULT 0`)
	// read_ts: when you last read the chat in tuon. Messenger doesn't know
	// (we send no "seen"), so its updates can't make older messages unread.
	_, _ = db.Exec(`ALTER TABLE tuon_chats ADD COLUMN read_ts INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE tuon_messages ADD COLUMN reply_to TEXT NOT NULL DEFAULT ''`)
	// missing: a "loading from your backup" placeholder (media is encrypted,
	// so it can't be searched for)
	if _, err := db.Exec(`ALTER TABLE tuon_messages ADD COLUMN missing INTEGER NOT NULL DEFAULT 0`); err == nil {
		_, _ = db.Exec(`UPDATE tuon_messages SET missing = 1 WHERE media LIKE '%"Kind":"missing"%'`)
	}
	// first run with folders: start with Work and Personal (and move chats
	// tagged before folders existed)
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM tuon_spaces`).Scan(&n)
	var seeded string
	_ = db.QueryRow(`SELECT v FROM tuon_settings WHERE k = 'folders'`).Scan(&seeded)
	if n == 0 && seeded == "" {
		_, _ = db.Exec(`INSERT INTO tuon_spaces (name, pos) VALUES ('Work', 0), ('Personal', 1)`)
		_, _ = db.Exec(`UPDATE tuon_chats SET space = 'Work' WHERE space = 'work'`)
		_, _ = db.Exec(`UPDATE tuon_chats SET space = 'Personal' WHERE space = 'personal'`)
		_, _ = db.Exec(`INSERT INTO tuon_settings (k, v) VALUES ('folders', 'seeded')`)
	}
	_ = os.Chmod(path, 0o600)
	return &Store{DB: db}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) UpsertChat(c connector.Chat) error {
	_, err := s.DB.Exec(`
INSERT INTO tuon_chats (id, name, snippet, last_ts, unread, grp, encrypted)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	name      = CASE WHEN excluded.name <> '' THEN excluded.name ELSE name END,
	snippet   = CASE WHEN excluded.last_ts >= last_ts AND excluded.snippet <> '' THEN excluded.snippet ELSE snippet END,
	unread    = CASE WHEN excluded.last_ts >= last_ts
	                 THEN (excluded.unread AND excluded.last_ts > read_ts) ELSE unread END,
	last_ts   = MAX(last_ts, excluded.last_ts),
	grp       = MAX(grp, excluded.grp),
	encrypted = MAX(encrypted, excluded.encrypted)`,
		c.ID, c.Name, s.enc(c.Snippet), c.LastActivity.UnixMilli(), b2i(c.Unread), b2i(c.Group), b2i(c.Encrypted))
	return err
}

func (s *Store) UpsertContact(id, name string) error {
	_, err := s.DB.Exec(`INSERT INTO tuon_contacts (id, name) VALUES (?, ?)
ON CONFLICT(id) DO UPDATE SET name = excluded.name`, id, name)
	return err
}

// AddMessage saves a message. If it is the newest in its chat, the chat's
// snippet and time move forward too. Returns true if the message was new.
func (s *Store) AddMessage(m connector.Message, chatOpen bool) (bool, error) {
	media := ""
	if len(m.Media) > 0 {
		b, _ := json.Marshal(m.Media)
		media = string(b)
	}
	if m.ReplacesID != "" {
		_, _ = s.DB.Exec(`DELETE FROM tuon_messages WHERE id = ?`, m.ReplacesID)
	}
	var exists bool
	_ = s.DB.QueryRow(`SELECT 1 FROM tuon_messages WHERE id = ?`, m.ID).Scan(&exists)
	if exists && IsMissing(m.Media) {
		return false, nil // a failed retry never replaces what we already have
	}
	_, err := s.DB.Exec(`INSERT INTO tuon_messages (id, chat_id, sender_id, text, ts, from_me, media, pending, reply_to, missing)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET text = excluded.text, media = excluded.media, missing = excluded.missing,
	pending = excluded.pending, reply_to = CASE WHEN excluded.reply_to <> '' THEN excluded.reply_to ELSE reply_to END`,
		m.ID, m.ChatID, m.SenderID, s.enc(m.Text), m.Time.UnixMilli(), b2i(m.FromMe), s.enc(media), b2i(m.Pending), m.ReplyToID, b2i(IsMissing(m.Media)))
	if err != nil {
		return false, err
	}

	var grp bool
	_ = s.DB.QueryRow(`SELECT grp FROM tuon_chats WHERE id = ?`, m.ChatID).Scan(&grp)
	snippet := m.Text
	if snippet == "" && len(m.Media) > 0 {
		snippet = "[" + string(m.Media[0].Kind) + "]"
		if IsMissing(m.Media) {
			snippet = connector.MissingSnippet
		}
		if md := m.Media[0]; md.Kind == connector.Link && md.Title != "" {
			snippet = "Shared: " + md.Title
		}
	}
	switch {
	case m.FromMe:
		snippet = "You: " + snippet
	case grp:
		// admin lines ("Vic voted…", "Ann sent 9 photos") already name the sender
		if name := s.contactName(m.SenderID); name != "" && !strings.HasPrefix(snippet, strings.Fields(name)[0]) {
			snippet = strings.Fields(name)[0] + ": " + snippet
		}
	}
	unread := !m.FromMe && !chatOpen
	_, err = s.DB.Exec(`
INSERT INTO tuon_chats (id, snippet, last_ts, unread) VALUES (?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	-- the newest stored message names the chat, even if Messenger already
	-- bumped the chat's time (an encrypted message that loaded late)
	snippet = CASE WHEN excluded.last_ts >= last_ts
	                 OR excluded.last_ts >= (SELECT MAX(ts) FROM tuon_messages WHERE chat_id = excluded.id)
	               THEN excluded.snippet ELSE snippet END,
	unread  = CASE WHEN excluded.last_ts > last_ts
	               THEN (excluded.unread AND excluded.last_ts > read_ts) ELSE unread END,
	last_ts = MAX(last_ts, excluded.last_ts)`,
		m.ChatID, s.enc(strings.Join(strings.Fields(snippet), " ")), m.Time.UnixMilli(), b2i(unread))
	return !exists, err
}

// IsMissing: the message is a placeholder for one that couldn't be decrypted.
func IsMissing(media []connector.Media) bool {
	return len(media) == 1 && media[0].Kind == connector.Missing
}

// Behind reports whether Messenger says a chat had activity after its
// newest stored message: something arrived that we don't have yet.
func (s *Store) Behind(chatID string, activity time.Time) bool {
	var newest sql.NullInt64
	_ = s.DB.QueryRow(`SELECT MAX(ts) FROM tuon_messages WHERE chat_id = ?`, chatID).Scan(&newest)
	return newest.Valid && activity.UnixMilli() > newest.Int64+1000
}

// HasMissing reports whether a chat still has placeholders waiting for
// their real message.
func (s *Store) HasMissing(chatID string) bool {
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM tuon_messages WHERE chat_id = ? AND missing = 1`, chatID).Scan(&n)
	return n > 0
}

// GiveUpMissing turns a chat's "loading" placeholders into "couldn't load"
// once the backup checks are over: they'll never fill.
func (s *Store) GiveUpMissing(chatID string) error {
	b, _ := json.Marshal([]connector.Media{{Kind: connector.Unavailable}})
	if _, err := s.DB.Exec(`UPDATE tuon_messages SET missing = 2, media = ? WHERE chat_id = ? AND missing = 1`, s.enc(string(b)), chatID); err != nil {
		return err
	}
	s.refreshSnippet(chatID)
	return nil
}

// MissingChats lists chats that still have placeholders.
func (s *Store) MissingChats() []string {
	rows, err := s.DB.Query(`SELECT DISTINCT chat_id FROM tuon_messages WHERE missing = 1`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// Waiting: the chat has a placeholder, or activity newer than its newest message.
func (s *Store) Waiting(chatID string) bool {
	if s.HasMissing(chatID) {
		return true
	}
	var last int64
	_ = s.DB.QueryRow(`SELECT last_ts FROM tuon_chats WHERE id = ?`, chatID).Scan(&last)
	return s.Behind(chatID, time.UnixMilli(last))
}

func (s *Store) contactName(id string) string {
	var name string
	_ = s.DB.QueryRow(`SELECT name FROM tuon_contacts WHERE id = ?`, id).Scan(&name)
	return name
}

const chatCols = `c.id, c.name, c.snippet, c.last_ts, c.unread, c.grp, c.encrypted, c.space, c.muted,
	COALESCE(NULLIF(c.name, ''), ct.name, c.id)`

func (s *Store) scanChatRow(row interface{ Scan(...any) error }) (ChatRow, error) {
	r, err := scanChat(row)
	r.Snippet = s.dec(r.Snippet)
	return r, err
}

func scanChat(rows interface{ Scan(...any) error }) (ChatRow, error) {
	var r ChatRow
	var ts int64
	var space string
	err := rows.Scan(&r.ID, &r.Name, &r.Snippet, &ts, &r.Unread, &r.Group, &r.Encrypted, &space, &r.Muted, &r.Display)
	r.LastActivity = time.UnixMilli(ts)
	r.Space = Space(space)
	if r.Muted {
		r.Unread = false
	}
	return r, err
}

// Chats lists a space's chats, newest first. A query searches names in
// every space (case-insensitive).
func (s *Store) Chats(space Space, query string) ([]ChatRow, error) {
	rows, err := s.DB.Query(`SELECT `+chatCols+`
FROM tuon_chats c LEFT JOIN tuon_contacts ct ON ct.id = c.id
WHERE c.last_ts > 0
	AND (? <> '' OR c.space = ? OR (? AND c.unread = 1 AND c.muted = 0))  -- searching looks everywhere
	AND (? = '' OR COALESCE(NULLIF(c.name, ''), ct.name, c.id) LIKE '%' || ? || '%' ESCAPE '\')
ORDER BY c.last_ts DESC`, query, string(space), space == Unread, query, likeText(query))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatRow
	for rows.Next() {
		r, err := scanChat(rows)
		r.Snippet = s.dec(r.Snippet)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range out {
		s.nameUnnamed(&out[i])
	}
	return out, nil
}

func (s *Store) Chat(id string) (ChatRow, error) {
	id = s.Resolve(id)
	r, err := s.scanChatRow(s.DB.QueryRow(`SELECT `+chatCols+`
FROM tuon_chats c LEFT JOIN tuon_contacts ct ON ct.id = c.id WHERE c.id = ?`, id))
	if err == nil {
		s.nameUnnamed(&r)
	}
	return r, err
}

// nameUnnamed labels a chat with no name like Messenger does, from its
// people: "Sherwin, Ed and 3 others". A chat with several people writing in
// it is a group even if we never got its details.
func (s *Store) nameUnnamed(r *ChatRow) {
	if r.Name != "" || (!r.Group && r.Display != r.ID) {
		return
	}
	people, err := s.People(r.ID, s.Self)
	if err != nil || len(people) == 0 {
		return
	}
	if len(people) > 1 {
		r.Group = true
	}
	if !r.Group {
		r.Display = people[0].Name
		return
	}
	var names []string
	for _, p := range people {
		names = append(names, strings.Fields(p.Name)[0])
	}
	switch n := len(names); {
	case n <= 3:
		r.Display = strings.Join(names[:max(0, n-1)], ", ")
		if n > 1 {
			r.Display += " and "
		}
		r.Display += names[n-1]
	default:
		r.Display = fmt.Sprintf("%s, %s and %d others", names[0], names[1], n-2)
	}
}

// UnreadCounts returns unread chats per space.
func (s *Store) UnreadCounts() map[Space]int {
	out := map[Space]int{}
	rows, err := s.DB.Query(`SELECT space, COUNT(*) FROM tuon_chats WHERE unread = 1 AND muted = 0 GROUP BY space`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var sp string
		var n int
		if rows.Scan(&sp, &n) == nil {
			out[Space(sp)] = n
		}
	}
	return out
}

func (s *Store) SetSpace(chatID string, sp Space) error {
	_, err := s.DB.Exec(`UPDATE tuon_chats SET space = ? WHERE id = ?`, string(sp), chatID)
	return err
}

func (s *Store) ToggleMute(chatID string) error {
	_, err := s.DB.Exec(`UPDATE tuon_chats SET muted = 1 - muted WHERE id = ?`, chatID)
	return err
}

func (s *Store) MarkRead(chatID string) error {
	_, err := s.DB.Exec(`UPDATE tuon_chats SET unread = 0, read_ts = MAX(read_ts, last_ts) WHERE id = ?`, chatID)
	return err
}

func (s *Store) RemoveMessage(id string) error {
	var chatID string
	var missing bool
	_ = s.DB.QueryRow(`SELECT chat_id, missing <> 0 FROM tuon_messages WHERE id = ?`, id).Scan(&chatID, &missing)
	if _, err := s.DB.Exec(`DELETE FROM tuon_messages WHERE id = ?`, id); err != nil {
		return err
	}
	if missing && chatID != "" { // the preview may still say "loading": show the newest real message
		s.refreshSnippet(chatID)
	}
	return nil
}

// refreshSnippet sets a chat's preview from its newest stored message.
func (s *Store) refreshSnippet(chatID string) {
	msgs, err := s.Messages(chatID, 1, s.Self)
	if err != nil {
		return
	}
	snippet := ""
	if len(msgs) == 1 {
		m := msgs[0]
		snippet = m.Text
		if snippet == "" && len(m.Media) > 0 {
			snippet = "[" + string(m.Media[0].Kind) + "]"
		}
		if m.FromMe {
			snippet = "You: " + snippet
		} else if c, err := s.Chat(chatID); err == nil && c.Group && m.SenderName != "" {
			snippet = strings.Fields(m.SenderName)[0] + ": " + snippet
		}
	}
	_, _ = s.DB.Exec(`UPDATE tuon_chats SET snippet = ? WHERE id = ?`, s.enc(strings.Join(strings.Fields(snippet), " ")), chatID)
}

// SetReaction stores one person's reaction; emoji "" removes it.
func (s *Store) SetReaction(e connector.ReactionEvent) error {
	if e.Emoji == "" {
		_, err := s.DB.Exec(`DELETE FROM tuon_reactions WHERE message_id = ? AND actor_id = ?`, e.MessageID, e.ActorID)
		return err
	}
	_, err := s.DB.Exec(`INSERT INTO tuon_reactions (message_id, actor_id, chat_id, emoji) VALUES (?, ?, ?, ?)
ON CONFLICT(message_id, actor_id) DO UPDATE SET emoji = excluded.emoji`, e.MessageID, e.ActorID, e.ChatID, e.Emoji)
	return err
}

func (s *Store) AddParticipant(e connector.ParticipantEvent) error {
	_, err := s.DB.Exec(`INSERT INTO tuon_participants (chat_id, user_id, nickname) VALUES (?, ?, ?)
ON CONFLICT(chat_id, user_id) DO UPDATE SET nickname = CASE WHEN excluded.nickname <> '' THEN excluded.nickname ELSE nickname END`,
		e.ChatID, e.UserID, e.Nickname)
	return err
}

// People lists who can be @mentioned in a chat (everyone but selfID):
// group members, anyone who has written there, or the other person in a 1:1.
func (s *Store) People(chatID, selfID string) ([]Person, error) {
	rows, err := s.DB.Query(`
SELECT u.id, COALESCE(NULLIF(p.nickname, ''), ct.name, '') AS name FROM (
	SELECT user_id AS id FROM tuon_participants WHERE chat_id = ?1
	UNION SELECT DISTINCT sender_id FROM tuon_messages WHERE chat_id = ?1
	UNION SELECT id FROM tuon_chats WHERE id = ?1 AND grp = 0
) u
LEFT JOIN tuon_participants p ON p.chat_id = ?1 AND p.user_id = u.id
LEFT JOIN tuon_contacts ct ON ct.id = u.id
WHERE u.id <> ?2 AND name <> ''
ORDER BY name`, chatID, selfID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Person
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.Name); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Messages returns the newest `limit` messages of a chat, oldest first,
// with reactions. selfID marks which reaction is ours.
func (s *Store) Messages(chatID string, limit int, selfID string) ([]MessageRow, error) {
	rows, err := s.DB.Query(`SELECT * FROM (
	SELECT m.id, m.chat_id, m.sender_id, m.text, m.ts, m.from_me, m.media, COALESCE(ct.name, ''), m.pending, m.reply_to
	FROM tuon_messages m LEFT JOIN tuon_contacts ct ON ct.id = m.sender_id
	WHERE m.chat_id = ? ORDER BY m.ts DESC LIMIT ?
) ORDER BY 5 ASC`, chatID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessageRow
	for rows.Next() {
		var r MessageRow
		var ts int64
		var media string
		if err := rows.Scan(&r.ID, &r.ChatID, &r.SenderID, &r.Text, &ts, &r.FromMe, &media, &r.SenderName, &r.Pending, &r.ReplyToID); err != nil {
			return nil, err
		}
		r.Time = time.UnixMilli(ts)
		r.Text, media = s.dec(r.Text), s.dec(media)
		if media != "" {
			_ = json.Unmarshal([]byte(media), &r.Media)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	s.attachReplies(out, selfID)
	return out, s.attachReactions(chatID, selfID, out)
}

func (s *Store) attachReactions(chatID, selfID string, msgs []MessageRow) error {
	idx := map[string]int{}
	for i, m := range msgs {
		idx[m.ID] = i
	}
	rows, err := s.DB.Query(`SELECT message_id, emoji, COUNT(*), MAX(actor_id = ?), MIN(rowid)
FROM tuon_reactions WHERE chat_id = ? GROUP BY message_id, emoji ORDER BY 5`, selfID, chatID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var mid string
		var rc ReactionCount
		var first int64
		if err := rows.Scan(&mid, &rc.Emoji, &rc.Count, &rc.Mine, &first); err != nil {
			return err
		}
		if i, ok := idx[mid]; ok {
			msgs[i].Reactions = append(msgs[i].Reactions, rc)
			if rc.Mine {
				msgs[i].MyReaction = rc.Emoji
			}
		}
	}
	return rows.Err()
}

// Prune keeps the newest keep messages per chat (older ones reload from
// Messenger when you scroll up) and drops reactions left without a message.
func (s *Store) Prune(keep int) error {
	_, err := s.DB.Exec(`DELETE FROM tuon_messages WHERE id IN (
	SELECT id FROM (
		SELECT id, ROW_NUMBER() OVER (PARTITION BY chat_id ORDER BY ts DESC) AS n FROM tuon_messages
	) WHERE n > ?)`, keep)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`DELETE FROM tuon_reactions WHERE message_id NOT IN (SELECT id FROM tuon_messages)`)
	return err
}

// Drafts returns the unsent text saved per chat.
func (s *Store) Drafts() map[string]string {
	out := map[string]string{}
	rows, err := s.DB.Query(`SELECT chat_id, text FROM tuon_drafts`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, text string
		if rows.Scan(&id, &text) == nil {
			out[id] = s.dec(text)
		}
	}
	return out
}

// SaveDrafts replaces the saved drafts (text only; files aren't kept).
func (s *Store) SaveDrafts(drafts map[string]string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM tuon_drafts`); err != nil {
		return err
	}
	for id, text := range drafts {
		if strings.TrimSpace(text) == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO tuon_drafts (chat_id, text) VALUES (?, ?)`, id, s.enc(text)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SettingAccount: the Messenger user ID this data belongs to.
const SettingAccount = "account"

// Account is the user ID the local data belongs to: the saved setting, or
// for data from before it was saved, the encrypted-chat device's owner.
func (s *Store) Account() string {
	if a := s.Setting(SettingAccount, ""); a != "" {
		return a
	}
	var jid string
	_ = s.DB.QueryRow(`SELECT jid FROM whatsmeow_device LIMIT 1`).Scan(&jid)
	if i := strings.IndexAny(jid, ":@"); i > 0 {
		return jid[:i]
	}
	return ""
}

// EncryptAll seals everything written before encryption was on: message
// text and media, chat previews, drafts. Then it checkpoints and compacts,
// so no plain copy is left in the file. Safe to run every start.
func (s *Store) EncryptAll() (int, error) {
	if s.seal == nil {
		return 0, nil
	}
	n := 0
	seal := func(table, key, col string) error {
		rows, err := s.DB.Query(`SELECT ` + key + `, ` + col + ` FROM ` + table + ` WHERE ` + col + ` <> '' AND ` + col + ` NOT LIKE 'v1:%'`)
		if err != nil {
			return err
		}
		type kv struct{ k, v string }
		var todo []kv
		for rows.Next() {
			var x kv
			if rows.Scan(&x.k, &x.v) == nil {
				todo = append(todo, x)
			}
		}
		rows.Close()
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, x := range todo {
			if _, err := tx.Exec(`UPDATE `+table+` SET `+col+` = ? WHERE `+key+` = ?`, s.seal.SealString(x.v), x.k); err != nil {
				return err
			}
			n++
		}
		return tx.Commit()
	}
	for _, c := range [][3]string{
		{"tuon_messages", "id", "text"}, {"tuon_messages", "id", "media"},
		{"tuon_chats", "id", "snippet"}, {"tuon_drafts", "chat_id", "text"},
	} {
		if err := seal(c[0], c[1], c[2]); err != nil {
			return n, err
		}
	}
	if n > 0 { // push the change out of the WAL and rebuild the file
		_, _ = s.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
		_, _ = s.DB.Exec(`VACUUM`)
	}
	return n, nil
}

// Compact gives back disk space once a quarter of the file is free pages
// (deleted messages); SQLite otherwise keeps the file at its biggest size.
func (s *Store) Compact() (bool, error) {
	var pages, free int64
	if err := s.DB.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		return false, err
	}
	if err := s.DB.QueryRow(`PRAGMA freelist_count`).Scan(&free); err != nil {
		return false, err
	}
	if pages == 0 || free*4 < pages {
		return false, nil
	}
	_, err := s.DB.Exec(`VACUUM`)
	return err == nil, err
}

func (s *Store) Setting(k, def string) string {
	var v string
	if err := s.DB.QueryRow(`SELECT v FROM tuon_settings WHERE k = ?`, k).Scan(&v); err != nil {
		return def
	}
	return v
}

func (s *Store) SetSetting(k, v string) error {
	_, err := s.DB.Exec(`INSERT INTO tuon_settings (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, k, v)
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Spaces lists your folders in order, then Inbox.
func (s *Store) Spaces() []Space {
	rows, err := s.DB.Query(`SELECT name FROM tuon_spaces ORDER BY pos, name`)
	if err != nil {
		return []Space{Inbox}
	}
	defer rows.Close()
	var out []Space
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			out = append(out, Space(n))
		}
	}
	return append(out, Inbox)
}

// Label is how a space is shown.
func Label(sp Space) string {
	switch sp {
	case Inbox:
		return "Inbox"
	case Unread:
		return "Unread"
	}
	return string(sp)
}

func cleanName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	switch {
	case name == "":
		return "", fmt.Errorf("give the folder a name")
	case strings.EqualFold(name, string(Inbox)):
		return "", fmt.Errorf("Inbox is built in")
	case len([]rune(name)) > 24:
		return "", fmt.Errorf("keep folder names under 24 characters")
	}
	return name, nil
}

func (s *Store) AddSpace(name string) (Space, error) {
	name, err := cleanName(name)
	if err != nil {
		return "", err
	}
	_, err = s.DB.Exec(`INSERT INTO tuon_spaces (name, pos) VALUES (?, (SELECT COALESCE(MAX(pos), -1) + 1 FROM tuon_spaces))`, name)
	if err != nil {
		return "", fmt.Errorf("there's already a folder called %q", name)
	}
	return Space(name), nil
}

// RenameSpace renames a folder; its chats move with it.
func (s *Store) RenameSpace(old Space, name string) (Space, error) {
	if old == Inbox {
		return "", fmt.Errorf("Inbox can't be renamed")
	}
	name, err := cleanName(name)
	if err != nil {
		return "", err
	}
	// one step: the folder and its chats move together, or neither does
	tx, err := s.DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE tuon_spaces SET name = ? WHERE name = ?`, name, string(old))
	if err != nil {
		return "", fmt.Errorf("there's already a folder called %q", name)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", fmt.Errorf("there's no folder called %q", string(old))
	}
	if _, err := tx.Exec(`UPDATE tuon_chats SET space = ? WHERE space = ?`, name, string(old)); err != nil {
		return "", err
	}
	return Space(name), tx.Commit()
}

// DeleteSpace removes a folder; its chats go back to Inbox.
func (s *Store) DeleteSpace(sp Space) error {
	if sp == Inbox {
		return fmt.Errorf("Inbox can't be deleted")
	}
	if _, err := s.DB.Exec(`UPDATE tuon_chats SET space = ? WHERE space = ?`, string(Inbox), string(sp)); err != nil {
		return err
	}
	_, err := s.DB.Exec(`DELETE FROM tuon_spaces WHERE name = ?`, string(sp))
	return err
}

// MoveSpace shifts a folder left (-1) or right (+1).
func (s *Store) MoveSpace(sp Space, d int) error {
	list := s.Spaces()
	list = list[:len(list)-1] // not Inbox
	i := -1
	for j, x := range list {
		if x == sp {
			i = j
		}
	}
	if i < 0 || i+d < 0 || i+d >= len(list) {
		return nil
	}
	list[i], list[i+d] = list[i+d], list[i]
	for pos, x := range list {
		if _, err := s.DB.Exec(`UPDATE tuon_spaces SET pos = ? WHERE name = ?`, pos, string(x)); err != nil {
			return err
		}
	}
	return nil
}

// SpaceCounts: how many chats are in each space.
func (s *Store) SpaceCounts() map[Space]int {
	out := map[Space]int{}
	rows, err := s.DB.Query(`SELECT space, COUNT(*) FROM tuon_chats WHERE last_ts > 0 GROUP BY space`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var sp string
		var n int
		if rows.Scan(&sp, &n) == nil {
			out[Space(sp)] = n
		}
	}
	return out
}

// likeText escapes LIKE's wildcards, so "a_b" or "50%" match literally.
func likeText(q string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
}

// SearchContacts finds people by name (not you).
func (s *Store) SearchContacts(query string, limit int) []Person {
	rows, err := s.DB.Query(`SELECT id, name FROM tuon_contacts
WHERE id <> ? AND name LIKE '%' || ? || '%' ESCAPE '\' ORDER BY name LIMIT ?`, s.Self, likeText(query), limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Person
	for rows.Next() {
		var p Person
		if rows.Scan(&p.ID, &p.Name) == nil {
			out = append(out, p)
		}
	}
	return out
}

// EnsureChat makes sure a chat row exists (for a 1:1 opened from search).
func (s *Store) EnsureChat(id string, encrypted bool) error {
	_, err := s.DB.Exec(`INSERT INTO tuon_chats (id, encrypted) VALUES (?, ?) ON CONFLICT(id) DO NOTHING`, id, b2i(encrypted))
	return err
}

// attachReplies fills in who and what each reply answers.
func (s *Store) attachReplies(msgs []MessageRow, selfID string) {
	byID := map[string]MessageRow{}
	for _, m := range msgs {
		byID[m.ID] = m
	}
	for i := range msgs {
		id := msgs[i].ReplyToID
		if id == "" {
			continue
		}
		parent, ok := byID[id]
		if !ok { // older than what's loaded
			var media string
			var ts int64
			err := s.DB.QueryRow(`SELECT m.sender_id, m.text, m.media, m.ts, COALESCE(ct.name, '')
FROM tuon_messages m LEFT JOIN tuon_contacts ct ON ct.id = m.sender_id WHERE m.id = ?`, id).
				Scan(&parent.SenderID, &parent.Text, &media, &ts, &parent.SenderName)
			if err != nil {
				msgs[i].ReplyTo = &Quote{Name: "", Text: "an earlier message"}
				continue
			}
			parent.Text, media = s.dec(parent.Text), s.dec(media)
			if media != "" {
				_ = json.Unmarshal([]byte(media), &parent.Media)
			}
		}
		q := &Quote{Name: parent.SenderName, Text: parent.Text}
		if parent.SenderID == selfID {
			q.Name = "You"
		} else if f := strings.Fields(q.Name); len(f) > 0 {
			q.Name = f[0]
		}
		if q.Text == "" && len(parent.Media) > 0 {
			q.Text = "[" + string(parent.Media[0].Kind) + "]"
		}
		msgs[i].ReplyTo = q
	}
}

// Resolve maps a chat ID that was merged into another chat to that chat.
func (s *Store) Resolve(id string) string {
	var to string
	if s.DB.QueryRow(`SELECT to_id FROM tuon_alias WHERE from_id = ?`, id).Scan(&to) == nil && to != "" {
		return to
	}
	return id
}

// Merge folds chat from into chat to: Messenger lists an encrypted 1:1
// under a thread number, but its messages arrive under the person's ID.
// Messages, reactions and people move over; the folder, name and unread
// state carry across, and from is remembered so later events land in to.
func (s *Store) Merge(from, to string) error {
	to = s.Resolve(to)
	if from == "" || to == "" || from == to {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	steps := []string{
		`INSERT INTO tuon_alias (from_id, to_id) VALUES (?1, ?2) ON CONFLICT(from_id) DO UPDATE SET to_id = excluded.to_id`,
		`UPDATE tuon_alias SET to_id = ?2 WHERE to_id = ?1`,
		`INSERT INTO tuon_chats (id) VALUES (?2) ON CONFLICT(id) DO NOTHING`,
		`UPDATE tuon_chats SET
			name      = CASE WHEN name = '' THEN (SELECT name FROM tuon_chats WHERE id = ?1) ELSE name END,
			snippet   = CASE WHEN (SELECT last_ts FROM tuon_chats WHERE id = ?1) > last_ts AND (SELECT snippet FROM tuon_chats WHERE id = ?1) <> ''
			                 THEN (SELECT snippet FROM tuon_chats WHERE id = ?1) ELSE snippet END,
			unread    = MAX(unread, (SELECT unread FROM tuon_chats WHERE id = ?1)),
			last_ts   = MAX(last_ts, (SELECT last_ts FROM tuon_chats WHERE id = ?1)),
			read_ts   = MAX(read_ts, (SELECT read_ts FROM tuon_chats WHERE id = ?1)),
			muted     = MAX(muted, (SELECT muted FROM tuon_chats WHERE id = ?1)),
			encrypted = MAX(encrypted, (SELECT encrypted FROM tuon_chats WHERE id = ?1)),
			space     = CASE WHEN space = 'inbox' THEN (SELECT space FROM tuon_chats WHERE id = ?1) ELSE space END
		WHERE id = ?2 AND EXISTS (SELECT 1 FROM tuon_chats WHERE id = ?1)`,
		`UPDATE tuon_messages SET chat_id = ?2 WHERE chat_id = ?1`,
		`UPDATE tuon_reactions SET chat_id = ?2 WHERE chat_id = ?1`,
		`INSERT OR IGNORE INTO tuon_participants (chat_id, user_id, nickname) SELECT ?2, user_id, nickname FROM tuon_participants WHERE chat_id = ?1`,
		`DELETE FROM tuon_participants WHERE chat_id = ?1`,
		`INSERT OR IGNORE INTO tuon_drafts (chat_id, text) SELECT ?2, text FROM tuon_drafts WHERE chat_id = ?1`,
		`DELETE FROM tuon_drafts WHERE chat_id = ?1`,
		`DELETE FROM tuon_chats WHERE id = ?1`,
	}
	for _, q := range steps {
		if _, err := tx.Exec(q, from, to); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MergeOneToOne merges every encrypted 1:1 thread into the chat under the
// other person's ID (where its messages arrive).
func (s *Store) MergeOneToOne() error {
	if s.Self == "" {
		return nil
	}
	rows, err := s.DB.Query(`
SELECT c.id, MIN(p.user_id) FROM tuon_chats c JOIN tuon_participants p ON p.chat_id = c.id AND p.user_id <> ?
WHERE c.encrypted = 1 AND c.grp = 0
GROUP BY c.id HAVING COUNT(*) = 1 AND MIN(p.user_id) <> c.id`, s.Self)
	if err != nil {
		return err
	}
	var pairs [][2]string
	for rows.Next() {
		var a, b string
		if rows.Scan(&a, &b) == nil {
			pairs = append(pairs, [2]string{a, b})
		}
	}
	rows.Close()
	for _, p := range pairs {
		if err := s.Merge(p[0], p[1]); err != nil {
			return err
		}
	}
	return nil
}

// ErrRunning means another tuon already has the data folder open.
var ErrRunning = errors.New("tuon is already running in another window; use that one, or quit it first")
