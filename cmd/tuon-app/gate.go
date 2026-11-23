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
			target := strings.TrimSpace(sc.Text())
			if target == "" {
				continue
			}
			log.Warn().Str("target", target).Msg("network gate refused a connection")
			p.Send(ui.WarnMsg("Blocked a connection to " + target + ": not a Meta server (see ~/Library/Caches/tuon/net.log)"))
		}
	}()
}
