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
func spawnClientTerminal(debug bool, role string) error {
	exe, args, err := clientExeAndArgs(debug, role)
	if err != nil {
		return err
	}

	// Quote the path for AppleScript and the shell layer it invokes.
	asEscapedPath := strings.ReplaceAll(exe, `\`, `\\`)
	asEscapedPath = strings.ReplaceAll(asEscapedPath, `"`, `\"`)
	shellCommand := fmt.Sprintf(`\"%s\" %s`, asEscapedPath, strings.Join(args, " "))
	script := fmt.Sprintf(`tell application "Terminal" to do script "%s"`, shellCommand)

	cmd := exec.Command("osascript", "-e", script)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}
