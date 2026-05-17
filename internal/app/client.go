package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	defaultBaseURL = "https://openrouter.ai/api/v1/chat/completions"
	defaultModel   = "anthropic/claude-haiku-4.5"
	// defaultFastModel runs short classification tasks (popup categorization,
	// etc.) — picked for low latency and ~10× lower per-token cost than the
	// main translation model.
	defaultFastModel = "google/gemini-2.5-flash-lite"
)

// runClient connects to the host process, filters trivial NetHack messages,
// and ships interesting ones to OpenRouter for Chinese translation.
// The role determines what kinds of events this client will receive:
//
//	roleText: streaming MSG batches + narrative POPUPs
//	roleMenu: STATUS bar + menu/inventory POPUPs (status pane visible)
func RunClient(debug bool, role string) error {
	dbg, err := openDebugLog(debug, "client-"+role)
	if err != nil {
		return fmt.Errorf("open debug log: %w", err)
	}
	defer dbg.Close()
	dbg.Raw("client start: debug=%v role=%s", debug, role)

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	st, err := openStore()
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() {
		// Snapshot on the way out so each session leaves a dated point-
		// in-time copy under ./db/. Keep the last 20.
		if path, snapErr := st.Snapshot(20); snapErr != nil {
			dbg.Raw("snapshot failed: %v", snapErr)
		} else if path != "" {
			dbg.Raw("snapshot written: %s", path)
		}
		st.Close()
	}()
	if n, err := st.CountGlossary(); err == nil && n > 0 {
		fmt.Printf("nh-helper client: glossary loaded with %d terms.\n", n)
	}
	dbg.Raw("db opened, glossary terms=%d", mustCount(st))

	uiPane := newUI(role)
	uiPane.Init()
	defer uiPane.Restore()

	fmt.Printf("nh-helper client (role=%s): connecting to host ...\n", role)
	conn, err := dialHost(15 * time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Announce our role so the host's router knows where to send events.
	if _, err := fmt.Fprintf(conn, "%s %s\n", roleHeader, role); err != nil {
		return fmt.Errorf("send role header: %w", err)
	}
	fmt.Printf("Connected as %s. Waiting for events ...\n", role)
	fmt.Println(strings.Repeat("-", 60))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		fmt.Println("\nShutting down ...")
		cancel()
		_ = conn.Close()
	}()

	translator := newTranslator(cfg, dbg, st, uiPane)

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var (
		recent     string
		pending    []string
		pendingMu  sync.Mutex
		flushAfter = 600 * time.Millisecond
		flushTimer = time.NewTimer(time.Hour)
	)
	flushTimer.Stop()

	flush := func() {
		pendingMu.Lock()
		if len(pending) == 0 {
			pendingMu.Unlock()
			return
		}
		batch := strings.Join(pending, "\n")
		n := len(pending)
		pending = pending[:0]
		pendingMu.Unlock()

		dbg.Raw("flush batch: %d msgs, %d bytes", n, len(batch))
		go translator.translate(ctx, batch, displayNarrative)
	}

	events := make(chan event, 128)
	go parseEvents(scanner, events)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-flushTimer.C:
			flush()
		case ev, ok := <-events:
			if !ok {
				flush()
				return nil
			}
			switch ev.kind {
			case eventKindMessage:
				dbg.Raw("recv MSG: %q", ev.text)
				msg := strings.TrimSpace(ev.text)
				if msg == "" {
					dbg.Raw("MSG dropped: empty after trim")
					continue
				}
				if msg == recent {
					dbg.Raw("MSG dropped: dedupe == recent")
					continue
				}
				recent = msg
				if reason := shouldTranslateReason(msg); reason != "" {
					dbg.Raw("MSG dropped by filter: %s", reason)
					continue
				}
				dbg.Raw("MSG queued for batch")
				pendingMu.Lock()
				pending = append(pending, msg)
				pendingMu.Unlock()
				flushTimer.Reset(flushAfter)
			case eventKindPopup:
				dbg.Raw("recv POPUP: %d bytes, %d lines", len(ev.text), strings.Count(ev.text, "\n")+1)
				dbg.RawBlock("popup content", ev.text)
				// Popups are coherent multi-line dumps — flush any
				// queued message batch first so output stays in order,
				// then ship the popup as its own translation.
				flush()
				if reason := shouldTranslatePopupReason(ev.text); reason != "" {
					dbg.Raw("POPUP dropped by filter: %s", reason)
					continue
				}
				dbg.Raw("POPUP queued for translation (display kind=%s)", popupDisplayKind(role))
				recent = "" // popup may overwrite message area on exit
				go translator.translate(ctx, ev.text, popupDisplayKind(role))
			case eventKindStatus:
				dbg.Raw("recv STATUS: %d bytes", len(ev.text))
				// Pure local rendering — labels are translated by a
				// fixed map, values pass through verbatim, no LLM call.
				uiPane.DrawStatus(ev.text, st)
			}
		}
	}
}

// Wire-protocol event kinds. The on-the-wire framing constants
// (eventMsgPrefix, eventPopupBegin, eventPopupEnd, eventStatusBegin,
// eventStatusEnd) are shared with host.go.
const (
	eventKindMessage = "msg"
	eventKindPopup   = "popup"
	eventKindStatus  = "status"
)

type event struct {
	kind string
	text string
}

// parseEvents consumes the host's framed stream and emits decoded events.
// It blocks until the scanner is exhausted, then closes out.
func parseEvents(scanner *bufio.Scanner, out chan<- event) {
	defer close(out)
	var (
		inPopup   bool
		inStatus  bool
		popupBuf  []string
		statusBuf []string
	)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case inPopup:
			if line == eventPopupEnd {
				inPopup = false
				out <- event{kind: eventKindPopup, text: strings.Join(popupBuf, "\n")}
				popupBuf = popupBuf[:0]
				continue
			}
			popupBuf = append(popupBuf, line)
		case inStatus:
			if line == eventStatusEnd {
				inStatus = false
				out <- event{kind: eventKindStatus, text: strings.Join(statusBuf, "\n")}
				statusBuf = statusBuf[:0]
				continue
			}
			statusBuf = append(statusBuf, line)
		case line == eventPopupBegin:
			inPopup = true
			popupBuf = popupBuf[:0]
		case line == eventStatusBegin:
			inStatus = true
			statusBuf = statusBuf[:0]
		case strings.HasPrefix(line, eventMsgPrefix):
			out <- event{kind: eventKindMessage, text: strings.TrimPrefix(line, eventMsgPrefix)}
		}
	}
}

func dialHost(timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", localAddr, 2*time.Second)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	return nil, fmt.Errorf("dial host: %w", lastErr)
}

// Filtering ---------------------------------------------------------------

var (
	trivialExact = map[string]struct{}{
		"It's a wall.":           {},
		"Never mind.":            {},
		"You stop.":              {},
		"You stop running.":      {},
		"Nothing happens.":       {},
		"You can't go that way.": {},
		"You see no door there.": {},
	}

	trivialPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^You see here\s`),
		regexp.MustCompile(`^You move\b`),
		regexp.MustCompile(`^(North|South|East|West|Northeast|Northwest|Southeast|Southwest)\.?$`),
		regexp.MustCompile(`^Pick up what\?`),
		regexp.MustCompile(`^Ouch!\s*$`),
		regexp.MustCompile(`^You are carrying:`),
		regexp.MustCompile(`Dlvl:\s*\d+.*HP:`),
		regexp.MustCompile(`^Hit return to continue`),
		regexp.MustCompile(`^\$\s*$`),
		// dgamelaunch / alt.org banner & menu lines.
		regexp.MustCompile(`^##\s`),
		regexp.MustCompile(`nethack\.alt\.org`),
		regexp.MustCompile(`^https?://`),
	}

)

// shouldTranslateReason returns "" when the message should be translated,
// or a short reason string when it should be dropped. Empty string == pass.
func shouldTranslateReason(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return "empty"
	}
	if _, ok := trivialExact[t]; ok {
		return "trivial-exact"
	}
	for _, p := range trivialPatterns {
		if p.MatchString(t) {
			return "trivial-pattern: " + p.String()
		}
	}

	// Anything not on the trivial allowlist gets translated, regardless of
	// length. We used to drop short messages, which silently lost farlook
	// nouns ("kobold", "doorway") and prompts ("In what direction?").
	return ""
}

// shouldTranslatePopupReason returns "" when the popup should be translated,
// or a short reason string when it should be dropped.
func shouldTranslatePopupReason(text string) string {
	t := strings.TrimSpace(text)
	if len(t) < 8 {
		return fmt.Sprintf("too-short (len=%d)", len(t))
	}

	lower := strings.ToLower(t)
	if strings.Contains(lower, "nethack.alt.org") {
		return "alt.org banner"
	}

	// Count how many non-blank lines look like dgamelaunch decoration
	// (## banner lines, bare URLs, or single-letter menu items like
	// "l) Login"). If most of the popup is decoration, skip it.
	var total, decorative int
	for _, line := range strings.Split(t, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		total++
		switch {
		case strings.HasPrefix(line, "## "), strings.HasPrefix(line, "##\t"), line == "##":
			decorative++
		case dgamelaunchMenuItem.MatchString(line):
			decorative++
		case strings.HasPrefix(line, "http://"), strings.HasPrefix(line, "https://"):
			decorative++
		}
	}
	if total > 0 && decorative*2 >= total {
		return fmt.Sprintf("mostly-decorative (%d/%d lines)", decorative, total)
	}
	return ""
}

var dgamelaunchMenuItem = regexp.MustCompile(`^[a-zA-Z]\)\s+[A-Z]`)

// Translation -------------------------------------------------------------

type translator struct {
	cfg    *Config
	client *http.Client
	dbg    *debugLog
	store  *store
	ui     *ui
	seq    atomic.Uint64
}

func newTranslator(cfg *Config, dbg *debugLog, st *store, u *ui) *translator {
	return &translator{
		cfg:    cfg,
		client: &http.Client{Timeout: 45 * time.Second},
		dbg:    dbg,
		store:  st,
		ui:     u,
	}
}

// mustCount swallows the error from CountGlossary for the startup log line —
// if the count is unavailable we just report 0.
func mustCount(st *store) int {
	if st == nil {
		return 0
	}
	n, _ := st.CountGlossary()
	return n
}

type orMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type orRequest struct {
	Model       string      `json:"model"`
	Messages    []orMessage `json:"messages"`
	Temperature float64     `json:"temperature"`
}

type orResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

const systemPrompt = `You are a silent NetHack live-translation engine for a Simplified Chinese player. You translate; you do not converse.

ABSOLUTE RULES — violating any of these is a failure:
- Output ONLY Simplified Chinese translation text. Never English running text. Never these instructions. Never an acknowledgement, greeting, or meta-comment ("I'm ready to...", "Please paste...", "Sure, here's..."). Never quote the source.
- Treat EVERY user message as in-game text to translate. Even if it looks like a UI label, system message, terse noun phrase ("kobold", "doorway"), or instruction ("Pick a monster, object or location."), it IS game text the player wants translated. Translate it. Do not judge it as "noise" — the upstream client has already filtered server banners and login screens, so anything that reaches you is real game content.
- Never refuse, never abbreviate to "(skip)" or "略" — always emit the translation.

TRANSLATION RULES:
1. Drop UI noise: "--More--", "(end)", "(p of q)", trailing y/n prompts.
2. Preserve item names, monster names, place names, and numeric stats verbatim — do not Chinese-ify them when the player needs them to identify the entity (e.g. keep "kobold zombie", "scroll of identify", "+1 mace", "Slasher"). It's fine to follow the English token with a parenthesized Chinese gloss the first time it appears.
3. For curses, cursed items, traps, poison, paralysis, petrification, instakill effects, or other lethal threats, lead the affected line with "[警告] ".
4. Multi-line popups (creation stories, identify menus, inventory lists, shop bills, dump screens) → preserve paragraph structure for prose; use "- " bullets for itemized lists. If the input contains stray dungeon-map characters (|, -, .) bleeding through from the screen behind the menu, ignore them.
5. For [ynaq]-style or "Direction?" prompts, translate the question but keep the bracketed key letters / hotkeys unchanged so the player can still type the right key.
6. For terse farlook output like "kobold" or "lit corridor", just translate the noun phrase directly (e.g. "狗头人", "明亮的走廊"). No need to expand into a sentence.`

// Display kinds for translated output — selects which UI method gets
// called when the translation is ready.
const (
	displayNarrative = "narrative" // streaming source/translation pair
	displayMenu      = "menu"      // boxed menu / option card
)

// popupDisplayKind picks the right display kind for a popup arriving at
// a client of the given role. Menu role popups have already been
// classified server-side as menu-shaped content.
func popupDisplayKind(role string) string {
	if role == roleMenu {
		return displayMenu
	}
	return displayNarrative
}

func (t *translator) translate(ctx context.Context, text, kind string) {
	if cached, ok := t.store.GetTranslation(text); ok {
		t.dbg.Translate("cache hit: input_bytes=%d kind=%s", len(text), kind)
		t.display(text, cached, kind)
		return
	}

	id := t.seq.Add(1)
	started := time.Now()

	// Look up any glossary terms that appear in this input and inject them
	// into the user message as a hint block. Robust against an empty
	// glossary — first runs simply skip the hint.
	hints, err := t.store.LookupGlossary(text)
	if err != nil {
		t.dbg.Translate("req #%d glossary lookup error: %v", id, err)
	}
	userMessage := buildUserMessage(text, hints)

	t.dbg.Translate("req #%d model=%s temp=0.2 input_bytes=%d glossary_hits=%d",
		id, t.cfg.Model, len(text), len(hints))
	t.dbg.TranslateBlock(fmt.Sprintf("req #%d user message", id), userMessage)

	body, err := json.Marshal(orRequest{
		Model:       t.cfg.Model,
		Temperature: 0.2,
		Messages: []orMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userMessage},
		},
	})
	if err != nil {
		t.dbg.Translate("req #%d marshal error: %v", id, err)
		fmt.Fprintf(os.Stderr, "[translate] marshal: %v\n", err)
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, t.cfg.BaseURL, bytes.NewReader(body))
	if err != nil {
		t.dbg.Translate("req #%d new-request error: %v", id, err)
		fmt.Fprintf(os.Stderr, "[translate] new request: %v\n", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.cfg.OpenRouterAPIKey)
	req.Header.Set("HTTP-Referer", "https://localhost/nh-helper")
	req.Header.Set("X-Title", "nh-helper")

	resp, err := t.client.Do(req)
	if err != nil {
		t.dbg.Translate("req #%d http error after %s: %v", id, time.Since(started), err)
		fmt.Fprintf(os.Stderr, "[translate] http: %v\n", err)
		return
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.dbg.Translate("req #%d read-body error: %v", id, err)
		fmt.Fprintf(os.Stderr, "[translate] read body: %v\n", err)
		return
	}

	if resp.StatusCode/100 != 2 {
		t.dbg.Translate("req #%d HTTP %d in %s body=%q", id, resp.StatusCode, time.Since(started), strings.TrimSpace(string(raw)))
		fmt.Fprintf(os.Stderr, "[translate] http %d: %s\n", resp.StatusCode, strings.TrimSpace(string(raw)))
		return
	}

	var parsed orResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.dbg.Translate("req #%d decode error: %v raw=%q", id, err, string(raw))
		fmt.Fprintf(os.Stderr, "[translate] decode: %v\n", err)
		return
	}
	if parsed.Error != nil {
		t.dbg.Translate("req #%d api error: %s", id, parsed.Error.Message)
		fmt.Fprintf(os.Stderr, "[translate] api error: %s\n", parsed.Error.Message)
		return
	}
	if len(parsed.Choices) == 0 {
		t.dbg.Translate("req #%d empty choices array", id)
		return
	}

	out := strings.TrimSpace(parsed.Choices[0].Message.Content)
	latency := time.Since(started)
	t.dbg.Translate("req #%d ok in %s output_bytes=%d", id, latency, len(out))
	t.dbg.TranslateBlock(fmt.Sprintf("req #%d output", id), out)

	if out == "" {
		t.dbg.Translate("req #%d suppressed (empty response)", id)
		return
	}

	if err := t.store.PutTranslation(text, out, t.cfg.Model); err != nil {
		t.dbg.Translate("req #%d cache write failed: %v", id, err)
	}
	t.display(text, out, kind)

	// Fire-and-forget glossary curation. We only bother for substantial
	// content (popups, long messages) to avoid doubling the API bill on
	// every "You swap places with Slasher." line.
	if len(text) >= 60 {
		go t.extractGlossary(context.Background(), text, out)
	}
}

// display prints a source/translation pair to stdout with a timestamp,
// using the UI method appropriate for the kind. The UI mutex serializes
// against status-pane redraws so the cursor positioning stays clean.
func (t *translator) display(text, out, kind string) {
	stamp := time.Now().Format("15:04:05")
	switch kind {
	case displayMenu:
		t.ui.PrintMenu(text, out, stamp)
	default:
		t.ui.PrintTranslation(text, out, stamp)
	}
}

// buildUserMessage prepends glossary hints (if any) to the raw NetHack text
// so the model uses consistent Chinese renderings for known terms.
func buildUserMessage(text string, hints []glossaryEntry) string {
	if len(hints) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString("GLOSSARY (use these renderings when the term appears):\n")
	for _, h := range hints {
		b.WriteString("- ")
		b.WriteString(h.En)
		b.WriteString(" = ")
		b.WriteString(h.Zh)
		if h.Category != "" && h.Category != "misc" {
			b.WriteString(" (")
			b.WriteString(h.Category)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	b.WriteString("\nTEXT TO TRANSLATE:\n")
	b.WriteString(text)
	return b.String()
}

const glossaryExtractPrompt = `You analyze NetHack game text to extract glossary terms.

Given an English game text and its Chinese translation, identify NetHack-specific vocabulary that should be in a reusable glossary: monster names, item types, place types, status conditions, terrain features. Skip generic English words and skip proper nouns of named entities (pet names like "Slasher", player names).

Output one term per line in this exact format:

<english> || <chinese> || <category>

Where category is one of: monster, item, place, status, terrain, misc.

Rules:
- Use the Chinese rendering as it appears in the translation. If the translation kept the English verbatim, supply a sensible Chinese gloss anyway.
- Each English headword should be its canonical singular base form (e.g. "kobold" not "kobolds", "scroll of identify" not "scrolls of identify").
- If there are no new terms worth adding, output exactly: NONE

Output ONLY the term list or "NONE". No explanations, no other text.`

// extractGlossary asks the LLM to mine new glossary entries from a
// translation pair and inserts them into the store. Runs asynchronously so
// it never blocks the user-visible translation pipeline. Failures are
// logged and swallowed.
func (t *translator) extractGlossary(ctx context.Context, source, translation string) {
	prompt := fmt.Sprintf("ORIGINAL:\n%s\n\nTRANSLATION:\n%s", source, translation)
	body, err := json.Marshal(orRequest{
		Model:       t.cfg.Model,
		Temperature: 0.1,
		Messages: []orMessage{
			{Role: "system", Content: glossaryExtractPrompt},
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		t.dbg.Translate("glossary extract marshal: %v", err)
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, t.cfg.BaseURL, bytes.NewReader(body))
	if err != nil {
		t.dbg.Translate("glossary extract new-request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.cfg.OpenRouterAPIKey)
	req.Header.Set("HTTP-Referer", "https://localhost/nh-helper")
	req.Header.Set("X-Title", "nh-helper")

	resp, err := t.client.Do(req)
	if err != nil {
		t.dbg.Translate("glossary extract http: %v", err)
		return
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode/100 != 2 {
		t.dbg.Translate("glossary extract HTTP %d err=%v", resp.StatusCode, err)
		return
	}
	var parsed orResponse
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Choices) == 0 {
		return
	}
	out := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if out == "" || strings.EqualFold(out, "NONE") {
		t.dbg.Translate("glossary extract: no new terms")
		return
	}

	added := 0
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "NONE") {
			continue
		}
		parts := strings.Split(line, "||")
		if len(parts) < 2 {
			continue
		}
		entry := glossaryEntry{
			En: strings.TrimSpace(parts[0]),
			Zh: strings.TrimSpace(parts[1]),
		}
		if len(parts) >= 3 {
			entry.Category = strings.TrimSpace(parts[2])
		}
		if entry.En == "" || entry.Zh == "" {
			continue
		}
		if err := t.store.PutGlossary(entry, "auto"); err != nil {
			t.dbg.Translate("glossary insert %q: %v", entry.En, err)
			continue
		}
		added++
	}
	if added > 0 {
		t.dbg.Translate("glossary extract: added %d new term(s)", added)
	}
}

// dim wraps a string in ANSI dim formatting; falls back to plain on non-ttys.
func dim(s string) string {
	if !isTerminal(os.Stdout) {
		return s
	}
	return "\x1b[2m" + s + "\x1b[0m"
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
