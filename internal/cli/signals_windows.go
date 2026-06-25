//go:build windows

package cli

import (
	"os"
	"os/signal"
	"syscall"
)

func notifyStopSignals(c chan os.Signal) {
	signal.Notify(c, syscall.SIGTERM)
}

func notifyWinchSignals(_ chan os.Signal) {
	// SIGWINCH is not available on Windows; terminal resize is handled differently.
}
