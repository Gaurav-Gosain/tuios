package app

import (
	"image"
	"slices"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Help mode is the hover help the leader then h opens. It is not the help
// overlay: nothing is listed up front and no key is taken from the pane. The
// pointer, or Tab and Shift+Tab, moves between the components on screen; Up
// and Down move into and out of a component that has parts; h shows the whole
// keybinding set of the component selected; esc leaves. The pane that explains
// the component is drawn above or below it, never over it where the screen has
// room, and its border is the error ink, so it reads as an inspection surface
// rather than as one more dock or rail.

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
	keys    []helpKey
	allKeys []helpKey
	cli     []string
	rect    image.Rectangle
}

// EnterHelpMode turns help mode on and selects the first component on screen.
// The leader chord that reaches it has already closed every menu.
func (m *OS) EnterHelpMode() {
	m.ShowHelp = false
	m.CloseContextMenu()
	m.CloseHints()
	m.ClosePaneLabels()
	m.HelpMode = true
	m.helpShowKeys = false
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
// leader menu again, and esc goes back to the pane.
func (m *OS) ExitHelpMode() {
	m.HelpMode = false
	m.helpShowKeys = false
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
// back. Tab and Shift+Tab call it.
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
	m.helpInvalidate()
}

// HelpDeeper moves Down: into the selected component's first part, or to its
// next sibling once inside one.
func (m *OS) HelpDeeper() {
	comps := m.helpComponents()
	cur := helpIndex(comps, m.helpTarget)
	if cur < 0 {
		m.HelpNext(1)
		return
	}
	if kids := helpChildren(comps, comps[cur].id); len(kids) > 0 {
		m.helpTarget = kids[0].id
		m.helpInvalidate()
		return
	}
	if comps[cur].parent == "" {
		return
	}
	if next, ok := helpSibling(comps, comps[cur], 1); ok {
		m.helpTarget = next.id
		m.helpInvalidate()
	}
}

// HelpShallower moves Up: to the previous sibling inside a component, or back
// out to the component itself from its first part.
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
		m.helpInvalidate()
		return
	}
	m.helpTarget = comps[cur].parent
	m.helpInvalidate()
}

// HelpToggleRail shows or hides a rail from inside help mode, so a hidden rail
// can be brought back, looked at, and put away again without leaving the mode.
// other picks the rail on the opposite edge. A rail that was hidden has no
// rectangle to hover, so it is still listed as a component; this is the key
// that gives it one.
func (m *OS) HelpToggleRail(other bool) {
	if m.NoRail {
		return
	}
	if other {
		m.ToggleOtherSidebar()
	} else {
		m.ToggleSidebar()
	}
	m.helpComps = nil
	comps := m.helpComponents()
	wanted := "rail:left"
	if other || m.legacySidebarEdge() == sidebarRight {
		wanted = "rail:right"
	}
	switch {
	case helpIndex(comps, wanted) >= 0:
		m.helpTarget = wanted
	case helpIndex(comps, m.helpTarget) >= 0:
		// Keep the selection when the rail that went away was not it.
	default:
		if tops := helpTopLevel(comps); len(tops) > 0 {
			m.helpTarget = tops[0].id
		} else {
			m.helpTarget = ""
		}
	}
	m.helpInvalidate()
}

// HelpToggleKeys shows or hides the selected component's whole keybinding set.
// h calls it.
func (m *OS) HelpToggleKeys() {
	if !m.HelpMode {
		return
	}
	m.helpShowKeys = !m.helpShowKeys
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
// rectangle to hover, but Tab can reach it, and s (or S for the opposite edge)
// shows it from inside help mode.
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
	toggle := "s"
	toggleDesc := "Show or hide this rail from help mode"
	if other {
		toggle = "S"
		toggleDesc = "Show or hide the opposite rail from help mode"
	}
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

	purpose := "The session rail is the column beside the panes. It lists the sessions on this machine and on other hosts, the panes of the session it is following, the agents that need a person, the files below the focused pane, and the controls that add and collapse."
	if !shown {
		purpose = "This rail is hidden, so it has no rectangle to hover. Press " + toggle + " in help mode to show it, look at it, and press " + toggle + " again to put it away. " + purpose
	}
	keys := append([]helpKey{{press: toggle, desc: toggleDesc}},
		m.helpScopedKeys(config.ScopeSidebar, "activate", "cursor_down", "cursor_up", "collapse", "expand", "section", "new_session", "new_window")...)
	railAction := "prefix_toggle_sidebar"
	if other {
		railAction = "prefix_toggle_other_rail"
	}
	keys = append(keys, m.helpKeys(railAction, "prefix_explore")...)
	keys = helpCap(keys, 7)

	all := append([]helpKey{{press: toggle, desc: toggleDesc}}, m.helpAllScopedKeys(
		config.ScopeSidebar, config.ScopeSidebarFiles, config.ScopeSidebarAgents)...)
	all = append(all, m.helpKeys(
		railAction, "prefix_toggle_spotlight",
		"prefix_explore", "prefix_session_switcher", "prefix_inbox", "prefix_mail")...)

	parent := helpComponent{
		id:      id,
		title:   title,
		purpose: purpose,
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
		out = append(out, m.helpRailChild(group, id, r))
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
func (m *OS) helpRailChild(group, parent string, rect image.Rectangle) helpComponent {
	c := helpComponent{id: parent + "/" + group, parent: parent, rect: rect}
	switch group {
	case "sessions":
		c.title = "Sessions"
		c.purpose = "One row per daemon session. Enter opens the session this client shows; n makes a new one; r renames; x kills. The number ahead of a name is the switch key."
		c.keys = helpCap(m.helpScopedKeys(config.ScopeSidebar,
			"activate", "new_session", "rename", "kill", "cursor_down", "cursor_up"), 6)
		c.cli = []string{"tuios ls", "tuios new work", "tuios rename-session work", "tuios kill-session work"}
	case "panes":
		c.title = "Panes"
		c.purpose = "The panes of the session the rail follows. Enter focuses one; the + makes another. A pane row is also the drag handle for moving the pane between workspaces."
		c.keys = helpCap(m.helpScopedKeys(config.ScopeSidebar,
			"activate", "new_window", "cursor_down", "cursor_up", "reorder_down", "reorder_up"), 6)
		c.keys = append(c.keys, m.helpKeys("prefix_next_window", "prefix_prev_window")...)
		c.cli = []string{"tuios new-window", "tuios list-windows", "tuios select-window <name>"}
	case "agents":
		c.title = "Agents"
		c.purpose = "Agents that are working, waiting for an answer, or finished. a cycles all/here, s cycles priority/recent, m opens the mailbox, and Enter opens the agent's pane."
		c.keys = append(m.helpScopedKeys(config.ScopeSidebarAgents,
			"activate", "cursor_down", "cursor_up"), m.helpScopedKeys(config.ScopeSidebar,
			"agents_filter", "agents_sort", "mail", "cursor_down", "cursor_up")...)
		c.keys = helpCap(c.keys, 7)
		c.keys = append(c.keys, m.helpKeys("prefix_inbox", "prefix_next_attention")...)
		c.cli = []string{"tuios list-agents", "tuios respond <action>", "tuios send-agent-message <text>"}
	case "files":
		c.title = "Files"
		c.purpose = "The directory below the focused pane. Enter opens a name, e edits it in the configured editor, / searches, and p pastes a copied or cut entry. The header's cd sends the pane there."
		c.keys = helpCap(m.helpScopedKeys(config.ScopeSidebarFiles,
			"file_open", "file_edit", "file_search", "file_delete", "file_paste", "file_copy", "file_cut"), 7)
		c.cli = []string{"tuios set-config appearance.sidebar.sections sessions:25,terminals,files:25,agents:34", "tuios config edit"}
	case "controls":
		c.title = "Rail controls"
		c.purpose = "The rail's own controls: collapse it to a strip, widen or narrow it, put the files section on and off, and move keyboard focus into the rail."
		c.keys = append(m.helpScopedKeys(config.ScopeSidebar,
			"collapse", "expand", "narrow", "widen", "file_search"), m.helpKeys(
			"prefix_toggle_sidebar", "prefix_toggle_other_rail", "prefix_explore")...)
		c.keys = helpCap(c.keys, 7)
		c.cli = []string{"tuios set-config appearance.sidebar.width 30", "tuios set-config appearance.sidebar.position left"}
	}
	switch group {
	case "agents":
		c.allKeys = m.helpAllScopedKeys(config.ScopeSidebarAgents)
	case "files":
		c.allKeys = m.helpAllScopedKeys(config.ScopeSidebarFiles)
	default:
		c.allKeys = m.helpAllScopedKeys(config.ScopeSidebar)
	}
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

	names := make([]string, 0, len(m.dockCustomHits))
	for _, hit := range m.dockCustomHits {
		names = append(names, hit.Name)
	}
	sort.Strings(names)
	seen := map[string]bool{
		"workspaces":       true,
		"windows":          true,
		"messages":         true,
		"session-controls": true,
		"overflow":         true,
	}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		for _, hit := range m.dockCustomHits {
			if hit.Name != name {
				continue
			}
			out = append(out, m.helpDockCellComponent(name, dock.id,
				image.Rect(hit.X0, hit.Y, hit.X1, hit.Y+1)))
			break
		}
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
	purpose := "The dock is the status bar: the mode pill, the workspace strip, minimized panes, session controls, notifications and whatever cells the configuration puts on it. Clicking a cell acts on the row it names."
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
		title:   "Dock",
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
		c.title = "Workspaces"
		c.purpose = "The workspace strip. Each pill is a workspace and its panes; clicking one switches to it, and the arrows step a strip too long for the dock."
		c.keys = helpCap(m.helpKeys("prefix_workspace", "prefix_workspace_switcher", "next_workspace", "prev_workspace"), 6)
		c.cli = []string{"tuios select-workspace 2", "tuios set-workspace-name 2 work", "tuios set-config appearance.dock_workspace_tabs true"}
	case name == "windows":
		c.title = "Minimized panes"
		c.purpose = "One entry per minimized pane. Clicking an entry restores that pane; the overflow marker opens the list of entries the dock had no room for."
		c.keys = helpCap(append(m.helpKeys("prefix_minimize", "minimize_prefix_restore_all"),
			m.helpScopedKeys(config.ScopeSidebar, "activate")...), 6)
		c.cli = []string{"tuios list-windows", "tuios restore-window <name>", "tuios set-config appearance.dock_compact true"}
	case name == "session-controls":
		c.title = "Session controls"
		c.purpose = "The client controls: open the session list, leave this client (detach), close the session, or start a new one. They act on the daemon session, not on one pane."
		c.keys = helpCap(m.helpKeys("prefix_session_switcher", "prefix_detach", "prefix_close_session", "prefix_quit"), 6)
		c.cli = []string{"tuios ls", "tuios detach-client", "tuios kill-session <name>", "tuios new"}
	case name == "messages":
		c.title = "Messages"
		c.purpose = "The newest notification. Enter or a click acts on it, esc dismisses it, and a cut message can be held to read the rest. Notifications also carry agent asks."
		c.keys = helpCap(m.helpKeys("prefix_jump_notif", "prefix_last_message", "prefix_inbox", "prefix_mail"), 6)
		c.cli = []string{"tuios notify test", "tuios set-config notifications.duration 4", "tuios set-config notifications.error_sticky true"}
	case name == "overflow":
		c.title = "Minimized overflow"
		c.purpose = "The marker for minimized panes the dock could not fit. It opens the aggregate list, which names every one of them."
		c.cli = []string{"tuios list-windows", "tuios set-config appearance.dock_compact true"}
	case name == "clock":
		c.title = "Clock"
		c.purpose = "The current time, drawn by the dock. Its format and whether it appears are both appearance settings."
		c.cli = []string{"tuios set-config appearance.show_clock true", "tuios set-config appearance.clock_format 15:04"}
	case name == "cpu":
		c.title = "CPU"
		c.purpose = "The CPU readout. It samples the machine while the dock is shown; turn it off when the dock is not the place you watch load."
		c.cli = []string{"tuios set-config appearance.show_cpu true"}
	case name == "ram":
		c.title = "RAM"
		c.purpose = "The memory readout, in the same units the dock uses for the machine's total. It is a dock cell, not a per-pane figure."
		c.cli = []string{"tuios set-config appearance.show_ram true"}
	case name == "transfers":
		c.title = "Transfers"
		c.purpose = "The copy and paste buffer strip: what is held, and whether a transfer is in flight. Clicking it opens the buffer list."
		c.keys = helpCap(m.helpKeys("prefix_transfers", "paste_buffer", "choose_buffer"), 6)
		c.cli = []string{"tuios list-buffers", "tuios set-config appearance.dock_compact true"}
	case name == "session":
		c.title = "Session name"
		c.purpose = "The daemon session this client is showing, its accent, and whether other clients are attached. Clicking it opens the session switcher."
		c.keys = helpCap(m.helpKeys("prefix_session_switcher", "prefix_close_session"), 6)
		c.cli = []string{"tuios rename-session <name>", "tuios set-session-accent <colour>", "tuios ls"}
	case name == "mode":
		c.title = "Mode"
		c.purpose = "The input mode: window management or terminal. It is the dock's answer to where a key will go, and it changes as the prefix menu opens."
		c.keys = helpCap(m.helpKeys("prefix_window", "prefix_help"), 4)
		c.cli = []string{"tuios set-config appearance.dock_mode_pill true", "tuios keybinds list"}
	case name == "trail":
		c.title = "Trail"
		c.purpose = "The trail cell records where this client has been: the panes and workspaces it visited, newest first. It is built into the dock and is not a command runner."
		c.cli = []string{"tuios list-dock-components", "tuios config edit  # [dock] lists"}
	case name == "tape":
		c.title = "Tape"
		c.purpose = "The tape cell reports a tape playing in this session, with its status. It is drawn only while a tape is running."
		c.keys = helpCap(m.helpKeys("prefix_tape"), 3)
		c.cli = []string{"tuios tape list", "tuios tape play <file>"}
	case name == "notifications":
		c.title = "Notifications"
		c.purpose = "The notification cell carries the newest message and its level. Its duration and whether errors stay until dismissed are notification settings."
		c.cli = []string{"tuios notify test", "tuios set-config notifications.duration 4", "tuios set-config notifications.error_sticky true"}
	case name == "copy-help":
		c.title = "Copy help"
		c.purpose = "The copy-help cell is the dock's reminder of the copy-mode chord while copy mode is available. It is documentation, not a control."
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
		title:   "Pane: " + name,
		purpose: "One terminal window. It owns a pty and a shell; keys go to the shell in terminal mode and to the window manager in window mode. The title bar names it and carries its buttons, and the body is the terminal.",
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
		title:   "Title bar: " + name,
		purpose: "The pane's title bar. It names the pane, shows which pane has focus and which workspace it is on, and carries the close and minimize buttons. Dragging it moves the pane; the edge under the pointer resizes it.",
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
		title:   "Terminal body: " + name,
		purpose: "The terminal itself. In terminal mode every key but the leader goes to the shell; copy mode moves over the scrollback without sending anything. The scroll bar and the selection are drawn here too.",
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
		title:   "Workspace",
		purpose: "The desktop behind the panes, on the current workspace. Clicking it leaves terminal mode; the leader then a workspace key switches workspaces. Tiling arranges the panes here, and the scratch pane opens over them.",
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
	width := m.helpPaneWidth()
	lines := m.helpBodyLines(comps, cur, width, pal)

	// The pane cannot be taller than the screen it is drawn on. Losing the
	// tail is better than a box whose bottom is off screen.
	limit := max(m.GetRenderHeight()-3, 2)
	if len(lines) > limit {
		lines = lines[:limit]
		lines = append(lines, overlay.Style(pal.Surface).Foreground(pal.FgMute).Render("…"))
	}

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
	return lipgloss.NewLayer(box).X(x).Y(y).Z(config.ZIndexWhichKey + 1).ID("help-mode")
}

// helpPaneWidth is the pane's target width: wide enough for a key, its
// description and a CLI line, never wider than the screen keeps room for.
func (m *OS) helpPaneWidth() int {
	width := min(60, m.GetRenderWidth()-4)
	if width < 20 {
		width = m.GetRenderWidth() - 2
	}
	return max(width, 10)
}

// helpBodyLines lays out the pane's text. The styles carry the pane's ground,
// so every line fills the box rather than leaving the terminal's own background
// showing through.
func (m *OS) helpBodyLines(comps []helpComponent, cur *helpComponent, width int, pal overlay.Palette) []string {
	inner := max(width-4, 8)
	bg := pal.Surface
	title := overlay.Style(bg).Foreground(pal.Fg).Bold(true)
	muted := overlay.Style(bg).Foreground(pal.FgMute)
	dim := overlay.Style(bg).Foreground(pal.FgDim)
	body := overlay.Style(bg).Foreground(pal.Fg)
	key := overlay.Style(bg).Foreground(pal.Accent).Bold(true)
	note := overlay.Style(bg).Foreground(pal.FgMute)

	var lines []string
	lines = append(lines, title.Render(overlay.Truncate(cur.title, inner)))
	if path := m.helpPath(comps, cur); path != "" && path != cur.title {
		lines = append(lines, muted.Render(overlay.Truncate(path, inner)))
	}

	if m.helpShowKeys {
		lines = append(lines, "")
		lines = append(lines, dim.Bold(true).Render("Available keybindings"))
		if len(cur.allKeys) == 0 {
			lines = append(lines, muted.Render("Nothing is bound to this component."))
		} else {
			lines = append(lines, helpKeyLines(cur.allKeys, inner, key, body)...)
		}
	} else {
		if cur.purpose != "" {
			lines = append(lines, "")
			for _, line := range wrapPlain(cur.purpose, inner) {
				lines = append(lines, body.Render(line))
			}
		}
		if len(cur.keys) > 0 {
			lines = append(lines, "")
			lines = append(lines, dim.Bold(true).Render("Hotkeys"))
			lines = append(lines, helpKeyLines(cur.keys, inner, key, body)...)
		}
		if len(cur.cli) > 0 {
			lines = append(lines, "")
			lines = append(lines, dim.Bold(true).Render("CLI"))
			for _, command := range cur.cli {
				for i, line := range wrapPlain(command, inner-2) {
					if i == 0 {
						lines = append(lines, muted.Render("$ ")+note.Render(line))
					} else {
						lines = append(lines, "  "+note.Render(line))
					}
				}
			}
		}
	}

	footer := "Tab components    Up/Down in and out    h all keys"
	if helpIndex(comps, "rail:left") >= 0 || helpIndex(comps, "rail:right") >= 0 {
		footer += "    s rail    S other rail"
	}
	footer += "    Esc exit"
	lines = append(lines, "")
	for _, line := range wrapPlain(footer, inner) {
		lines = append(lines, muted.Render(line))
	}
	return lines
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
		lines = append(lines, keyStyle.Render(press+pad)+"  "+descStyle.Render(desc[0]))
		for _, line := range desc[1:] {
			lines = append(lines, strings.Repeat(" ", keyWidth+2)+descStyle.Render(line))
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
