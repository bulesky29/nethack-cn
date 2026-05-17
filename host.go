package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hinshun/vt10x"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// runHost wires the local terminal to a remote nethack@alt.org SSH session
// while streaming the top two rows of the screen to a sibling client process
// over a local TCP socket.
func runHost(debug bool) error {
	dbg, err := openDebugLog(debug, "host")
	if err != nil {
		return fmt.Errorf("open debug log: %w", err)
	}
	defer dbg.Close()
	dbg.Raw("host start: debug=%v", debug)

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	listener, err := net.Listen("tcp", localAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", localAddr, err)
	}
	defer listener.Close()

	if err := spawnClientTerminal(debug); err != nil {
		return fmt.Errorf("spawn client: %w", err)
	}

	fmt.Printf("nh-helper host: waiting for client on %s ...\n", localAddr)
	if tl, ok := listener.(*net.TCPListener); ok {
		_ = tl.SetDeadline(time.Now().Add(60 * time.Second))
	}
	conn, err := listener.Accept()
	if err != nil {
		return fmt.Errorf("accept client: %w", err)
	}
	defer conn.Close()
	dbg.Raw("client TCP accepted from %s", conn.RemoteAddr())
	fmt.Println("Client connected. Establishing SSH session ...")

	return runSession(cfg, conn, dbg)
}

func runSession(cfg *Config, conn net.Conn, dbg *debugLog) error {
	stdinFd := int(os.Stdin.Fd())
	stdoutFd := int(os.Stdout.Fd())

	cols, rows, err := term.GetSize(stdoutFd)
	if err != nil || cols == 0 || rows == 0 {
		cols, rows = 80, 24
	}

	// dgamelaunch-style game gateways (alt.org, hardfought, etc.) often only
	// advertise keyboard-interactive auth; bare ssh.Password isn't enough.
	// We register both and answer every prompt with the configured password,
	// which is empty for alt.org's public "nethack" account.
	kbInteractive := ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
		answers := make([]string, len(questions))
		for i := range questions {
			answers[i] = cfg.SSHPassword
		}
		return answers, nil
	})
	sshCfg := &ssh.ClientConfig{
		User:            cfg.SSHUser,
		Auth:            []ssh.AuthMethod{ssh.Password(cfg.SSHPassword), kbInteractive},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         20 * time.Second,
	}

	client, err := ssh.Dial("tcp", sshHost, sshCfg)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", sshHost, err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	modes := ssh.TerminalModes{
		ssh.ECHO:          0,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	termType := os.Getenv("TERM")
	if termType == "" {
		termType = "xterm-256color"
	}
	if err := session.RequestPty(termType, rows, cols, modes); err != nil {
		return fmt.Errorf("request pty: %w", err)
	}

	stdinPipe, err := session.StdinPipe()
	if err != nil {
		return err
	}
	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		return err
	}
	stderrPipe, err := session.StderrPipe()
	if err != nil {
		return err
	}

	if err := session.Shell(); err != nil {
		return fmt.Errorf("start shell: %w", err)
	}

	oldState, err := term.MakeRaw(stdinFd)
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	restoreOnce := sync.Once{}
	restoreTerminal := func() {
		restoreOnce.Do(func() {
			_ = term.Restore(stdinFd, oldState)
		})
	}
	defer restoreTerminal()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGWINCH)
	defer signal.Stop(sigs)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case sig := <-sigs:
				if sig == syscall.SIGWINCH {
					if c, r, err := term.GetSize(stdoutFd); err == nil {
						_ = session.WindowChange(r, c)
					}
					continue
				}
				restoreTerminal()
				cancel()
				return
			}
		}
	}()

	vt := vt10x.New(vt10x.WithSize(cols, rows))

	var wg sync.WaitGroup

	// Local stdin -> remote SSH stdin.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(stdinPipe, os.Stdin)
		_ = stdinPipe.Close()
	}()

	// Remote stdout -> local stdout AND virtual terminal.
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 8192)
		for {
			n, err := stdoutPipe.Read(buf)
			if n > 0 {
				_, _ = os.Stdout.Write(buf[:n])
				_, _ = vt.Write(buf[:n])
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					// errors here usually mean the session was torn down
				}
				return
			}
		}
	}()

	// Remote stderr -> local stderr.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(os.Stderr, stderrPipe)
	}()

	// Watch the screen for both top-row messages and full-screen popups
	// (story dumps, menus, inventory) and ship events to the client.
	wg.Add(1)
	go func() {
		defer wg.Done()
		watchScreen(ctx, vt, conn, dbg)
	}()

	// Detect end of SSH session.
	sessionDone := make(chan struct{})
	go func() {
		_ = session.Wait()
		close(sessionDone)
	}()

	select {
	case <-sessionDone:
	case <-ctx.Done():
	}
	cancel()
	_ = session.Close()
	_ = stdinPipe.Close()

	// Best-effort wait on goroutines; stdin copy may block on the
	// local TTY, so give it a short window then return.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
	}

	restoreTerminal()
	return nil
}

// Wire-protocol markers shared with the client. Single-line messages are
// prefixed; popups and status updates are bracketed.
const (
	eventMsgPrefix   = "[MSG] "
	eventPopupBegin  = "[POPUP_BEGIN]"
	eventPopupEnd    = "[POPUP_END]"
	eventStatusBegin = "[STATUS_BEGIN]"
	eventStatusEnd   = "[STATUS_END]"
)

// Pagination markers that indicate a full-screen popup is showing:
// --More-- (story / multi-message dumps), (end) (last page of menu),
// (1 of 5) (interior menu page).
var (
	popupMarkerLiteral = []string{"--More--", "(end)"}
	popupMarkerPaged   = regexp.MustCompile(`\(\d+ of \d+\)`)
	// statusRowPattern matches NetHack's second status row: "Dlvl:N ...HP:N(N)".
	statusRowPattern = regexp.MustCompile(`Dlvl:\s*\d+.*HP:`)
	// turnCounterPattern strips the T:N turn counter for dedupe purposes —
	// it changes every turn and would otherwise spam updates.
	turnCounterPattern = regexp.MustCompile(`\bT:\s*\d+`)
)

type screenState struct {
	popup      string
	topMessage string
}

func watchScreen(ctx context.Context, vt vt10x.Terminal, w io.Writer, dbg *debugLog) {
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastMsg        string
		lastPopup      string
		lastStatusNorm string
		inPopupMode    bool
	)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			lines := snapshotScreen(vt)
			if len(lines) == 0 {
				continue
			}

			// Status bar lives in the bottom two rows during play. We
			// emit it independently of popup/message detection so the
			// fixed-position pane in the client stays current even when
			// the player is inside a menu.
			if status := extractStatus(lines); status != "" {
				normalized := turnCounterPattern.ReplaceAllString(status, "T:_")
				if normalized != lastStatusNorm {
					lastStatusNorm = normalized
					dbg.Raw("emit STATUS")
					dbg.RawBlock("status content", status)
					if err := emitStatus(w, status); err != nil {
						dbg.Raw("emit STATUS failed: %v", err)
						return
					}
				}
			}

			state, markerRow := classifyScreen(lines)
			if state.popup != "" {
				if !inPopupMode {
					dbg.Raw("popup ENTER: marker_row=%d", markerRow)
					dbg.RawBlock("popup captured", state.popup)
					inPopupMode = true
				}
				if state.popup == lastPopup {
					continue
				}
				if lastPopup != "" {
					dbg.RawBlock("popup CHANGED, new content", state.popup)
				}
				lastPopup = state.popup
				lastMsg = "" // a popup invalidates the message-area cache
				dbg.Raw("emit POPUP: %d bytes, %d lines", len(state.popup), strings.Count(state.popup, "\n")+1)
				if err := emitPopup(w, state.popup); err != nil {
					dbg.Raw("emit POPUP failed: %v", err)
					return
				}
				continue
			}
			// Not in popup mode — reset popup dedupe so the *next*
			// popup is sent even if its content matches the previous one.
			if inPopupMode {
				dbg.Raw("popup LEAVE: last_marker_row=%d", markerRow)
				inPopupMode = false
			}
			lastPopup = ""

			if state.topMessage == "" || state.topMessage == lastMsg {
				continue
			}
			lastMsg = state.topMessage
			dbg.Raw("emit MSG: %q", state.topMessage)
			if err := emitMessage(w, state.topMessage); err != nil {
				dbg.Raw("emit MSG failed: %v", err)
				return
			}
		}
	}
}

// snapshotScreen reads every visible row of the virtual terminal once,
// trimming trailing whitespace. Returned slice is indexed by row.
func snapshotScreen(vt vt10x.Terminal) []string {
	cols, rows := vt.Size()
	if rows < 1 || cols < 1 {
		return nil
	}
	lines := make([]string, rows)
	vt.Lock()
	for y := 0; y < rows; y++ {
		lines[y] = strings.TrimRight(readRow(vt, y, cols), " ")
	}
	vt.Unlock()
	return lines
}

// classifyScreen decides whether the current screen is showing a full-screen
// popup or a normal play frame, and extracts the relevant text accordingly.
// The second return value is the row where a pagination marker was found,
// or -1 if none — useful for debug logging.
func classifyScreen(lines []string) (screenState, int) {
	markerRow := findPaginationMarker(lines)
	if markerRow > 1 {
		return screenState{popup: extractPopup(lines, markerRow)}, markerRow
	}
	return screenState{topMessage: extractTopMessage(lines)}, markerRow
}

// extractStatus pulls NetHack's two-row status bar from the bottom of the
// screen. Returns "" when no status bar is detected (e.g. dgamelaunch menus,
// character creation, full-screen popups that hide the bar).
func extractStatus(lines []string) string {
	if len(lines) < 2 {
		return ""
	}
	row2 := strings.TrimSpace(lines[len(lines)-1])
	row1 := strings.TrimSpace(lines[len(lines)-2])
	if !statusRowPattern.MatchString(row2) {
		return ""
	}
	if row1 == "" {
		return row2
	}
	return row1 + "\n" + row2
}

func findPaginationMarker(lines []string) int {
	for i, line := range lines {
		for _, m := range popupMarkerLiteral {
			if strings.Contains(line, m) {
				return i
			}
		}
		if popupMarkerPaged.MatchString(line) {
			return i
		}
	}
	return -1
}

func extractPopup(lines []string, markerRow int) string {
	out := make([]string, 0, markerRow+1)
	for i := 0; i <= markerRow; i++ {
		cleaned := stripPopupMarkers(lines[i])
		cleaned = strings.TrimSpace(cleaned)
		if cleaned == "" {
			continue
		}
		out = append(out, cleaned)
	}
	return strings.Join(out, "\n")
}

func stripPopupMarkers(s string) string {
	for _, m := range popupMarkerLiteral {
		s = strings.ReplaceAll(s, m, "")
	}
	return popupMarkerPaged.ReplaceAllString(s, "")
}

func extractTopMessage(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	combined := strings.TrimRight(lines[0], " ")
	if len(lines) >= 2 {
		if l2 := strings.TrimRight(lines[1], " "); l2 != "" {
			combined = strings.TrimRight(combined+" "+l2, " ")
		}
	}
	combined = stripPopupMarkers(combined)
	return strings.TrimSpace(combined)
}

func emitMessage(w io.Writer, msg string) error {
	_, err := fmt.Fprintln(w, eventMsgPrefix+msg)
	return err
}

func emitPopup(w io.Writer, popup string) error {
	if _, err := fmt.Fprintln(w, eventPopupBegin); err != nil {
		return err
	}
	for _, line := range strings.Split(popup, "\n") {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, eventPopupEnd)
	return err
}

func emitStatus(w io.Writer, status string) error {
	if _, err := fmt.Fprintln(w, eventStatusBegin); err != nil {
		return err
	}
	for _, line := range strings.Split(status, "\n") {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, eventStatusEnd)
	return err
}

func readRow(vt vt10x.Terminal, y, cols int) string {
	var b strings.Builder
	b.Grow(cols)
	for x := 0; x < cols; x++ {
		g := vt.Cell(x, y)
		r := g.Char
		if r == 0 {
			r = ' '
		}
		b.WriteRune(r)
	}
	return b.String()
}

// spawnClientTerminal opens a new Terminal.app window running this binary in
// client mode, so the user gets the translation pane side-by-side.
func spawnClientTerminal(debug bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	// Quote the path for AppleScript and the shell layer it invokes.
	asEscapedPath := strings.ReplaceAll(exe, `\`, `\\`)
	asEscapedPath = strings.ReplaceAll(asEscapedPath, `"`, `\"`)
	debugFlag := ""
	if debug {
		debugFlag = " -debug"
	}
	shellCommand := fmt.Sprintf(`\"%s\" -mode client%s`, asEscapedPath, debugFlag)
	script := fmt.Sprintf(`tell application "Terminal" to do script "%s"`, shellCommand)

	cmd := exec.Command("osascript", "-e", script)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}
