package app

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
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

// sessionColorOS is a rail attached to "main" beside two sessions that carry
// panes of their own, which is the only shape the colours exist for: more than
// one session on screen at once.
func sessionColorOS(t *testing.T, w, h int) (*OS, sessiontree.Tree) {
	t.Helper()
	m := newNarrowOS(t, w, h)
	m.CurrentWorkspace = 1
	m.SessionName = "main"
	m.Windows = []*terminal.Window{
		{ID: "aaaaaaaa1111", CustomName: "nvim", Width: 40, Height: 20, Workspace: 1},
		{ID: "bbbbbbbb2222", CustomName: "refactor", Width: 40, Height: 20, Workspace: 1, AgentState: "working"},
	}
	m.FocusedWindow = 0
	m.DaemonClient = &session.TUIClient{}
	m.IsDaemonSession = true
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m.Settings = config.Global
	m.SidebarOrder = nil
	return m, sessionColorTree()
}

// withSessionColors pins the config key for one test and puts it back.
func withSessionColors(t *testing.T, on bool) {
	t.Helper()
	prev := config.Global.SessionColors
	config.Global.SessionColors = on
	t.Cleanup(func() { config.Global.SessionColors = prev })
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

// fgSeq is the escape sequence a foreground color renders as, so a row can be
// checked for the color it was actually drawn in rather than for a color name.
func fgSeq(c color.Color) string {
	rendered := lipgloss.NewStyle().Foreground(c).Render("x")
	return rendered[:strings.Index(rendered, "x")]
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

// only returns the single context fired for event, failing when the count is
// anything but one: a hook firing twice for one action is as wrong as not
// firing, and a resize hook that runs per mouse-motion event is the specific
// version of that this guards.
func (r *hookRecorder) only(t *testing.T, m *OS, event hooks.Event) hooks.Context {
	t.Helper()
	m.HookManager.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()

	var got []hooks.Context
	for _, c := range r.fired {
		if c.EventType == event {
			got = append(got, c)
		}
	}
	if len(got) != 1 {
		t.Fatalf("%s fired %d times, want 1 (all fired: %v)", event, len(got), r.events())
	}
	return got[0]
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

// railAgentRow returns the rendered lines of the agents-section row for a
// window, joined, or "" when the rail drew none. A row is one line or two, and
// which one a fact landed on is the layout's business rather than these tests'.
func railAgentRow(m *OS, lines []string, windowID string) string {
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowAgent && h.WindowID == windowID {
			top := h.Y0 - m.GetTopMargin()
			return strings.Join(lines[top:min(h.Y1-m.GetTopMargin(), len(lines))], "\n")
		}
	}
	return ""
}

// notifTestOS is an OS wide enough to draw a dock, with nothing else on it.
func notifTestOS(t testing.TB, width int) *OS {
	t.Helper()
	win := newTestWindow(t, "notif-render-0001", 60, 20)
	win.Workspace = 1
	m := newTestOS(win)
	m.Width, m.Height = width, 40
	m.CurrentWorkspace = 1
	return m
}

// hostAgentOS is a client with one machine besides this one, whose sessions
// are in the states the argument is about.
func hostAgentOS(t *testing.T, status federation.Status, states ...string) *OS {
	t.Helper()
	m := sidebarTestOS(t, 120, 40, "left")
	m.SessionName = "home"

	sessions := make([]FederationSession, 0, len(states))
	for i, st := range states {
		sessions = append(sessions, FederationSession{
			Name:        string(rune('a'+i)) + "-session",
			WindowCount: 1,
			AgentState:  st,
		})
	}
	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 1,
		Snapshot: FederationSnapshot{Hosts: []FederationHost{
			{
				Name:     federation.LocalHostName,
				Status:   string(federation.StatusUp),
				Sessions: []FederationSession{{Name: "home", WindowCount: 1}},
			},
			{Name: "build", Status: string(status), Sessions: sessions},
		}},
	})
	return m
}

// remoteNode is the tree node for one of build's session rows.
func remoteNode(t *testing.T, m *OS, name string) sessiontree.Node {
	t.Helper()
	for _, n := range m.hostGroupNodes() {
		if n.Kind == sessiontree.KindSession && strings.Contains(n.ID, name) {
			return n
		}
	}
	t.Fatalf("no row for %q among the machine's sessions", name)
	return sessiontree.Node{}
}

func searchOS(t *testing.T) *OS {
	t.Helper()
	useTempConfig(t)
	m := &OS{Settings: config.Global, Width: 120, Height: 44, UserConfig: config.DefaultConfig()}
	m.OpenSettings()
	return m
}

// narrowScreens are the sizes the overlays have to survive: a tall narrow
// terminal, a short wide one, the narrowest viewport worth supporting, and a
// normal terminal as a control.
var narrowScreens = []struct {
	name string
	w, h int
}{
	{"tall-narrow", 51, 37},
	{"short-wide", 90, 20},
	{"very-narrow", 30, 24},
	{"very-short", 100, 12},
	{"desktop", 120, 40},
	// The accent picker's wide layout: the first screen that gets it, one just
	// over it, and a wide screen too short to keep everything.
	{"wide-picker-floor", 73, 30},
	{"wide-picker", 74, 20},
	{"wide-picker-short", 100, 14},
}

// assertFitsScreen fails if any line of out is wider than w, the block is
// taller than h, or any line pads itself with a control character. An overlay
// wider than the screen has its right-hand side drawn off the edge, where it
// cannot be read or scrolled to.
//
// The control-character half guards a different way of getting the same answer
// wrong. Every row here is padded to the panel width with literal spaces, and
// the fitting arithmetic in overlay_fit.go measures those rows to decide what
// else fits. A tab would break that: it is one byte that lipgloss measures as
// one cell but a terminal advances to its next tab stop, so the panel's own
// idea of where a row ends would stop matching the screen's. Carriage returns
// and the rest are the same failure with a different glyph.
func assertFitsScreen(t *testing.T, name, out string, w, h int) {
	t.Helper()
	if out == "" {
		return
	}
	lines := strings.Split(out, "\n")
	for i, ln := range lines {
		if j := strings.IndexAny(ln, "\t\r\v\f"); j >= 0 {
			t.Errorf("%s: line %d pads with a control character (%q at byte %d): %q",
				name, i, ln[j], j, ln)
			return
		}
		if lw := lipgloss.Width(ln); lw > w {
			t.Errorf("%s: line %d is %d cells wide, screen is %d: %q", name, i, lw, w, ln)
			return
		}
	}
	if len(lines) > h {
		t.Errorf("%s: %d lines tall, screen is %d", name, len(lines), h)
	}
}

func sessionColorTree() sessiontree.Tree {
	return sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Focused: true, Workspace: 1},
			{ID: "bbbbbbbb2222", Title: "refactor", AgentState: "working", Workspace: 1},
		}},
		{Name: "api", CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "dddddddd4444", Title: "server", AgentState: "working", Workspace: 1},
		}},
		{Name: "docs", CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "ffffffff6666", Title: "site", AgentState: "idle", Workspace: 1},
		}},
	})
}

// events lists what fired, for failure messages.
func (r *hookRecorder) events() []hooks.Event {
	out := make([]hooks.Event, 0, len(r.fired))
	for _, c := range r.fired {
		out = append(out, c.EventType)
	}
	return out
}
