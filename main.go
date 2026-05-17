package main

import (
	"flag"
	"fmt"
	"os"
)

const (
	localAddr = "127.0.0.1:9999"
	sshHost   = "alt.org:22"
)

func main() {
	mode := flag.String("mode", "host", "Operating mode: host or client")
	debug := flag.Bool("debug", false, "Write verbose logs to nh-helper.raw.log and nh-helper.translate.log next to the binary")
	flag.Parse()

	// Generate the Chinese reference manual on first launch. Idempotent —
	// the function checks for an existing file and skips when present, so
	// host + client invocations both call it without racing meaningfully.
	if path, wrote, err := ensureManual(); err != nil {
		fmt.Fprintf(os.Stderr, "nh-helper: manual generation skipped (%v)\n", err)
	} else if wrote {
		fmt.Printf("nh-helper: generated reference manual → %s\n", path)
	}

	var err error
	switch *mode {
	case "host":
		err = runHost(*debug)
	case "client":
		err = runClient(*debug)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode: %s (expected host or client)\n", *mode)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "nh-helper [%s] error: %v\n", *mode, err)
		os.Exit(1)
	}
}
