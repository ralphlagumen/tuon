// Command tuon is a calm, chat-only terminal client for Messenger.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"

	"github.com/ralph/tuon/internal/auth"
	"github.com/ralph/tuon/internal/connector/messenger"
	"github.com/ralph/tuon/internal/store"
	"github.com/ralph/tuon/internal/ui"
)

func main() {
	// stay lean: collect garbage sooner, and aim to stay under 96 MB
	debug.SetGCPercent(50)
	debug.SetMemoryLimit(96 << 20)
	privateFiles() // everything tuon writes is readable only by you
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "--here", "-here": // tuon always runs in place now; kept for old habits
		case "setup":
			if err := setup(); err != nil {
				fail(err)
			}
			return
		case "login":
			if err := login(); err != nil {
				fail(err)
			}
			if err := history(); err != nil { // a new login may be a new account
				fmt.Fprintln(os.Stderr, "  Couldn't restore history:", err)
			}
			return
		case "history":
			if err := history(); err != nil {
				fail(err)
			}
			return
		case "update":
			if err := update(); err != nil {
				fail(err)
			}
			return
		case "logout":
			if err := logout(len(args) > 1 && args[1] == "--wipe"); err != nil {
				fail(err)
			}
			return
		case "-h", "--help", "help":
			fmt.Print(usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "tuon: unknown option %q\n\n%s", args[0], usage)
			os.Exit(2)
		}
	}
	for {
		err := run()
		if !errors.Is(err, messenger.ErrLoggedOut) {
			if err != nil {
				fail(err)
			}
			return
		}
		// the session expired: log in again right here, then open tuon
		fmt.Println("\n  Messenger logged you out. Log in again to keep going.")
		if err := login(); err != nil {
			fail(err)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "tuon:", err)
	os.Exit(1)
}

func run() error {
	creds, err := loadLogin()
	if err != nil {
		return err
	}
	release, err := store.Lock()
	if err != nil {
		return err
	}
	defer release()
	cleanTemp()
	defer cleanTemp()

	st, err := store.Open()
	if err != nil {
		return err
	}
	defer st.Close()
	_ = st.Prune(ui.KeepMessages)
	_, _ = st.Compact()
	trimPhotoCache(filepath.Join(store.Dir(), "cache", "img"))

	// Logs go to a file so they never draw over the UI.
	logPath := filepath.Join(store.Dir(), "tuon.log")
	trimLog(logPath)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	level := zerolog.WarnLevel
	if os.Getenv("TUON_DEBUG") != "" {
		level = zerolog.DebugLevel // may log message details: only for troubleshooting
	}
	zerolog.SetGlobalLevel(level)
	log := zerolog.New(logFile).With().Timestamp().Logger()
	zlog.Logger = log // messagix also logs through the global logger

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn := messenger.New(creds, st.DB, log)
	defer conn.Close()

	p := tea.NewProgram(ui.New(ctx, st, conn, "messenger"), tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx))

	// Pass connector events to the UI. It sleeps until something arrives,
	// then gathers whatever else comes within 30ms into one redraw, so a big
	// sync doesn't redraw hundreds of times and an idle tuon uses no CPU.
	go func() {
		for {
			var batch []any
			select {
			case e := <-conn.Events():
				batch = append(batch, e)
			case <-ctx.Done():
				return
			}
			wait := time.NewTimer(30 * time.Millisecond)
		gather:
			for {
				select {
				case e := <-conn.Events():
					batch = append(batch, e)
				case <-wait.C:
					break gather
				case <-ctx.Done():
					wait.Stop()
					return
				}
			}
			p.Send(ui.EventsMsg(batch))
		}
	}()

	// Turn off the terminal's line wrap while tuon runs. If a character is
	// ever drawn wider than we measured, the line is clipped at the edge
	// instead of wrapping and pushing every row below it down.
	if os.Getenv("TUON_WRAP") == "" { // debug switch to compare
		fmt.Print("\x1b[?7l")
		defer fmt.Print("\x1b[?7h")
	}

	final, err := p.Run()
	fm, _ := final.(ui.Model)
	fm.Cleanup()
	// Messenger refreshes the session as it goes: keep the newest one.
	if c := conn.Creds(); c.Valid() && os.Getenv("TUON_COOKIES") == "" {
		_ = auth.Save(store.Dir(), c)
	}
	if err == tea.ErrProgramKilled {
		err = nil
	}
	if err == nil && fm.LoggedOut() {
		return messenger.ErrLoggedOut
	}
	return err
}
