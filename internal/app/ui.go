package app

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/term"
)

// ui owns the client terminal layout. Two flavours, picked by role:
//
//	menu role: top statusRows fixed for the live status pane, scroll
//	           region below for menu / popup translations.
//	text role: alternate screen + clear-and-redraw on every translation,
//	           showing the most recent textRecentMax cards. No scroll
//	           history — user asked for "刷屏" (refresh-style).
//
// When the client's stdout is not a TTY (piped, redirected) the whole
// thing becomes a no-op and translations stream normally.
type ui struct {
	mu             sync.Mutex
	enabled        bool
	role           string // roleMenu vs roleText — drives layout decisions
	hasStatusPane  bool   // menu role pins a status pane at the top
	statusRows     int    // height of the fixed pane when hasStatusPane
	cols, rows     int
	lastStatusHash string // dedupes redundant redraws

	// Refresh-mode state (text role).
	refreshMode bool         // alt-screen + clear-and-redraw on every print
	recentMax   int          // how many recent items to keep on screen
	recent      []recentItem // ring of the last N translations
}

// recentItem is one entry in the refresh-mode rolling buffer.
type recentItem struct {
	text, out, stamp string
}

// statusPaneRows: 4 rows = top border + stats row A + stats row B + bottom border.
const statusPaneRows = 4

// textRecentMax is the number of past translations kept visible in the
// text role's refresh window. Chosen so even a long popup translation
// plus the next two short messages comfortably fit in a default 24-row
// terminal.
const textRecentMax = 3

// ANSI control + colour palette. Building strings from these constants
// keeps the renderer readable without a giant string-soup escape.
const (
	ansiClearScreen   = "\x1b[2J"
	ansiHomeClearDown = "\x1b[H\x1b[J"  // jump home + clear from cursor to end
	ansiResetScroll   = "\x1b[r"
	ansiEnterAlt      = "\x1b[?1049h"   // enter alternate screen buffer
	ansiExitAlt       = "\x1b[?1049l"   // leave alternate screen buffer
	ansiSaveCursor    = "\x1b7"
	ansiRestoreCursor = "\x1b8"
	ansiClearLine     = "\x1b[2K"
	ansiReset         = "\x1b[0m"
	ansiBold          = "\x1b[1m"
	ansiDim           = "\x1b[2m"
	ansiCyan          = "\x1b[36m"
	ansiBrightCyan    = "\x1b[96m"
	ansiYellow        = "\x1b[33m"
	ansiBrightYellow  = "\x1b[93m"
	ansiGreen         = "\x1b[32m"
	ansiBrightGreen   = "\x1b[92m"
	ansiRed           = "\x1b[31m"
	ansiBrightRed     = "\x1b[91m"
	ansiMagenta       = "\x1b[35m"
	ansiBrightMagenta = "\x1b[95m"
	ansiBgRed         = "\x1b[41m"
)

func newUI(role string) *ui {
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		return &ui{enabled: false, role: role}
	}
	cols, rows, err := term.GetSize(fd)
	if err != nil {
		return &ui{enabled: false, role: role}
	}
	hasStatus := role == roleMenu
	if hasStatus && rows < statusPaneRows+4 {
		hasStatus = false
	}
	return &ui{
		enabled:       true,
		role:          role,
		hasStatusPane: hasStatus,
		statusRows:    statusPaneRows,
		cols:          cols,
		rows:          rows,
		refreshMode:   role == roleText,
		recentMax:     textRecentMax,
	}
}

// Init sets up the terminal for whichever role we're playing:
//
//	menu role: clear, set DECSTBM scroll region below the status pane,
//	           park cursor in the scroll region, draw an empty status
//	           frame so the layout is visible immediately.
//	text role: enter the alternate screen buffer so refresh-style
//	           redraws don't pollute scrollback, then clear and home.
func (u *ui) Init() {
	if u == nil || !u.enabled {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.refreshMode {
		fmt.Print(ansiEnterAlt)
		fmt.Print(ansiHomeClearDown)
		return
	}
	if u.hasStatusPane {
		fmt.Print(ansiClearScreen)
		fmt.Printf("\x1b[%d;%dr", u.statusRows+1, u.rows)
		fmt.Printf("\x1b[%d;1H", u.statusRows+1)
		u.drawFrameLocked(emptyFrame(u.cols))
		return
	}
	fmt.Print(ansiClearScreen)
	fmt.Print("\x1b[1;1H")
}

// Restore undoes Init: leaves the alternate screen / resets the scroll
// region so the user's terminal is back to its pre-launch state.
func (u *ui) Restore() {
	if u == nil || !u.enabled {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.refreshMode {
		fmt.Print(ansiExitAlt)
		return
	}
	if u.hasStatusPane {
		fmt.Print(ansiResetScroll)
	}
}

// PrintTranslation writes a source/translation pair with a subtle divider
// and indented bodies, locked against status redraws.
//
// In refresh mode (text role): keep a rolling buffer of the last N
// translations, clear the alternate screen, and redraw them all so the
// player only sees what's recent. No scroll history.
//
// Otherwise (menu role's PrintTranslation calls, plus the non-TTY
// fallback): write the new translation as a stream-append.
func (u *ui) PrintTranslation(text, out string, stamp string) {
	if u == nil || !u.enabled {
		fmt.Printf("\n[%s] %s\n%s\n", stamp, text, out)
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.refreshMode {
		u.recent = append(u.recent, recentItem{text: text, out: out, stamp: stamp})
		if len(u.recent) > u.recentMax {
			u.recent = u.recent[len(u.recent)-u.recentMax:]
		}
		u.redrawRecentLocked()
		return
	}

	u.writeTranslationLocked(text, out, stamp)
}

// writeTranslationLocked is the stream-append render — one card with a
// dim timestamp rule, indented source in dim, blank line, indented
// translation in normal weight.
func (u *ui) writeTranslationLocked(text, out, stamp string) {
	hr := strings.Repeat("─", maxInt(0, u.cols-len(stamp)-6))
	fmt.Printf("\n%s── %s %s%s\n", ansiDim, stamp, hr, ansiReset)
	writeIndented(text, ansiDim)
	fmt.Println()
	writeIndented(out, "")
}

// redrawRecentLocked clears the (alt) screen and reprints the rolling
// buffer top-to-bottom (oldest first → newest at the bottom, closest
// to the prompt). Must be called with u.mu held.
func (u *ui) redrawRecentLocked() {
	fmt.Print(ansiHomeClearDown)
	for _, item := range u.recent {
		u.writeTranslationLocked(item.text, item.out, item.stamp)
	}
}

// PrintMenu renders a translated menu / inventory popup. It de-emphasises
// the noisy source (often laced with dungeon-map ASCII bleed-through) by
// only showing a compact preview, and frames the translation in a box.
func (u *ui) PrintMenu(text, out string, stamp string) {
	if u == nil || !u.enabled {
		fmt.Printf("\n[%s 菜单] %s\n", stamp, out)
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()

	headerLeft := fmt.Sprintf("═ 菜单 / 选项  %s ", stamp)
	headerWidth := u.cols - visibleWidth(headerLeft) - 1
	if headerWidth < 0 {
		headerWidth = 0
	}
	fmt.Printf("\n%s%s%s%s\n", ansiBold, ansiCyan, headerLeft, strings.Repeat("═", headerWidth)+ansiReset)
	// One-line source preview, dim, truncated — the LLM has the full one.
	preview := strings.ReplaceAll(text, "\n", "  ·  ")
	if w := visibleWidth(preview); w > u.cols-4 {
		preview = truncateRunes(preview, u.cols-7) + "..."
	}
	fmt.Printf("%s  %s%s\n", ansiDim, preview, ansiReset)
	for _, line := range strings.Split(out, "\n") {
		fmt.Printf("  %s\n", line)
	}
}

// truncateRunes returns a string clipped to at most n columns of display
// width, honouring CJK double-wide cells.
func truncateRunes(s string, n int) string {
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := 1
		if r >= 0x1100 && (r >= 0x2e80 && r <= 0xa4cf ||
			r >= 0xac00 && r <= 0xd7a3 ||
			r >= 0xf900 && r <= 0xfaff ||
			r >= 0xff00 && r <= 0xff60) {
			rw = 2
		}
		if w+rw > n {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String()
}

// writeIndented prints `body` to stdout, every line prefixed with two
// spaces, optionally wrapped in an ANSI colour code. Trailing newline
// is omitted; caller controls separation.
func writeIndented(body, colour string) {
	for _, line := range strings.Split(body, "\n") {
		if colour != "" {
			fmt.Printf("  %s%s%s\n", colour, line, ansiReset)
		} else {
			fmt.Printf("  %s\n", line)
		}
	}
}

// DrawStatus parses the raw two-row status into structured fields and
// repaints the fixed pane with a coloured box, HP/Pw bars, and condition
// badges. Safe to call concurrently. No-op when the UI has no status
// pane (text role).
func (u *ui) DrawStatus(raw string, store *store) {
	if u == nil || !u.enabled || !u.hasStatusPane {
		return
	}
	frame := renderStatusFrame(parseStatus(raw, store), u.cols)
	u.mu.Lock()
	defer u.mu.Unlock()
	if frame.hash == u.lastStatusHash {
		return
	}
	u.lastStatusHash = frame.hash
	u.drawFrameLocked(frame)
}

// drawFrameLocked rewrites rows 1..statusRows from a prepared frame.
// Must be called with u.mu held.
func (u *ui) drawFrameLocked(f statusFrame) {
	fmt.Print(ansiSaveCursor)
	for i := 0; i < u.statusRows; i++ {
		line := ""
		if i < len(f.lines) {
			line = f.lines[i]
		}
		fmt.Printf("\x1b[%d;1H%s%s", i+1, ansiClearLine, line)
	}
	fmt.Print(ansiRestoreCursor)
}

// Status parsing ----------------------------------------------------------

// statusFields holds the parsed pieces of NetHack's two-row status bar.
type statusFields struct {
	name, title, align       string
	titleZh                  string // glossary-resolved Chinese title, if any
	st, dx, co, in, wi, ch   string
	dlvl, gold, ac, xp, turn string
	hp, hpMax                int
	pw, pwMax                int
	conditions               []string
	raw                      string
}

var (
	playerHeaderPattern = regexp.MustCompile(`\[([^\]]+?)\s+the\s+([A-Za-z]+)\s*\]`)
	alignPattern        = regexp.MustCompile(`\b(Lawful|Neutral|Chaotic)\b`)
	hpPattern           = regexp.MustCompile(`HP:(\d+)\((\d+)\)`)
	pwPattern           = regexp.MustCompile(`Pw:(\d+)\((\d+)\)`)
	goldPattern         = regexp.MustCompile(`\$:(\d+)`)
	scalarPattern       = regexp.MustCompile(`\b(Dlvl|St|Dx|Co|In|Wi|Ch|AC|Xp|HD|T):(\S+)`)
)

func parseStatus(raw string, store *store) statusFields {
	f := statusFields{raw: raw}

	if m := playerHeaderPattern.FindStringSubmatch(raw); len(m) == 3 {
		f.name = strings.TrimSpace(m[1])
		f.title = m[2]
		if store != nil {
			if hits, _ := store.LookupGlossary(f.title); len(hits) > 0 {
				for _, h := range hits {
					if strings.EqualFold(h.En, f.title) {
						f.titleZh = h.Zh
						break
					}
				}
			}
		}
	}
	if m := alignPattern.FindStringSubmatch(raw); len(m) == 2 {
		f.align = m[1]
	}
	if m := hpPattern.FindStringSubmatch(raw); len(m) == 3 {
		f.hp, _ = strconv.Atoi(m[1])
		f.hpMax, _ = strconv.Atoi(m[2])
	}
	if m := pwPattern.FindStringSubmatch(raw); len(m) == 3 {
		f.pw, _ = strconv.Atoi(m[1])
		f.pwMax, _ = strconv.Atoi(m[2])
	}
	if m := goldPattern.FindStringSubmatch(raw); len(m) == 2 {
		f.gold = m[1]
	}
	for _, m := range scalarPattern.FindAllStringSubmatch(raw, -1) {
		switch m[1] {
		case "St":
			f.st = m[2]
		case "Dx":
			f.dx = m[2]
		case "Co":
			f.co = m[2]
		case "In":
			f.in = m[2]
		case "Wi":
			f.wi = m[2]
		case "Ch":
			f.ch = m[2]
		case "Dlvl":
			f.dlvl = m[2]
		case "AC":
			f.ac = m[2]
		case "Xp", "HD":
			f.xp = m[2]
		case "T":
			f.turn = m[2]
		}
	}
	// Iterate the sorted list (not the map) so condition order is stable
	// across runs — keeps the dedupe hash from churning on map ordering.
	for _, en := range conditionsOrdered {
		if conditionPatterns[en].MatchString(raw) {
			f.conditions = append(f.conditions, en)
		}
	}
	return f
}

// conditionsOrdered / conditionPatterns are derived once at startup from
// statusConditions so parseStatus doesn't recompile regexes on every
// status event.
var (
	conditionsOrdered = func() []string {
		out := make([]string, 0, len(statusConditions))
		for k := range statusConditions {
			out = append(out, k)
		}
		// Lexicographic sort: deterministic, no semantic meaning needed.
		for i := 1; i < len(out); i++ {
			for j := i; j > 0 && out[j-1] > out[j]; j-- {
				out[j-1], out[j] = out[j], out[j-1]
			}
		}
		return out
	}()
	conditionPatterns = func() map[string]*regexp.Regexp {
		m := make(map[string]*regexp.Regexp, len(statusConditions))
		for k := range statusConditions {
			m[k] = regexp.MustCompile(`\b` + regexp.QuoteMeta(k) + `\b`)
		}
		return m
	}()
)

// Status rendering --------------------------------------------------------

type statusFrame struct {
	lines []string // raw rendered rows including ANSI escapes
	hash  string   // colour-free version used for dedupe
}

func emptyFrame(cols int) statusFrame {
	width := paneWidth(cols)
	return statusFrame{
		lines: []string{
			topBorder(width, ""),
			sideBorder(width, ""),
			sideBorder(width, ""),
			bottomBorder(width),
		},
		hash: "empty",
	}
}

// renderStatusFrame produces a 4-row coloured pane:
//
//	┌─ Bulesky29 · 穴居人 (Troglodyte) · 守序 · T 129 ─────────────┐
//	│ HP █████████░ 23/23   Pw █░░░░░░░░ 5/5   AC 8  Xp 2  楼层 1 │
//	│ St 18/03  Dx 13  Co 17  In 7  Wi 10  Ch 7   $ 0   [饥饿]    │
//	└──────────────────────────────────────────────────────────────┘
func renderStatusFrame(f statusFields, cols int) statusFrame {
	width := paneWidth(cols)
	header := buildHeader(f)
	row1 := buildVitalsRow(f)
	row2 := buildStatsRow(f)
	lines := []string{
		topBorder(width, header),
		sideBorder(width, row1),
		sideBorder(width, row2),
		bottomBorder(width),
	}
	// Hash is a colour-stripped version of the visible content so we
	// don't redraw when only the (already-painted) ANSI escapes change.
	var h strings.Builder
	for _, l := range lines {
		h.WriteString(stripANSI(l))
		h.WriteByte('\n')
	}
	return statusFrame{lines: lines, hash: h.String()}
}

func paneWidth(cols int) int {
	if cols > 110 {
		return 110
	}
	if cols < 60 {
		return 60
	}
	return cols
}

// buildHeader → "Bulesky29 · 穴居人 (Troglodyte) · 守序 · 回合 129"
func buildHeader(f statusFields) string {
	var parts []string
	if f.name != "" {
		parts = append(parts, ansiBold+ansiBrightCyan+f.name+ansiReset)
	}
	if f.title != "" {
		titleStr := f.title
		if f.titleZh != "" {
			titleStr = fmt.Sprintf("%s%s%s (%s%s%s)", ansiCyan, f.titleZh, ansiReset, ansiDim, f.title, ansiReset)
		} else {
			titleStr = ansiCyan + f.title + ansiReset
		}
		parts = append(parts, titleStr)
	}
	if f.align != "" {
		parts = append(parts, colourAlign(f.align))
	}
	if f.turn != "" {
		parts = append(parts, ansiDim+"回合 "+f.turn+ansiReset)
	}
	return strings.Join(parts, ansiDim+" · "+ansiReset)
}

// buildVitalsRow → HP bar, Pw bar, AC, Xp, Dlvl, gold — all Chinese labels.
func buildVitalsRow(f statusFields) string {
	var parts []string
	if f.hpMax > 0 {
		parts = append(parts, fmt.Sprintf("%s生命%s %s %s%d/%d%s",
			ansiDim, ansiReset,
			renderBar(f.hp, f.hpMax, 10, hpPalette(f.hp, f.hpMax)),
			hpPalette(f.hp, f.hpMax), f.hp, f.hpMax, ansiReset))
	}
	if f.pwMax > 0 {
		parts = append(parts, fmt.Sprintf("%s法力%s %s %s%d/%d%s",
			ansiDim, ansiReset,
			renderBar(f.pw, f.pwMax, 8, ansiBrightMagenta),
			ansiMagenta, f.pw, f.pwMax, ansiReset))
	}
	if f.ac != "" {
		parts = append(parts, fmt.Sprintf("%s护甲%s %s%s%s", ansiDim, ansiReset, ansiBold, f.ac, ansiReset))
	}
	if f.xp != "" {
		parts = append(parts, fmt.Sprintf("%s经验%s %s", ansiDim, ansiReset, f.xp))
	}
	if f.dlvl != "" {
		parts = append(parts, fmt.Sprintf("%s楼层%s %s%s%s", ansiDim, ansiReset, ansiBold, f.dlvl, ansiReset))
	}
	if f.gold != "" {
		parts = append(parts, fmt.Sprintf("%s金币%s %s%s%s", ansiYellow, ansiReset, ansiBrightYellow, f.gold, ansiReset))
	}
	return strings.Join(parts, "   ")
}

// buildStatsRow → six attribute scalars + condition badges, Chinese labels.
func buildStatsRow(f statusFields) string {
	var parts []string
	add := func(label, val string) {
		if val == "" {
			return
		}
		parts = append(parts, fmt.Sprintf("%s%s%s %s", ansiDim, label, ansiReset, val))
	}
	add("力量", f.st)
	add("敏捷", f.dx)
	add("体格", f.co)
	add("智力", f.in)
	add("感知", f.wi)
	add("魅力", f.ch)

	row := strings.Join(parts, "  ")
	if len(f.conditions) > 0 {
		var badges []string
		for _, c := range f.conditions {
			zh := statusConditions[c]
			if zh == "" {
				zh = c
			}
			badges = append(badges, fmt.Sprintf("%s%s %s %s", ansiBgRed, ansiBold, zh, ansiReset))
		}
		row += "   " + strings.Join(badges, " ")
	}
	return row
}

// renderBar produces a unicode-block bar of `width` cells, coloured.
func renderBar(cur, max, width int, colour string) string {
	if max <= 0 {
		return strings.Repeat("░", width)
	}
	if cur < 0 {
		cur = 0
	}
	if cur > max {
		cur = max
	}
	filled := cur * width / max
	if cur > 0 && filled == 0 {
		filled = 1 // never show a fully-empty bar when HP > 0
	}
	return fmt.Sprintf("%s%s%s%s%s", colour, strings.Repeat("█", filled),
		ansiDim, strings.Repeat("░", width-filled), ansiReset)
}

// hpPalette picks a colour based on hp / hpMax ratio.
func hpPalette(hp, max int) string {
	if max <= 0 {
		return ansiGreen
	}
	ratio := float64(hp) / float64(max)
	switch {
	case ratio > 0.67:
		return ansiBrightGreen
	case ratio > 0.34:
		return ansiBrightYellow
	default:
		return ansiBrightRed
	}
}

func colourAlign(a string) string {
	zh := alignmentLabels[a]
	col := ansiYellow
	switch a {
	case "Lawful":
		col = ansiBrightCyan
	case "Neutral":
		col = ansiYellow
	case "Chaotic":
		col = ansiBrightRed
	}
	if zh != "" {
		return fmt.Sprintf("%s%s%s", col, zh, ansiReset)
	}
	return fmt.Sprintf("%s%s%s", col, a, ansiReset)
}

// Box-drawing helpers -----------------------------------------------------

func topBorder(width int, header string) string {
	headerDisplay := visibleWidth(header)
	leftRule := 2
	rightRule := width - 2 - leftRule - headerDisplay - 2 // two spaces around header
	if rightRule < 1 {
		rightRule = 1
	}
	if header == "" {
		return fmt.Sprintf("%s┌%s┐%s", ansiDim, strings.Repeat("─", width-2), ansiReset)
	}
	return fmt.Sprintf("%s┌%s %s %s%s┐%s",
		ansiDim,
		strings.Repeat("─", leftRule),
		header,
		ansiDim, strings.Repeat("─", rightRule),
		ansiReset,
	)
}

func sideBorder(width int, content string) string {
	inner := width - 4 // borders + one space padding each side
	pad := inner - visibleWidth(content)
	if pad < 0 {
		pad = 0
	}
	return fmt.Sprintf("%s│%s %s%s %s│%s",
		ansiDim, ansiReset, content, strings.Repeat(" ", pad), ansiDim, ansiReset)
}

func bottomBorder(width int) string {
	return fmt.Sprintf("%s└%s┘%s", ansiDim, strings.Repeat("─", width-2), ansiReset)
}

// visibleWidth strips ANSI escapes and counts display cells, treating each
// CJK rune as 2 columns (NetHack's status bar is ASCII; the CJK widths
// matter for the translated labels we inject).
func visibleWidth(s string) int {
	clean := stripANSI(s)
	w := 0
	for _, r := range clean {
		if r >= 0x1100 && (r <= 0x115f ||
			(r >= 0x2e80 && r <= 0xa4cf) || // CJK
			(r >= 0xac00 && r <= 0xd7a3) || // Hangul
			(r >= 0xf900 && r <= 0xfaff) || // CJK Compat
			(r >= 0xfe30 && r <= 0xfe4f) || // CJK Compat Forms
			(r >= 0xff00 && r <= 0xff60) || // Fullwidth Forms
			(r >= 0xffe0 && r <= 0xffe6)) {
			w += 2
		} else if r >= 0x20 {
			w += 1
		}
	}
	return w
}

var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]|\x1b[78]`)

func stripANSI(s string) string {
	return ansiEscapeRe.ReplaceAllString(s, "")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ensure utf8 is used so the import isn't dropped if visibleWidth changes
var _ = utf8.RuneLen

// Static label maps -------------------------------------------------------

var alignmentLabels = map[string]string{
	"Lawful":  "守序",
	"Neutral": "中立",
	"Chaotic": "混乱",
}

// statusConditions maps NetHack's per-turn affliction tokens to their
// Chinese badge text. Order doesn't matter (we render in detection order).
var statusConditions = map[string]string{
	"Hungry":   "饥饿",
	"Weak":     "虚弱",
	"Fainting": "晕厥",
	"Starving": "濒死",
	"Satiated": "过饱",
	"Confused": "困惑",
	"Stunned":  "眩晕",
	"Hallu":    "幻觉",
	"Blind":    "失明",
	"Burdened": "负重",
	"Stressed": "负担重",
	"Strained": "极限",
	"FoodPois": "食物中毒",
	"Ill":      "患病",
	"Slimed":   "黏液化",
	"Held":     "被抓",
	"Greasy":   "油腻",
}
