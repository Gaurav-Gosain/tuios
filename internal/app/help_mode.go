package app

import (
	"fmt"
	"image"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app/helptext"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Help mode is the hover help the leader then h opens. It is not the help
// overlay: nothing is listed up front, and it reads only the keys it binds.
// The pointer, or Shift+Tab, moves between the components on screen;
// Shift+Up and Shift+Down move into and out of a component that has parts; h
// shows the whole keybinding set of the component selected; esc leaves. The
// rest of the keyboard is the app's, so a chord the pane lists runs while the
// pane explains it, and the pane follows the layout that chord leaves. The
// pane that explains the component is drawn above or below it, never over it
// where the screen has room, and its border is the error ink, so it reads as
// an inspection surface rather than as one more dock or rail.

// helpKey is one binding as the pane shows it: the whole press, chord
// included, and what it does.
type helpKey struct {
	press string
	desc  string
}

// helpComponent is one part of the interface help mode can explain: a rail, a
// dock and its cells, a pane and its title bar. parent is empty for a top-level
// component; rect is in screen cells.
type helpComponent struct {
	id      string
	parent  string
	title   string
	purpose string
	// moves are the keys that act on the whole component rather than on one
	// of its rows, drawn in their own section ahead of the rest.
	moves []helpKey
	// values are the labels a readout can show, drawn in their own section
	// after the purpose. The mode pill's states are not keys, so they are
	// listed here rather than among the bindings.
	values []helpKey
	// rows are the current members of a group, drawn as a list under their own
	// heading: the pills the centre drew, and the entries it had no room for.
	rows    []helpKey
	keys    []helpKey
	allKeys []helpKey
	cli     []string
	rect    image.Rectangle
}

// EnterHelpMode turns help mode on and selects the first component on screen.
// The leader chord that reaches it, or Shift+F1, has already closed every menu.
func (m *OS) EnterHelpMode() {
	m.ShowHelp = false
	m.CloseContextMenu()
	m.CloseHints()
	m.ClosePaneLabels()
	m.HelpMode = true
	m.helpShowKeys = false
	m.helpScroll = 0
	m.helpTarget = ""
	m.helpPaneRect = image.Rectangle{}
	m.helpComps = nil
	comps := m.helpComponents()
	if len(comps) > 0 {
		m.helpTarget = comps[0].id
	}
	m.helpInvalidate()
}

// ExitHelpMode leaves help mode. A leader chord after it opens the ordinary
// leader menu again, and esc or Shift+F1 goes back to the pane.
func (m *OS) ExitHelpMode() {
	m.HelpMode = false
	m.helpShowKeys = false
	m.helpScroll = 0
	m.helpTarget = ""
	m.helpPaneRect = image.Rectangle{}
	m.helpComps = nil
	m.helpInvalidate()
}

// helpInvalidate makes the next frame compose rather than serve the last one.
func (m *OS) helpInvalidate() {
	m.cachedViewContent = ""
	m.renderSkipped = false
}

// helpComponents is the component list for the frame already drawn, or a fresh
// one when none has been built. Mouse motion reads it without rebuilding the
// content of every row, and the next drawn frame replaces it with the geometry
// that frame actually drew.
func (m *OS) helpComponents() []helpComponent {
	if m.helpComps == nil {
		m.helpComps = m.buildHelpComponents()
	}
	return m.helpComps
}

// HelpNext moves to the next top-level component, or the one delta places
// back. Shift+Tab calls it.
func (m *OS) HelpNext(delta int) {
	comps := m.helpComponents()
	tops := helpTopLevel(comps)
	if len(tops) == 0 {
		return
	}
	idx := -1
	top := helpTopAncestor(comps, m.helpTarget)
	for i, c := range tops {
		if c.id == top {
			idx = i
			break
		}
	}
	if idx < 0 {
		if delta < 0 {
			idx = len(tops) - 1
		} else {
			idx = 0
		}
	} else {
		idx = (idx + delta) % len(tops)
		if idx < 0 {
			idx += len(tops)
		}
	}
	m.helpTarget = tops[idx].id
	m.helpScroll = 0
	m.helpInvalidate()
}

// HelpDeeper moves Shift+Down: into the selected component's first part, or to
// its next sibling once inside one.
func (m *OS) HelpDeeper() {
	comps := m.helpComponents()
	cur := helpIndex(comps, m.helpTarget)
	if cur < 0 {
		m.HelpNext(1)
		return
	}
	if kids := helpChildren(comps, comps[cur].id); len(kids) > 0 {
		m.helpTarget = kids[0].id
		m.helpScroll = 0
		m.helpInvalidate()
		return
	}
	if comps[cur].parent == "" {
		return
	}
	if next, ok := helpSibling(comps, comps[cur], 1); ok {
		m.helpTarget = next.id
		m.helpScroll = 0
		m.helpInvalidate()
	}
}

// HelpShallower moves Shift+Up: to the previous sibling inside a component, or
// back out to the component itself from its first part.
func (m *OS) HelpShallower() {
	comps := m.helpComponents()
	cur := helpIndex(comps, m.helpTarget)
	if cur < 0 {
		m.HelpNext(-1)
		return
	}
	if comps[cur].parent == "" {
		return
	}
	if prev, ok := helpSibling(comps, comps[cur], -1); ok {
		m.helpTarget = prev.id
		m.helpScroll = 0
		m.helpInvalidate()
		return
	}
	m.helpTarget = comps[cur].parent
	m.helpScroll = 0
	m.helpInvalidate()
}

// HelpScroll moves the explanation by delta lines. The pane's title and the
// navigation footer do not move: only the part of the box that can overflow
// scrolls. Up, Down and the wheel call it.
func (m *OS) HelpScroll(delta int) {
	if !m.HelpMode || delta == 0 {
		return
	}
	m.helpScroll = max(m.helpScroll+delta, 0)
	m.helpInvalidate()
}

// HelpToggleKeys shows or hides the selected component's whole keybinding set.
// h calls it.
func (m *OS) HelpToggleKeys() {
	if !m.HelpMode {
		return
	}
	m.helpShowKeys = !m.helpShowKeys
	m.helpScroll = 0
	m.helpInvalidate()
}

// HelpHoverAt selects the smallest component under the pointer. Motion over
// the explaining pane itself is ignored, so reading the pane does not select
// whatever it covers.
func (m *OS) HelpHoverAt(x, y int) {
	if !m.HelpMode {
		return
	}
	sx, sy := m.ScreenPoint(x, y)
	if !m.helpPaneRect.Empty() && image.Pt(sx, sy).In(m.helpPaneRect) {
		return
	}
	comps := m.helpComponents()
	best, bestArea := "", 0
	for _, c := range comps {
		if c.rect.Empty() || !image.Pt(sx, sy).In(c.rect) {
			continue
		}
		area := c.rect.Dx() * c.rect.Dy()
		if best == "" || area < bestArea {
			best, bestArea = c.id, area
		}
	}
	if best != "" && best != m.helpTarget {
		m.helpTarget = best
		m.helpScroll = 0
		m.helpInvalidate()
	}
}

func helpTopLevel(comps []helpComponent) []helpComponent {
	out := make([]helpComponent, 0, len(comps))
	for _, c := range comps {
		if c.parent == "" {
			out = append(out, c)
		}
	}
	return out
}

func helpIndex(comps []helpComponent, id string) int {
	for i, c := range comps {
		if c.id == id {
			return i
		}
	}
	return -1
}

func helpComponentByID(comps []helpComponent, id string) *helpComponent {
	for i := range comps {
		if comps[i].id == id {
			return &comps[i]
		}
	}
	return nil
}

func helpTopAncestor(comps []helpComponent, id string) string {
	cur := helpComponentByID(comps, id)
	for cur != nil && cur.parent != "" {
		cur = helpComponentByID(comps, cur.parent)
	}
	if cur == nil {
		return ""
	}
	return cur.id
}

func helpChildren(comps []helpComponent, id string) []helpComponent {
	out := make([]helpComponent, 0, 4)
	for _, c := range comps {
		if c.parent == id {
			out = append(out, c)
		}
	}
	return out
}

func helpSibling(comps []helpComponent, cur helpComponent, delta int) (helpComponent, bool) {
	sibs := helpChildren(comps, cur.parent)
	for i, c := range sibs {
		if c.id != cur.id {
			continue
		}
		j := i + delta
		if j < 0 || j >= len(sibs) {
			return helpComponent{}, false
		}
		return sibs[j], true
	}
	return helpComponent{}, false
}

// helpKeys is the first press of each named action, with the description the
// rest of the interface shows for it.
func (m *OS) helpKeys(actions ...string) []helpKey {
	if m.KeybindRegistry == nil {
		return nil
	}
	presses := config.PressesByAction(m.KeybindRegistry)
	out := make([]helpKey, 0, len(actions))
	for _, action := range actions {
		p := presses[action]
		if len(p) == 0 {
			continue
		}
		desc := config.ActionDescriptions[action]
		if desc == "" {
			desc = action
		}
		out = append(out, helpKey{press: helpPressLabel(p[0]), desc: desc})
	}
	return out
}

// helpRailWideKeys are the moves that act on a rail as a whole rather than on
// one row: putting the rail away and putting the keyboard into it. They are the
// rail's own keys, live with help mode up as well, so every rail component
// gives them their own section rather than folding them into its focused keys.
func (m *OS) helpRailWideKeys(other bool) []helpKey {
	action := "prefix_toggle_sidebar"
	toggleDesc := "Toggle this rail"
	if other {
		action = "prefix_toggle_other_rail"
		toggleDesc = "Toggle the opposite rail"
	}
	keys := m.helpKeys(action)
	for i := range keys {
		keys[i].desc = toggleDesc
	}
	for _, k := range m.helpKeys("prefix_explore") {
		k.desc = "Focus this rail, or leave it for the panes"
		keys = append(keys, k)
	}
	return keys
}

// helpRailToggleLabel is the rail toggle as the rail's own help spells it, for
// a sentence that has to name it.
func helpRailToggleLabel(wide []helpKey) string {
	if len(wide) == 0 || wide[0].press == "" {
		return "the rail toggle"
	}
	return wide[0].press
}

// helpScopedKeys is helpKeys for a section that is read in its own scope
// rather than through the prefix map, such as the rail's keys.
func (m *OS) helpScopedKeys(scope string, actions ...string) []helpKey {
	if m.KeybindRegistry == nil {
		return nil
	}
	bindings := m.KeybindRegistry.Bindings()
	out := make([]helpKey, 0, len(actions))
	for _, action := range actions {
		for _, b := range bindings {
			if b.Scope != scope || b.Action != action || b.Unbound || b.Shadowed || b.Press == "" {
				continue
			}
			desc := b.Desc
			if desc == "" {
				desc = action
			}
			out = append(out, helpKey{press: helpPressLabel(b.Press), desc: desc})
			break
		}
	}
	return out
}

// helpAllScopedKeys is every live binding in the named scopes, which is what h
// shows for a component whose keys live in their own section.
func (m *OS) helpAllScopedKeys(scopes ...string) []helpKey {
	if m.KeybindRegistry == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []helpKey
	for _, b := range m.KeybindRegistry.Bindings() {
		if b.Unbound || b.Shadowed || b.Press == "" || !slices.Contains(scopes, b.Scope) {
			continue
		}
		id := b.Press + "\x00" + b.Action
		if seen[id] {
			continue
		}
		seen[id] = true
		desc := b.Desc
		if desc == "" {
			desc = b.Action
		}
		out = append(out, helpKey{press: helpPressLabel(b.Press), desc: desc})
	}
	return out
}

// helpPressLabel spells a press for a person: every chord part capitalised the
// way the which-key menu spells it, with the presses left apart.
func helpPressLabel(press string) string {
	parts := strings.Fields(press)
	for i := range parts {
		parts[i] = config.KeyLabel(parts[i])
	}
	return strings.Join(parts, " ")
}

func helpCap(keys []helpKey, n int) []helpKey {
	if len(keys) <= n {
		return keys
	}
	return keys[:n]
}

// buildHelpComponents names every component the frame can explain. The list is
// built from live geometry for the rail and the panes, and from the rectangles
// the dock recorded on its last pass for the dock's cells: a cell's columns are
// a fact about the painted frame, and recomputing them from settings would name
// a rectangle the user is not looking at.
func (m *OS) buildHelpComponents() []helpComponent {
	comps := make([]helpComponent, 0, 24)
	comps = append(comps, m.helpRailComponents()...)
	comps = append(comps, m.helpPaneComponents()...)
	comps = append(comps, m.helpDockComponents()...)
	comps = append(comps, m.helpDesktopComponent())
	return comps
}

// helpRailComponents adds the legacy rail and, when the opposite edge is
// configured, its rail. A hidden rail still gets a component: it has no
// rectangle to hover, but Shift+Tab can reach it, and its toggle chord works
// from inside help mode, which is how the rail comes back with the pane open.
func (m *OS) helpRailComponents() []helpComponent {
	if m.NoRail {
		return nil
	}
	var out []helpComponent
	primaryEdge := m.legacySidebarEdge()
	primaryWidth := m.GetSidebarWidth()
	primaryShown := primaryWidth > 0 && m.Settings.SidebarEnabled && m.Settings.SidebarPosition != "hidden"
	out = append(out, m.helpRailComponent(primaryEdge, primaryWidth, primaryShown, false)...)

	otherEdge := sidebarLeft
	if primaryEdge == sidebarLeft {
		otherEdge = sidebarRight
	}
	if m.helpOtherRailConfigured() {
		width := m.secondarySidebarWidth()
		out = append(out, m.helpRailComponent(otherEdge, width, width > 0, true)...)
	}
	return out
}

// helpOtherRailConfigured reports whether the edge opposite the legacy rail
// has a table of its own. Only a table that sets enabled can be toggled, so a
// table alone never earns a component here.
func (m *OS) helpOtherRailConfigured() bool {
	if m.UserConfig == nil {
		return false
	}
	cfg := m.UserConfig.Appearance.Sidebar.Left
	if m.legacySidebarEdge() == sidebarLeft {
		cfg = m.UserConfig.Appearance.Sidebar.Right
	}
	return cfg != nil && cfg.Enabled != nil
}

// helpRailComponent builds one rail and the parts of it the last frame drew.
func (m *OS) helpRailComponent(edge sidebarEdge, width int, shown, other bool) []helpComponent {
	id := "rail:left"
	position := "left"
	if edge == sidebarRight {
		id = "rail:right"
		position = "right"
	}
	title := "Session rail"
	if other {
		title = "Opposite rail"
	}
	wide := m.helpRailWideKeys(other)
	if !shown {
		title += " (hidden)"
	}
	rect := image.Rectangle{}
	top := m.viewReserve().Top
	height := m.ViewUsableHeight()
	if shown && width > 0 && height > 0 {
		x := sidebarEdgeX(edge, width, m.GetRenderWidth())
		rect = image.Rect(x, top, x+width, top+height)
	}

	railText := dockText(m.helpLanguage(), helptext.IDRail, "Session rail")
	purpose := railText.Purpose
	if !shown {
		// Help mode reads only its navigation keys, so the toggle it lists
		// works from here; the pane stays up and redraws around the rail. The
		// sentence and the page behind it come from the same language, so a
		// translation reads as one paragraph.
		purpose = hiddenRailText(m.helpLanguage(), helpRailToggleLabel(wide)) + " " + purpose
	}
	keys := m.helpScopedKeys(config.ScopeSidebar, "activate", "cursor_down", "cursor_up", "collapse", "expand", "section", "new_session", "new_window")
	keys = helpCap(keys, 7)

	all := append([]helpKey{}, wide...)
	all = append(all, m.helpAllScopedKeys(
		config.ScopeSidebar, config.ScopeSidebarFiles, config.ScopeSidebarAgents)...)
	all = append(all, m.helpKeys(
		"prefix_toggle_spotlight",
		"prefix_session_switcher", "prefix_inbox", "prefix_mail")...)

	parent := helpComponent{
		id:      id,
		title:   title,
		purpose: purpose,
		moves:   wide,
		keys:    keys,
		allKeys: all,
		cli: []string{
			"tuios set-config appearance.sidebar.enabled true",
			"tuios set-config appearance.sidebar.position " + position,
			"tuios set-config appearance.sidebar.width 30",
			"tuios set-config appearance.sidebar.sections sessions:25,terminals,files:25,agents:34",
		},
		rect: rect,
	}
	out := []helpComponent{parent}

	groups := map[string]image.Rectangle{}
	for _, hit := range m.SidebarHits {
		if width <= 0 || rect.Empty() {
			break
		}
		if hit.X1 <= rect.Min.X || hit.X0 >= rect.Max.X || hit.Y1 <= rect.Min.Y || hit.Y0 >= rect.Max.Y {
			continue
		}
		group := helpRailGroup(hit.Kind)
		if group == "" {
			continue
		}
		r := image.Rect(hit.X0, hit.Y0, hit.X1, hit.Y1)
		if prev, ok := groups[group]; ok {
			groups[group] = prev.Union(r)
		} else {
			groups[group] = r
		}
	}
	for _, group := range []string{"sessions", "panes", "agents", "files", "controls"} {
		r, ok := groups[group]
		if !ok {
			continue
		}
		out = append(out, m.helpRailChild(group, id, r, other))
	}
	return out
}

// helpRailGroup maps a rail row kind to the section help mode explains. Empty
// means the row has no section worth naming on its own.
func helpRailGroup(kind sidebarRowKind) string {
	switch kind {
	case sidebarRowSession, sidebarRowNewSession:
		return "sessions"
	case sidebarRowWindow, sidebarRowNewWindow, sidebarRowRailPaneStart:
		return "panes"
	case sidebarRowAgent, sidebarRowAgentFilter, sidebarRowAgentSort, sidebarRowAgentMail,
		sidebarRowAgentFold, sidebarRowSubagentToggle:
		return "agents"
	case sidebarRowFileCd, sidebarRowFileUp, sidebarRowFileEntry, sidebarRowRepo:
		return "files"
	case sidebarRowCollapse, sidebarRowFiles, sidebarRowHostSession, sidebarRowHostNew,
		sidebarRowHostSignIn, sidebarRowDivider, sidebarRowHost, sidebarRowGlobalNew:
		return "controls"
	}
	return ""
}

// helpRailChild explains one section of a rail.
func (m *OS) helpRailChild(group, parent string, rect image.Rectangle, other bool) helpComponent {
	c := helpComponent{id: parent + "/" + group, parent: parent, rect: rect}
	switch group {
	case "sessions":
		text := dockText(m.helpLanguage(), helptext.RailSection("sessions"), "Sessions")
		c.title = text.Title
		c.purpose = text.Purpose
		c.keys = helpCap(m.helpScopedKeys(config.ScopeSidebar,
			"activate", "new_session", "rename", "kill", "cursor_down", "cursor_up"), 4)
		c.cli = []string{"tuios ls", "tuios new work", "tuios rename-session work", "tuios kill-session work"}
	case "panes":
		text := dockText(m.helpLanguage(), helptext.RailSection("panes"), "Panes")
		c.title = text.Title
		c.purpose = text.Purpose
		c.keys = helpCap(m.helpScopedKeys(config.ScopeSidebar,
			"activate", "new_window", "cursor_down", "cursor_up", "reorder_down", "reorder_up"), 4)
		c.keys = append(c.keys, m.helpKeys("prefix_next_window", "prefix_prev_window")...)
		c.cli = []string{"tuios new-window", "tuios list-windows", "tuios select-window <name>"}
	case "agents":
		text := dockText(m.helpLanguage(), helptext.RailSection("agents"), "Agents")
		c.title = text.Title
		c.purpose = text.Purpose
		c.keys = append(m.helpScopedKeys(config.ScopeSidebarAgents,
			"activate", "cursor_down", "cursor_up"), m.helpScopedKeys(config.ScopeSidebar,
			"agents_filter", "agents_sort", "mail", "cursor_down", "cursor_up")...)
		c.keys = helpCap(c.keys, 6)
		c.keys = append(c.keys, m.helpKeys("prefix_inbox", "prefix_next_attention")...)
		c.cli = []string{"tuios list-agents", "tuios respond <action>", "tuios send-agent-message <text>"}
	case "files":
		text := dockText(m.helpLanguage(), helptext.RailSection("files"), "Files")
		c.title = text.Title
		c.purpose = text.Purpose
		c.keys = helpCap(m.helpScopedKeys(config.ScopeSidebarFiles,
			"file_open", "file_edit", "file_search", "file_delete", "file_paste", "file_copy", "file_cut"), 5)
		c.cli = []string{"tuios set-config appearance.sidebar.sections sessions:25,terminals,files:25,agents:34", "tuios config edit"}
	case "controls":
		text := dockText(m.helpLanguage(), helptext.RailSection("controls"), "Rail controls")
		c.title = text.Title
		c.purpose = text.Purpose
		c.keys = m.helpScopedKeys(config.ScopeSidebar,
			"collapse", "expand", "narrow", "widen", "file_search")
		c.cli = []string{"tuios set-config appearance.sidebar.width 30", "tuios set-config appearance.sidebar.position left"}
	}
	// A rail section is read from the rail's pane, and both of these moves act
	// on the whole rail, so they get their own section rather than competing
	// with the section's own keys for the cap.
	wide := m.helpRailWideKeys(other)
	c.moves = wide
	switch group {
	case "agents":
		c.allKeys = m.helpAllScopedKeys(config.ScopeSidebarAgents)
	case "files":
		c.allKeys = m.helpAllScopedKeys(config.ScopeSidebarFiles)
	default:
		c.allKeys = m.helpAllScopedKeys(config.ScopeSidebar)
	}
	c.allKeys = append(append([]helpKey{}, wide...), c.allKeys...)
	if group == "panes" {
		c.allKeys = append(c.allKeys, m.helpKeys(
			"prefix_next_window", "prefix_prev_window", "prefix_close_window",
			"prefix_fullscreen", "prefix_window")...)
	}
	return c
}

// helpDockComponents adds the dock and the cells the last frame recorded.
func (m *OS) helpDockComponents() []helpComponent {
	dock := m.helpDockComponent()
	out := []helpComponent{dock}
	if dock.rect.Empty() {
		return out
	}

	var workspaces, items, sessions []image.Rectangle
	for _, hit := range m.dockWorkspaceHits {
		workspaces = append(workspaces, image.Rect(hit.X0, hit.Y, hit.X1, hit.Y+1))
	}
	for _, hit := range m.dockWorkspaceArrowHits {
		workspaces = append(workspaces, image.Rect(hit.X0, hit.Y, hit.X1, hit.Y+1))
	}
	for _, hit := range m.dockItemHits {
		items = append(items, image.Rect(hit.X0, hit.Y, hit.X1, hit.Y+1))
	}
	for _, hit := range m.dockSessionHits {
		sessions = append(sessions, image.Rect(hit.X0, hit.Y, hit.X1, hit.Y+1))
	}
	if m.notifHit.Active {
		out = append(out, m.helpDockCellComponent("messages", dock.id,
			image.Rect(m.notifHit.X0, m.notifHit.Y, m.notifHit.X1, m.notifHit.Y+1)))
	}
	if r := helpRectUnion(workspaces); !r.Empty() {
		out = append(out, m.helpDockCellComponent("workspaces", dock.id, r))
	}
	if r := helpRectUnion(items); !r.Empty() {
		out = append(out, m.helpDockCellComponent("windows", dock.id, r))
	}
	if r := helpRectUnion(sessions); !r.Empty() {
		out = append(out, m.helpDockCellComponent("session-controls", dock.id, r))
	}
	if m.dockOverflowHit.Active {
		out = append(out, m.helpDockCellComponent("overflow", dock.id,
			image.Rect(m.dockOverflowHit.X0, m.dockOverflowHit.Y, m.dockOverflowHit.X1, m.dockOverflowHit.Y+1)))
	}

	// Every named cell the dock drew becomes one part, in draw order, so the
	// count in the header is the number of parts a hover or Shift+Up reaches.
	// The grouped parts above were recorded from their own unions; a cell that
	// belongs to one of them (the workspace strip, a minimized entry) is
	// skipped here rather than explaining the same thing twice.
	seen := map[string]bool{
		"workspaces":       true,
		"windows":          true,
		"messages":         true,
		"session-controls": true,
		"overflow":         true,
	}
	for _, hit := range m.dockHelpHits {
		if seen[hit.Name] || hit.X1 <= hit.X0 {
			continue
		}
		seen[hit.Name] = true
		out = append(out, m.helpDockCellComponent(hit.Name, dock.id,
			image.Rect(hit.X0, hit.Y, hit.X1, hit.Y+1)))
	}
	return out
}

// helpDockComponent names the bar itself. A hidden dock keeps a component with
// no rectangle, so its setting is still one Tab can reach.
func (m *OS) helpDockComponent() helpComponent {
	hidden := m.Settings.DockbarPosition == "hidden"
	height := m.Settings.DockHeight()
	y0 := 0
	if !hidden && m.Settings.DockbarPosition != "top" {
		y0 = m.GetRenderHeight() - height
	}
	rect := image.Rectangle{}
	if !hidden && height > 0 && y0 >= 0 {
		rect = image.Rect(0, y0, m.GetRenderWidth(), y0+height)
	}
	position := m.Settings.DockbarPosition
	if position == "" {
		position = "top"
	}
	purpose := dockText(m.helpLanguage(), helptext.IDDock, "Dock").Purpose
	if hidden {
		purpose = "The dock is hidden, so it has no rectangle to hover. Show it with the CLI line below and it becomes a component here again. " + purpose
	}
	keys := append(m.helpKeys(
		"prefix_workspace_switcher", "prefix_workspace", "prefix_minimize",
		"prefix_session_switcher", "prefix_detach", "prefix_close_session"),
		m.helpScopedKeys(config.ScopeSidebar, "new_session")...)
	cli := []string{
		"tuios set-config appearance.dockbar_position top",
		"tuios set-config appearance.dockbar_position bottom",
		"tuios set-config appearance.show_clock false",
		"tuios refresh-dock",
	}
	if !hidden {
		cli[0] = "tuios set-config appearance.dockbar_position " + position
	}
	return helpComponent{
		id:      "dock",
		title:   dockText(m.helpLanguage(), helptext.IDDock, "Dock").Title,
		purpose: purpose,
		keys:    helpCap(keys, 7),
		allKeys: m.helpKeys(
			"prefix_workspace_switcher", "prefix_workspace", "prefix_minimize",
			"minimize_prefix_restore_all", "prefix_session_switcher", "prefix_workspace",
			"prefix_detach", "prefix_close_session", "prefix_quit", "prefix_inbox",
			"prefix_mail", "prefix_jump_notif", "prefix_last_message", "prefix_next_attention",
			"prefix_toggle_tiling", "prefix_next_window", "prefix_prev_window"),
		cli:  cli,
		rect: rect,
	}
}

// hiddenRailText names why a hidden rail has no rectangle, in the language
// the rest of the page is in.
func hiddenRailText(lang helptext.Language, key string) string {
	if e, ok := helptext.Lookup(lang, helptext.IDRail); ok && e.Hidden != nil {
		return e.Hidden(key)
	}
	return "This rail is hidden, so it has no rectangle to hover. Press " + key +
		" to show it, and the pane follows the layout as it changes."
}

// paneText is a pane page's title or paragraph in a language, with the pane's
// name written where the page names it.
func paneText(m *OS, id, fallback, name string) helptext.Entry {
	e := dockText(m.helpLanguage(), id, fallback)
	e.Title = strings.ReplaceAll(e.Title, "{name}", name)
	return e
}

// helpLanguage is the table this client's help reads in: [ui] language, or
// the locale when it is unset. The lookup is a string compare per frame, and
// the warning a load owes is owed once, which Load arranges.
func (m *OS) helpLanguage() helptext.Language {
	return helptext.Load(m.Settings.Language, nil)
}

// helpPartLabel is what the count after a page's name reads: "parts" in a
// language whose table says nothing, and that language's own word when it
// does. A dock cell's parts are the cells the bar drew, so a dock cell takes
// the dock's inflection.
func helpPartLabel(lang helptext.Language, n int, id string) string {
	owner := id
	if strings.HasPrefix(id, helptext.IDDock+"/") {
		owner = helptext.IDDock
	}
	if e, ok := lang.Entries[owner]; ok && e.Plural != nil {
		return e.Plural(n)
	}
	if n == 1 {
		return "part"
	}
	return "parts"
}

// dockText reads a dock page's text in a language, and falls back to the
// title the caller would have given it when neither the language nor English
// has an entry yet: a cell added before its text exists still opens a page.
func dockText(lang helptext.Language, id, fallback string) helptext.Entry {
	return helptext.Text(lang, id, fallback)
}

// helpDockCellComponent explains one dock cell. Custom and plugin cells carry
// the command or the plugin they came from; the built-in cells name their own
// setting.
func (m *OS) helpDockCellComponent(name, parent string, rect image.Rectangle) helpComponent {
	c := helpComponent{id: "dock/" + name, parent: parent, rect: rect}
	switch {
	case strings.HasPrefix(name, config.DockCustomPrefix):
		key := strings.TrimPrefix(name, config.DockCustomPrefix)
		c.title = "Dock cell: " + key
		c.purpose = "A custom dock cell. Its command is run on the refresh rule set for it, and clicking it runs the on-click command."
		if m.UserConfig != nil {
			if spec, ok := m.UserConfig.Dock.Custom[key]; ok && spec.Command != "" {
				c.purpose += " It runs: " + spec.Command + "."
			}
		}
		c.cli = []string{
			"tuios config edit  # [dock.custom." + key + "]",
			"tuios refresh-dock " + name,
			"tuios list-dock-components",
		}
	case strings.HasPrefix(name, config.DockPluginPrefix):
		c.title = "Dock cell: plugin"
		c.purpose = "A cell supplied by a plugin. The plugin owns what it draws and what a click does; its manifest and cells are described in the plugin's own documentation."
		c.cli = []string{"tuios plugins list", "tuios config edit  # [dock] lists", "tuios list-dock-components"}
	case name == "workspaces":
		text := dockText(m.helpLanguage(), helptext.DockCell("workspaces"), "Workspaces")
		c.title = text.Title
		c.purpose = text.Purpose
		c.keys = helpCap(m.helpKeys("prefix_workspace", "prefix_workspace_switcher", "next_workspace", "prev_workspace"), 6)
		c.cli = []string{"tuios select-workspace 2", "tuios set-workspace-name 2 work", "tuios set-config appearance.dock_workspace_tabs true"}
	case name == "windows":
		text := dockText(m.helpLanguage(), helptext.DockCell("windows"), "Minimized panes")
		c.title = text.Title
		c.purpose = text.Purpose
		if len(m.dockCenterLabels) == 0 {
			c.rows = append(c.rows, helpKey{press: "none", desc: "No pane is minimized, so the centre draws no pills."})
		}
		for i, label := range m.dockCenterLabels {
			c.rows = append(c.rows, helpKey{press: fmt.Sprintf("%d", i+1), desc: label})
		}
		if n := m.dockOverflowHit.Overflowed; n > 0 {
			c.rows = append(c.rows, helpKey{press: "...", desc: fmt.Sprintf("%d more did not fit; the marker opens them", n)})
		}
		if len(m.dockCenterParts) > 0 {
			c.rows = append(c.rows, helpKey{press: "centre", desc: strings.Join(m.dockCenterParts, ", ")})
		}
		c.keys = helpCap(append(m.helpKeys("prefix_minimize", "minimize_prefix_restore_all"),
			m.helpScopedKeys(config.ScopeSidebar, "activate")...), 6)
		c.cli = []string{"tuios list-windows", "tuios restore-window <name>", "tuios set-config appearance.dock_compact true"}
	case name == "session-controls":
		text := dockText(m.helpLanguage(), helptext.DockCell("session-controls"), "Session controls")
		c.title = text.Title
		c.purpose = text.Purpose
		c.keys = helpCap(m.helpKeys("prefix_session_switcher", "prefix_detach", "prefix_close_session", "prefix_quit"), 6)
		c.cli = []string{"tuios ls", "tuios detach-client", "tuios kill-session <name>", "tuios new"}
	case name == "messages":
		text := dockText(m.helpLanguage(), helptext.DockCell("messages"), "Messages")
		c.title = text.Title
		c.purpose = text.Purpose
		c.keys = helpCap(m.helpKeys("prefix_jump_notif", "prefix_last_message", "prefix_inbox", "prefix_mail"), 6)
		c.cli = []string{"tuios notify test", "tuios set-config notifications.duration 4", "tuios set-config notifications.error_sticky true"}
	case name == "overflow":
		text := dockText(m.helpLanguage(), helptext.DockCell("overflow"), "Minimized overflow")
		c.title = text.Title
		c.purpose = text.Purpose
		c.cli = []string{"tuios list-windows", "tuios set-config appearance.dock_compact true"}
	case name == "clock":
		text := dockText(m.helpLanguage(), helptext.DockCell("clock"), "Clock")
		c.title = text.Title
		c.purpose = text.Purpose
		c.cli = []string{"tuios set-config appearance.show_clock true", "tuios set-config appearance.clock_format 15:04"}
	case name == "cpu":
		text := dockText(m.helpLanguage(), helptext.DockCell("cpu"), "CPU")
		c.title = text.Title
		c.purpose = text.Purpose
		c.cli = []string{"tuios set-config appearance.show_cpu true"}
	case name == "ram":
		text := dockText(m.helpLanguage(), helptext.DockCell("ram"), "RAM")
		c.title = text.Title
		c.purpose = text.Purpose
		c.cli = []string{"tuios set-config appearance.show_ram true"}
	case name == "transfers":
		text := dockText(m.helpLanguage(), helptext.DockCell("transfers"), "Transfers")
		c.title = text.Title
		c.purpose = text.Purpose
		c.keys = helpCap(m.helpKeys("prefix_transfers", "paste_buffer", "choose_buffer"), 6)
		c.cli = []string{"tuios list-buffers", "tuios set-config appearance.dock_compact true"}
	case name == "session":
		text := dockText(m.helpLanguage(), helptext.DockCell("session"), "Session name")
		c.title = text.Title
		c.purpose = text.Purpose
		c.keys = helpCap(m.helpKeys("prefix_session_switcher", "prefix_close_session"), 6)
		c.cli = []string{"tuios rename-session <name>", "tuios set-session-accent <colour>", "tuios ls"}
	case name == "mode":
		text := dockText(m.helpLanguage(), helptext.DockCell("mode"), "Mode")
		c.title = text.Title
		c.purpose = text.Purpose
		c.values = []helpKey{
			{press: "H", desc: "Tiling is on and the next split is stacked: the new pane takes a row, above or below."},
			{press: "V", desc: "Tiling is on and the next split is side by side: the new pane takes a column, left or right."},
			{press: "HOLD", desc: "The hold key has borrowed window mode, so window-mode keys act while it is held."},
			{press: "HINTS", desc: "Hints mode owns the keyboard; the legend at the dock's other end lists its keys."},
			{press: "SIDEBAR", desc: "The rail owns the keyboard, so the prefix and its keys act on the rail."},
			{press: "NEST ...", desc: "Nest mode: keys go to the pane, and the named key turns tuios back on."},
			{press: "MULTI", desc: "Multi-copy drives several panes; after a search it reads MULTI matched/total."},
			{press: "row:col", desc: "The focused pane is in copy mode; the pill shows the cursor's row and column."},
			{press: "lock glyph", desc: "Lock mode: every key but the leader goes to the focused pane."},
			{press: "leader glyph", desc: "A prefix is armed and waits for its key; the pill takes the warning colour."},
			{press: "Z", desc: "The workspace has a zoomed pane: one pane fills the box and the others are off screen."},
		}
		c.keys = helpCap(m.helpKeys("prefix_window", "prefix_help", "prefix_toggle_tiling"), 4)
		c.cli = []string{
			"tuios set-config appearance.dock_mode_pill input  # the mode icon while tiling",
			"tuios set-config appearance.dock_mode_split_letter false",
			"tuios set-config appearance.dock_prefix_indicator badge",
			"tuios keybinds list",
		}
	case name == "trail":
		text := dockText(m.helpLanguage(), helptext.DockCell("trail"), "Workspace counter")
		c.title = text.Title
		c.purpose = text.Purpose
		c.rows = []helpKey{
			{press: "1:1", desc: "Workspace 1 holds one pane."},
			{press: "2:3", desc: "Workspace 2 holds three panes."},
		}
		c.cli = []string{"tuios list-workspaces", "tuios list-dock-components", "tuios config edit  # [dock] lists"}
	case name == "tape":
		text := dockText(m.helpLanguage(), helptext.DockCell("tape"), "Tape")
		c.title = text.Title
		c.purpose = text.Purpose
		c.keys = helpCap(m.helpKeys("prefix_tape"), 3)
		c.cli = []string{"tuios tape list", "tuios tape play <file>"}
	case name == "notifications":
		text := dockText(m.helpLanguage(), helptext.DockCell("notifications"), "Notifications")
		c.title = text.Title
		c.purpose = text.Purpose
		c.cli = []string{"tuios notify test", "tuios set-config notifications.duration 4", "tuios set-config notifications.error_sticky true"}
	case name == "copy-help":
		text := dockText(m.helpLanguage(), helptext.DockCell("copy-help"), "Copy help")
		c.title = text.Title
		c.purpose = text.Purpose
		c.cli = []string{"tuios keybinds list"}
	default:
		c.title = "Dock cell: " + name
		c.purpose = "A built-in dock cell. Its name is what [dock] left, center or right lists; see the dock components for what it draws."
		c.cli = []string{"tuios list-dock-components", "tuios config edit  # [dock] lists"}
	}
	c.allKeys = c.keys
	return c
}

// helpPaneComponents adds every visible pane, and the title bar and body of
// each one whose chrome the frame actually draws.
func (m *OS) helpPaneComponents() []helpComponent {
	windows := m.GetVisibleWindows()
	out := make([]helpComponent, 0, len(windows)*3)
	for _, w := range windows {
		if w == nil || w.Width <= 0 || w.Height <= 0 {
			continue
		}
		pane, title, body := m.helpPaneRects(w)
		if pane.Empty() {
			continue
		}
		out = append(out, m.helpPaneComponent(w, pane))
		if !title.Empty() {
			out = append(out, m.helpPaneTitleComponent(w, title))
		}
		if !body.Empty() {
			out = append(out, m.helpPaneContentComponent(w, body))
		}
	}
	return out
}

// helpPaneRects gives a pane's visible rectangle and the visible parts of its
// title bar and body, in screen cells. The pane's own geometry is in the
// session's layout frame, which is the screen unless this client is showing a
// crop of a larger session.
func (m *OS) helpPaneRects(w *terminal.Window) (pane, title, body image.Rectangle) {
	layoutPane := image.Rect(w.X, w.Y, w.X+w.Width, w.Y+w.Height)
	layoutTitle := image.Rectangle{}
	layoutBody := layoutPane
	if m.Settings.WindowTitlePosition != "hidden" && !rendersBorderless(w) && w.Height > 1 {
		if m.Settings.WindowTitlePosition == "bottom" {
			layoutTitle = image.Rect(w.X, w.Y+w.Height-1, w.X+w.Width, w.Y+w.Height)
			layoutBody.Max.Y--
		} else {
			layoutTitle = image.Rect(w.X, w.Y, w.X+w.Width, w.Y+1)
			layoutBody.Min.Y++
		}
	}
	v := m.sessionView
	if !v.on {
		return layoutPane, layoutTitle, layoutBody
	}
	shift := image.Pt(v.dx, v.dy)
	pane = layoutPane.Add(shift).Intersect(v.clip)
	if !layoutTitle.Empty() {
		title = layoutTitle.Add(shift).Intersect(v.clip)
	}
	body = layoutBody.Add(shift).Intersect(v.clip)
	return pane, title, body
}

func (m *OS) helpPaneComponent(w *terminal.Window, rect image.Rectangle) helpComponent {
	name := w.Title()
	if name == "" {
		name = w.ID
	}
	keys := append(m.helpKeys("prefix_rename_window", "prefix_fullscreen", "prefix_split_vertical",
		"prefix_split_horizontal", "prefix_close_window", "prefix_minimize"), m.helpKeys(
		"prefix_toggle_tiling", "prefix_next_window", "prefix_prev_window")...)
	return helpComponent{
		id:      "pane:" + w.ID,
		title:   paneText(m, helptext.PanePage("pane:"+w.ID), "Pane: "+name, name).Title,
		purpose: paneText(m, helptext.PanePage("pane:"+w.ID), "Pane", name).Purpose,
		keys:    helpCap(keys, 7),
		allKeys: append(keys, m.helpKeys("prefix_window", "prefix_layout", "prefix_close_session")...),
		cli: []string{
			"tuios split-window vertical",
			"tuios set-layout <layout>",
			"tuios close-window " + name,
			"tuios set-config appearance.border_style rounded",
		},
		rect: rect,
	}
}

func (m *OS) helpPaneTitleComponent(w *terminal.Window, rect image.Rectangle) helpComponent {
	name := w.Title()
	if name == "" {
		name = w.ID
	}
	return helpComponent{
		id:      "pane:" + w.ID + "/title",
		parent:  "pane:" + w.ID,
		title:   paneText(m, helptext.PanePage("pane:"+w.ID+"/title"), "Title bar: "+name, name).Title,
		purpose: paneText(m, helptext.PanePage("pane:"+w.ID+"/title"), "Title bar", name).Purpose,
		keys: helpCap(m.helpKeys(
			"prefix_rename_window", "prefix_fullscreen", "prefix_close_window",
			"prefix_minimize"), 6),
		allKeys: m.helpKeys(
			"prefix_rename_window", "prefix_fullscreen", "prefix_close_window",
			"prefix_minimize", "prefix_window", "prefix_selection"),
		cli: []string{
			"tuios set-config appearance.window_title_style pill",
			"tuios set-config appearance.window_title_align center",
			"tuios set-config appearance.window_title_position top",
			"tuios set-config appearance.hide_window_buttons false",
			"tuios set-config appearance.window_button_position right",
		},
		rect: rect,
	}
}

func (m *OS) helpPaneContentComponent(w *terminal.Window, rect image.Rectangle) helpComponent {
	name := w.Title()
	if name == "" {
		name = w.ID
	}
	return helpComponent{
		id:      "pane:" + w.ID + "/content",
		parent:  "pane:" + w.ID,
		title:   paneText(m, helptext.PanePage("pane:"+w.ID+"/content"), "Terminal body: "+name, name).Title,
		purpose: paneText(m, helptext.PanePage("pane:"+w.ID+"/content"), "Terminal body", name).Purpose,
		keys: helpCap(m.helpKeys(
			"prefix_selection", "prefix_screenshot", "prefix_split_vertical",
			"prefix_split_horizontal"), 7),
		allKeys: m.helpKeys(
			"prefix_selection", "prefix_screenshot", "prefix_split_vertical",
			"prefix_split_horizontal", "prefix_fullscreen", "prefix_close_window",
			"prefix_scrollback", "prefix_toggle_tiling"),
		cli: []string{
			"tuios send-keys <text>",
			"tuios capture-pane <name>",
			"tuios set-config appearance.dim_unfocused 20",
			"tuios set-config appearance.border_focused_color_window <colour>",
		},
		rect: rect,
	}
}

// helpDesktopComponent is the fallback: the workspace area behind the panes.
// Its rectangle is the whole screen, so the smallest containing rectangle wins
// whenever the pointer is over something more specific.
func (m *OS) helpDesktopComponent() helpComponent {
	return helpComponent{
		id:      "desktop",
		title:   dockText(m.helpLanguage(), helptext.IDDesktop, "Workspace").Title,
		purpose: dockText(m.helpLanguage(), helptext.IDDesktop, "Workspace").Purpose,
		keys: helpCap(m.helpKeys(
			"prefix_workspace", "prefix_toggle_tiling", "prefix_next_window",
			"prefix_prev_window", "next_workspace", "prev_workspace"), 7),
		allKeys: m.helpKeys(
			"prefix_workspace", "prefix_toggle_tiling", "prefix_next_window",
			"prefix_prev_window", "new_workspace", "close_workspace", "last_workspace",
			"next_workspace", "prev_workspace", "toggle_scratch"),
		cli: []string{
			"tuios select-workspace 2",
			"tuios set-workspace-name 2 work",
			"tuios set-layout <layout>",
			"tuios set-config workspaces.return_when_empty true",
			"tuios set-config scratch.width 80",
		},
		rect: image.Rect(0, 0, m.GetRenderWidth(), m.GetRenderHeight()),
	}
}

// helpRectUnion is the smallest rectangle enclosing every rect. An empty list
// is the zero rectangle.
func helpRectUnion(rects []image.Rectangle) image.Rectangle {
	var out image.Rectangle
	for _, r := range rects {
		if r.Empty() {
			continue
		}
		if out.Empty() {
			out = r
			continue
		}
		out = out.Union(r)
	}
	return out
}

// renderHelpMode draws the explanation pane for the selected component. It is
// called once per frame after the rail, the panes and the dock have drawn, so
// the rectangles it places against are the ones the user is looking at.
func (m *OS) renderHelpMode() *lipgloss.Layer {
	if !m.HelpMode {
		m.helpPaneRect = image.Rectangle{}
		return nil
	}
	comps := m.buildHelpComponents()
	m.helpComps = comps
	cur := helpComponentByID(comps, m.helpTarget)
	if cur == nil {
		if len(comps) == 0 {
			return nil
		}
		cur = &comps[0]
		m.helpTarget = cur.id
	}

	pal := m.overlayUI()
	width := m.helpPaneWidth(cur)
	lang := m.helpLanguage()
	header := m.helpHeaderLines(comps, cur, width, pal, lang)
	body := m.helpSectionLines(cur, width, pal)

	// The pane cannot be taller than the screen it is drawn on. The title and
	// the navigation footer stay where they are, and the explanation between
	// them scrolls rather than losing its tail. The footer says so when it can.
	limit := max(m.GetRenderHeight()-3, 3)
	footer := m.helpFooterLines(comps, cur, width, pal, false)
	room := limit - len(header) - 1 - len(footer)
	if room < 1 {
		// A screen this short has no use for the title; the footer is the part
		// that has to stay.
		header = nil
		room = max(limit-1-len(footer), 1)
	}
	if len(body) > room {
		footer = m.helpFooterLines(comps, cur, width, pal, true)
		// A short screen fills the footer first, so the marker that says the
		// body continues sits with the title, which stays put while it scrolls.
		room = max(limit-len(header)-1-len(footer)-1, 1)
		header = append(header, m.helpScrollLine(len(body), room, width, pal))
	}
	m.helpScroll = min(max(m.helpScroll, 0), max(len(body)-room, 0))
	visible := body[m.helpScroll:min(m.helpScroll+room, len(body))]

	lines := make([]string, 0, len(header)+len(visible)+1+len(footer))
	lines = append(lines, header...)
	lines = append(lines, visible...)
	lines = append(lines, "")
	lines = append(lines, footer...)

	border := lipgloss.RoundedBorder()
	box := lipgloss.NewStyle().
		Background(pal.Surface).
		Foreground(pal.Fg).
		Border(border).
		BorderForeground(pal.Warn).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
	boxW, boxH := lipgloss.Width(box), lipgloss.Height(box)
	x, y := m.placeHelpPane(cur.rect, boxW, boxH)
	m.helpPaneRect = image.Rect(x, y, x+boxW, y+boxH)
	return lipgloss.NewLayer(box).X(x).Y(y).Z(config.ZIndexWhichKey + 1).ID(helpLayerID)
}

// helpScrollLine is the marker that says the explanation continues past the
// fold and that the wheel and the arrow keys move it. It carries the range on
// screen, so the reader knows how much is left without counting.
func (m *OS) helpScrollLine(total, room, width int, pal overlay.Palette) string {
	from := min(m.helpScroll+1, total)
	to := min(m.helpScroll+room, total)
	text := fmt.Sprintf("lines %d-%d of %d · Up/Down or the wheel", from, to, total)
	return overlay.Style(pal.Surface).Foreground(pal.FgMute).
		Render(overlay.Truncate(text, max(width-4, 8)))
}

// helpLayerID is the compositor id of the help pane.
const helpLayerID = "help-mode"

// helpPaneWidth is the pane's target width: wide enough for a key, its
// description and a CLI line, never wider than the screen keeps room for. A
// rail is a column the pane is placed beside rather than over, so a rail target
// also has to leave the rails their columns; on a narrow screen that narrows
// the pane instead of covering the rail it is explaining.
func (m *OS) helpPaneWidth(cur *helpComponent) int {
	width := min(60, m.GetRenderWidth()-4)
	if width < 20 {
		width = m.GetRenderWidth() - 2
	}
	width = max(width, 10)
	if rail, ok := m.helpRailBand(cur.rect); ok {
		room := m.GetRenderWidth() - rail.Dx() - 2
		if other := m.helpOtherRailBand(rail); !other.Empty() {
			room = min(room, m.GetRenderWidth()-rail.Dx()-other.Dx()-2)
		}
		width = min(width, max(room, 10))
	}
	return width
}

// helpHeaderLines is the pane's fixed top: the component's name, the number of
// parts it has, and where it sits. It stays put while the body scrolls.
func (m *OS) helpHeaderLines(comps []helpComponent, cur *helpComponent, width int, pal overlay.Palette, lang helptext.Language) []string {
	inner := max(width-4, 8)
	bg := pal.Surface
	title := overlay.Style(bg).Foreground(pal.Fg).Bold(true)
	muted := overlay.Style(bg).Foreground(pal.FgMute)

	var lines []string
	// Name how many parts Shift+Down can reach, so a component worth walking
	// into says so before the key is pressed.
	heading := cur.title
	if kids := len(helpChildren(comps, cur.id)); kids > 0 {
		heading += fmt.Sprintf(" (%d %s)", kids, helpPartLabel(lang, kids, cur.id))
	}
	lines = append(lines, title.Render(overlay.Truncate(heading, inner)))
	if path := m.helpPath(comps, cur); path != "" && path != cur.title {
		lines = append(lines, muted.Render(overlay.Truncate(path, inner)))
	}
	lines = append(lines, m.helpStateLine(cur, pal))
	return lines
}

// helpStateLine says which mode the keys are read in and whether the component
// the pane explains is the one holding the keyboard. A hotkey list is only
// worth reading against the state it will run in: the rail's keys do nothing
// while a pane has the keyboard, and a window-mode key typed in terminal mode
// is text for the shell. A rail reads its keys in either mode, so its line
// gives only whether it holds the keyboard: the mode would be noise the
// reader has to discard on every visit.
func (m *OS) helpStateLine(cur *helpComponent, pal overlay.Palette) string {
	mode := "window mode"
	if m.Mode == TerminalMode {
		mode = "terminal mode"
	}
	muted := overlay.Style(pal.Surface).Foreground(pal.FgMute)
	focused, focusable := m.helpFocus(cur)
	if strings.HasPrefix(cur.id, "rail:") {
		if focused {
			return overlay.Style(pal.Surface).Foreground(pal.Success).Render("focused")
		}
		return overlay.Style(pal.Surface).Foreground(pal.FgDim).Render("not focused")
	}
	switch {
	case focused:
		return muted.Render(mode+" · ") +
			overlay.Style(pal.Surface).Foreground(pal.Success).Render("focused")
	case focusable:
		return muted.Render(mode+" · ") +
			overlay.Style(pal.Surface).Foreground(pal.FgDim).Render("not focused")
	default:
		return muted.Render(mode + " · takes no focus")
	}
}

// helpFocus reports whether the component holds the keyboard and whether it can
// hold it at all. The rail is the one component that takes the keyboard from
// the panes without unmapping them, so a pane is focused only while the rail is
// not; the dock and the desktop never take it.
func (m *OS) helpFocus(cur *helpComponent) (focused, focusable bool) {
	switch {
	case strings.HasPrefix(cur.id, "rail:"):
		return m.SidebarFocused, true
	case strings.HasPrefix(cur.id, "pane:"):
		id, _, _ := strings.Cut(strings.TrimPrefix(cur.id, "pane:"), "/")
		win := m.GetFocusedWindow()
		return !m.SidebarFocused && win != nil && win.ID == id, true
	}
	return false, false
}

// helpSectionLines is the scrollable part of the pane: what the component is
// for, the keys that act on it, and the CLI that changes it. The styles carry
// the pane's ground, so every line fills the box rather than leaving the
// terminal's own background showing through.
func (m *OS) helpSectionLines(cur *helpComponent, width int, pal overlay.Palette) []string {
	inner := max(width-4, 8)
	bg := pal.Surface
	muted := overlay.Style(bg).Foreground(pal.FgMute)
	dim := overlay.Style(bg).Foreground(pal.FgDim)
	body := overlay.Style(bg).Foreground(pal.Fg)
	key := overlay.Style(bg).Foreground(pal.Accent).Bold(true)
	note := overlay.Style(bg).Foreground(pal.FgMute)

	// A key list is read against the state it will run in, so a component that
	// does not hold the keyboard has its keys drawn quieter than the ones that
	// would act: the state line above says why, and the dimming repeats it in
	// the list. The dock and the desktop never take the keyboard, so theirs are
	// not dimmed: their keys are chords that act wherever the keyboard is.
	focused, focusable := m.helpFocus(cur)
	keysLive := !focusable || focused
	keyInk, descInk := key, body
	if !keysLive {
		// Faint as well as muted: at 16 colours a role can resolve to the
		// terminal's own ink, and then colour alone would leave the line as
		// bright as a live one. The attribute survives every depth.
		keyInk = overlay.Style(bg).Foreground(pal.FgMute).Faint(true)
		descInk = muted
	}

	var lines []string
	if m.helpShowKeys {
		lines = append(lines, "")
		lines = append(lines, dim.Bold(true).Render("Available keybindings"))
		if len(cur.allKeys) == 0 {
			lines = append(lines, muted.Render("Nothing is bound to this component."))
		} else {
			lines = append(lines, helpKeyLines(cur.allKeys, inner, keyInk, descInk)...)
		}
		return lines
	}
	if cur.purpose != "" {
		lines = append(lines, "")
		for _, line := range wrapPlain(cur.purpose, inner) {
			lines = append(lines, body.Render(line))
		}
	}
	if len(cur.moves) > 0 {
		lines = append(lines, "")
		lines = append(lines, dim.Bold(true).Render("Toggle/focus"))
		lines = append(lines, helpKeyLines(cur.moves, inner, key, body)...)
	}
	if len(cur.values) > 0 {
		lines = append(lines, "")
		lines = append(lines, dim.Bold(true).Render("Possible values"))
		lines = append(lines, helpKeyLines(cur.values, inner, key, body)...)
	}
	if len(cur.rows) > 0 {
		lines = append(lines, "")
		lines = append(lines, dim.Bold(true).Render("On the bar"))
		lines = append(lines, helpKeyLines(cur.rows, inner, key, body)...)
	}
	if len(cur.keys) > 0 {
		lines = append(lines, "")
		// The rail's keys are live only while the rail holds the keyboard,
		// and its sections share that scope.
		heading := "Hotkeys"
		if strings.HasPrefix(cur.id, "rail:") {
			heading = "Hotkeys when focused"
		}
		lines = append(lines, dim.Bold(true).Render(heading))
		lines = append(lines, helpKeyLines(cur.keys, inner, keyInk, descInk)...)
	}
	if len(cur.cli) > 0 {
		lines = append(lines, "")
		lines = append(lines, dim.Bold(true).Render("CLI"))
		for _, command := range cur.cli {
			for i, line := range wrapPlain(command, inner-2) {
				if i == 0 {
					lines = append(lines, muted.Render("$ ")+note.Render(line))
				} else {
					lines = append(lines, note.Render("  "+line))
				}
			}
		}
	}
	return lines
}

// helpFooterLines is the navigation help under the body. It never scrolls: the
// keys stay in view however long the explanation is. The keys are drawn in the
// palette's on/enabled green, the one colour that reads as "press this", and
// the words they run are muted.
func (m *OS) helpFooterLines(comps []helpComponent, cur *helpComponent, width int, pal overlay.Palette, scrollable bool) []string {
	inner := max(width-4, 8)
	bg := pal.Surface
	key := overlay.Style(bg).Foreground(pal.Success).Bold(true)
	muted := overlay.Style(bg).Foreground(pal.FgMute)
	title := overlay.Style(bg).Foreground(pal.FgDim).Bold(true)

	type hint struct{ press, desc string }
	hints := []hint{{"Shift+Tab", "components"}}
	// Shift+Down and Shift+Up do nothing on a top-level component with no
	// parts: there is nowhere deeper to go and nothing to come back out of.
	// Naming them there promises a move the keys cannot make.
	if cur.parent != "" || len(helpChildren(comps, cur.id)) > 0 {
		hints = append(hints, hint{"Shift+Up/Down", "in and out"})
	}
	if scrollable {
		hints = append(hints, hint{"Up/Down", "scroll, or the wheel"})
	}
	hints = append(hints, hint{"h", "all keys"}, hint{"Esc", "exit"})

	const gap = "    "
	lines := []string{title.Render("Navigating help")}
	var line strings.Builder
	lineWidth := 0
	for _, h := range hints {
		w := lipgloss.Width(h.press) + 1 + lipgloss.Width(h.desc)
		if lineWidth > 0 && lineWidth+len(gap)+w > inner {
			lines = append(lines, line.String())
			line.Reset()
			lineWidth = 0
		}
		if lineWidth > 0 {
			line.WriteString(muted.Render(gap))
			lineWidth += len(gap)
		}
		line.WriteString(key.Render(h.press))
		line.WriteString(muted.Render(" " + h.desc))
		lineWidth += w
	}
	return append(lines, line.String())
}

// helpKeyLines draws a key column and its descriptions, wrapping a long
// description under itself rather than under the key.
func helpKeyLines(keys []helpKey, width int, keyStyle, descStyle lipgloss.Style) []string {
	keyWidth := 0
	for _, k := range keys {
		keyWidth = max(keyWidth, lipgloss.Width(k.press))
	}
	keyWidth = min(keyWidth, max(width/2, 8))
	descWidth := max(width-keyWidth-2, 8)

	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		press := overlay.Truncate(k.press, keyWidth)
		pad := strings.Repeat(" ", max(keyWidth-lipgloss.Width(press), 0))
		desc := wrapPlain(k.desc, descWidth)
		if len(desc) == 0 {
			desc = []string{""}
		}
		// The gap between the two columns is drawn with the description's
		// ground: an unstyled space loses the pane's fill and shows the
		// terminal's own background through it.
		lines = append(lines, keyStyle.Render(press+pad)+descStyle.Render("  "+desc[0]))
		for _, line := range desc[1:] {
			lines = append(lines, descStyle.Render(strings.Repeat(" ", keyWidth+2)+line))
		}
	}
	return lines
}

// helpPath is the component's place in its tree, outermost first.
func (m *OS) helpPath(comps []helpComponent, cur *helpComponent) string {
	var parts []string
	for c := cur; c != nil; {
		parts = append(parts, c.title)
		if c.parent == "" {
			break
		}
		c = helpComponentByID(comps, c.parent)
	}
	slices.Reverse(parts)
	return strings.Join(parts, " › ")
}

// placeHelpPane puts the pane below the component, then above it, then beside
// it. Only when none of those fits does it clamp into the screen, which is the
// one case where it can cover the component it explains.
//
// A rail and the parts of a rail are the exception: a rail is a column of
// rows, so there is no above or below a section that is not the rail itself.
// Those go beside their rail, which `placeHelpPaneBesideRail` keeps visible.
func (m *OS) placeHelpPane(anchor image.Rectangle, w, h int) (int, int) {
	screenW, screenH := m.GetRenderWidth(), m.GetRenderHeight()
	if screenW < 1 || screenH < 1 {
		return 0, 0
	}
	clamp := func(x, y int) (int, int) {
		x = min(max(x, 0), max(screenW-w, 0))
		y = min(max(y, 0), max(screenH-h, 0))
		return x, y
	}
	if anchor.Empty() {
		return clamp((screenW-w)/2, (screenH-h)/2)
	}
	if rail, ok := m.helpRailBand(anchor); ok {
		return m.placeHelpPaneBesideRail(rail, anchor, w, h)
	}

	dock := m.helpDockBand()
	overDock := func(x, y int) bool {
		if dock.Empty() || anchor.Overlaps(dock) {
			return false
		}
		return image.Rect(x, y, x+w, y+h).Overlaps(dock)
	}
	centerX := anchor.Min.X + (anchor.Dx()-w)/2
	centerY := anchor.Min.Y + (anchor.Dy()-h)/2

	candidates := []image.Point{
		{X: centerX, Y: anchor.Max.Y},
		{X: centerX, Y: anchor.Min.Y - h},
		{X: anchor.Max.X, Y: centerY},
		{X: anchor.Min.X - w, Y: centerY},
	}
	for _, p := range candidates {
		if p.X < 0 || p.Y < 0 || p.X+w > screenW || p.Y+h > screenH {
			continue
		}
		if overDock(p.X, p.Y) {
			continue
		}
		return p.X, p.Y
	}

	// Nothing fits cleanly. Prefer the side with the most room, still away
	// from the dock when that is possible.
	if anchor.Min.Y >= screenH-anchor.Max.Y && anchor.Min.Y >= h && !overDock(centerX, anchor.Min.Y-h) {
		return clamp(centerX, anchor.Min.Y-h)
	}
	if anchor.Max.Y+h <= screenH && !overDock(centerX, anchor.Max.Y) {
		return clamp(centerX, anchor.Max.Y)
	}
	return clamp(centerX, anchor.Max.Y)
}

// placeHelpPaneBesideRail puts the pane on the side of the rail with room for
// it, level with the section it explains and kept off the dock. The pane is
// sized by helpPaneWidth to fit beside the rails, so the side away from the
// screen edge is the one that fits; the clamp is for a screen too narrow even
// for that, where something has to be covered and the pane at least stays on
// screen.
func (m *OS) placeHelpPaneBesideRail(rail, anchor image.Rectangle, w, h int) (int, int) {
	screenW, screenH := m.GetRenderWidth(), m.GetRenderHeight()
	x := rail.Max.X
	if x+w > screenW {
		x = rail.Min.X - w
	}
	x = min(max(x, 0), max(screenW-w, 0))
	y := anchor.Min.Y + (anchor.Dy()-h)/2
	if dock := m.helpDockBand(); !dock.Empty() && !anchor.Overlaps(dock) {
		if dock.Min.Y == 0 {
			y = max(y, dock.Max.Y)
		} else {
			y = min(y, dock.Min.Y-h)
		}
	}
	return x, min(max(y, 0), max(screenH-h, 0))
}

// helpRailBand is the rail containing anchor, when the anchor is a rail or a
// part of one. Placement asks this to put the pane beside the rail instead of
// over it.
func (m *OS) helpRailBand(anchor image.Rectangle) (image.Rectangle, bool) {
	if anchor.Empty() {
		return image.Rectangle{}, false
	}
	for _, id := range []string{"rail:left", "rail:right"} {
		rail := helpComponentByID(m.helpComps, id)
		if rail == nil || rail.rect.Empty() {
			continue
		}
		if anchor.Min.X >= rail.rect.Min.X && anchor.Max.X <= rail.rect.Max.X &&
			anchor.Min.Y >= rail.rect.Min.Y && anchor.Max.Y <= rail.rect.Max.Y {
			return rail.rect, true
		}
	}
	return image.Rectangle{}, false
}

// helpOtherRailBand is the rail on the other edge, when one is shown. Sizing
// the pane for a rail target leaves that rail its columns too, so both rails
// stay visible.
func (m *OS) helpOtherRailBand(band image.Rectangle) image.Rectangle {
	for _, id := range []string{"rail:left", "rail:right"} {
		rail := helpComponentByID(m.helpComps, id)
		if rail == nil || rail.rect.Empty() || rail.rect == band {
			continue
		}
		return rail.rect
	}
	return image.Rectangle{}
}

// helpDockBand is the dock's rows on this frame, or the zero rectangle when
// the dock is hidden.
func (m *OS) helpDockBand() image.Rectangle {
	if m.Settings.DockbarPosition == "hidden" {
		return image.Rectangle{}
	}
	height := m.Settings.DockHeight()
	if height <= 0 {
		return image.Rectangle{}
	}
	if m.Settings.DockbarPosition == "top" {
		return image.Rect(0, 0, m.GetRenderWidth(), height)
	}
	return image.Rect(0, m.GetRenderHeight()-height, m.GetRenderWidth(), m.GetRenderHeight())
}
