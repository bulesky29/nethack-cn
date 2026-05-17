//go:build darwin

package app

import (
	"fmt"
	"os/exec"
	"strings"
)

// spawnClientTerminal opens a new Terminal.app window running this binary
// in client mode with the given role. Uses AppleScript via /usr/bin/
// osascript — works on every Mac without extra dependencies.
//
// The AppleScript also waits for the binary to exit (Terminal's tab
// reports `busy = false` once the foreground process is gone) and then
// closes the window. So when the host process dies, every client's TCP
// socket EOFs → client process exits → its window auto-closes. The
// player only has to Ctrl-C the host terminal; the two side windows
// follow.
func spawnClientTerminal(debug bool, role string) error {
	exe, args, err := clientExeAndArgs(debug, role)
	if err != nil {
		return err
	}

	// Quote the path for AppleScript and the shell layer it invokes.
	asEscapedPath := strings.ReplaceAll(exe, `\`, `\\`)
	asEscapedPath = strings.ReplaceAll(asEscapedPath, `"`, `\"`)
	// Chain "; exit" so the surrounding shell quits when the binary
	// returns. Without that the shell prompt comes back, and Terminal
	// reports `busy of tab` = true (interactive shell counts as busy),
	// so our polling loop never fires.
	shellCommand := fmt.Sprintf(`\"%s\" %s; exit`, asEscapedPath, strings.Join(args, " "))

	// Multi-line AppleScript: open a tab, hold the reference, poll
	// `busy of tab` every half-second, then close the containing
	// window. The `try` swallows the case where the user closed the
	// window manually first.
	script := fmt.Sprintf(`tell application "Terminal"
    set newTab to (do script "%s")
    try
        set targetWindow to first window whose tabs contains newTab
        repeat while busy of newTab
            delay 0.5
        end repeat
        close targetWindow saving no
    end try
end tell`, shellCommand)

	cmd := exec.Command("osascript", "-e", script)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}
