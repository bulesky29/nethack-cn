// Command nh-helper is the entry point for the NetHack live-translation
// helper. All real logic lives in the internal/app package; this file is
// a thin CLI wrapper that parses flags and dispatches.
package main

import (
	"flag"
	"fmt"
	"os"

	"nh-helper/internal/app"
)

func main() {
	mode := flag.String("mode", "host", "Operating mode: host or client")
	role := flag.String("role", "text", "Client role: text (action/narrative translations) or menu (status bar + menu/inventory popups)")
	debug := flag.Bool("debug", false, "Write verbose logs to nh-helper.raw.log and nh-helper.translate.log next to the binary")
	flag.Parse()

	// Generate the Chinese reference manual on first launch. Idempotent —
	// presence check skips when the file is already there, so host + both
	// client invocations all call it without racing meaningfully.
	if path, wrote, err := app.EnsureManual(); err != nil {
		fmt.Fprintf(os.Stderr, "nh-helper: manual generation skipped (%v)\n", err)
	} else if wrote {
		fmt.Printf("nh-helper: generated reference manual → %s\n", path)
	}

	var err error
	switch *mode {
	case "host":
		err = app.RunHost(*debug)
	case "client":
		if *role != app.RoleText && *role != app.RoleMenu {
			fmt.Fprintf(os.Stderr, "unknown role: %s (expected %s or %s)\n", *role, app.RoleText, app.RoleMenu)
			os.Exit(2)
		}
		err = app.RunClient(*debug, *role)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode: %s (expected host or client)\n", *mode)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "nh-helper [%s] error: %v\n", *mode, err)
		os.Exit(1)
	}
}
