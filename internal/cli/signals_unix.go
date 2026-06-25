//go:build !windows

package cli

import (
	"os"
	"os/signal"
	"syscall"
)

func notifyStopSignals(c chan os.Signal) {
	signal.Notify(c, syscall.SIGTERM, syscall.SIGHUP)
}

func notifyWinchSignals(c chan os.Signal) {
	signal.Notify(c, syscall.SIGWINCH)
}
