package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"
	"golang.org/x/term"

	"github.com/ralph/tuon/internal/connector"
	"github.com/ralph/tuon/internal/connector/messenger"
	"github.com/ralph/tuon/internal/store"
)

// history unlocks Messenger's encrypted backup with your PIN, so chats can
// load their older encrypted messages. The PIN is used once and not kept.
func history() error {
	creds, err := loadLogin()
	if err != nil {
		return err
	}
	release, err := store.Lock()
	if err != nil {
		return err
	}
	defer release()
	st, err := store.Open()
	if err != nil {
		return err
	}
	defer st.Close()
	kdb, allowNew, err := useVault(st)
	if err != nil {
		return err
	}
	if kdb != nil {
		defer kdb.Close()
	}
	logFile, err := os.OpenFile(filepath.Join(store.Dir(), "tuon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	zerolog.SetGlobalLevel(zerolog.WarnLevel)
	log := zerolog.New(logFile).With().Timestamp().Logger()
	zlog.Logger = log

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	conn := messenger.New(creds, st.DB, log)
	if vlt != nil {
		conn.SetSealer(vlt)
	}
	if kdb != nil {
		conn.SetKeyDB(kdb)
	}
	conn.AllowNewDevice = allowNew
	fmt.Println("\n  Connecting to Messenger…")
	if err := conn.Start(ctx); err != nil {
		return err
	}
	defer conn.Close()
	st.Self = conn.SelfID()
	go keep(st, conn) // messages that arrive meanwhile still get saved
	wctx, wcancel := context.WithTimeout(ctx, 45*time.Second)
	err = conn.WaitEncrypted(wctx) // also finishes registering a new device
	wcancel()
	if err != nil {
		return err
	}

	if conn.HasHistory() {
		fmt.Println("  Older encrypted messages are already set up.")
		return nil
	}
	if ok, err := conn.HasBackup(ctx); err != nil {
		return fmt.Errorf("check Messenger's backup: %w", err)
	} else if !ok {
		fmt.Println("  Your account has no encrypted-chat backup, so there's nothing older to restore.")
		fmt.Println("  New messages still arrive. To start a backup: Messenger, Settings, Privacy & safety,")
		fmt.Println("  End-to-end encrypted chats, Message storage. Then run `tuon history`.")
		return nil
	}
	fmt.Print(`
  Restore older encrypted chats

  Messenger keeps a backup of your encrypted chats, locked with the
  6-digit PIN you chose in Messenger. Tuon uses the PIN once to join that
  backup as one of your devices, then forgets it.

  Messenger allows only a few wrong tries before it locks the backup.
  If you're not sure of your PIN, check or reset it in Messenger first:
  Settings, Privacy & safety, End-to-end encrypted chats, Message storage.

`)
	for {
		fmt.Print("  PIN (leave empty to skip): ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return err
		}
		pin := strings.TrimSpace(string(b))
		if pin == "" {
			fmt.Println("  Skipped. Run `tuon history` any time.")
			return nil
		}
		// keep just the digits: a pasted PIN can carry invisible paste
		// markers, and people type "12 34 56" or "123-456"
		if digits := pinDigits(pin); len(digits) != 6 {
			fmt.Printf("  That wasn't 6 digits (got %d characters, %d of them digits). Try again; nothing was sent.\n", len([]rune(pin)), len(digits))
			continue
		} else {
			pin = digits
		}
		err = conn.RestoreHistory(ctx, pin)
		pin = ""
		var wrong *messenger.AttemptsError
		switch {
		case err == nil:
			fmt.Println("\n  Done. Each encrypted chat now loads its older messages when you open it.")
			return nil
		case errors.As(err, &wrong) && wrong.Left > 0:
			fmt.Printf("  %v.\n", err)
			if wrong.Left <= 2 {
				fmt.Println("  To be safe, check your PIN in Messenger before trying again.")
				return nil
			}
		default:
			return err
		}
	}
}

// pinDigits drops everything but 0-9, including terminal paste markers
// ("ESC[200~" … "ESC[201~"), whose own digits must not count.
func pinDigits(s string) string {
	s = strings.NewReplacer("\x1b[200~", "", "\x1b[201~", "").Replace(s)
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// keep saves what arrives while `tuon history` runs.
func keep(st *store.Store, conn *messenger.Conn) {
	for e := range conn.Events() {
		switch e := e.(type) {
		case connector.ContactEvent:
			_ = st.UpsertContact(e.ID, e.Name)
		case connector.ChatAliasEvent:
			_ = st.Merge(e.From, e.To)
		case connector.ChatEvent:
			e.Chat.ID = st.Resolve(e.Chat.ID)
			_ = st.UpsertChat(e.Chat)
		case connector.MessageEvent:
			e.Message.ChatID = st.Resolve(e.Message.ChatID)
			_, _ = st.AddMessage(e.Message, false)
		case connector.ParticipantEvent:
			e.ChatID = st.Resolve(e.ChatID)
			_ = st.AddParticipant(e)
		case connector.ReactionEvent:
			e.ChatID = st.Resolve(e.ChatID)
			_ = st.SetReaction(e)
		}
	}
}

// setup is the first-run path: log in if needed, then restore history.
func setup() error {
	if _, err := openVault(); err != nil { // a cancelled unlock isn't "not logged in"
		return err
	}
	if _, err := loggedIn(); err != nil {
		if err := login(); err != nil {
			return err
		}
	} else {
		fmt.Println("\n  Already logged in. (`tuon login` switches accounts.)")
	}
	if err := history(); err != nil {
		if errors.Is(err, store.ErrRunning) {
			return err
		}
		fmt.Fprintln(os.Stderr, "  Couldn't restore history:", err)
		fmt.Println("  You can still use tuon. Try `tuon history` later.")
		if term.IsTerminal(int(os.Stdin.Fd())) { // don't let tuon's screen cover this
			fmt.Print("  Press enter to continue.")
			readLine()
		}
	}
	return nil
}
