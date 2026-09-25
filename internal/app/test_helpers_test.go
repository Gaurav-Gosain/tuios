package app

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/adrg/xdg"
	"github.com/charmbracelet/x/ansi"
)

// Fixtures and helpers shared across the package's tests. They used to sit in
// the files of the tests that were removed, beside the tests still using them.

// cellAt returns the rune at x, y of a frame grid, or a space off its edge.
func cellAt(g [][]rune, x, y int) rune {
	if y < 0 || y >= len(g) || x < 0 || x >= len(g[y]) {
		return ' '
	}
	return g[y][x]
}

// railLines renders the rail and returns its rows with the styling stripped.
func railLines(t *testing.T, m *OS) []string {
	t.Helper()
	lines, w := m.sidebarPanelLines()
	if w <= 0 || lines == nil {
		t.Fatalf("the rail reserved no columns (w=%d)", w)
	}
	out := make([]string, len(lines))
	for i, ln := range lines {
		out[i] = ansi.Strip(ln)
	}
	return out
}

// openFilesOn puts the files section on the rail and waits for its first
// listing, so a render test has names to look at. It drives the real command,
// which is the only way the entries ever arrive in the app.
func openFilesOn(t *testing.T, m *OS, dir string) {
	t.Helper()
	if !m.OpenFileView(dir) {
		t.Fatal("OpenFileView refused a rail that is on and expanded")
	}
	cmd := m.TakeSidebarCmd()
	if cmd == nil {
		t.Fatal("opening the section scheduled no read")
	}
	msg, ok := cmd().(fileListMsg)
	if !ok {
		t.Fatalf("the read answered with %T, not a listing", msg)
	}
	m.HandleFileList(msg)
}

// saverSettings is the settings a saver engine is built from, with the frame
// rate the caller names. NormalFPS is the only field screensaverBuild reads,
// and it is per session now, so a test says the rate by handing one over
// instead of writing a package variable another session could read.
func saverSettings(rate int) *config.Settings {
	s := config.DefaultSettings()
	s.NormalFPS = rate
	return &s
}

// defaultSaverSettings is saverSettings at the rate a session starts on, for
// the tests that build an engine but make no claim about its clock.
func defaultSaverSettings() *config.Settings {
	return saverSettings(config.DefaultSettings().NormalFPS)
}

// findSetting locates a setting row by category and label, returning its
// category/item indices and the item itself.
func findSetting(m *OS, category, label string) (catIdx, itemIdx int, item settingItem, ok bool) {
	for ci, cat := range m.settingsCategories() {
		if cat.Name != category {
			continue
		}
		for ii, it := range cat.Items {
			if it.Label == label {
				return ci, ii, it, true
			}
		}
	}
	return 0, 0, settingItem{}, false
}

// focusSetting selects a setting row so the Settings* methods act on it.
func focusSetting(t *testing.T, m *OS, category, label string) settingItem {
	t.Helper()
	ci, ii, item, ok := findSetting(m, category, label)
	if !ok {
		t.Fatalf("setting %q not found in category %q", label, category)
	}
	m.SettingsCategory = ci
	m.SettingsSelected = ii
	return item
}

// useTempConfig points the XDG config dir at a temp location of this test's
// own, so one test's saved settings cannot be read by the next, and returns the
// resolved path.
func useTempConfig(t *testing.T) string {
	t.Helper()
	// Registered before t.Setenv so it runs after it: cleanups are LIFO, and
	// without it the xdg globals stayed on this test's temp dir for the rest of
	// the binary, after the directory is gone.
	t.Cleanup(xdg.Reload)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	xdg.Reload()
	path, err := xdg.ConfigFile("tuios/config.toml")
	if err != nil {
		t.Fatalf("resolve temp config path: %v", err)
	}
	return path
}

// styleGlyphs is every glyph the style draws its own frame with. A divider cell
// outside this set is borrowed from another style, which is the failure being
// guarded: a box-drawing tee welded onto a bar of blocks.
func styleGlyphs(b lipgloss.Border) string {
	return b.Top + b.Bottom + b.Left + b.Right +
		b.TopLeft + b.TopRight + b.BottomLeft + b.BottomRight +
		b.Middle + b.MiddleTop + b.MiddleBottom + b.MiddleLeft + b.MiddleRight
}

// dividerCells lists every cell the dividers of this layout own, clipped to the
// content region: the whole of each division, both ends included.
func dividerCells(m *OS) []layout.Rect {
	b := m.GetBSPBounds()
	var cells []layout.Rect
	for _, s := range m.separatorSplits() {
		if s.Vertical {
			for y := max(s.From, b.Y); y <= min(s.To, b.Y+b.H-1); y++ {
				cells = append(cells, layout.Rect{X: s.Pos, Y: y})
			}
			continue
		}
		for x := max(s.From, b.X); x <= min(s.To, b.X+b.W-1); x++ {
			cells = append(cells, layout.Rect{X: x, Y: s.Pos})
		}
	}
	return cells
}

// gapTestOS builds a tiled session of n panes under shared borders, each
// pane holding a marker that starts in its own first column.
func gapTestOS(t *testing.T, n int) *OS {
	t.Helper()
	origAnim := config.Global.AnimationsEnabled
	config.Global.AnimationsEnabled = false
	t.Cleanup(func() { config.Global.AnimationsEnabled = origAnim })

	m := &OS{
		Settings: config.Global,
		// The layout reads the model's session-settled geometry, seeded from
		// the globals the way NewOS seeds it.
		SharedBorders:    config.Global.SharedBorders,
		PaneGap:          config.Global.PaneGap,
		Windows:          make([]*terminal.Window, 0, n),
		FocusedWindow:    0,
		WorkspaceFocus:   map[int]int{},
		WorkspaceTrees:   map[int]*layout.BSPTree{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            160,
		Height:           48,
		AutoTiling:       true,
		MasterRatio:      0.5,

		WorkspaceLayouts:     map[int][]WindowLayout{},
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceMasterRatio: map[int]float64{},
		PendingResizes:       map[string][2]int{},
	}
	for i := range n {
		win := newTestWindow(t, fmt.Sprintf("gap-%d-%d", n, i), 40, 20)
		win.Workspace = 1
		m.Windows = append(m.Windows, win)
	}
	return m
}

// paneMarker is the text a pane paints into its own top-left cell.
func paneMarker(i int) string { return fmt.Sprintf("PANE%dEDGE", i) }

// swapBool sets a config global for the duration of the test.
func swapBool(t *testing.T, p *bool, v bool) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

// withSidebar sets the sidebar globals for a test and restores them after. It
// also points the sidebar state file at a scratch directory so a test that
// toggles or reorders never touches the developer's real state.
func withSidebar(t *testing.T, enabled bool, pos string, width int) {
	t.Helper()
	pe, pp, pw := config.Global.SidebarEnabled, config.Global.SidebarPosition, config.Global.SidebarWidth
	config.Global.SidebarEnabled = enabled
	config.Global.SidebarPosition = pos
	config.Global.SidebarWidth = width
	dir := t.TempDir()
	prevDir := sidebarStateDir
	sidebarStateDir = func() string { return dir }
	t.Cleanup(func() {
		config.Global.SidebarEnabled, config.Global.SidebarPosition, config.Global.SidebarWidth = pe, pp, pw
		sidebarStateDir = prevDir
	})
}

// railFixtureWidth is the rail width the rail's row tests were measured at,
// the shipped width before v0.8.0. The shipped width is now 24, where an
// agent row's second line and the files section's read-only mark are cut
// short; these fixtures test what a row says, so they keep the room to say it.
const railFixtureWidth = 28

// sidebarTestOS builds an OS with a few local windows and the sidebar enabled.
func sidebarTestOS(t *testing.T, w, h int, pos string) *OS {
	t.Helper()
	m := newNarrowOS(t, w, h)
	m.CurrentWorkspace = 1
	m.SessionName = ""
	m.Windows = []*terminal.Window{
		{ID: "aaaaaaaa1111", CustomName: "editor", Width: 40, Height: 20, Workspace: 1, AgentState: "working"},
		{ID: "bbbbbbbb2222", CustomName: "a-very-long-window-name-that-will-not-fit", Width: 40, Height: 20, Workspace: 1, AgentState: "needs_input"},
		{ID: "cccccccc3333", CustomName: "logs", Width: 40, Height: 20, Workspace: 1},
	}
	m.FocusedWindow = 0
	withSidebar(t, true, pos, railFixtureWidth)
	m.Settings = config.Global
	// NewOS ran before withSidebar redirected the state dir, so it read the
	// tree the whole binary shares, where an earlier test may have saved an
	// order. Drop it, so the rows come out in the order set below.
	m.SidebarOrder = nil
	return m
}

// sectionsTestOS is a rail attached to "main" beside two foreign sessions that
// carry real panes, which is what a peek needs to have something to preview.
func sectionsTestOS(t *testing.T, w, h int) (*OS, sessiontree.Tree) {
	t.Helper()
	m := newNarrowOS(t, w, h)
	m.CurrentWorkspace = 1
	m.SessionName = "main"
	m.Windows = []*terminal.Window{
		{ID: "aaaaaaaa1111", CustomName: "nvim", Width: 40, Height: 20, Workspace: 1},
		{ID: "bbbbbbbb2222", CustomName: "refactor", Width: 40, Height: 20, Workspace: 1, AgentState: "done", AgentHarness: "claude-code"},
		{ID: "cccccccc3333", CustomName: "build", Width: 40, Height: 20, Workspace: 2, AgentState: "working", AgentHarness: "claude-code", AgentMessage: "editing files"},
	}
	m.FocusedWindow = 0
	m.DaemonClient = &session.TUIClient{}
	m.IsDaemonSession = true
	withSidebar(t, true, "left", railFixtureWidth)
	m.Settings = config.Global
	m.SidebarOrder = nil

	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Focused: true, Workspace: 1},
			{ID: "bbbbbbbb2222", Title: "refactor", AgentState: "done", Harness: "claude-code", Workspace: 1},
			{ID: "cccccccc3333", Title: "build", AgentState: "working", Harness: "claude-code", Message: "editing files", Workspace: 2},
		}},
		{Name: "api", CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "dddddddd4444", Title: "server", AgentState: "needs_input", Harness: "codex", Message: "awaiting approval", Workspace: 1},
			{ID: "eeeeeeee5555", Title: "worker", Workspace: 3},
		}},
		{Name: "docs"},
	})
	return m, tree
}

// stripOS is a collapsed rail with three sessions, one of them attached and one
// of them holding two panes that want a human.
func stripOS(t *testing.T, w, h int) (*OS, sessiontree.Tree) {
	t.Helper()
	m, _ := sectionsTestOS(t, w, h)
	m.SidebarCollapsed = true
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Focused: true},
			{ID: "bbbbbbbb2222", Title: "build", AgentState: "working"},
		}},
		{Name: "api", Windows: []sessiontree.WindowInput{
			{ID: "dddddddd4444", Title: "server", AgentState: "needs_input"},
			{ID: "eeeeeeee5555", Title: "tests", AgentState: "errored"},
		}},
		{Name: "docs"},
	})
	return m, tree
}

// ContextMenuSelectedActionAt selects the row carrying action and takes it the
// way the keyboard path does, so the carry is set exactly as it is in use.
func (m *OS) ContextMenuSelectedActionAt(t *testing.T, action string) string {
	t.Helper()
	cm := m.ContextMenu
	for i, it := range cm.Items {
		if it.Action == action {
			cm.Selected = i
			return m.ContextMenuSelectedAction()
		}
	}
	t.Fatalf("no row carries %q", action)
	return ""
}

// drawableSizes records the size every pane can draw in, keyed by pane ID.
func drawableSizes(m *OS) map[string][2]int {
	sizes := make(map[string][2]int, len(m.Windows))
	for _, w := range m.Windows {
		sizes[w.ID] = [2]int{w.ContentWidth(), w.ContentHeight()}
	}
	return sizes
}

// callCounts snapshots how many times each pane's PTY has been told a size.
func callCounts(told map[string]*toldSize) map[string]int {
	counts := make(map[string]int, len(told))
	for id, rec := range told {
		counts[id] = rec.calls
	}
	return counts
}

// navIndexOfWindow returns the nav index of a window row, or -1.
func navIndexOfWindow(m *OS, id string) int {
	for i, r := range m.SidebarNav {
		if r.Kind == sidebarRowWindow && r.WindowID == id {
			return i
		}
	}
	return -1
}

// navIndexOfSession returns the nav index of a session row, or -1.
func navIndexOfSession(m *OS, id string) int {
	for i, r := range m.SidebarNav {
		if r.Kind == sidebarRowSession && r.SessionID == id {
			return i
		}
	}
	return -1
}

// isUnderlined reports whether any SGR sequence in s sets the underline
// attribute. The parameters arrive merged with the colours, so the sequence is
// parsed rather than matched as a literal.
func isUnderlined(s string) bool {
	for _, seq := range strings.Split(s, "\x1b[") {
		end := strings.IndexByte(seq, 'm')
		if end < 0 {
			continue
		}
		params := strings.Split(seq[:end], ";")
		for i := 0; i < len(params); i++ {
			// A colour carries its channels as parameters of its own, and one of
			// them may well be a 4.
			if p := params[i]; p == "38" || p == "48" {
				if i+1 < len(params) && params[i+1] == "5" {
					i += 2
					continue
				}
				i += 4
				continue
			}
			if params[i] == "4" {
				return true
			}
		}
	}
	return false
}

// renderSettingsHit renders the settings panel and records its hit geometry the
// way renderOverlays would, so the mouse routing can be exercised in a test.
func (m *OS) renderSettingsHit() {
	m.reconcileOverlayZOrder()
	content, geo, rows := m.renderSettings()
	_ = content
	x, y := m.overlayOrigin("settings", geo)
	m.OverlayHits = []overlayPanelHit{{Kind: "settings", OriginX: x, OriginY: y, Z: m.overlayZ("settings"), Geo: geo, Rows: rows}}
}

func (m *OS) settingsHit() overlayPanelHit { return m.OverlayHits[0] }

func itoa(n int) string {
	return strconv.Itoa(n)
}

// railPlain renders the rail and strips the styling, which is what most of the
// claims below are about: where a row landed, not how it was painted.
func railPlain(t *testing.T, m *OS, tree sessiontree.Tree) []string {
	t.Helper()
	lines, _ := m.sidebarPanelLinesForTree(tree)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = stripANSIForTrace(l)
	}
	return out
}

// lineOf returns the index of the first rendered line containing want, or -1.
func lineOf(lines []string, want string) int {
	for i, l := range lines {
		if strings.Contains(l, want) {
			return i
		}
	}
	return -1
}

func fgParams(c color.Color) string {
	// Rendered rather than formatted: a palette index leaves as SGR 3x or 9x,
	// and only a literal colour leaves as 38;2.
	rendered := lipgloss.NewStyle().Foreground(c).Render("X")
	return strings.TrimSuffix(strings.TrimPrefix(rendered[:strings.Index(rendered, "X")], "\x1b["), "m")
}

// newSwitchOS builds a client with panes spread over two workspaces, each pane
// carrying a recorder for the sizes its PTY is told.
func newSwitchOS(t *testing.T, width, height int, perWorkspace map[int]int) (*OS, map[string]*toldSize) {
	t.Helper()
	m := &OS{
		Settings: config.Global,
		// The layout reads the model's session-settled geometry, seeded from
		// the globals the way NewOS seeds it.
		SharedBorders:        config.Global.SharedBorders,
		PaneGap:              config.Global.PaneGap,
		NumWorkspaces:        9,
		CurrentWorkspace:     1,
		WorkspaceFocus:       make(map[int]int),
		WorkspaceLayouts:     make(map[int][]WindowLayout),
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceMasterRatio: map[int]float64{},
		Width:                width,
		Height:               height,
		AutoTiling:           true,
		UseBSPLayout:         true,
		PendingResizes:       make(map[string][2]int),
	}
	told := make(map[string]*toldSize)
	for ws := 1; ws <= 2; ws++ {
		for i := range perWorkspace[ws] {
			id := fmt.Sprintf("ws%d-pane-%d", ws, i+1)
			win, rec := newAnnounceWindow(t, id, 60, 20)
			win.Workspace = ws
			told[id] = rec
			m.Windows = append(m.Windows, win)
		}
	}
	m.FocusedWindow = 0
	return m, told
}

// screenText reads the guest's visible grid as text.
func screenText(w *terminal.Window) string {
	w.RLockIO()
	defer w.RUnlockIO()
	out := ""
	for y := range w.Terminal.Height() {
		for x := range w.Terminal.Width() {
			cell := w.Terminal.CellAt(x, y)
			if cell == nil || cell.String() == "" {
				out += " "
				continue
			}
			out += cell.String()
		}
		out += "\n"
	}
	return out
}

// zoomPeekOS is four panes in a two by two split, which is the layout that makes
// the anchoring visible: each pane has a neighbour on exactly two sides.
func zoomPeekOS(t *testing.T) (*OS, []*terminal.Window) {
	t.Helper()
	prev := config.Global
	t.Cleanup(func() { config.Global = prev })

	var wins []*terminal.Window
	for i := range 4 {
		w := newTestWindow(t, string(rune('a'+i))+"0000000000000000000000000000000", 40, 20)
		w.Workspace = 1
		wins = append(wins, w)
	}
	// Top left, top right, bottom left, bottom right of a 120x40 region.
	wins[0].X, wins[0].Y, wins[0].Width, wins[0].Height = 0, 0, 60, 20
	wins[1].X, wins[1].Y, wins[1].Width, wins[1].Height = 60, 0, 60, 20
	wins[2].X, wins[2].Y, wins[2].Width, wins[2].Height = 0, 20, 60, 20
	wins[3].X, wins[3].Y, wins[3].Width, wins[3].Height = 60, 20, 60, 20

	m := &OS{
		Settings:         config.Global,
		Windows:          wins,
		FocusedWindow:    0,
		WorkspaceFocus:   map[int]int{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            120,
		Height:           40,
		PendingResizes:   map[string][2]int{},
	}
	return m, wins
}
