package app

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

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

// openStore opens (or creates) the SQLite file next to the binary and
// applies the schema. The DSN sets sane single-user defaults: WAL mode for
// concurrent readers, foreign keys on, busy timeout to ride out short
// contention.
func openStore() (*store, error) {
	dir, err := binaryDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "nh-helper.db")
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping %s: %w", path, err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &store{db: db}, nil
}

func (s *store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// GetTranslation returns the cached Chinese output for an input, plus a
// bool indicating whether the cache had it. It also increments a hit
// counter so we can later inspect which entries are paying their keep.
func (s *store) GetTranslation(input string) (string, bool) {
	if s == nil {
		return "", false
	}
	var out string
	err := s.db.QueryRow(`SELECT output FROM translations WHERE input = ?`, input).Scan(&out)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		return "", false
	}
	// Fire-and-forget hit increment; failure here doesn't affect correctness.
	_, _ = s.db.Exec(`UPDATE translations SET hits = hits + 1 WHERE input = ?`, input)
	return out, true
}

// PutTranslation stores or overwrites a translation. We use REPLACE so a
// later run with a different model can update the cached rendering — the
// most recent translation wins.
func (s *store) PutTranslation(input, output, model string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO translations (input, output, model, created, hits)
		 VALUES (?, ?, ?, ?, COALESCE((SELECT hits FROM translations WHERE input = ?), 0))`,
		input, output, model, time.Now().Unix(), input,
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

// Snapshot atomically copies the current database into ./db/ with a
// timestamped filename, then prunes the directory to the most recent
// `keep` snapshots. Uses SQLite's VACUUM INTO for a consistent point-in-
// time copy (handles WAL correctly, no torn writes).
//
// Returns the path of the new snapshot.
func (s *store) Snapshot(keep int) (string, error) {
	if s == nil {
		return "", nil
	}
	dir, err := binaryDir()
	if err != nil {
		return "", err
	}
	snapDir := filepath.Join(dir, "db")
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", snapDir, err)
	}
	stamp := time.Now().Format("20060102-150405")
	path := filepath.Join(snapDir, fmt.Sprintf("nh-helper-%s.db", stamp))

	// VACUUM INTO doesn't support placeholder binding for the path, so
	// we sprintf — safe here because `path` is built from a timestamp
	// and a fixed dir, no user input.
	s.mu.Lock()
	_, execErr := s.db.Exec(fmt.Sprintf("VACUUM INTO %q", path))
	s.mu.Unlock()
	if execErr != nil {
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
