package messenger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"go.mau.fi/mautrix-meta/pkg/messagix/securehistory"
	"go.mau.fi/mautrix-meta/pkg/messagix/table"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waArmadilloBackupMessage"
	"go.mau.fi/whatsmeow/proto/waConsumerApplication"
	"go.mau.fi/whatsmeow/proto/waMsgApplication"
	"google.golang.org/protobuf/proto"

	"github.com/ralph/tuon/internal/connector"
)

// Older encrypted messages live in Messenger's backup ("secure storage"),
// locked with your 6-digit PIN. RestoreHistory uses the PIN once to enroll
// tuon as a backup device; the PIN itself is never kept. After that, older
// messages load from the backup like any other history.

// ErrNoBackup means the account has no PIN backup to restore from.
var ErrNoBackup = errors.New("this account has no Messenger PIN backup (set one up in Messenger: Settings, Privacy & safety, End-to-end encrypted chats, Message storage)")

var pinRe = regexp.MustCompile(`^[0-9]{6}$`)

const historyTable = `CREATE TABLE IF NOT EXISTS tuon_history_device (
	id   INTEGER PRIMARY KEY CHECK (id = 1),
	data TEXT NOT NULL
)`

func (c *Conn) historyDevice() *securehistory.Device {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hdev != nil {
		return c.hdev
	}
	_, _ = c.db.Exec(historyTable)
	var data string
	if c.db.QueryRow(`SELECT data FROM tuon_history_device WHERE id = 1`).Scan(&data) != nil {
		return nil
	}
	wasPlain := c.seal != nil && !c.seal.IsSealed(data)
	if c.seal != nil && !wasPlain {
		plain, err := c.seal.OpenString(data)
		if err != nil {
			return nil
		}
		data = plain
	}
	var d securehistory.Device
	if json.Unmarshal([]byte(data), &d) != nil || d.ID == "" {
		return nil
	}
	c.hdev = &d
	if wasPlain { // saved before encryption: seal it now
		go func() { _ = c.saveHistoryDevice(&d) }()
	}
	return c.hdev
}

func (c *Conn) saveHistoryDevice(d *securehistory.Device) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if _, err := c.db.Exec(historyTable); err != nil {
		return err
	}
	data := string(b)
	if c.seal != nil {
		data = c.seal.SealString(data)
	}
	if _, err := c.db.Exec(`INSERT INTO tuon_history_device (id, data) VALUES (1, ?)
ON CONFLICT(id) DO UPDATE SET data = excluded.data`, data); err != nil {
		return err
	}
	c.mu.Lock()
	c.hdev = d
	c.mu.Unlock()
	return nil
}

// HasHistory reports whether older encrypted messages can be loaded.
func (c *Conn) HasHistory() bool { return c.historyDevice() != nil }

// HasBackup reports whether the account has a PIN backup to restore from.
func (c *Conn) HasBackup(ctx context.Context) (bool, error) {
	_, err := c.cli.GetSecureHistoryClient().GetBackup(ctx)
	if errors.Is(err, securehistory.ErrNoPINBackup) {
		return false, nil
	}
	return err == nil, err
}

// AttemptsError is a wrong PIN, with how many tries Messenger still allows.
type AttemptsError struct{ Left int }

func (e *AttemptsError) Error() string {
	if e.Left == 0 {
		return "wrong PIN, and no attempts are left: Messenger has locked the backup"
	}
	return fmt.Sprintf("wrong PIN; Messenger allows %d more attempts", e.Left)
}

// RestoreHistory unlocks the PIN backup and enrolls tuon so it can read
// older encrypted messages. Call it after Start, once encrypted chats are
// connected.
func (c *Conn) RestoreHistory(ctx context.Context, pin string) error {
	if c.HasHistory() {
		return nil
	}
	if !pinRe.MatchString(pin) {
		return errors.New("the PIN is 6 digits")
	}
	var wa *whatsmeow.Client
	for i := 0; i < 60 && wa == nil; i++ { // encrypted chats start just after connecting
		c.mu.Lock()
		wa = c.wa
		c.mu.Unlock()
		if wa == nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	if _, err := c.readyWA(ctx, wa); err != nil {
		return err
	}
	if c.wadev == nil {
		return errors.New("encrypted chats aren't set up yet")
	}
	h := c.cli.GetSecureHistoryClient()
	backup, err := h.GetBackup(ctx)
	if errors.Is(err, securehistory.ErrNoPINBackup) {
		return ErrNoBackup
	} else if err != nil {
		return fmt.Errorf("check backup: %w", err)
	}
	rec, err := h.RecoverPIN(ctx, backup, []byte(pin))
	if err != nil {
		var re *securehistory.RecoveryError
		if errors.As(err, &re) && re.AttemptsRemaining != nil {
			return &AttemptsError{Left: *re.AttemptsRemaining}
		}
		return fmt.Errorf("unlock backup: %w", err)
	}
	virtual, err := h.GetVirtualDevice(ctx, backup, c.self, rec.Code)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	epochs, err := virtual.DeriveEpochs()
	if err != nil {
		return fmt.Errorf("read backup keys: %w", err)
	}
	enrollment, err := virtual.PrepareDevice(backup.ID, h.RegistrationID, c.wadev.IdentityKey, epochs)
	if err != nil {
		return fmt.Errorf("prepare backup device: %w", err)
	}
	dev, err := h.AddDevice(ctx, enrollment)
	if err != nil {
		return fmt.Errorf("enroll backup device: %w", err)
	}
	return c.saveHistoryDevice(dev)
}

// loadEncryptedHistory fetches one page of backed-up messages older than
// before, and emits them like live ones.
func (c *Conn) loadEncryptedHistory(ctx context.Context, chatID string, group bool, before time.Time) error {
	dev := c.historyDevice()
	if dev == nil {
		return nil
	}
	h := c.cli.GetSecureHistoryClient()
	ts := before.UnixMilli()
	if before.IsZero() {
		ts = time.Now().Add(time.Minute).UnixMilli()
	}
	page, err := h.FetchMessages(ctx, dev, chatID, true, ts, 50)
	if err != nil {
		return err
	}
	if page.Epochs != nil && len(page.Epochs.Edges) > 0 {
		if nd, err := dev.WithEpochs(page.Epochs); err == nil {
			_ = c.saveHistoryDevice(nd)
			dev = nd
		}
	}
	if page.Exception != nil && *page.Exception != "" {
		return fmt.Errorf("backup: %s", connector.SafeText(*page.Exception, false))
	}
	msgs, err := page.Decrypt(dev, chatID, c.self, group)
	if err != nil {
		return fmt.Errorf("decrypt history: %w", err)
	}
	shown := 0
	for _, m := range msgs {
		if m == nil || m.Message == nil {
			continue
		}
		if msg, ok := c.archiveMessage(chatID, m.Message); ok {
			c.emit(connector.MessageEvent{Message: msg})
			shown++
		} else if r, placeholder, ok := c.archiveReaction(chatID, m.Message); ok {
			// a reaction that failed to decrypt live showed as a "loading"
			// placeholder under its own ID: apply it, drop the placeholder
			c.emit(r)
			c.emit(connector.MessageRemovedEvent{ID: placeholder})
		}
	}
	// counts only, never message content
	c.log.Warn().Str("chat", chatID).Int("fetched", len(page.Messages)).Int("decrypted", len(msgs)).
		Int("shown", shown).Bool("thread_missing", page.ThreadMissing).Int64("before", ts).Msg("encrypted history page")
	return nil
}

// archiveReaction reads a backed-up reaction: the reaction to apply, and
// the reaction's own message ID (a live placeholder may carry it).
func (c *Conn) archiveReaction(chatID string, p *securehistory.DecryptedProtobuf) (connector.ReactionEvent, string, bool) {
	content, ok := backupContent(p)
	if !ok || content.GetReactionMessage() == nil {
		return connector.ReactionEvent{}, "", false
	}
	r := content.GetReactionMessage()
	meta := p.Message.GetMetadata()
	if meta.GetMessageID() == "" || meta.GetSenderID() == "" || r.GetKey().GetID() == "" {
		return connector.ReactionEvent{}, "", false
	}
	return connector.ReactionEvent{
		ChatID: chatID, MessageID: r.GetKey().GetID(),
		ActorID: meta.GetSenderID(), Emoji: connector.SafeText(r.GetText(), false),
	}, meta.GetMessageID(), true
}

// backupContent unwraps a backed-up message to its content.
func backupContent(p *securehistory.DecryptedProtobuf) (*waConsumerApplication.ConsumerApplication_Content, bool) {
	payload, ok := p.Message.GetPayload().(*waArmadilloBackupMessage.BackupMessage_EncryptedTransportMessage)
	if !ok {
		return nil, false
	}
	var app waMsgApplication.MessageApplication
	if proto.Unmarshal(payload.EncryptedTransportMessage, &app) != nil {
		return nil, false
	}
	dec, err := whatsmeow.DecodeFBApplication(&app)
	if err != nil {
		return nil, false
	}
	ca, ok := dec.Message.(*waConsumerApplication.ConsumerApplication)
	if !ok {
		return nil, false
	}
	content := ca.GetPayload().GetContent()
	return content, content != nil
}

func (c *Conn) archiveMessage(chatID string, p *securehistory.DecryptedProtobuf) (connector.Message, bool) {
	payload, ok := p.Message.GetPayload().(*waArmadilloBackupMessage.BackupMessage_EncryptedTransportMessage)
	if !ok {
		return connector.Message{}, false // events, admin lines
	}
	var app waMsgApplication.MessageApplication
	if proto.Unmarshal(payload.EncryptedTransportMessage, &app) != nil {
		return connector.Message{}, false
	}
	dec, err := whatsmeow.DecodeFBApplication(&app)
	if err != nil {
		return connector.Message{}, false
	}
	ca, ok := dec.Message.(*waConsumerApplication.ConsumerApplication)
	if !ok {
		return connector.Message{}, false
	}
	content := ca.GetPayload().GetContent()
	if content == nil || content.GetReactionMessage() != nil || content.GetEditMessage() != nil {
		return connector.Message{}, false
	}
	text, media := waContent(content)
	text = connector.SafeText(text, true)
	if text == "" && len(media) == 0 {
		return connector.Message{}, false
	}
	meta := p.Message.GetMetadata()
	if meta.GetMessageID() == "" || meta.GetSenderID() == "" {
		return connector.Message{}, false
	}
	return connector.Message{
		ID:        meta.GetMessageID(),
		ChatID:    chatID,
		SenderID:  meta.GetSenderID(),
		Text:      text,
		Time:      time.UnixMilli(p.TimestampMS),
		FromMe:    meta.GetSenderID() == strconv.FormatInt(c.self, 10),
		Media:     media,
		ReplyToID: app.GetMetadata().GetQuotedMessage().GetStanzaID(),
	}, true
}

func (c *Conn) isEncryptedGroup(key int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.threadType[key] == table.ENCRYPTED_OVER_WA_GROUP
}
