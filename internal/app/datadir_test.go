package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveDataDir locks in the priority order: own dir if marked,
// then parent dir if marked, else own dir. The probe makes ./bin/nh-helper
// pick up ../config.json without any flag.
func TestResolveDataDir(t *testing.T) {
	t.Run("neither dir marked → returns own", func(t *testing.T) {
		parent := t.TempDir()
		bin := filepath.Join(parent, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := resolveDataDir(bin); got != bin {
			t.Errorf("want %q, got %q", bin, got)
		}
	})

	t.Run("only parent marked → returns parent", func(t *testing.T) {
		parent := t.TempDir()
		bin := filepath.Join(parent, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		// Drop a config.json in parent.
		if err := os.WriteFile(filepath.Join(parent, "config.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := resolveDataDir(bin); got != parent {
			t.Errorf("want parent %q, got %q", parent, got)
		}
	})

	t.Run("only own dir marked → returns own", func(t *testing.T) {
		parent := t.TempDir()
		bin := filepath.Join(parent, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "config.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := resolveDataDir(bin); got != bin {
			t.Errorf("want own %q, got %q", bin, got)
		}
	})

	t.Run("both marked → own wins (more specific)", func(t *testing.T) {
		parent := t.TempDir()
		bin := filepath.Join(parent, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(parent, "config.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "config.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := resolveDataDir(bin); got != bin {
			t.Errorf("want own %q (more specific), got %q", bin, got)
		}
	})

	t.Run("any of {config.json, db/, log/} counts as marker", func(t *testing.T) {
		for _, marker := range dataMarkers {
			t.Run(marker, func(t *testing.T) {
				parent := t.TempDir()
				bin := filepath.Join(parent, "bin")
				if err := os.MkdirAll(bin, 0o755); err != nil {
					t.Fatal(err)
				}
				p := filepath.Join(parent, marker)
				// config.json is a file; db / log are directories.
				if marker == "config.json" {
					if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.MkdirAll(p, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				if got := resolveDataDir(bin); got != parent {
					t.Errorf("marker=%s: want parent %q, got %q", marker, parent, got)
				}
			})
		}
	})
}
