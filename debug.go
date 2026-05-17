package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// debugLog wraps two append-mode log files next to the binary:
//   - nh-helper.raw.log: screen scans, wire events, filter decisions
//   - nh-helper.translate.log: full LLM request/response transcripts
//
// A nil *debugLog is a no-op for every method, so callers can keep the
// debug-disabled path branch-free.
type debugLog struct {
	side string
	raw  *log.Logger
	tr   *log.Logger
	rawF *os.File
	trF  *os.File
}

var (
	debugOnce sync.Once
	debugRef  *debugLog
)

// openDebugLog opens (or creates) the two log files in append mode and
// returns a logger tagged with the given side ("host" or "client").
// Returns nil when enabled is false.
func openDebugLog(enabled bool, side string) (*debugLog, error) {
	if !enabled {
		return nil, nil
	}
	var (
		out *debugLog
		err error
	)
	debugOnce.Do(func() {
		dir, derr := binaryDir()
		if derr != nil {
			err = derr
			return
		}
		rawPath := filepath.Join(dir, "nh-helper.raw.log")
		trPath := filepath.Join(dir, "nh-helper.translate.log")

		rawF, ferr := os.OpenFile(rawPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if ferr != nil {
			err = fmt.Errorf("open %s: %w", rawPath, ferr)
			return
		}
		trF, ferr := os.OpenFile(trPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if ferr != nil {
			rawF.Close()
			err = fmt.Errorf("open %s: %w", trPath, ferr)
			return
		}

		prefix := fmt.Sprintf("[%s] ", side)
		flags := log.LstdFlags | log.Lmicroseconds
		out = &debugLog{
			side: side,
			raw:  log.New(rawF, prefix, flags),
			tr:   log.New(trF, prefix, flags),
			rawF: rawF,
			trF:  trF,
		}
		// Session marker so the human reader can find the latest run.
		stamp := time.Now().Format(time.RFC3339)
		out.raw.Printf("==== session start side=%s pid=%d t=%s ====", side, os.Getpid(), stamp)
		out.tr.Printf("==== session start side=%s pid=%d t=%s ====", side, os.Getpid(), stamp)
		debugRef = out
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = debugRef
	}
	return out, nil
}

func binaryDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe), nil
}

// Close flushes and closes both underlying log files.
func (d *debugLog) Close() {
	if d == nil {
		return
	}
	if d.rawF != nil {
		_ = d.rawF.Close()
	}
	if d.trF != nil {
		_ = d.trF.Close()
	}
}

// Raw writes one entry to nh-helper.raw.log. No-op when d is nil.
func (d *debugLog) Raw(format string, args ...any) {
	if d == nil || d.raw == nil {
		return
	}
	d.raw.Printf(format, args...)
}

// RawBlock writes a header line plus an indented multi-line payload to
// nh-helper.raw.log. Useful for dumping captured screen / popup content.
func (d *debugLog) RawBlock(header, body string) {
	if d == nil || d.raw == nil {
		return
	}
	d.raw.Printf("%s ↓", header)
	for _, line := range strings.Split(body, "\n") {
		d.raw.Printf("    │ %s", line)
	}
	d.raw.Printf("    └─ end")
}

// Translate writes one entry to nh-helper.translate.log. No-op when nil.
func (d *debugLog) Translate(format string, args ...any) {
	if d == nil || d.tr == nil {
		return
	}
	d.tr.Printf(format, args...)
}

// TranslateBlock dumps a labeled multi-line block to translate.log.
func (d *debugLog) TranslateBlock(header, body string) {
	if d == nil || d.tr == nil {
		return
	}
	d.tr.Printf("%s ↓", header)
	for _, line := range strings.Split(body, "\n") {
		d.tr.Printf("    │ %s", line)
	}
	d.tr.Printf("    └─ end")
}
