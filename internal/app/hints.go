package app

import (
	"fmt"
	"image"
	"image/color"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hints"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
	uv "github.com/charmbracelet/ultraviolet"
)

// Hints mode, after tmux-fingers and kitty's hints kitten.
//
// A key puts a short label on every URL, path, hash, address and number on the
// focused pane and dims the rest. The all-panes form (the hints_all_panes
// action, or hints.all_panes in the config) does the same on every pane the
// workspace shows, with one set of labels across them: the focused pane gets
// the shortest, then the panes nearest it. Typing a label copies what it names. The
// same label typed with Shift copies it and types it into the pane, and with
// Ctrl copies it and opens it. Esc closes.
//
// What it works from. The pane's cells are copied once, when hints mode opens,
// and everything after that (the matches, the labels, the frame) comes from
// the copy. Each pane in hints mode has its own copy. A pane that keeps printing would otherwise move its text out from
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

// hintMatch is one match on a pane.
type hintMatch struct {
	// pane is the index in hintsState.panes of the pane the match is on.
	pane  int
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

// hintsPane is the copy of one pane's view that hints mode labels.
type hintsPane struct {
	windowID string
	// x and y are the pane's position when the copy was taken. The covers
	// and the region were worked out for it, so a pane that moved closes
	// hints mode.
	x, y int
	// w and h are the pane's content size when the copy was taken. A pane
	// resized since is showing different text, and hints mode closes.
	w, h int
	// cells is the copy of the pane's view, row by row.
	cells [][]uv.Cell
	// wraps says, per row, whether the emulator wrapped the row onto the
	// next one. See paneRowWraps.
	wraps []bool
	// owner maps a cell (y*w+x) to its index in hintsState.matches, or -1.
	owner []int
	// labelRune maps a cell to the label letter drawn on it, and labelOf to
	// the match whose label it is.
	labelRune map[int]rune
	labelOf   map[int]int
}

// hintsState is hints mode while it is open.
type hintsState struct {
	// focusID is the pane that had focus when hints mode opened. Shift and a
	// label types into it, whichever pane the match is on.
	focusID string
	// panes are the panes in hints mode, the focused one first.
	panes []*hintsPane
	// matches is every match on every pane, pane by pane, each pane's in
	// reading order.
	matches []hintMatch
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
// that needs to say what is on screen without reading cells. A text on two
// panes can have a label per pane, and then the map holds one of them.
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

// closeStaleHints closes hints mode when one of its panes is gone, hidden or
// resized, or the pane it opened on no longer has focus.
func (m *OS) closeStaleHints() {
	if m.hints != nil && m.hintsFocused() == nil {
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

// OpenHints is the hints action. It opens hints mode on the focused pane, or
// on every pane the workspace shows when hints.all_panes is on.
func (m *OS) OpenHints() { m.openHints(m.hintsConfig().AllPanes) }

// OpenHintsAllPanes is the hints_all_panes action: hints mode on every pane
// the workspace shows, whatever hints.all_panes says.
func (m *OS) OpenHintsAllPanes() { m.openHints(true) }

// hintsPaneStride keeps one pane's distances apart from the next pane's, so
// every match on a nearer pane gets a label no longer than any match on a
// pane further away.
const hintsPaneStride = 1 << 24

// openHints opens hints mode on the focused pane, and with all on every other
// pane the workspace shows as well. It says so and does nothing when there is
// no pane or nothing on it to label.
func (m *OS) openHints(all bool) {
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

	windows := []*terminal.Window{window}
	if all {
		windows = m.hintsWindows(window)
	}
	state := &hintsState{
		focusID:  window.ID,
		alphabet: cfg.LabelAlphabet(),
		dim:      cfg.DimPercent(),
	}
	var targets []hints.Target
	// hidden, per pane, reports whether a cell of the pane is off the
	// screen: outside the content region, or under a pane drawn above it.
	// The single-pane form labels the focused pane as it always has, and
	// has none.
	var hidden []func(hintCell) bool
	for rank, w := range windows {
		pane := snapshotHints(w)
		pane.x, pane.y = w.X, w.Y
		cursor := hintsCursor(w, pane)
		var covered func(hintCell) bool
		if all {
			covered = m.hintsHidden(w)
		}
		hidden = append(hidden, covered)
		for _, match := range pane.find(matcher) {
			at := match.cells[0]
			if covered != nil && covered(at) {
				continue
			}
			match.pane = len(state.panes)
			state.matches = append(state.matches, match)
			t := hints.Target{
				Text:     match.text,
				Distance: rank*hintsPaneStride + min(hintDistance(at, cursor, pane.w), hintsPaneStride-1),
			}
			if all && hintPaneScoped(match) {
				// A relative path names a file in its own pane's folder, so
				// the same path on two panes is two things to open.
				t.Key = w.ID + "\x00" + match.text
			}
			targets = append(targets, t)
		}
		state.panes = append(state.panes, pane)
	}
	// A label must be whole on the screen. Its length is known only once the
	// labels are handed out, so a match whose label would be partly hidden is
	// dropped and the rest are labelled again. Each round drops at least one
	// match, and fewer matches never need longer labels.
	for len(state.matches) > 0 {
		state.label(targets)
		keep := 0
		for i, match := range state.matches {
			if h := hidden[match.pane]; h != nil && hintLabelHidden(match, state.panes[match.pane].w, h) {
				continue
			}
			state.matches[keep], targets[keep] = state.matches[i], targets[i]
			keep++
		}
		if keep == len(state.matches) {
			break
		}
		state.matches, targets = state.matches[:keep], targets[:keep]
	}
	if len(state.matches) == 0 {
		if all {
			m.ShowNotification("Nothing to label in the panes on this workspace.", "info", m.Settings.NotificationDuration)
		} else {
			m.ShowNotification("Nothing to label in this pane.", "info", m.Settings.NotificationDuration)
		}
		return
	}
	// The keys are not said in a message. The dock shows them as a legend
	// for as long as the labels are up (see mode_legend.go).
	m.hints = state
	m.CancelCopyFlash()
}

// hintsWindows is every pane the all-panes form labels: the focused pane
// first, then each other pane the workspace shows, nearest the focused pane
// first. It leaves out what the frame does not draw: a minimised pane, a pane
// behind a zoom that fills the region, and a pane wholly outside the content
// region (a column the scrolling layout has moved off the screen).
func (m *OS) hintsWindows(focused *terminal.Window) []*terminal.Window {
	region := m.hintsRegion()
	var others []*terminal.Window
	for _, w := range m.Windows {
		if w == nil || w == focused || w.Terminal == nil || !m.hintsDrawn(w) {
			continue
		}
		if w.ContentWidth() <= 0 || w.ContentHeight() <= 0 {
			continue
		}
		if !paneContentRect(w).Overlaps(region) {
			continue
		}
		others = append(others, w)
	}
	c := hintsCenter(focused)
	slices.SortStableFunc(others, func(a, b *terminal.Window) int {
		return hintsGap(c, hintsCenter(a)) - hintsGap(c, hintsCenter(b))
	})
	return append([]*terminal.Window{focused}, others...)
}

// hintsDrawn reports whether the frame draws w at all. It follows the skips
// in the render loop: another workspace, minimised, or behind a zoomed pane
// that fills the region (a popup is drawn over the zoom).
func (m *OS) hintsDrawn(w *terminal.Window) bool {
	if w.Workspace != m.CurrentWorkspace || w.Minimized {
		return false
	}
	zoomed := m.zoomedWindow()
	return zoomed == nil || w == zoomed || w.IsPopup || !m.zoomCoversRegion(zoomed)
}

// hintsRegion is the content region, where the frame draws panes. Anything
// outside it is off the screen or under the dock or the rail.
func (m *OS) hintsRegion() image.Rectangle {
	left, top := m.GetLeftMargin(), m.GetTopMargin()
	r := image.Rect(left, top, left+m.GetContentWidth(), top+m.GetUsableHeight())
	// A view of a larger session shows only part of it. See pane_view.go.
	if v := m.sessionView; v.on {
		r = r.Intersect(v.visible())
	}
	return r
}

// hintsCenter is the middle of a pane's box.
func hintsCenter(w *terminal.Window) image.Point {
	return image.Pt(w.X+w.Width/2, w.Y+w.Height/2)
}

// hintsGap is how far apart two points are, in cells. A row is about two
// columns tall, so rows count twice.
func hintsGap(a, b image.Point) int {
	dx, dy := a.X-b.X, a.Y-b.Y
	return max(dx, -dx) + 2*max(dy, -dy)
}

// hintsCovers is the box of every pane the frame draws above w, whatever
// its kind. With tiling off, plain panes overlap too, stacked by their Z.
func (m *OS) hintsCovers(w *terminal.Window) []image.Rectangle {
	z := windowLayerZ(w, false)
	var out []image.Rectangle
	for _, o := range m.Windows {
		if o == nil || o == w || !m.hintsDrawn(o) {
			continue
		}
		if windowLayerZ(o, false) <= z {
			continue
		}
		out = append(out, image.Rect(o.X, o.Y, o.X+o.Width, o.Y+o.Height))
	}
	return out
}

// hintsHidden returns a test for a cell of w's content: true when the cell
// is off the screen, which is outside the content region or under a pane
// drawn above w.
func (m *OS) hintsHidden(w *terminal.Window) func(hintCell) bool {
	region := m.hintsRegion()
	covers := m.hintsCovers(w)
	origin := paneContentRect(w).Min
	return func(c hintCell) bool {
		p := image.Pt(origin.X+c.x, origin.Y+c.y)
		if !p.In(region) {
			return true
		}
		for _, r := range covers {
			if p.In(r) {
				return true
			}
		}
		return false
	}
}

// hintLabelHidden reports whether any cell the match's label is drawn on is
// hidden. The label is placed already (see hintsState.label).
func hintLabelHidden(match hintMatch, width int, hidden func(hintCell) bool) bool {
	for j := range len(match.label) {
		c := hintCell{x: match.labelAt.x + j, y: match.labelAt.y}
		if c.x >= width {
			break
		}
		if hidden(c) {
			return true
		}
	}
	return false
}

// hintPaneScoped reports whether what a match names depends on its pane: a
// path or a file link, which Ctrl and a label opens from that pane's folder
// and machine.
func hintPaneScoped(match hintMatch) bool {
	switch match.kind {
	case hints.Path, hints.Diff:
		return true
	case hints.URL:
		return strings.HasPrefix(strings.ToLower(match.text), "file:")
	}
	return false
}

// CloseHints closes hints mode. The pane is drawn from its own cells again on
// the next frame, because the frame pass is the only thing hints mode changed.
func (m *OS) CloseHints() {
	if m.hints == nil {
		return
	}
	panes := m.hints.panes
	m.hints = nil
	for _, p := range panes {
		if w := m.windowByID(p.windowID); w != nil {
			w.ContentDirty = true
		}
	}
}

// hintsFocused is the pane hints mode opened on, or nil when hints mode no
// longer matches the screen: that pane lost focus, or any pane in hints mode
// is gone, hidden or resized.
func (m *OS) hintsFocused() *terminal.Window {
	if m.hints == nil {
		return nil
	}
	f := m.GetFocusedWindow()
	if f == nil || f.ID != m.hints.focusID {
		return nil
	}
	for _, p := range m.hints.panes {
		if m.hintsPaneWindow(p) == nil {
			return nil
		}
	}
	return f
}

// hintsPaneWindow is the pane p is a copy of, or nil when it is gone, on
// another workspace, minimised, moved or resized. Any pane in hints mode
// that moves or resizes closes hints mode, a pane without focus too: the
// labels and what hides them were worked out for the old layout.
func (m *OS) hintsPaneWindow(p *hintsPane) *terminal.Window {
	w := m.windowByID(p.windowID)
	if w == nil || w.Workspace != m.CurrentWorkspace || w.Minimized {
		return nil
	}
	if w.ContentWidth() != p.w || w.ContentHeight() != p.h || w.X != p.x || w.Y != p.y {
		return nil
	}
	return w
}

// hintsHelpKey closes hints mode and opens the help on its keys. It is not a
// letter, so no label alphabet can take it.
const hintsHelpKey = "?"

// HintsShowKeys is the hintsHelpKey: hints mode closes, and the help opens on
// the section that lists all of its keys, scrolled to them. The dock's legend
// shows only the keys that fit, and this is the way to the rest.
func (m *OS) HintsShowKeys() {
	m.CloseHints()
	m.OpenHelpAtCategory(HelpCategoryCopyMode)
	cats := m.HelpCategories()
	if m.HelpCategory < 0 || m.HelpCategory >= len(cats) {
		return
	}
	for i, b := range cats[m.HelpCategory].Bindings {
		if strings.HasPrefix(b.Description, helpHintsPrefix) {
			// The render clamps this to the last page, which holds
			// every hints line when they are the last in the section.
			m.HelpScrollOffset = i
			return
		}
	}
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
	focused := m.hintsFocused()
	if focused == nil {
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
			// The same text on two panes shares a label. The first is on
			// the pane nearest the focus.
			if hit == nil {
				hit = &h.matches[i]
			}
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
	// hintsFocused has checked every pane in hints mode is still there.
	source := m.windowByID(h.panes[match.pane].windowID)
	m.CloseHints()
	return m.runHint(focused, source, match, act)
}

// runHint does what a completed label asked for. source is the pane the
// match is on, and focused is the pane the person is in. A copy or an open
// works from the source pane. Typing goes to the focused pane.
func (m *OS) runHint(focused, source *terminal.Window, match hintMatch, action HintAction) tea.Cmd {
	window := source
	switch action {
	case HintType:
		cmd := m.copyHint(window, match, "")
		// The match is pane output, so it is pasted with control
		// characters removed, like any other paste.
		if err := focused.Paste(match.text); err != nil {
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
	if len(match.cells) == 0 {
		return
	}
	base := window.ScrollbackLen() - window.ScrollbackOffset
	first, last := match.cells[0], match.cells[len(match.cells)-1]
	m.NoteCopyFlashRegion(window,
		terminal.Position{X: first.x, Y: base + first.y},
		terminal.Position{X: last.x, Y: base + last.y})
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
func snapshotHints(window *terminal.Window) *hintsPane {
	window.RLockIO()
	defer window.RUnlockIO()
	w := min(window.ContentWidth(), window.Terminal.Width())
	h := window.ContentHeight()
	state := &hintsPane{
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
				vt.BlankSixelCell(&row[x])
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
func hintsCursor(window *terminal.Window, state *hintsPane) hintCell {
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
func (s *hintsPane) rowWraps(y int) bool {
	return y >= 0 && y < len(s.wraps) && s.wraps[y]
}

// hintChars counts the characters a person sees in text, which is what the
// dock message reports: a path of accented or CJK names is fewer characters
// than it is bytes.
func hintChars(text string) int { return utf8.RuneCountInString(text) }
