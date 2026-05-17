//go:build windows

package app

import (
	"fmt"
	"os/exec"
)

// spawnClientTerminal opens a new console window running this binary in
// client mode. Prefers Windows Terminal (`wt.exe`) when installed —
// modern, supports CJK and ANSI properly — and falls back to a plain
// detached cmd via `start`.
func spawnClientTerminal(debug bool, role string) error {
	exe, args, err := clientExeAndArgs(debug, role)
	if err != nil {
		return err
	}

	// Prefer Windows Terminal: opens a new tab/window with sane defaults.
	if wt, err := exec.LookPath("wt.exe"); err == nil {
		full := append([]string{wt, exe}, args...)
		cmd := exec.Command(full[0], full[1:]...)
		if err := cmd.Start(); err == nil {
			return nil
		}
	}

	// Fallback: cmd.exe /C start "" <exe> <args...>
	// The empty "" is the window title — cmd's `start` requires it when
	// the first quoted arg would otherwise be parsed as the title.
	startArgs := append([]string{"/C", "start", "", exe}, args...)
	cmd := exec.Command("cmd.exe", startArgs...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn console window: %w", err)
	}
	return nil
}
