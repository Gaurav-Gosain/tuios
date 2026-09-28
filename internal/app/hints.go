package app

import (
	"fmt"
	"image/color"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hints"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

// Hints mode, after tmux-fingers and kitty's hints kitten.
//
// A key puts a short label on every URL, path, hash, address and number on the
// focused pane and dims the rest. Typing a label copies what it names. The
// same label typed with Shift copies it and types it into the pane, and with
// Ctrl copies it and opens it. Esc closes.
//
// What it works from. The pane's cells are copied once, when hints mode opens,
// and everything after that (the matches, the labels, the frame) comes from
// the copy. A pane that keeps printing would otherwise move its text out from
// under the labels between two keystrokes, and a label that now points at
// different text is worse than no label. The copy is also what makes hints
// cost nothing while it is closed: nothing here runs until the key is pressed,
// and the frame pass in hints_render.go is one nil check per layer.
//
// Which machine opens a match is link_open.go's question, and it answers it
// here too: a URL opens on this machine only for a local client, and a path
// opens only when the pane runs on this machine. Nothing is ever run through
// a shell: the opener gets the match as one argument.

// HintAction is what completing a label does.
type HintAction int

const (
	// HintCopy copies the match. It is the plain letter.
	HintCopy HintAction = iota
	// HintType copies the match and types it into the pane. It is the letter
	// with Shift, tmux-fingers' paste action.
	HintType
	// HintOpen opens the match when it is a URL or a path. It is the letter
	// with Ctrl.
	HintOpen
)

// hintCell is one cell of the pane's view.
type hintCell struct{ x, y int }

// hintMatch is one match on the pane.
type hintMatch struct {
	text  string
	kind  string
	label string
	// cells are the cells the match covers, first to last in reading order.
	// A match that wraps covers the end of one row and the start of the next.
	cells []hintCell
	// labelAt is where the label's first letter is drawn. It is the match's
	// first cell unless the row is too short to the right of it, and then
	// the label moves left until it fits.
	labelAt hintCell
}

// hintsState is hints mode while it is open.
type hintsState struct {
	windowID string
	// w and h are the pane's content size when the copy was taken. A pane
	// resized since is showing different text, and hints mode closes.
	w, h int
	// cells is the copy of the pane's view, row by row.
	cells [][]uv.Cell
	// wraps says, per row, whether the emulator wrapped the row onto the
	// next one. See paneRowWraps.
	wraps []bool
	// matches is every match, in reading order.
	matches []hintMatch
	// owner maps a cell (y*w+x) to its match index, or -1.
	owner []int
	// labelRune maps a cell to the label letter drawn on it, and labelOf to
	// the match whose label it is.
	labelRune map[int]rune
	labelOf   map[int]int
	// alphabet is the letters labels are made of.
	alphabet string
	// typed is the start of a label the person has typed so far.
	typed string
	// action is the strongest action any typed letter asked for. Shift or
	// Ctrl on any letter of a two-letter label counts.
	action HintAction
	// dim is the percent of its light the text around the matches loses,
	// and dimmed holds the colours already worked out for it.
	dim    int
	dimmed map[[2]uint32]color.Color
	// noticeID is the dock message that says the keys, taken down on close.
	noticeID string
}

// HintsOpen reports whether hints mode is open on a pane that is still there,
// on screen and focused. Hints mode on any other pane is closed first, so a
// caller that routes on this never sends a key to labels nobody can see.
func (m *OS) HintsOpen() bool {
	m.closeStaleHints()
	return m.hints != nil
}

// HintsTyped is the start of a label typed so far.
func (m *OS) HintsTyped() string {
	if m.hints == nil {
		return ""
	}
	return m.hints.typed
}

// HintLabels maps each match's text to its label, for tests and for anything
// that needs to say what is on screen without reading cells.
func (m *OS) HintLabels() map[string]string {
	if m.hints == nil {
		return nil
	}
	out := make(map[string]string, len(m.hints.matches))
	for _, h := range m.hints.matches {
		out[h.text] = h.label
	}
	return out
}

// closeStaleHints closes hints mode when its pane is gone, hidden, resized or
// no longer focused.
func (m *OS) closeStaleHints() {
	if m.hints != nil && m.hintsWindow() == nil {
		m.CloseHints()
	}
}

// hintsConfig is the [hints] section in force.
func (m *OS) hintsConfig() config.HintsConfig {
	if m.UserConfig == nil {
		return config.DefaultConfig().Hints
	}
	return m.UserConfig.Hints
}

// OpenHints opens hints mode on the focused pane. It says so and does nothing
// when there is no pane or nothing on it to label.
func (m *OS) OpenHints() {
	m.CloseHints()
	window := m.GetFocusedWindow()
	if window == nil || window.Terminal == nil {
		m.ShowNotification("Open a pane first. Hints label the text in a pane.", "info", m.Settings.NotificationDuration)
		return
	}
	cfg := m.hintsConfig()
	builtins, _ := hints.ParseBuiltins(cfg.Builtins)
	matcher, errs := hints.New(builtins, cfg.Patterns)
	for _, err := range errs {
		m.LogError("hints: %v", err)
	}

	state := snapshotHints(window)
	state.alphabet = cfg.LabelAlphabet()
	state.dim = cfg.DimPercent()
	state.findMatches(matcher, hintsCursor(window, state))
	if len(state.matches) == 0 {
		m.ShowNotification("Nothing to label in this pane.", "info", m.Settings.NotificationDuration)
		return
	}
	m.hints = state
	m.CancelCopyFlash()
	// The keys are said while the labels are up and taken back when they go,
	// so opening hints a few times does not queue a message per open.
	before := len(m.Notifications)
	m.ShowNotification("Type a label to copy. Shift+label types it. Ctrl+label opens it. Esc closes.", "info", m.Settings.NotificationDuration)
	if len(m.Notifications) > before {
		state.noticeID = m.Notifications[len(m.Notifications)-1].ID
	}
}

// CloseHints closes hints mode. The pane is drawn from its own cells again on
// the next frame, because the frame pass is the only thing hints mode changed.
func (m *OS) CloseHints() {
	if m.hints == nil {
		return
	}
	id, notice := m.hints.windowID, m.hints.noticeID
	m.hints = nil
	if notice != "" {
		for i, n := range m.Notifications {
			if n.ID == notice {
				m.Notifications = append(m.Notifications[:i], m.Notifications[i+1:]...)
				break
			}
		}
	}
	if w := m.windowByID(id); w != nil {
		w.ContentDirty = true
	}
}

// hintsWindow is the pane hints mode is open on, or nil when it is gone,
// lost focus, or changed size, all of which mean the copy no longer matches
// the screen.
func (m *OS) hintsWindow() *terminal.Window {
	if m.hints == nil {
		return nil
	}
	w := m.GetFocusedWindow()
	if w == nil || w.ID != m.hints.windowID || w.Workspace != m.CurrentWorkspace || w.Minimized {
		return nil
	}
	if w.ContentWidth() != m.hints.w || w.ContentHeight() != m.hints.h {
		return nil
	}
	return w
}

// HintsUsesLetter reports whether r is one of the letters labels are made
// of. A letter outside the alphabet is free for hints mode's own keys.
func (m *OS) HintsUsesLetter(r rune) bool {
	return m.hints != nil && strings.ContainsRune(m.hints.alphabet, r)
}

// HintsBackspace takes back the last letter typed.
func (m *OS) HintsBackspace() {
	if m.hints == nil || m.hints.typed == "" {
		return
	}
	m.hints.typed = m.hints.typed[:len(m.hints.typed)-1]
	if m.hints.typed == "" {
		m.hints.action = HintCopy
	}
}

// HintsPress takes one letter of a label. A letter that starts no label is
// ignored, so a slip does not end hints mode. When the letters typed so far
// are a whole label, the action runs and hints mode closes.
func (m *OS) HintsPress(letter rune, action HintAction) tea.Cmd {
	h := m.hints
	if h == nil {
		return nil
	}
	window := m.hintsWindow()
	if window == nil {
		m.CloseHints()
		return nil
	}
	letter = unicode.ToLower(letter)
	if !strings.ContainsRune(h.alphabet, letter) {
		return nil
	}
	typed := h.typed + string(letter)
	var hit *hintMatch
	prefix := false
	for i := range h.matches {
		switch label := h.matches[i].label; {
		case label == typed:
			hit = &h.matches[i]
		case strings.HasPrefix(label, typed):
			prefix = true
		}
	}
	if hit == nil && !prefix {
		return nil
	}
	h.typed = typed
	h.action = max(h.action, action)
	if hit == nil {
		return nil
	}
	match := *hit
	act := h.action
	m.CloseHints()
	return m.runHint(window, match, act)
}

// runHint does what a completed label asked for.
func (m *OS) runHint(window *terminal.Window, match hintMatch, action HintAction) tea.Cmd {
	switch action {
	case HintType:
		cmd := m.copyHint(window, match, "")
		payload := match.text
		if window.Terminal != nil && window.Terminal.BracketedPasteEnabled() {
			payload = "\x1b[200~" + payload + "\x1b[201~"
		}
		if err := window.SendInput([]byte(payload)); err != nil {
			m.ShowNotification("Copied the text. Could not type it into the pane.", "warning", m.Settings.NotificationDuration)
			return cmd
		}
		m.ShowNotification(fmt.Sprintf("Copied %d chars and typed them into the pane", hintChars(match.text)), "success", m.Settings.NotificationDuration)
		return cmd
	case HintOpen:
		return m.openHint(window, match)
	default:
		return m.copyHint(window, match, "")
	}
}

// copyHint writes the match to the clipboard through the one copy path (the
// native tool where it applies, and OSC 52 always), sweeps the copy flash over
// it, and says so. note replaces the dock message when it is not empty.
func (m *OS) copyHint(window *terminal.Window, match hintMatch, note string) tea.Cmd {
	m.CancelPendingCopy()
	m.noteHintFlash(window, match)
	if note == "" {
		note = fmt.Sprintf("Copied %d chars", hintChars(match.text))
	}
	m.ShowNotification(note, "success", m.Settings.NotificationDuration)
	return m.clipboardWriteCmd(match.text)
}

// noteHintFlash sweeps the copy flash over the match, the way a mouse copy
// sweeps it over the selection. The flash takes absolute rows, in which the
// scrollback comes first and the screen follows it.
func (m *OS) noteHintFlash(window *terminal.Window, match hintMatch) {
	if !m.Settings.CopyFlash || m.copyFlashDuration() <= 0 || !m.Settings.MotionAllows(config.MotionBasic) {
		return
	}
	if len(match.cells) == 0 {
		return
	}
	base := window.ScrollbackLen() - window.ScrollbackOffset
	first, last := match.cells[0], match.cells[len(match.cells)-1]
	m.copyFlash = &copyFlash{
		WindowID: window.ID,
		Start:    terminal.Position{X: first.x, Y: base + first.y},
		End:      terminal.Position{X: last.x, Y: base + last.y},
		At:       time.Now(),
	}
	window.ContentDirty = true
}

// openHint opens a URL or a path and copies anything else.
//
// A web address goes through OpenLink, which holds the scheme list and knows a
// remote client cannot open anything for its viewer. A file (a path, or a
// file:// address) is opened only when every machine involved is this one:
// the pane, the session, the client and the directory the pane is in. On any
// other machine the same path names somebody else's file, and opening a local
// file of the same name would show the wrong thing. Each refusal copies the
// text and says why.
func (m *OS) openHint(window *terminal.Window, match hintMatch) tea.Cmd {
	if !m.hintsConfig().OpenEnabled() {
		return m.copyHint(window, match, fmt.Sprintf("Opening is off. Copied %d chars", hintChars(match.text)))
	}
	switch match.kind {
	case hints.URL:
		if !strings.HasPrefix(strings.ToLower(match.text), "file:") {
			m.CancelPendingCopy()
			return m.OpenLink(match.text)
		}
		if why := m.hintFileBlocked(window); why != "" {
			return m.copyHint(window, match, why+" Copied the link")
		}
		path, ok := linkFilePath(match.text)
		if !ok {
			return m.copyHint(window, match, "The link names a file on another machine. Copied the link")
		}
		m.CancelPendingCopy()
		return m.openLocalPath(path, match.text)
	case hints.Path, hints.Diff:
		if why := m.hintFileBlocked(window); why != "" {
			return m.copyHint(window, match, why+" Copied the path")
		}
		path, why := hintLocalPath(window, match.text)
		if why != "" {
			return m.copyHint(window, match, why+" Copied the path")
		}
		m.CancelPendingCopy()
		return m.openLocalPath(path, path)
	default:
		return m.copyHint(window, match, fmt.Sprintf("tuios opens only links and paths. Copied %d chars", hintChars(match.text)))
	}
}

// hintRemoteShells are the programs whose pane shows another machine's files
// even though the pane itself runs here: remote shells, and the tools that
// open a shell in a container or a cluster. kitten is kitty's, whose ssh
// kitten is the common case; docker, kubectl and podman are refused whatever
// they are doing, because their exec is the one that matters and the name is
// all tuios can see.
var hintRemoteShells = []string{
	"ssh", "autossh", "mosh", "mosh-client", "et", "telnet", "tsh", "kitten",
	"docker", "kubectl", "podman",
}

// hintForeground is the name of the program running in the pane in front of
// its shell, or "" when the shell itself has the terminal. A daemon session
// reports it; a standalone pane is asked through its terminal.
func hintForeground(window *terminal.Window) string {
	if window.ForegroundCmd != "" {
		return window.ForegroundCmd
	}
	if window.HasForegroundProcess() {
		if name := window.ForegroundCommand(); name != "" {
			return name
		}
		return "a program"
	}
	return ""
}

// hintFileBlocked says why a file named in the pane may not be opened on this
// machine, or "" when it may.
func (m *OS) hintFileBlocked(window *terminal.Window) string {
	switch {
	case window.Host != "":
		return "The pane runs on another machine."
	case m.AttachedHost != "":
		return "This session runs on another machine."
	case m.IsRemoteClient():
		return "A remote client can not open files."
	case slices.Contains(hintRemoteShells, hintForeground(window)):
		return "The pane shows another machine."
	}
	if host, ok := hintCwdHost(window.Cwd); ok && host != "" && !isLocalHost(host) {
		return "The pane's folder is on another machine."
	}
	return ""
}

// hintCwdHost is the host an OSC 7 directory names, and whether it names one
// at all. A bare path names no host.
func hintCwdHost(cwd string) (string, bool) {
	if !strings.HasPrefix(cwd, "file://") {
		return "", false
	}
	u, err := url.Parse(cwd)
	if err != nil {
		return "", false
	}
	return u.Hostname(), true
}

// hintLocalPath turns a path match into an absolute path on this machine: the
// :line:col a compiler prints is dropped, ~ is the home directory, and a
// relative path is taken from the pane's working directory. It says why when
// the path cannot be placed: a relative path with no known directory, or a
// directory the shell reported on another machine.
func hintLocalPath(window *terminal.Window, text string) (string, string) {
	path := stripLineCol(text)
	if rest, ok := strings.CutPrefix(path, "~"); ok && (rest == "" || rest[0] == '/') {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "The home folder is not known."
		}
		path = filepath.Join(home, rest)
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), ""
	}
	// A relative path is relative to where the program that printed it
	// runs. The folder tuios knows is the one the shell last reported, which
	// is that program's only while the shell itself has the terminal: a
	// program in front of it may have changed folder, or be a remote shell
	// or a container that no name list can cover.
	if fg := hintForeground(window); fg != "" {
		return "", "The pane runs " + fg + ", so its folder is not known."
	}
	// The directory the shell last reported wins. When it names another
	// machine there is no local directory to fall back to: the process
	// directory is this machine's, and the path is not.
	if window.Cwd != "" {
		if host, ok := hintCwdHost(window.Cwd); ok && host != "" && !isLocalHost(host) {
			return "", "The pane's folder is on another machine."
		}
		if cwd, ok := localCwdPath(window.Cwd); ok {
			return filepath.Join(cwd, path), ""
		}
		return "", "The pane's folder is not known."
	}
	if cwd := window.CWD(); cwd != "" {
		return filepath.Join(cwd, path), ""
	}
	return "", "The pane's folder is not known."
}

// stripLineCol drops a trailing :line or :line:col.
func stripLineCol(text string) string {
	for range 2 {
		i := strings.LastIndexByte(text, ':')
		if i <= 0 || i == len(text)-1 || strings.Trim(text[i+1:], "0123456789") != "" {
			break
		}
		text = text[:i]
	}
	return text
}

// snapshotHints copies the pane's view, from the scrollback when the pane is
// scrolled back, so hints label exactly what is on the screen.
func snapshotHints(window *terminal.Window) *hintsState {
	window.RLockIO()
	defer window.RUnlockIO()
	w := min(window.ContentWidth(), window.Terminal.Width())
	h := window.ContentHeight()
	state := &hintsState{
		windowID: window.ID,
		w:        window.ContentWidth(),
		h:        h,
		cells:    make([][]uv.Cell, h),
		wraps:    make([]bool, h),
	}
	blank := uv.Cell{Content: " ", Width: 1}
	for y := range h {
		row := make([]uv.Cell, state.w)
		for x := range state.w {
			row[x] = blank
			if x >= w {
				continue
			}
			if c := paneCellAt(window, x, y); c != nil {
				row[x] = *c
				if row[x].Content == "" && row[x].Width == 0 {
					continue
				}
				if row[x].Content == "" {
					row[x].Content = " "
				}
				if row[x].Width <= 0 {
					row[x].Width = 1
				}
			}
		}
		state.cells[y] = row
		state.wraps[y] = paneRowWraps(window, y)
	}
	return state
}

// hintsCursor is where the person is looking: the cursor when it is on the
// screen, and the bottom row otherwise. The nearest matches get the shortest
// labels.
func hintsCursor(window *terminal.Window, state *hintsState) hintCell {
	if window.ScrollbackOffset == 0 && window.Terminal != nil {
		p := window.Terminal.CursorPosition()
		if p.Y >= 0 && p.Y < state.h {
			return hintCell{x: p.X, y: p.Y}
		}
	}
	return hintCell{x: 0, y: max(state.h-1, 0)}
}

// hintsWrapRows bounds how many rows one wrapped line may join, like the link
// hover's bound.
const hintsWrapRows = 16

// rowWraps reports whether row y carries on to row y+1 because the emulator
// wrapped it. The flag was read when the copy was taken; a full last column
// on its own is never taken for a wrap.
func (s *hintsState) rowWraps(y int) bool {
	return y >= 0 && y < len(s.wraps) && s.wraps[y]
}

// hintChars counts the characters a person sees in text, which is what the
// dock message reports: a path of accented or CJK names is fewer characters
// than it is bytes.
func hintChars(text string) int { return utf8.RuneCountInString(text) }
