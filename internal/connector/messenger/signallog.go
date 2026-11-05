package messenger

import (
	"sync"

	"github.com/rs/zerolog"
	signallog "go.mau.fi/libsignal/logger"

	"github.com/ralph/tuon/internal/connector"
)

// signalLog passes libsignal's own warnings and errors to tuon.log. Without
// it, a failed decrypt only says "no valid sessions" and the real reason
// (bad MAC, wrong version, missing key…) is thrown away. Debug and info are
// dropped: they can include ciphertext.
type signalLog struct{ log zerolog.Logger }

func (l signalLog) Debug(string, string) {}
func (l signalLog) Info(string, string)  {}
func (l signalLog) Warning(caller, msg string) {
	l.log.Warn().Str("c", "libsignal").Str("at", caller).Msg(connector.SafeText(msg, false))
}
func (l signalLog) Error(caller, msg string) {
	l.log.Warn().Str("c", "libsignal").Str("at", caller).Msg(connector.SafeText(msg, false))
}
func (l signalLog) Configure(string) {}

var signalLogOnce sync.Once

func setupSignalLog(log zerolog.Logger) {
	signalLogOnce.Do(func() {
		var l signallog.Loggable = signalLog{log: log}
		signallog.Setup(&l)
	})
}
