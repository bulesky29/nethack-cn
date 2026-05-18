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

// TestSnapshotIdempotentWithinSecond simulates the menu + text clients
// exiting at the same second: both call Snapshot, both want to write to
// the identical "nh-helper-<timestamp>.db" file. The second one must
// noticethe sibling's file and short-circuit instead of erroring out.
func TestSnapshotIdempotentWithinSecond(t *testing.T) {
	// Drive the store at a tempdir by stuffing the binary's resolved dir
	// into NH_HELPER_DATA_DIR — actually we just chdir into a temp dir
	// since dataDir falls back to binaryDir which uses os.Executable.
	// Cleanest: drive Snapshot directly with a known dbDir.
	//
	// openStore expects dataDir()/db/. We mock by symlinking the test
	// binary path... too fragile. Instead, run openStore in a tempdir
	// by changing the current working dir + relying on the absence of
	// markers (parent probe falls through to binary dir which is irrelevant
	// here because we're testing Snapshot's race path directly).
	tmp := t.TempDir()
	// Build a store directly with a real SQLite file in tmp/db/.
	dbDir := filepath.Join(tmp, dbDirName)
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := openStoreAt(filepath.Join(dbDir, liveDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Two snapshots within the same second land on the same filename;
	// the second one must NOT error out even though VACUUM INTO would
	// normally refuse a non-empty destination.
	p1, err := st.snapshotInto(dbDir, 20)
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	p2, err := st.snapshotInto(dbDir, 20)
	if err != nil {
		t.Fatalf("second (concurrent) snapshot: %v", err)
	}
	if p1 != p2 {
		t.Errorf("expected same path back when sibling already snapshotted, got %q and %q", p1, p2)
	}
	if _, err := os.Stat(p1); err != nil {
		t.Errorf("snapshot file missing: %v", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
