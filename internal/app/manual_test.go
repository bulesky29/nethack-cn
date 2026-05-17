package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGenerateManualSmoke renders the manual to a temp directory and
// asserts the file appears with non-trivial size. This catches font
// loading regressions and broken PDF writes without needing the full
// nh-helper runtime (SSH, OpenRouter, etc.).
func TestGenerateManualSmoke(t *testing.T) {
	font, err := findCJKFont()
	if err != nil {
		t.Skipf("no CJK font available on this machine: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "out.pdf")
	if err := generateManual(path, font); err != nil {
		t.Fatalf("generateManual: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() < 20_000 {
		t.Fatalf("generated PDF is suspiciously small: %d bytes", info.Size())
	}
	t.Logf("generated PDF: %s (%d bytes)", path, info.Size())
}

// TestGenerateManualInPlace is gated behind an env var so the test suite
// stays read-only by default. Set NH_GENERATE_MANUAL=1 to write the PDF
// directly into the binary's directory — useful for eyeballing layout
// changes without launching the full app.
func TestGenerateManualInPlace(t *testing.T) {
	if os.Getenv("NH_GENERATE_MANUAL") == "" {
		t.Skip("set NH_GENERATE_MANUAL=1 to write the manual into the project dir")
	}
	font, err := findCJKFont()
	if err != nil {
		t.Fatalf("no CJK font: %v", err)
	}
	wd, _ := os.Getwd()
	path := filepath.Join(wd, manualFileName)
	if err := generateManual(path, font); err != nil {
		t.Fatalf("generateManual: %v", err)
	}
	info, _ := os.Stat(path)
	t.Logf("wrote %s (%d bytes)", path, info.Size())
}
