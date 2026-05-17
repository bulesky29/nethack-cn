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

// menuLinePattern matches the "<letter> - " prefix NetHack uses for menu
// rows. Both lowercase and uppercase letters appear (e.g. capital
// selectors like "A - All worn"). Spaces around the dash vary slightly
// between menus so we allow a couple of forms.
var menuLinePattern = regexp.MustCompile(`^[a-zA-Z]\s*-\s`)

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
