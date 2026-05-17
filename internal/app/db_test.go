package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestResolveLiveDBSource exercises the priority order:
//
//  1. legacy root-level nh-helper.db
//  2. newest timestamped snapshot in db/
//  3. nothing
//
// The resolver doesn't see db/nh-helper.db itself — that's caller-checked
// before invocation.
func TestResolveLiveDBSource(t *testing.T) {
	t.Run("nothing → empty", func(t *testing.T) {
		bin, dbDir := makeDirs(t)
		path, _ := resolveLiveDBSource(bin, dbDir)
		if path != "" {
			t.Errorf("want empty, got %q", path)
		}
	})

	t.Run("only snapshot → that snapshot", func(t *testing.T) {
		bin, dbDir := makeDirs(t)
		snap := filepath.Join(dbDir, "nh-helper-20260101-120000.db")
		writeFile(t, snap, "snap")
		path, reason := resolveLiveDBSource(bin, dbDir)
		if path != snap {
			t.Errorf("want %q, got %q (reason=%s)", snap, path, reason)
		}
	})

	t.Run("legacy root beats snapshot", func(t *testing.T) {
		bin, dbDir := makeDirs(t)
		legacy := filepath.Join(bin, liveDBName)
		writeFile(t, legacy, "legacy")
		writeFile(t, filepath.Join(dbDir, "nh-helper-20260101-120000.db"), "snap")
		path, reason := resolveLiveDBSource(bin, dbDir)
		if path != legacy {
			t.Errorf("want legacy %q, got %q (reason=%s)", legacy, path, reason)
		}
	})

	t.Run("newest snapshot wins by mtime", func(t *testing.T) {
		bin, dbDir := makeDirs(t)
		older := filepath.Join(dbDir, "nh-helper-20260101-100000.db")
		newer := filepath.Join(dbDir, "nh-helper-20260101-120000.db")
		writeFile(t, older, "old")
		writeFile(t, newer, "new")
		// Touch mtimes so the test isn't at the mercy of write order.
		past := time.Now().Add(-2 * time.Hour)
		if err := os.Chtimes(older, past, past); err != nil {
			t.Fatal(err)
		}
		path, _ := resolveLiveDBSource(bin, dbDir)
		if path != newer {
			t.Errorf("want newer %q, got %q", newer, path)
		}
	})

	t.Run("live DB siblings are ignored", func(t *testing.T) {
		// resolveLiveDBSource is only called when the live DB is absent;
		// it must not pick up the live DB filename even if somehow
		// present in the dir. (Defensive — caller guards against this.)
		bin, dbDir := makeDirs(t)
		writeFile(t, filepath.Join(dbDir, liveDBName), "live")
		path, _ := resolveLiveDBSource(bin, dbDir)
		if path != "" {
			t.Errorf("live DB should be ignored by source resolver, got %q", path)
		}
	})
}

func makeDirs(t *testing.T) (binaryDir, dbDir string) {
	t.Helper()
	binaryDir = t.TempDir()
	dbDir = filepath.Join(binaryDir, "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
