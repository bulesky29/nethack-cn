package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/term"
)

// ui owns the client terminal layout. It reserves the top few rows for a
// fixed-position translated status pane and confines streaming
// translations to a scroll region below it.
//
// When the client's stdout is not a TTY (piped, redirected) the whole
// thing becomes a no-op and translations stream normally.
type ui struct {
	mu             sync.Mutex
	enabled        bool
	statusRows     int // height of the fixed pane including the separator row
	cols, rows     int
	lastStatusLine string // for redraw idempotence
}

const (
	statusPaneRows = 3 // 2 raw status rows + 1 separator
)

// ANSI control sequences. DECSTBM (`CSI top;bottom r`) sets the scroll
// region; DECSC / DECRC save and restore the cursor across status redraws.
const (
	ansiClearScreen   = "\x1b[2J"
	ansiResetScroll   = "\x1b[r"
	ansiSaveCursor    = "\x1b7"
	ansiRestoreCursor = "\x1b8"
	ansiClearLine     = "\x1b[2K"
)

func newUI() *ui {
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		return &ui{enabled: false}
	}
	cols, rows, err := term.GetSize(fd)
	if err != nil || rows < statusPaneRows+4 {
		return &ui{enabled: false}
	}
	return &ui{
		enabled:    true,
		statusRows: statusPaneRows,
		cols:       cols,
		rows:       rows,
	}
}

// Init reserves the top statusRows lines for the status pane and parks
// the cursor just below them so subsequent normal output flows in the
// scroll region.
func (u *ui) Init() {
	if u == nil || !u.enabled {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	// Clear, set scroll region [statusRows+1 .. bottom], home cursor below pane.
	fmt.Print(ansiClearScreen)
	fmt.Printf("\x1b[%d;%dr", u.statusRows+1, u.rows)
	fmt.Printf("\x1b[%d;1H", u.statusRows+1)
	// Pre-draw an empty pane so the separator line is visible immediately.
	u.drawPaneLocked("")
}

// Restore returns the terminal scroll region to its default before exit.
// Callers should invoke this from a defer in runClient.
func (u *ui) Restore() {
	if u == nil || !u.enabled {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	fmt.Print(ansiResetScroll)
}

// PrintTranslation writes a source/translation pair to the scroll region,
// taking the same mutex as DrawStatus so the cursor doesn't get clobbered
// by an interleaving status redraw.
func (u *ui) PrintTranslation(text, out string, stamp string) {
	if u == nil {
		fmt.Printf("\n[%s] %s\n%s\n", stamp, text, out)
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	fmt.Printf("\n[%s] %s\n%s\n", stamp, dimIfTerm(text), out)
}

// DrawStatus translates the raw status text from NetHack and renders it
// into the fixed-position pane. Safe to call concurrently.
func (u *ui) DrawStatus(raw string, store *store) {
	if u == nil || !u.enabled {
		return
	}
	translated := translateStatus(raw, store)
	u.mu.Lock()
	defer u.mu.Unlock()
	if translated == u.lastStatusLine {
		return
	}
	u.lastStatusLine = translated
	u.drawPaneLocked(translated)
}

// drawPaneLocked rewrites rows 1..statusRows. Must be called with u.mu held.
func (u *ui) drawPaneLocked(translated string) {
	fmt.Print(ansiSaveCursor)
	lines := strings.Split(translated, "\n")
	// First N-1 rows hold the status content (clipped if too tall),
	// the last row of the pane is a horizontal separator.
	contentRows := u.statusRows - 1
	for i := 0; i < contentRows; i++ {
		fmt.Printf("\x1b[%d;1H%s", i+1, ansiClearLine)
		if i < len(lines) {
			fmt.Print(lines[i])
		}
	}
	// Separator row.
	sepWidth := u.cols
	if sepWidth > 120 {
		sepWidth = 120
	}
	fmt.Printf("\x1b[%d;1H%s%s", u.statusRows, ansiClearLine, strings.Repeat("─", sepWidth))
	fmt.Print(ansiRestoreCursor)
}

// dimIfTerm wraps in ANSI dim when stdout is a TTY (always true if we
// reached here, but the helper keeps the call symmetric with the non-UI
// path).
func dimIfTerm(s string) string {
	return "\x1b[2m" + s + "\x1b[0m"
}

// Status translation -------------------------------------------------------

// statLabels maps the short English stat names that appear in NetHack's
// status bar to their Chinese counterparts. Labels are always followed by
// ":" in the source text, which makes the substitution unambiguous.
var statLabels = map[string]string{
	"St":   "力量",
	"Dx":   "敏捷",
	"Co":   "体格",
	"In":   "智力",
	"Wi":   "感知",
	"Ch":   "魅力",
	"Dlvl": "楼层",
	"HP":   "生命",
	"Pw":   "法力",
	"AC":   "护甲",
	"Xp":   "经验",
	"HD":   "等级",
	"T":    "回合",
}

var alignmentLabels = map[string]string{
	"Lawful":  "守序",
	"Neutral": "中立",
	"Chaotic": "混乱",
}

// statusConditions translates the per-turn affliction words that NetHack
// appends to the status line (e.g. "Hungry", "Confused", "Burdened").
var statusConditions = map[string]string{
	"Hungry":   "饥饿",
	"Weak":     "虚弱",
	"Fainting": "晕厥",
	"Starving": "饿死边缘",
	"Satiated": "过饱",
	"Confused": "困惑",
	"Stunned":  "眩晕",
	"Hallu":    "幻觉",
	"Blind":    "失明",
	"Burdened": "负担",
	"Stressed": "压重",
	"Strained": "过载",
	"FoodPois": "食物中毒",
	"Ill":      "患病",
	"Slimed":   "黏液化",
	"Held":     "被抓",
	"Greasy":   "油腻",
}

// statLabelOrdered preserves longest-prefix-first ordering so "Dlvl:" is
// substituted before "Dx:" / "D:" — Go map iteration is randomized, and
// the longest label must win.
var statLabelOrdered = sortedKeysByLength(statLabels)

// labelPattern is precompiled per label so word-boundary substitution stays
// safe (we don't want "In:" to nibble into "Inside:" should a future status
// row format change).
var labelPatterns = func() map[string]*regexp.Regexp {
	m := make(map[string]*regexp.Regexp, len(statLabels))
	for en := range statLabels {
		m[en] = regexp.MustCompile(`\b` + regexp.QuoteMeta(en) + `:`)
	}
	return m
}()

// translateStatus rewrites the raw two-row status with Chinese labels and
// glossary-resolved class titles, leaving values and proper names intact.
func translateStatus(raw string, store *store) string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		out = append(out, translateStatusLine(line, store))
	}
	return strings.Join(out, "\n")
}

func translateStatusLine(line string, store *store) string {
	// Translate "[<name> the <title>     ]" — class titles benefit from
	// glossary lookup so the user's curated rendering takes precedence.
	line = playerHeaderPattern.ReplaceAllStringFunc(line, func(match string) string {
		groups := playerHeaderPattern.FindStringSubmatch(match)
		if len(groups) != 3 {
			return match
		}
		name, title := groups[1], groups[2]
		titleZh := title
		if store != nil {
			if hits, _ := store.LookupGlossary(title); len(hits) > 0 {
				for _, h := range hits {
					if strings.EqualFold(h.En, title) {
						titleZh = h.Zh
						break
					}
				}
			}
		}
		if titleZh == title {
			return fmt.Sprintf("[%s · %s]", name, title)
		}
		return fmt.Sprintf("[%s · %s (%s)]", name, titleZh, title)
	})

	// Replace label tokens in longest-first order so "Dlvl:" beats "D:".
	for _, en := range statLabelOrdered {
		zh := statLabels[en]
		line = labelPatterns[en].ReplaceAllString(line, zh+":")
	}
	for en, zh := range alignmentLabels {
		line = strings.Replace(line, en, zh, 1)
	}
	for en, zh := range statusConditions {
		line = strings.Replace(line, en, zh, 1)
	}
	return line
}

// playerHeaderPattern matches "[<name> the <Title>     ]" with optional
// trailing whitespace inside the brackets.
var playerHeaderPattern = regexp.MustCompile(`\[([^\]]+?)\s+the\s+([A-Za-z]+)\s*\]`)

func sortedKeysByLength(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple insertion sort by descending length — small N.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && len(keys[j-1]) < len(keys[j]); j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
