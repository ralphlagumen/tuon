// Command tuon is a calm, chat-only terminal client for Messenger.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"

	"github.com/ralph/tuon/internal/connector/messenger"
	"github.com/ralph/tuon/internal/store"
	"github.com/ralph/tuon/internal/ui"
)

func main() {
	for _, a := range os.Args[1:] {
		switch a {
		case "--here", "-here": // tuon always runs in place now; kept for old habits
		case "login":
			if _, err := login(); err != nil {
				fmt.Fprintln(os.Stderr, "tuon:", err)
				os.Exit(1)
			}
			return
		case "-h", "--help", "help":
			fmt.Print(usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "tuon: unknown option %q\n\n%s", a, usage)
			os.Exit(2)
		}
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tuon:", err)
		os.Exit(1)
	}
}

func run() error {
	cookies, err := loadCookies()
	if err != nil {
		return err
	}

	st, err := store.Open()
	if err != nil {
		return err
	}
	defer st.Close()

	// Logs go to a file so they never draw over the UI.
	logFile, err := os.OpenFile(filepath.Join(store.Dir(), "tuon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	level := zerolog.WarnLevel
	if os.Getenv("TUON_DEBUG") != "" {
		level = zerolog.DebugLevel
	}
	zerolog.SetGlobalLevel(level)
	log := zerolog.New(logFile).With().Timestamp().Logger()
	zlog.Logger = log // messagix also logs through the global logger

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn := messenger.New(cookies, st.DB, log)
	defer conn.Close()

	p := tea.NewProgram(ui.New(ctx, st, conn, "messenger"), tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx))

	// Pass connector events to the UI in small batches so a big sync
	// doesn't redraw hundreds of times.
	go func() {
		tick := time.NewTicker(30 * time.Millisecond)
		defer tick.Stop()
		var batch []any
		for {
			select {
			case e := <-conn.Events():
				batch = append(batch, e)
			case <-tick.C:
				if len(batch) > 0 {
					p.Send(ui.EventsMsg(batch))
					batch = nil
				}
			case <-ctx.Done():
				return
			}
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
	if fm, ok := final.(ui.Model); ok {
		fm.Cleanup()
	}
	if err == tea.ErrProgramKilled {
		err = nil
	}
	return err
}
