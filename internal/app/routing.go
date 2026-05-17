package app

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"sync"
)

// Network endpoints. Both host (listener) and client (dialer) reference
// these — they live in the routing file because routing is the shared
// network surface.
const (
	localAddr = "127.0.0.1:9999"
	sshHost   = "alt.org:22"
)

// Client roles. Each running client identifies itself on connect with
// "ROLE: <role>\n" so the host knows where to route which events.
// Exported so cmd/nh-helper can validate the -role flag without
// hard-coding the strings.
const (
	RoleText = "text" // streaming narrative / action translations
	RoleMenu = "menu" // fixed status bar + menu / inventory popups
)

// Internal aliases keep existing intra-package call sites readable.
const (
	roleText = RoleText
	roleMenu = RoleMenu
)

// roleHeader is the literal first line every client sends after the TCP
// handshake. The host blocks until it has received this from every
// expected connection so events never race startup.
const roleHeader = "ROLE:"

// router multiplexes wire events to one client per declared role. Writes
// to a missing role are silently dropped (e.g. user closed the menu
// window mid-game) — the SSH session keeps running.
type router struct {
	mu      sync.RWMutex
	clients map[string]io.Writer
}

func newRouter() *router {
	return &router{clients: make(map[string]io.Writer)}
}

// Register installs (or replaces) the writer for a role. Replaces any
// existing entry — useful if a client reconnects.
func (r *router) Register(role string, w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[role] = w
}

// Send writes a single line + newline to the client of the given role.
// Returns nil if no such client is registered — drop, don't error.
func (r *router) Send(role, line string) error {
	r.mu.RLock()
	w, ok := r.clients[role]
	r.mu.RUnlock()
	if !ok {
		return nil
	}
	_, err := fmt.Fprintln(w, line)
	return err
}

// SendLines writes multiple lines under a role — used for popup framing.
func (r *router) SendLines(role string, lines ...string) error {
	r.mu.RLock()
	w, ok := r.clients[role]
	r.mu.RUnlock()
	if !ok {
		return nil
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

// readRoleHeader parses the "ROLE: <role>" greeting from a client. It
// validates the role and returns it. Uses a temporary bufio.Reader on
// the conn so the caller can still wrap conn in its own buffered reader
// for normal IO afterwards.
func readRoleHeader(conn net.Conn) (string, error) {
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read role header: %w", err)
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, roleHeader) {
		return "", fmt.Errorf("expected %q greeting, got %q", roleHeader, line)
	}
	role := strings.TrimSpace(strings.TrimPrefix(line, roleHeader))
	if role != roleText && role != roleMenu {
		return "", fmt.Errorf("unknown role %q", role)
	}
	return role, nil
}

// Popup classification ----------------------------------------------------

// menuLinePattern catches "<letter> - <thing>" menu rows anywhere on a
// line — not just at the start. Inventory popups sit on top of the
// dungeon view, so a row often looks like
//
//	|.....|   B - Items known to be Blessed
//
// and the menu selector is *inside* the line, prefixed by dungeon
// noise. Matching with the embedded position lets classifyPopup count
// these as menu lines (which is what they are) and lets the menu-
// content extractor know where to slice from.
var menuLinePattern = regexp.MustCompile(`[a-zA-Z$#]\s+-\s+\S`)

// categoryHeaderPattern matches NetHack inventory section headers —
// "Coins ('$')", "Weapons (')')", "Gems/Stones ('*')" — possibly
// preceded by dungeon-map characters when the popup sits over the map.
var categoryHeaderPattern = regexp.MustCompile(`[A-Z][a-zA-Z/]+(?:\s+[A-Z][a-zA-Z/]+)?\s+\([^)]+\)`)

// farlookCardPattern catches single-line top-row messages that are
// actually farlook detail dumps — they always lead with the entity's
// map glyph, then 4+ spaces of right-padding, then the descriptive
// noun phrase. Examples:
//
//	d        a dog or other canine (tame little dog called Slasher) [seen: …]
//	<        a staircase up or a ladder up or a branch staircase up …
//	#        can be many things (corridor)
//
// These belong in the menu window (info cards), not the narrative
// stream. Bare farlook nouns like "kobold" or "open door" stay in the
// text stream because they're too short to be confidently classified.
var farlookCardPattern = regexp.MustCompile(`^[A-Za-z<>#@$\\]\s{4,}\S`)

// pickupPattern catches single-item pickup notifications — NetHack
// emits "<inventory-letter> - <item description>." on the message line
// each time you auto-pick or single-pick a thing. Examples:
//
//	$ - 2 gold pieces.
//	f - a +0 small shield.
//	a - a blessed +1 quarterstaff.
//
// Inventory letters are a-zA-Z plus the special `$` for gold and `#`
// for the Bag-of-Holding overflow letter. These are inventory state
// updates — the player wants to glance at the menu window to see
// what just landed in their pack.
var pickupPattern = regexp.MustCompile(`^[a-zA-Z$#]\s+-\s+\S`)

// extractMenuContent strips dungeon-map bleed-through from a menu
// popup so the LLM sees a clean structured list. Per line:
//
//  1. If a "<letter> - <thing>" menu row appears anywhere, slice from
//     its start position — leading dungeon noise (|.....|, --|--) is
//     dropped.
//  2. Otherwise if a "Category ('symbol')" header appears anywhere
//     (possibly with dungeon noise in front), slice from there.
//  3. Otherwise the line is pure dungeon noise — drop it entirely.
//
// Apply only to popups already classified as menu — narrative popups
// (creation stories, role cards) have no menu rows and would get
// completely emptied by this filter.
func extractMenuContent(raw string) string {
	out := make([]string, 0)
	for _, line := range strings.Split(raw, "\n") {
		if loc := menuLinePattern.FindStringIndex(line); loc != nil {
			out = append(out, strings.TrimSpace(line[loc[0]:]))
			continue
		}
		if loc := categoryHeaderPattern.FindStringIndex(line); loc != nil {
			out = append(out, strings.TrimSpace(line[loc[0]:]))
			continue
		}
		// Pure dungeon — skip silently.
	}
	return strings.Join(out, "\n")
}

// classifyMessage decides whether a top-row MSG event belongs in the
// menu window (info cards, reference-y) or the text window (narrative,
// actions, prompts). Default is text — only confidently-recognized
// info cards get diverted.
func classifyMessage(msg string) string {
	if farlookCardPattern.MatchString(msg) {
		return roleMenu
	}
	if pickupPattern.MatchString(msg) {
		return roleMenu
	}
	if strings.Contains(msg, "[seen:") {
		// The "[seen: normal vision, infravision]" tail is a tell-tale
		// of farlook detail even when the card prefix is missing.
		return roleMenu
	}
	return roleText
}

// classifyPopup decides whether a captured full-screen popup is a
// reference-style menu (inventory, identify, item-selection, etc.) or
// a narrative dump (creation story, role-card, prompt explanation).
//
// Heuristic: 2+ lines matching "<letter> - " → menu. This catches every
// real menu screen NetHack draws while leaving the prose popups
// untouched. No API call is needed for the common case; we can layer
// a fast-model fallback in later if false positives show up.
func classifyPopup(content string) string {
	hits := 0
	for _, line := range strings.Split(content, "\n") {
		// Allow whitespace before the letter — menus that overlap with
		// the dungeon map can pick up leading map characters before
		// the actual menu letter, but the cleaned popup we send has
		// already trimmed those (extractPopup → TrimSpace).
		if menuLinePattern.MatchString(strings.TrimSpace(line)) {
			hits++
			if hits >= 2 {
				return roleMenu
			}
		}
	}
	return roleText
}
