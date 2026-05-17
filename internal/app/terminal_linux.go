//go:build linux

package app

import (
	"fmt"
	"os/exec"
	"strings"
)

// spawnClientTerminal probes common Linux terminal emulators and launches
// the first one available with this binary in client mode. The argument
// syntax for "-e <command>" varies between emulators, so each entry has
// its own builder.
func spawnClientTerminal(debug bool, role string) error {
	exe, args, err := clientExeAndArgs(debug, role)
	if err != nil {
		return err
	}
	cmdline := shellEscape(exe) + " " + strings.Join(args, " ")

	for _, t := range linuxTerminals {
		path, err := exec.LookPath(t.bin)
		if err != nil {
			continue
		}
		cmd := exec.Command(path, t.argv(cmdline)...)
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no usable terminal emulator found; tried %s. Open a new terminal manually and run:\n  %s",
		linuxTerminalNames(), cmdline)
}

type linuxTerminal struct {
	bin  string
	argv func(cmdline string) []string
}

// linuxTerminals: probe in order of "most likely installed on a desktop
// distro" first.
var linuxTerminals = []linuxTerminal{
	// Debian/Ubuntu standard symlink.
	{"x-terminal-emulator", func(c string) []string { return []string{"-e", "sh", "-c", c} }},
	// GNOME default.
	{"gnome-terminal", func(c string) []string { return []string{"--", "sh", "-c", c} }},
	// KDE default.
	{"konsole", func(c string) []string { return []string{"-e", "sh", "-c", c} }},
	// Xfce.
	{"xfce4-terminal", func(c string) []string { return []string{"-e", "sh -c " + shellEscape(c)} }},
	// Tiling WM friendly.
	{"alacritty", func(c string) []string { return []string{"-e", "sh", "-c", c} }},
	{"kitty", func(c string) []string { return []string{"sh", "-c", c} }},
	// Last-resort universal X terminal.
	{"xterm", func(c string) []string { return []string{"-e", "sh", "-c", c} }},
}

func linuxTerminalNames() string {
	names := make([]string, 0, len(linuxTerminals))
	for _, t := range linuxTerminals {
		names = append(names, t.bin)
	}
	return strings.Join(names, ", ")
}

// shellEscape wraps a path in single quotes (and escapes any internal
// single quotes) so it survives intermediate sh -c layers intact.
func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
