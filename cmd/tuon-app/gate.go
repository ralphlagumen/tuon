package main

import (
	"bufio"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rs/zerolog"

	"github.com/ralph/tuon/internal/netgate"
	"github.com/ralph/tuon/internal/ui"
)

// watchGate reads the hosts the launcher's gate refused (fd 7) and warns
// in the header. The launcher also logs them where the app can't reach
// and repeats them after tuon quits, so this is only the early notice.
func watchGate(p *tea.Program, log zerolog.Logger) {
	if os.Getenv(netgate.EventsFDEnv) != "7" {
		return
	}
	f := os.NewFile(7, "netgate-events")
	if f == nil {
		return
	}
	go func() {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			kind, text, _ := strings.Cut(strings.TrimSpace(sc.Text()), " ")
			switch {
			case text == "":
			case kind == "refused":
				log.Warn().Str("target", text).Msg("network gate refused a connection")
				p.Send(ui.WarnMsg("Blocked a connection to " + text + ": not a Meta server (see ~/Library/Caches/tuon/net.log)"))
			case kind == "unusual":
				log.Warn().Str("what", text).Msg("network gate: unusual upload")
				p.Send(ui.WarnMsg("Unusual upload: " + text + ". If you didn't send big files, check Messenger on your phone"))
			}
		}
	}()
}
