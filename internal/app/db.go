package app

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// dbDirName is the on-disk directory that holds both the live database
// (no timestamp) and all timestamped snapshots written by Snapshot().
const dbDirName = "db"

// liveDBName is the canonical filename of the working database inside
// dbDirName. Snapshots are siblings with a timestamp suffix.
const liveDBName = "nh-helper.db"

// store wraps the local SQLite database holding the translation cache and
// the NetHack glossary. Concurrent use is safe — the underlying *sql.DB is
// goroutine-safe and we serialize writes through a single mutex when we
// need atomic upsert sequences.
type store struct {
	db *sql.DB
	mu sync.Mutex
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS translations (
    input    TEXT PRIMARY KEY,
    output   TEXT NOT NULL,
    model    TEXT NOT NULL,
    created  INTEGER NOT NULL,
    hits     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS glossary (
    en        TEXT PRIMARY KEY COLLATE NOCASE,
    zh        TEXT NOT NULL,
    category  TEXT NOT NULL DEFAULT 'misc',
    notes     TEXT,
    source    TEXT NOT NULL DEFAULT 'auto',
    updated   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_glossary_category ON glossary(category);
`

// openStore opens (or creates) the SQLite database under db/ next to the
// binary and applies the schema. The DSN sets sane single-user defaults:
// WAL mode for concurrent readers, foreign keys on, busy timeout to ride
// out short contention.
//
// Source-of-truth resolution, in order:
//  1. db/nh-helper.db (the live database).
//  2. A legacy nh-helper.db at the binary root from before the move —
//     migrated into db/ on sight.
//  3. The newest timestamped snapshot under db/ — copied into the live
//     slot so the player keeps their accumulated cache + glossary.
//  4. Otherwise we create a fresh db/nh-helper.db.
func openStore() (*store, error) {
	dir, err := dataDir()
	if err != nil {
		return nil, err
	}
	dbDir := filepath.Join(dir, dbDirName)
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dbDir, err)
	}
	livePath := filepath.Join(dbDir, liveDBName)

	if _, err := os.Stat(livePath); errors.Is(err, os.ErrNotExist) {
		if src, restored := resolveLiveDBSource(dir, dbDir); src != "" {
			if err := copyFile(src, livePath); err != nil {
				return nil, fmt.Errorf("seed %s from %s: %w", livePath, src, err)
			}
			fmt.Printf("nh-helper: restored database from %s (%s)\n", src, restored)
		}
	}

	return openStoreAt(livePath)
}

// openStoreAt opens the SQLite live DB at a specific path and applies
// the schema. Skips the dataDir / snapshot-recovery dance — tests use
// this directly to isolate the store from the filesystem-walking probe.
func openStoreAt(livePath string) (*store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", livePath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", livePath, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping %s: %w", livePath, err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &store{db: db}, nil
}

// resolveLiveDBSource picks the best file to seed the live database from
// when db/nh-helper.db doesn't yet exist. Returns ("", "") if there's
// nothing to copy and we should start fresh.
//
// Second return is a short reason string for the console message
// ("legacy root db", "latest snapshot", etc.).
func resolveLiveDBSource(rootDir, dbDir string) (string, string) {
	// Legacy root-level DB from before the move into db/.
	if legacy := filepath.Join(rootDir, liveDBName); fileExists(legacy) {
		return legacy, "legacy root location"
	}
	// Newest timestamped snapshot.
	if snap := newestSnapshot(dbDir); snap != "" {
		return snap, "newest snapshot"
	}
	return "", ""
}

// newestSnapshot returns the path of the most recently-modified
// `nh-helper-*.db` file in dir, ignoring the live DB itself. Empty
// string when there is none.
func newestSnapshot(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var (
		bestPath  string
		bestMtime time.Time
	)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == liveDBName || !strings.HasPrefix(name, "nh-helper-") || !strings.HasSuffix(name, ".db") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(bestMtime) {
			bestMtime = info.ModTime()
			bestPath = filepath.Join(dir, name)
		}
	}
	return bestPath
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// copyFile copies a file's contents and mode 0o644.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func (s *store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// GetTranslation returns the cached Chinese output for an input under
// the given modelTag (typically "<model>:<promptVersion>"). When the
// translator's prompt or model changes, the tag changes too and old
// entries naturally cache-miss — so a system-prompt rewrite doesn't
// keep serving stale Chinese.
//
// Increments a hit counter for the row.
func (s *store) GetTranslation(input, modelTag string) (string, bool) {
	if s == nil {
		return "", false
	}
	var out string
	err := s.db.QueryRow(
		`SELECT output FROM translations WHERE input = ? AND model = ?`,
		input, modelTag,
	).Scan(&out)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		return "", false
	}
	// Fire-and-forget hit increment; failure here doesn't affect correctness.
	_, _ = s.db.Exec(
		`UPDATE translations SET hits = hits + 1 WHERE input = ? AND model = ?`,
		input, modelTag,
	)
	return out, true
}

// PutTranslation stores or overwrites a translation under the given
// modelTag. INSERT OR REPLACE on the input primary key — a re-translate
// under a new tag transparently supersedes the old entry.
func (s *store) PutTranslation(input, output, modelTag string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO translations (input, output, model, created, hits)
		 VALUES (?, ?, ?, ?, COALESCE((SELECT hits FROM translations WHERE input = ? AND model = ?), 0))`,
		input, output, modelTag, time.Now().Unix(), input, modelTag,
	)
	return err
}

// glossaryEntry is the in-memory representation of a row in the glossary
// table.
type glossaryEntry struct {
	En       string
	Zh       string
	Category string
}

// LookupGlossary returns every glossary entry whose English headword
// appears as a case-insensitive substring of the given text. For a small
// glossary this is cheap; if it ever gets huge we can add an FTS5 index.
func (s *store) LookupGlossary(text string) ([]glossaryEntry, error) {
	if s == nil {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT en, zh, category FROM glossary
		 WHERE instr(lower(?), lower(en)) > 0
		 ORDER BY length(en) DESC`,
		text,
	)
	if err != nil {
		return nil, fmt.Errorf("glossary lookup: %w", err)
	}
	defer rows.Close()

	var out []glossaryEntry
	for rows.Next() {
		var e glossaryEntry
		if err := rows.Scan(&e.En, &e.Zh, &e.Category); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PutGlossary inserts a new term if (and only if) the English headword is
// not already present. Manually-curated entries are never overwritten by
// the LLM extractor — first definition wins.
func (s *store) PutGlossary(e glossaryEntry, source string) error {
	if s == nil {
		return nil
	}
	en := strings.TrimSpace(e.En)
	zh := strings.TrimSpace(e.Zh)
	cat := strings.TrimSpace(e.Category)
	if en == "" || zh == "" {
		return errors.New("glossary entry needs en and zh")
	}
	if cat == "" {
		cat = "misc"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO glossary (en, zh, category, source, updated)
		 VALUES (?, ?, ?, ?, ?)`,
		en, zh, cat, source, time.Now().Unix(),
	)
	return err
}

// CountGlossary returns the number of rows in the glossary, useful for a
// "loaded N terms" startup line.
func (s *store) CountGlossary() (int, error) {
	if s == nil {
		return 0, nil
	}
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM glossary`).Scan(&n)
	return n, err
}

// Snapshot atomically copies the current database into db/ with a
// timestamped filename, then prunes the directory to the most recent
// `keep` snapshots. Uses SQLite's VACUUM INTO for a consistent point-
// in-time copy (handles WAL correctly, no torn writes). The live DB
// (db/nh-helper.db) sits alongside the snapshots and is never pruned.
//
// Returns the path of the new snapshot.
func (s *store) Snapshot(keep int) (string, error) {
	if s == nil {
		return "", nil
	}
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	snapDir := filepath.Join(dir, dbDirName)
	return s.snapshotInto(snapDir, keep)
}

// snapshotInto is the testable core of Snapshot: takes the snapshot
// directory explicitly so unit tests can drive it without faking
// dataDir().
func (s *store) snapshotInto(snapDir string, keep int) (string, error) {
	if s == nil {
		return "", nil
	}
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", snapDir, err)
	}
	stamp := time.Now().Format("20060102-150405")
	path := filepath.Join(snapDir, fmt.Sprintf("nh-helper-%s.db", stamp))

	// Cooperative shutdown of the menu + text clients lands both
	// Snapshot calls within the same second. The second VACUUM INTO
	// would fail with "table translations already exists" because the
	// destination file is non-empty. Detect that case up front and
	// treat it as a no-op — they share the same source DB, so a single
	// snapshot already captures the canonical state.
	if _, statErr := os.Stat(path); statErr == nil {
		return path, nil
	}

	// VACUUM INTO doesn't support placeholder binding for the path, so
	// we sprintf — safe here because `path` is built from a timestamp
	// and a fixed dir, no user input.
	s.mu.Lock()
	_, execErr := s.db.Exec(fmt.Sprintf("VACUUM INTO %q", path))
	s.mu.Unlock()
	if execErr != nil {
		// Lost a race with the sibling client between stat and exec —
		// the file appeared and VACUUM blew up. Same outcome though:
		// a snapshot for this second exists.
		if _, statErr := os.Stat(path); statErr == nil {
			return path, nil
		}
		return "", fmt.Errorf("vacuum into %s: %w", path, execErr)
	}

	if err := pruneSnapshots(snapDir, keep); err != nil {
		// Snapshot succeeded; prune failure is non-fatal but worth surfacing.
		return path, fmt.Errorf("prune snapshots: %w", err)
	}
	return path, nil
}

// pruneSnapshots keeps the `keep` most recent `nh-helper-*.db` files in
// `dir` and removes the rest. Files are sorted by modification time so
// the same logic works after manual fiddling with filenames.
func pruneSnapshots(dir string, keep int) error {
	if keep <= 0 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type fileInfo struct {
		path  string
		mtime time.Time
	}
	var files []fileInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "nh-helper-") || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{
			path:  filepath.Join(dir, e.Name()),
			mtime: info.ModTime(),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].mtime.After(files[j].mtime) // newest first
	})
	for i := keep; i < len(files); i++ {
		_ = os.Remove(files[i].path)
	}
	return nil
}
