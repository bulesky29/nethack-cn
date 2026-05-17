//go:build !windows

package app

import (
	"os"
	"syscall"
)

// closeSignals are the OS signals that should trigger a graceful host
// shutdown — Ctrl-C, kill, and terminal hangup all qualify.
var closeSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}

// resizeSignals are the signals we listen for to refresh the SSH window
// dimensions. On Unix that's SIGWINCH; the platform-stub variant on
// Windows is empty.
var resizeSignals = []os.Signal{syscall.SIGWINCH}

func isResizeSignal(sig os.Signal) bool {
	return sig == syscall.SIGWINCH
}
