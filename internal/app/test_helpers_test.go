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
	"github.com/Gaurav-Gosain/tuios/internal/theme"
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

// ctxMenuOS builds an OS sized to a screen with one visible window filling the
// left half, one minimized window, and a registry, which between them can reach
// every context menu target.
func ctxMenuOS(t *testing.T, w, h int) *OS {
	t.Helper()
	m := newNarrowOS(t, w, h)
	m.Windows = []*terminal.Window{
		{ID: "a", CustomName: "editor", X: 0, Y: 0, Width: max(w/2, 10), Height: max(h-2, 4), Workspace: 1},
		{ID: "b", CustomName: "logs", Width: 20, Height: 10, Workspace: 1, Minimized: true},
	}
	m.CurrentWorkspace, m.FocusedWindow = 1, 0
	return m
}

// withDim turns the dim on for one test, with a theme so there is a ground to
// carry toward.
func withDim(t *testing.T, percent int) {
	t.Helper()
	prevDim := config.Global.DimUnfocused
	prevTheme := theme.CurrentThemeID()
	config.Global.DimUnfocused = percent
	_ = theme.Initialize("catppuccin_mocha")
	t.Cleanup(func() {
		config.Global.DimUnfocused = prevDim
		_ = theme.Initialize(prevTheme)
	})
}

// dockCrowdedOS is a session with named workspaces and minimized panes, which
// is the state the bar has to ration: the strip wants the left region, the
// meters want the right, and the entries are what is left in the middle.
func dockCrowdedOS(t testing.TB, width, workspaces, minimized int) *OS {
	t.Helper()
	m := &OS{
		Settings:         config.Global,
		WorkspaceFocus:   map[int]int{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            width,
		Height:           30,
		FocusedWindow:    -1,
	}
	names := []string{"editor", "server", "logs", "notes", "build", "review"}
	m.WorkspaceNames = map[int]string{}
	for ws := 1; ws <= workspaces; ws++ {
		win := newTestWindow(t, fmt.Sprintf("ws%d", ws), 40, 12)
		win.Workspace = ws
		m.Windows = append(m.Windows, win)
		// Named workspaces are what make the strip wide enough to be worth
		// rationing, which is the state the audit captured.
		m.WorkspaceNames[ws] = names[(ws-1)%len(names)]
	}
	for i := range minimized {
		win := newTestWindow(t, fmt.Sprintf("min%d", i), 40, 12)
		win.Workspace = 1
		win.CustomName = fmt.Sprintf("min%d", i)
		win.Minimized = true
		win.MinimizeOrder = int64(i + 1)
		m.Windows = append(m.Windows, win)
	}
	return m
}

// chipOS is a dock with three occupied workspaces, the middle one named.
func chipOS(t *testing.T) *OS {
	t.Helper()
	m := newNarrowOS(t, 140, 30)
	m.NumWorkspaces = 9
	m.CurrentWorkspace = 1
	m.Windows = []*terminal.Window{
		{ID: "w1", Width: 40, Height: 10, Workspace: 1},
		{ID: "w2", Width: 40, Height: 10, Workspace: 2},
		{ID: "w3", Width: 40, Height: 10, Workspace: 3},
	}
	m.adoptSessionLabels(&session.SessionState{WorkspaceNames: map[int]string{2: "review"}})
	prev := m.Settings.DockWorkspaceTabs
	m.Settings.DockWorkspaceTabs = true
	t.Cleanup(func() { m.Settings.DockWorkspaceTabs = prev })
	return m
}

// pillOS is a dock w columns wide with one window per listed workspace and the
// given names applied.
//
// ASCII glyphs are on throughout: every icon on the bar is then one cell, so a
// rune index into the drawn row is a screen column and a test can compare a
// recorded rectangle against the cells that were actually painted in it.
func pillOS(t *testing.T, w int, names map[int]string, workspaces ...int) *OS {
	t.Helper()
	prevTabs, prevASCII := config.Global.DockWorkspaceTabs, config.Global.UseASCIIOnly
	config.Global.DockWorkspaceTabs, config.Global.UseASCIIOnly = true, true
	t.Cleanup(func() { config.Global.DockWorkspaceTabs, config.Global.UseASCIIOnly = prevTabs, prevASCII })

	m := newNarrowOS(t, w, 30)
	m.NumWorkspaces = 9
	m.CurrentWorkspace = workspaces[0]
	m.Windows = make([]*terminal.Window, 0, len(workspaces))
	for i, ws := range workspaces {
		m.Windows = append(m.Windows, &terminal.Window{
			ID: "pill-" + strconv.Itoa(i), Width: 40, Height: 10, Workspace: ws,
		})
	}
	m.adoptSessionLabels(&session.SessionState{WorkspaceNames: names})
	return m
}

// dockBarRow renders the dock and returns its bar row as plain text, asserting
// the row measures one cell per rune so the caller may index it by column.
func dockBarRow(t *testing.T, m *OS) string {
	t.Helper()
	dock, _ := m.renderDockString()
	rows := strings.Split(stripANSIForTrace(dock), "\n")
	row := rows[len(rows)-1]
	if m.Settings.DockbarPosition == "top" {
		row = rows[0]
	}
	if lipgloss.Width(row) != len([]rune(row)) {
		t.Fatalf("the bar row is %d cells over %d runes, so a column is not a rune here",
			lipgloss.Width(row), len([]rune(row)))
	}
	return row
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
