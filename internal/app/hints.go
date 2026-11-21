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
	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
)

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

type hintCell struct{ x, y int }

type hintMatch struct {
	pane    int
	text    string
	kind    string
	label   string
	target  string
	cells   []hintCell
	labelAt hintCell
}

type hintsPane struct {
	windowID  string
	x, y      int
	w, h      int
	cells     [][]uv.Cell
	wraps     []bool
	owner     []int
	labelRune map[int]rune
	labelOf   map[int]int
}

type hintsState struct {
	focusID  string
	panes    []*hintsPane
	matches  []hintMatch
	alphabet string
	typed    string
	action   HintAction
	dim      int
	dimmed   map[[2]uint32]color.Color
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

func (m *OS) closeStaleHints() {
	if m.hints != nil && m.hintsFocused() == nil {
		m.CloseHints()
	}
}

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

const hintsPaneStride = 1 << 24

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
	if m.Settings.Links == config.LinksAll {
		matcher.WithLinkRules(m.Settings.LinkRules)
	}
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
				t.Key = w.ID + "\x00" + match.text
			}
			targets = append(targets, t)
		}
		state.panes = append(state.panes, pane)
	}
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
	m.hints = state
	m.CancelCopyFlash()
}

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

func (m *OS) hintsDrawn(w *terminal.Window) bool {
	if w.Workspace != m.CurrentWorkspace || w.Minimized {
		return false
	}
	zoomed := m.zoomedWindow()
	return zoomed == nil || w == zoomed || w.IsPopup || !m.zoomCoversRegion(zoomed)
}

func (m *OS) hintsRegion() image.Rectangle {
	left, top := m.GetLeftMargin(), m.GetTopMargin()
	r := image.Rect(left, top, left+m.GetContentWidth(), top+m.GetUsableHeight())
	if v := m.sessionView; v.on {
		r = r.Intersect(v.visible())
	}
	return r
}

func hintsCenter(w *terminal.Window) image.Point {
	return image.Pt(w.X+w.Width/2, w.Y+w.Height/2)
}

func hintsGap(a, b image.Point) int {
	dx, dy := a.X-b.X, a.Y-b.Y
	return max(dx, -dx) + 2*max(dy, -dy)
}

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

func (m *OS) hintsHidden(w *terminal.Window) func(hintCell) bool {
	region := m.hintsRegion()
	covers := m.hintsCovers(w)
	origin := paneContentRect(w).Min
	return func(c hintCell) bool {
		p := image.Pt(origin.X+c.x, origin.Y+c.y)
		if !p.In(region) {
			return true
		}
		return slices.ContainsFunc(covers, p.In)
	}
}

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

func hintPaneScoped(match hintMatch) bool {
	switch match.kind {
	case hints.Path, hints.Diff:
		return true
	case hints.URL:
		return strings.HasPrefix(strings.ToLower(match.openURL()), "file:")
	}
	return false
}

func (match hintMatch) openURL() string {
	if match.target != "" {
		return match.target
	}
	return match.text
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
	source := m.windowByID(h.panes[match.pane].windowID)
	m.CloseHints()
	if focused.InCopyMode() {
		focused.ExitCopyMode()
		focused.InvalidateCache()
	}
	return m.runHint(focused, source, match, act)
}

func (m *OS) runHint(focused, source *terminal.Window, match hintMatch, action HintAction) tea.Cmd {
	window := source
	switch action {
	case HintType:
		cmd := m.copyHint(window, match, "")
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

func (m *OS) copyHint(window *terminal.Window, match hintMatch, note string) tea.Cmd {
	m.CancelPendingCopy()
	m.noteHintFlash(window, match)
	if note == "" {
		note = fmt.Sprintf("Copied %d chars", hintChars(match.text))
	}
	m.ShowNotification(note, "success", m.Settings.NotificationDuration)
	return m.clipboardWriteCmd(match.text)
}

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

func (m *OS) openHint(window *terminal.Window, match hintMatch) tea.Cmd {
	if !m.hintsConfig().OpenEnabled() {
		return m.copyHint(window, match, fmt.Sprintf("Opening is off. Copied %d chars", hintChars(match.text)))
	}
	switch match.kind {
	case hints.URL:
		target := match.openURL()
		if !strings.HasPrefix(strings.ToLower(target), "file:") {
			m.CancelPendingCopy()
			return m.OpenLink(target)
		}
		if why := m.hintFileBlocked(window); why != "" {
			return m.copyHint(window, match, why+" Copied the link")
		}
		path, ok := linkFilePath(match.text)
		if !ok {
			return m.copyHint(window, match, "The link names a file on another machine. Copied the link")
		}
		m.CancelPendingCopy()
		bare, line := splitLineFragment(match.text)
		if line > 0 {
			if p, ok := linkFilePath(bare); ok {
				path = p
			}
		}
		return m.openLocalPath(path, line, match.text)
	case hints.Path, hints.Diff:
		if why := m.hintFileBlocked(window); why != "" {
			return m.copyHint(window, match, why+" Copied the path")
		}
		path, why := hintLocalPath(window, match.text)
		if why != "" {
			return m.copyHint(window, match, why+" Copied the path")
		}
		_, line, _ := hints.SplitLineCol(match.text)
		m.CancelPendingCopy()
		return m.openLocalPath(path, line, path)
	default:
		return m.copyHint(window, match, fmt.Sprintf("tuios opens only links and paths. Copied %d chars", hintChars(match.text)))
	}
}

var hintRemoteShells = []string{
	"ssh", "autossh", "mosh", "mosh-client", "et", "telnet", "tsh", "kitten",
	"docker", "kubectl", "podman",
}

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
	if fg := hintForeground(window); fg != "" {
		return "", "The pane runs " + fg + ", so its folder is not known."
	}
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

func hintsCursor(window *terminal.Window, state *hintsPane) hintCell {
	if window.ScrollbackOffset == 0 && window.Terminal != nil {
		p := window.Terminal.CursorPosition()
		if p.Y >= 0 && p.Y < state.h {
			return hintCell{x: p.X, y: p.Y}
		}
	}
	return hintCell{x: 0, y: max(state.h-1, 0)}
}

const hintsWrapRows = 16

func (s *hintsPane) rowWraps(y int) bool {
	return y >= 0 && y < len(s.wraps) && s.wraps[y]
}

func hintChars(text string) int { return utf8.RuneCountInString(text) }
