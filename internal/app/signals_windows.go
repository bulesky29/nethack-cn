//go:build windows

package app

import (
	"os"
	"syscall"
)

// closeSignals on Windows is just the basic interrupt + terminate set.
// SIGHUP doesn't exist (no terminal hangup model) so we don't listen.
var closeSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM}

// resizeSignals is empty on Windows — SIGWINCH doesn't exist. Terminal
// resize is handled by the conhost / Windows Terminal automatically and
// we don't get a notification. The SSH session keeps its initial size;
// users can re-launch if they need a new size.
var resizeSignals = []os.Signal{}

func isResizeSignal(_ os.Signal) bool { return false }
