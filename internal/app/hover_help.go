package app

import (
	"image"
	"math"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Hover help explains one component of the screen at a time: the one under
// the pointer, or the one the person walks to with the help keys. The leader
// then h (toggle_hover_help) turns it on and off; nothing else closes it.
//
// It is a view, not a mode. The explainer is drawn beside its target and owns
// no input: a key that is not one of the help keys reaches the pane, a click
// reaches whatever it lands on, and motion picks the target and then goes on
// to whatever wants it. The help keys are all modified (ctrl+alt and the
// arrows by default), so none of them is a key a shell needs bare, and no
// other table binds them. The explainer follows the layout those keys and
// clicks leave.
//
// The explainer is the theme's panel ground with no border, placed above or
// below its target and never over it. At 16 colours the ground is the
// terminal's own, so a blank cell keeps it apart from the target.

// helpKey is one line of a key list as the explainer shows it: the whole
// press, chord included, and what it does.
type helpKey struct {
	press string
	desc  string
	// dead says the key cannot act now, so it is drawn greyed out.
	dead bool
}

// helpComponent is one target hover help can explain. parent is empty for a
// component and names the component for a part. rect is in screen cells, and
// empty for a component with nothing on screen to point at.
type helpComponent struct {
	id      string
	parent  string
	title   string
	purpose string
	// values are the labels a readout can show, in their own section.
	values []helpKey
	// rows are the current members of a group, such as the minimized panes
	// the dock drew.
	rows []helpKey
	// keys are the main keys; allKeys is the whole set help_keys shows.
	keys    []helpKey
	allKeys []helpKey
	cli     []string
	rect    image.Rectangle
	// layer is the rank the part is drawn at: the desktop at the bottom, each
	// pane at its HitZ above it, and the dock and the rail over every pane.
	// The pointer picks from the highest layer under it first.
	layer int
}

// helpLayerChrome is the layer of the dock and the rail, drawn over the panes.
const helpLayerChrome = math.MaxInt

// hoverHelpState is hover help's state on one client.
type hoverHelpState struct {
	on bool
	// target is the id of the component or part being explained.
	target string
	// showKeys says the explainer lists every key of the target rather than
	// its account and main keys.
	showKeys bool
	// rect is where the explainer was drawn on the last frame. Motion over it
	// is ignored, so reading it does not change the target, and the wheel
	// scrolls it only there.
	rect image.Rectangle
	// scroll is the first body line shown when the explanation is taller than
	// the screen allows.
	scroll int
	// comps is the target list the last frame drew, so motion and the walk
	// keys do not rebuild every page.
	comps []helpComponent
}

// HoverHelpOn reports whether hover help is up.
func (m *OS) HoverHelpOn() bool { return m.hoverHelp.on }

// ToggleHoverHelp turns hover help on or off. toggle_hover_help calls it.
func (m *OS) ToggleHoverHelp() {
	if m.hoverHelp.on {
		m.hoverHelp = hoverHelpState{}
		m.helpInvalidate()
		return
	}
	m.hoverHelp = hoverHelpState{on: true}
	m.hoverHelp.target = m.helpStartTarget()
	m.helpInvalidate()
}

// helpStartTarget is the target hover help opens on: the smallest component
// under the last place the pointer was seen, or the first component when the
// pointer was never seen.
func (m *OS) helpStartTarget() string {
	comps := m.helpComponents()
	if x, y := m.PointerSeen(); x > 0 || y > 0 {
		if id := helpSmallestAt(comps, image.Pt(x, y)); id != "" {
			return id
		}
	}
	if len(comps) > 0 {
		return comps[0].id
	}
	return ""
}

// helpInvalidate makes the next frame compose rather than serve the last one.
func (m *OS) helpInvalidate() {
	m.cachedViewContent = ""
	m.renderSkipped = false
}

// helpComponents is the target list of the frame already drawn, or a fresh
// one when none has been built.
func (m *OS) helpComponents() []helpComponent {
	if m.hoverHelp.comps == nil {
		m.hoverHelp.comps = m.buildHelpComponents()
	}
	return m.hoverHelp.comps
}

// helpSet moves the explainer to a new target, from the top of its page.
func (m *OS) helpSet(id string) {
	m.hoverHelp.target = id
	m.hoverHelp.scroll = 0
	m.helpInvalidate()
}

// HelpNext moves to the next component, or delta components on; a negative
// delta walks back. help_next and help_prev call it.
func (m *OS) HelpNext(delta int) {
	comps := m.helpComponents()
	tops := helpTopLevel(comps)
	if len(tops) == 0 {
		return
	}
	idx := slices.IndexFunc(tops, func(c helpComponent) bool {
		return c.id == helpTopAncestor(comps, m.hoverHelp.target)
	})
	switch {
	case idx < 0 && delta < 0:
		idx = len(tops) - 1
	case idx < 0:
		idx = 0
	default:
		idx = ((idx+delta)%len(tops) + len(tops)) % len(tops)
	}
	m.helpSet(tops[idx].id)
}

// HelpDeeper moves into the target's first part, or to the next part once
// inside a component. help_deeper calls it.
func (m *OS) HelpDeeper() {
	comps := m.helpComponents()
	cur := helpComponentByID(comps, m.hoverHelp.target)
	if cur == nil {
		m.HelpNext(1)
		return
	}
	if kids := helpChildren(comps, cur.id); len(kids) > 0 {
		m.helpSet(kids[0].id)
		return
	}
	if next, ok := helpSibling(comps, *cur, 1); ok {
		m.helpSet(next.id)
	}
}

// HelpShallower moves to the previous part, or out to the component from its
// first part. help_shallower calls it.
func (m *OS) HelpShallower() {
	comps := m.helpComponents()
	cur := helpComponentByID(comps, m.hoverHelp.target)
	if cur == nil {
		m.HelpNext(-1)
		return
	}
	if cur.parent == "" {
		return
	}
	if prev, ok := helpSibling(comps, *cur, -1); ok {
		m.helpSet(prev.id)
		return
	}
	m.helpSet(cur.parent)
}

// HelpScroll moves the explanation by delta lines. The heading and the footer
// stay put. help_scroll_up, help_scroll_down and the wheel call it.
func (m *OS) HelpScroll(delta int) {
	if !m.hoverHelp.on || delta == 0 {
		return
	}
	m.hoverHelp.scroll = max(m.hoverHelp.scroll+delta, 0)
	m.helpInvalidate()
}

// HelpToggleKeys shows or hides every key of the target. help_keys calls it.
func (m *OS) HelpToggleKeys() {
	if !m.hoverHelp.on {
		return
	}
	m.hoverHelp.showKeys = !m.hoverHelp.showKeys
	m.hoverHelp.scroll = 0
	m.helpInvalidate()
}

// HelpHoverAt makes the smallest target under the pointer the one explained.
// Motion over the explainer itself is ignored.
func (m *OS) HelpHoverAt(x, y int) {
	// Under an open leader menu the pointer is not choosing a target: the
	// explainer comes back on the one it left.
	if !m.hoverHelp.on || m.MenuOpen() {
		return
	}
	p := image.Pt(m.ScreenPoint(x, y))
	if p.In(m.hoverHelp.rect) {
		return
	}
	if id := helpSmallestAt(m.helpComponents(), p); id != "" && id != m.hoverHelp.target {
		m.helpSet(id)
	}
}

// HelpExplainerAt reports whether the explainer covers the cell, which is
// where the wheel scrolls it.
func (m *OS) HelpExplainerAt(x, y int) bool {
	return m.hoverHelp.on && image.Pt(m.ScreenPoint(x, y)).In(m.hoverHelp.rect)
}

// helpSmallestAt is the target under p: of the parts holding p on the highest
// layer, the one with the smallest rectangle. Comparing sizes alone picked a
// smaller pane, or its title bar, through a floating pane drawn over it.
func helpSmallestAt(comps []helpComponent, p image.Point) string {
	best, bestLayer, bestArea := "", 0, 0
	for _, c := range comps {
		if !p.In(c.rect) {
			continue
		}
		area := c.rect.Dx() * c.rect.Dy()
		if best == "" || c.layer > bestLayer || c.layer == bestLayer && area < bestArea {
			best, bestLayer, bestArea = c.id, c.layer, area
		}
	}
	return best
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
	var out []helpComponent
	for _, c := range comps {
		if c.parent == id {
			out = append(out, c)
		}
	}
	return out
}

func helpSibling(comps []helpComponent, cur helpComponent, delta int) (helpComponent, bool) {
	if cur.parent == "" {
		return helpComponent{}, false
	}
	sibs := helpChildren(comps, cur.parent)
	i := slices.IndexFunc(sibs, func(c helpComponent) bool { return c.id == cur.id })
	if j := i + delta; i >= 0 && j >= 0 && j < len(sibs) {
		return sibs[j], true
	}
	return helpComponent{}, false
}

// helpKeys is the first live press of each named action, chord included, with
// the description the rest of the interface shows for it. An action with no
// live press is left out, so the explainer never promises a key that does
// nothing.
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

// helpOwnKey is the first key the config binds to one of hover help's own
// actions, as the explainer writes it, or "" when the config leaves it
// unbound.
func (m *OS) helpOwnKey(action string) string {
	if m.KeybindRegistry == nil {
		return ""
	}
	for _, key := range m.KeybindRegistry.Keys(config.TableHelp, action) {
		if key != "" {
			return helpPressLabel(key)
		}
	}
	return ""
}

// helpPressLabel writes a press for a person the way the which-key menu does,
// each key of a chord apart.
func helpPressLabel(press string) string {
	parts := strings.Fields(press)
	for i := range parts {
		parts[i] = config.KeyLabel(parts[i])
	}
	return strings.Join(parts, " ")
}

// helpToggleChord is the chord that turns hover help off, which is the one
// that turned it on, with the leader the config sets. Empty when unbound.
func (m *OS) helpToggleChord() string {
	if m.KeybindRegistry == nil {
		return ""
	}
	for _, press := range config.PressesByAction(m.KeybindRegistry)[config.ActionToggleHoverHelp] {
		if press != "" {
			return helpPressLabel(press)
		}
	}
	return ""
}

func helpCap(keys []helpKey, n int) []helpKey {
	if len(keys) <= n {
		return keys
	}
	return keys[:n]
}

// buildHelpComponents is every target the frame can explain. The dock's
// parts come from the cells its last pass recorded: where a cell is is a fact
// about the painted frame, and recomputing it from settings would name a
// rectangle the person is not looking at.
func (m *OS) buildHelpComponents() []helpComponent {
	comps := make([]helpComponent, 0, 24)
	comps = append(comps, m.helpDockComponents()...)
	comps = append(comps, m.helpRailComponents()...)
	for i := range comps {
		comps[i].layer = helpLayerChrome
	}
	comps = append(comps, m.helpPaneComponents()...)
	comps = append(comps, m.helpDesktopComponent())
	return comps
}

// helpDockComponents is the dock and the parts its last frame drew.
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
		out = append(out, m.helpDockCellComponent(config.DockComponentWorkspaces, dock.id, r))
	}
	if r := helpRectUnion(items); !r.Empty() {
		out = append(out, m.helpDockCellComponent(config.DockComponentWindows, dock.id, r))
	}
	if r := helpRectUnion(sessions); !r.Empty() {
		out = append(out, m.helpDockCellComponent(config.DockComponentSessionControls, dock.id, r))
	}
	if m.dockOverflowHit.Active {
		out = append(out, m.helpDockCellComponent("overflow", dock.id,
			image.Rect(m.dockOverflowHit.X0, m.dockOverflowHit.Y, m.dockOverflowHit.X1, m.dockOverflowHit.Y+1)))
	}

	// Every other named cell the dock drew is one part, in draw order. A cell
	// that belongs to a group above is skipped rather than explained twice.
	seen := map[string]bool{
		config.DockComponentWorkspaces:      true,
		config.DockComponentWindows:         true,
		config.DockComponentSessionControls: true,
		"messages":                          true,
		"overflow":                          true,
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

// helpDockBand is the dock's rows on this frame, or the zero rectangle when
// the dock is hidden.
func (m *OS) helpDockBand() image.Rectangle {
	height := m.Settings.DockHeight()
	if m.Settings.DockbarPosition == "hidden" || height <= 0 {
		return image.Rectangle{}
	}
	if m.Settings.DockbarPosition == "top" {
		return image.Rect(0, 0, m.GetRenderWidth(), height)
	}
	return image.Rect(0, m.GetRenderHeight()-height, m.GetRenderWidth(), m.GetRenderHeight())
}

// helpDockComponent is the bar itself. A hidden dock keeps a component with
// no rectangle, so the walk still reaches the setting that shows it.
func (m *OS) helpDockComponent() helpComponent {
	rect := m.helpDockBand()
	purpose := "The dock is the status bar: the mode pill, the workspace strip, " +
		"minimized panes, session controls, notifications and whatever cells the " +
		"configuration puts on it. Clicking a cell acts on what it names."
	position := m.Settings.DockbarPosition
	if position == "" {
		position = "top"
	}
	cli := []string{
		"tuios set-config appearance.dockbar_position " + position,
		"tuios list-dock-components",
		"tuios refresh-dock",
	}
	if rect.Empty() {
		purpose = "The dock is hidden, so there is nothing on screen to point at. " +
			"Show it with the first line below. " + purpose
		cli[0] = "tuios set-config appearance.dockbar_position bottom"
	}
	keys := m.helpKeys(
		"prefix_workspace_switcher", "prefix_workspace", "prefix_minimize",
		"prefix_session_switcher", "prefix_detach", "prefix_close_session")
	return helpComponent{
		id:      "dock",
		title:   "Dock",
		purpose: purpose,
		keys:    helpCap(keys, 7),
		allKeys: m.helpKeys(
			"prefix_workspace_switcher", "prefix_workspace", "prefix_minimize",
			"restore_all", "prefix_session_switcher", "prefix_detach",
			"prefix_close_session", "prefix_quit", "prefix_inbox", "prefix_mail",
			"prefix_jump_notif", "prefix_last_message", "prefix_next_attention",
			"prefix_toggle_tiling", "prefix_next_window", "prefix_prev_window"),
		cli:  cli,
		rect: rect,
	}
}

// helpDockCellComponent explains one dock cell. A custom or plugin cell names
// where it came from; a built-in names its own setting.
func (m *OS) helpDockCellComponent(name, parent string, rect image.Rectangle) helpComponent {
	c := helpComponent{id: "dock/" + name, parent: parent, rect: rect}
	switch {
	case strings.HasPrefix(name, config.DockCustomPrefix):
		key := strings.TrimPrefix(name, config.DockCustomPrefix)
		c.title = "Dock cell: " + key
		c.purpose = "A custom dock cell. Its command runs on the refresh rule set for it, " +
			"and clicking it runs the on-click command."
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
		c.purpose = "A cell supplied by a plugin. The plugin owns what it draws and what a " +
			"click does."
		c.cli = []string{"tuios plugins list", "tuios list-dock-components"}
	case name == config.DockComponentWorkspaces:
		c.title = "Workspaces"
		c.purpose = "The workspace strip. Each pill is a workspace and its panes; clicking " +
			"one switches to it, and the arrows step a strip too long for the dock."
		c.keys = m.helpKeys("prefix_workspace", "prefix_workspace_switcher", "next_workspace", "prev_workspace")
		c.cli = []string{"tuios select-workspace 2", "tuios set-workspace-name 2 work",
			"tuios set-config appearance.dock_workspace_tabs true"}
	case name == config.DockComponentWindows:
		c.title = "Minimized panes"
		c.purpose = "One entry per minimized pane. Clicking an entry restores that pane; the " +
			"overflow marker opens the list of entries the dock had no room for."
		for i, hit := range m.dockItemHits {
			if hit.WindowIndex >= 0 && hit.WindowIndex < len(m.Windows) && m.Windows[hit.WindowIndex] != nil {
				c.rows = append(c.rows, helpKey{press: strconv.Itoa(i + 1), desc: m.Windows[hit.WindowIndex].Title()})
			}
		}
		if n := m.dockOverflowHit.Overflowed; n > 0 {
			c.rows = append(c.rows, helpKey{press: "...", desc: strconv.Itoa(n) + " more did not fit; the marker opens them"})
		}
		c.keys = m.helpKeys("prefix_minimize", "restore_all")
		c.cli = []string{"tuios list-windows", "tuios set-config appearance.dock_compact true"}
	case name == config.DockComponentSessionControls:
		c.title = "Session controls"
		c.purpose = "The client controls: open the session list, leave this client (detach), " +
			"close the session, or start a new one. They act on the daemon session, not on one pane."
		c.keys = m.helpKeys("prefix_session_switcher", "prefix_detach", "prefix_close_session", "prefix_quit")
		c.cli = []string{"tuios ls", "tuios detach-client", "tuios kill-session <name>", "tuios new"}
	case name == "messages":
		c.title = "Messages"
		c.purpose = "The newest notification. A click acts on it and its right-hand end " +
			"dismisses it. Notifications also carry what agents ask."
		c.keys = m.helpKeys("prefix_jump_notif", "prefix_last_message", "prefix_inbox", "prefix_mail")
		c.cli = []string{"tuios set-config notifications.duration 4",
			"tuios set-config notifications.error_sticky true"}
	case name == "overflow":
		c.title = "Minimized overflow"
		c.purpose = "The marker for minimized panes the dock could not fit. It opens the " +
			"list that names every one of them."
		c.cli = []string{"tuios list-windows", "tuios set-config appearance.dock_compact true"}
	case name == config.DockComponentClock:
		c.title = "Clock"
		c.purpose = "The current time. Its format and whether it shows are appearance settings."
		c.cli = []string{"tuios set-config appearance.show_clock true",
			"tuios set-config appearance.clock_format 15:04"}
	case name == config.DockComponentCPU:
		c.title = "CPU"
		c.purpose = "The CPU readout. It samples the machine while the dock is shown."
		c.cli = []string{"tuios set-config appearance.show_cpu true"}
	case name == config.DockComponentRAM:
		c.title = "RAM"
		c.purpose = "The memory readout for the whole machine, not for one pane."
		c.cli = []string{"tuios set-config appearance.show_ram true"}
	case name == config.DockComponentTransfers:
		c.title = "Transfers"
		c.purpose = "The paste buffers and the transfers in flight. Clicking it opens the list."
		c.keys = m.helpKeys("prefix_transfers", "paste_buffer", "choose_buffer")
		c.cli = []string{"tuios list-buffers"}
	case name == config.DockComponentSession:
		c.title = "Session name"
		c.purpose = "The daemon session this client shows, its accent, and whether other " +
			"clients are attached. Clicking it opens the session switcher."
		c.keys = m.helpKeys("prefix_session_switcher", "prefix_close_session")
		c.cli = []string{"tuios rename-session <name>", "tuios set-session-accent <colour>", "tuios ls"}
	case name == config.DockComponentHelp:
		c.title = "Help control"
		c.purpose = "The ? control. A click turns hover help on or off, and it is lit while " +
			"hover help is on. It is in no default list; a [dock] list that names \"help\" draws it."
		c.keys = m.helpKeys(config.ActionToggleHoverHelp)
		c.cli = []string{"tuios list-dock-components"}
	case name == config.DockComponentButtons:
		c.title = "Buttons"
		c.purpose = "Buttons for the things a person new to tuios looks for first. Each one " +
			"does what its key does; on a narrow dock they lose their words, then fold into a more button."
		for _, i := range m.dockButtonRow() {
			for _, k := range m.helpKeys(dockButtons[i].action) {
				k.desc = dockButtons[i].label + ": " + k.desc
				c.keys = append(c.keys, k)
			}
		}
		c.cli = []string{"tuios list-dock-components"}
	case name == config.DockComponentMode:
		c.title = "Mode"
		c.purpose = "The input mode: window management or terminal. It answers where the next " +
			"key goes, and it changes while the leader waits for its key."
		c.values = []helpKey{
			{press: "H", desc: "Tiling is on and the next split is stacked."},
			{press: "V", desc: "Tiling is on and the next split is side by side."},
			{press: "HOLD", desc: "The hold key has borrowed window mode while it is held."},
			{press: "HINTS", desc: "Hints mode owns the keyboard."},
			{press: "SIDEBAR", desc: "The rail owns the keyboard."},
			{press: "MULTI", desc: "Multi-copy drives several panes."},
			{press: "row:col", desc: "The focused pane is in copy mode, at that row and column."},
			{press: "Z", desc: "A pane of this workspace is zoomed."},
		}
		c.keys = m.helpKeys("prefix_window", "toggle_help", "prefix_toggle_tiling")
		c.cli = []string{
			"tuios set-config appearance.dock_mode_pill input",
			"tuios set-config appearance.dock_mode_split_letter false",
			"tuios set-config appearance.dock_prefix_indicator badge",
			"tuios keybinds list",
		}
	case name == config.DockComponentTrail:
		c.title = "Workspace counter"
		c.purpose = "Each workspace that holds panes, and how many."
		c.rows = []helpKey{
			{press: "1:1", desc: "Workspace 1 holds one pane."},
			{press: "2:3", desc: "Workspace 2 holds three panes."},
		}
		c.cli = []string{"tuios list-workspaces", "tuios list-dock-components"}
	case name == config.DockComponentTape:
		c.title = "Tape"
		c.purpose = "A tape playing in this session, and its status. It is drawn only while a tape runs."
		c.keys = m.helpKeys("prefix_tape")
		c.cli = []string{"tuios tape list", "tuios tape play <file>"}
	case name == config.DockComponentCopyHelp:
		c.title = "Copy help"
		c.purpose = "The dock's reminder of the copy-mode keys while copy mode is on. It is " +
			"documentation, not a control."
		c.cli = []string{"tuios keybinds list"}
	default:
		c.title = "Dock cell: " + name
		c.purpose = "A built-in dock cell. Its name is what [dock] left, center or right lists."
		c.cli = []string{"tuios list-dock-components", "tuios config edit  # [dock] lists"}
	}
	c.allKeys = c.keys
	c.keys = helpCap(c.keys, 6)
	return c
}

// helpDesktopComponent is the desktop behind the panes. It is last, and its
// rectangle is the whole screen, so anything smaller under the pointer wins.
func (m *OS) helpDesktopComponent() helpComponent {
	return helpComponent{
		id:    "desktop",
		title: "Workspace",
		purpose: "The desktop behind the panes, on the current workspace. Clicking it " +
			"leaves terminal mode. Tiling arranges the panes here, and the scratch pane " +
			"opens over them.",
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
			"tuios list-workspaces",
		},
		rect: image.Rect(0, 0, m.GetRenderWidth(), m.GetRenderHeight()),
	}
}

// helpRectUnion is the smallest rectangle holding every non-empty rect.
func helpRectUnion(rects []image.Rectangle) image.Rectangle {
	var out image.Rectangle
	for _, r := range rects {
		out = out.Union(r)
	}
	return out
}

// helpLayerID is the compositor id of the explainer.
const helpLayerID = "hover-help"

// renderHoverHelp draws the explainer for the target. It runs after the dock
// has drawn, so the rectangles it places against are the ones on screen. It
// stands aside while the leader or one of its menus waits for a key: that
// menu is the next thing read, and two overlays would compete for one glance.
func (m *OS) renderHoverHelp() *lipgloss.Layer {
	h := &m.hoverHelp
	if !h.on || m.MenuOpen() {
		h.rect = image.Rectangle{}
		return nil
	}
	comps := m.buildHelpComponents()
	h.comps = comps
	cur := helpComponentByID(comps, h.target)
	if cur == nil {
		if len(comps) == 0 {
			return nil
		}
		cur = &comps[0]
		h.target = cur.id
	}

	pal := theme.UI()
	width := m.helpExplainerWidth()
	inner := width - 2
	header := m.helpHeaderLines(comps, cur, inner, pal)
	body := m.helpSectionLines(cur, inner, pal)
	footer := m.helpFooterLines(comps, cur, inner, pal, false)

	// The explainer is never taller than the screen. The heading and the
	// footer stay, and the body between them scrolls.
	limit := max(m.GetRenderHeight()-3, 3)
	room := limit - len(header) - 1 - len(footer)
	if room < 1 {
		header = nil
		room = max(limit-1-len(footer), 1)
	}
	if len(body) > room {
		footer = m.helpFooterLines(comps, cur, inner, pal, true)
		room = max(limit-len(header)-1-len(footer)-1, 1)
		header = append(header, m.helpScrollLine(len(body), room, inner, pal))
	}
	h.scroll = min(max(h.scroll, 0), max(len(body)-room, 0))
	visible := body[h.scroll:min(h.scroll+room, len(body))]

	lines := make([]string, 0, len(header)+len(visible)+1+len(footer))
	lines = append(lines, header...)
	lines = append(lines, visible...)
	lines = append(lines, "")
	lines = append(lines, footer...)

	box := lipgloss.NewStyle().
		Background(pal.Surface).
		Foreground(pal.Fg).
		Width(width).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
	boxW, boxH := lipgloss.Width(box), lipgloss.Height(box)
	// At 16 colours the panel ground is the terminal's own, so nothing but a
	// blank cell tells the explainer from its target.
	gap := 0
	if pal.Depth == overlay.Depth16 {
		gap = 1
	}
	// A part's explainer keeps off the whole component when it can: below a
	// rail section is the rest of the rail.
	var keepOff image.Rectangle
	if parent := helpComponentByID(comps, cur.parent); parent != nil {
		keepOff = parent.rect
	}
	x, y := m.placeExplainer(cur.rect, keepOff, boxW, boxH, gap)
	h.rect = image.Rect(x, y, x+boxW, y+boxH)
	return lipgloss.NewLayer(box).X(x).Y(y).Z(config.ZIndexWhichKey + 1).ID(helpLayerID)
}

// helpExplainerWidth is the explainer's width in cells, padding included.
func (m *OS) helpExplainerWidth() int {
	return max(min(60, m.GetRenderWidth()-2), 12)
}

// helpScrollLine says the explanation continues, which lines are shown, and
// how to move it.
func (m *OS) helpScrollLine(total, room, inner int, pal overlay.Palette) string {
	from := min(m.hoverHelp.scroll+1, total)
	to := min(m.hoverHelp.scroll+room, total)
	text := "lines " + strconv.Itoa(from) + "-" + strconv.Itoa(to) + " of " + strconv.Itoa(total)
	return overlay.Style(pal.Surface).Foreground(pal.FgMute).Render(overlay.Truncate(text, inner))
}

// helpHeaderLines is the explainer's fixed top: the target's name, how many
// parts it has, and where it sits.
func (m *OS) helpHeaderLines(comps []helpComponent, cur *helpComponent, inner int, pal overlay.Palette) []string {
	title := overlay.Style(pal.Surface).Foreground(pal.Fg).Bold(true)
	muted := overlay.Style(pal.Surface).Foreground(pal.FgMute)
	heading := cur.title
	if kids := len(helpChildren(comps, cur.id)); kids == 1 {
		heading += " (1 part)"
	} else if kids > 1 {
		heading += " (" + strconv.Itoa(kids) + " parts)"
	}
	lines := []string{title.Render(overlay.Truncate(heading, inner))}
	if path := helpPath(comps, cur); path != cur.title {
		lines = append(lines, muted.Render(overlay.Truncate(path, inner)))
	}
	mode := "window mode"
	if m.Mode == TerminalMode {
		mode = "terminal mode"
	}
	return append(lines, muted.Render(mode))
}

// helpPath is the target's place in its tree, outermost first.
func helpPath(comps []helpComponent, cur *helpComponent) string {
	var parts []string
	for c := cur; c != nil; c = helpComponentByID(comps, c.parent) {
		parts = append(parts, c.title)
		if c.parent == "" {
			break
		}
	}
	slices.Reverse(parts)
	return strings.Join(parts, " › ")
}

// helpSectionLines is the part of the explainer that scrolls: what the target
// is for, what it can show, its keys and the CLI that changes it.
func (m *OS) helpSectionLines(cur *helpComponent, inner int, pal overlay.Palette) []string {
	bg := pal.Surface
	muted := overlay.Style(bg).Foreground(pal.FgMute)
	heading := overlay.Style(bg).Foreground(pal.FgDim).Bold(true)
	body := overlay.Style(bg).Foreground(pal.Fg)
	key := overlay.Style(bg).Foreground(pal.Accent).Bold(true)

	section := func(lines []string, title string, keys []helpKey) []string {
		lines = append(lines, "", heading.Render(title))
		return append(lines, helpKeyLines(keys, inner, key, body, muted)...)
	}

	var lines []string
	if m.hoverHelp.showKeys {
		if len(cur.allKeys) == 0 {
			return append(lines, "", heading.Render("All keys"), muted.Render("No key acts on this."))
		}
		return section(lines, "All keys", cur.allKeys)
	}
	if cur.purpose != "" {
		lines = append(lines, "")
		for _, line := range wrapPlain(cur.purpose, inner) {
			lines = append(lines, body.Render(line))
		}
	}
	if len(cur.values) > 0 {
		lines = section(lines, "What it shows", cur.values)
	}
	if len(cur.rows) > 0 {
		lines = section(lines, "On the bar", cur.rows)
	}
	if len(cur.keys) > 0 {
		lines = section(lines, "Keys", cur.keys)
	}
	if len(cur.cli) > 0 {
		lines = append(lines, "", heading.Render("CLI"))
		for _, command := range cur.cli {
			for i, line := range wrapPlain(command, inner-2) {
				prefix := "  "
				if i == 0 {
					prefix = "$ "
				}
				lines = append(lines, muted.Render(prefix+line))
			}
		}
	}
	return lines
}

// helpKeyLines draws a key column and its descriptions, a long description
// wrapping under itself. A key that cannot act now is drawn greyed out: faint
// as well as muted, since at 16 colours the muted ink can be the terminal's
// own and the attribute is what survives.
func helpKeyLines(keys []helpKey, width int, keyStyle, descStyle, deadStyle lipgloss.Style) []string {
	keyWidth := 0
	for _, k := range keys {
		keyWidth = max(keyWidth, lipgloss.Width(k.press))
	}
	keyWidth = min(keyWidth, max(width/2, 8))
	descWidth := max(width-keyWidth-2, 8)

	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		ks, ds := keyStyle, descStyle
		if k.dead {
			ks, ds = deadStyle.Faint(true), deadStyle.Faint(true)
		}
		press := overlay.Truncate(k.press, keyWidth)
		pad := strings.Repeat(" ", max(keyWidth-lipgloss.Width(press), 0))
		desc := wrapPlain(k.desc, descWidth)
		if len(desc) == 0 {
			desc = []string{""}
		}
		lines = append(lines, ks.Render(press+pad)+ds.Render("  "+desc[0]))
		for _, line := range desc[1:] {
			lines = append(lines, ds.Render(strings.Repeat(" ", keyWidth+2)+line))
		}
	}
	return lines
}

// helpFooterLines is the explainer's fixed bottom: the help keys as the config
// binds them, and the chord that closes it. On a narrow screen only the key
// list and the close chord stay.
func (m *OS) helpFooterLines(comps []helpComponent, cur *helpComponent, inner int, pal overlay.Palette, scrollable bool) []string {
	bg := pal.Surface
	key := overlay.Style(bg).Foreground(pal.Success).Bold(true)
	muted := overlay.Style(bg).Foreground(pal.FgMute)

	type hint struct{ press, desc string }
	var hints []hint
	pair := func(a, b, desc string) {
		switch {
		case a != "" && b != "":
			hints = append(hints, hint{a + "/" + b, desc})
		case a != "":
			hints = append(hints, hint{a, desc})
		case b != "":
			hints = append(hints, hint{b, desc})
		}
	}
	keysHint := hint{m.helpOwnKey(config.ActionHelpKeys), "keys"}
	if m.hoverHelp.showKeys {
		keysHint.desc = "back"
	}
	closeHint := hint{m.helpToggleChord(), "close"}

	if inner >= 40 {
		pair(m.helpOwnKey(config.ActionHelpPrev), m.helpOwnKey(config.ActionHelpNext), "components")
		// The in and out keys do nothing on a component with no parts.
		if cur.parent != "" || len(helpChildren(comps, cur.id)) > 0 {
			pair(m.helpOwnKey(config.ActionHelpShallower), m.helpOwnKey(config.ActionHelpDeeper), "parts")
		}
		if scrollable {
			pair(m.helpOwnKey(config.ActionHelpScrollUp), m.helpOwnKey(config.ActionHelpScrollDown), "scroll")
		}
	}
	for _, h := range []hint{keysHint, closeHint} {
		if h.press != "" {
			hints = append(hints, h)
		}
	}

	const sep = " · "
	var lines []string
	var line strings.Builder
	lineWidth := 0
	for _, h := range hints {
		w := lipgloss.Width(h.press) + 1 + lipgloss.Width(h.desc)
		if lineWidth > 0 && lineWidth+len(sep)+w > inner {
			lines = append(lines, line.String())
			line.Reset()
			lineWidth = 0
		}
		if lineWidth > 0 {
			line.WriteString(muted.Render(sep))
			lineWidth += lipgloss.Width(sep)
		}
		line.WriteString(key.Render(h.press))
		line.WriteString(muted.Render(" " + h.desc))
		lineWidth += w
	}
	return append(lines, line.String())
}

// placeExplainer puts the explainer below its target, then above it, then
// beside it, gap cells away. It first looks for a place that also keeps off
// keepOff, the component a part belongs to, and only then for one that keeps
// off the target alone. Only when none of those fits does it clamp into the
// screen, the one case where it covers what it explains. It keeps off the
// dock unless the target is on the dock.
func (m *OS) placeExplainer(anchor, keepOff image.Rectangle, w, h, gap int) (int, int) {
	screenW, screenH := m.GetRenderWidth(), m.GetRenderHeight()
	clamp := func(x, y int) (int, int) {
		return min(max(x, 0), max(screenW-w, 0)), min(max(y, 0), max(screenH-h, 0))
	}
	// The desktop is the whole screen, so nothing is beside it; the explainer
	// goes in the middle.
	if anchor.Empty() || anchor == image.Rect(0, 0, screenW, screenH) {
		return clamp((screenW-w)/2, (screenH-h)/2)
	}
	dock := m.helpDockBand()
	fits := func(p image.Point) bool {
		r := image.Rect(p.X, p.Y, p.X+w, p.Y+h)
		if !r.In(image.Rect(0, 0, screenW, screenH)) {
			return false
		}
		return dock.Empty() || anchor.Overlaps(dock) || !r.Overlaps(dock)
	}
	cx := min(max(anchor.Min.X+(anchor.Dx()-w)/2, 0), max(screenW-w, 0))
	// Beside the target, the explainer is centred on it but stays within the
	// rows it may use, so a short target such as a rail section still has a
	// place beside it.
	top, bottom := 0, screenH
	if !dock.Empty() && !anchor.Overlaps(dock) {
		if dock.Min.Y <= 0 {
			top = dock.Max.Y
		} else {
			bottom = dock.Min.Y
		}
	}
	cy := min(max(anchor.Min.Y+(anchor.Dy()-h)/2, top), max(bottom-h, top))
	spots := []image.Point{
		{X: cx, Y: anchor.Max.Y + gap},
		{X: cx, Y: anchor.Min.Y - h - gap},
		{X: anchor.Max.X + gap, Y: cy},
		{X: anchor.Min.X - w - gap, Y: cy},
	}
	if !keepOff.Empty() {
		for _, p := range spots {
			if fits(p) && !image.Rect(p.X, p.Y, p.X+w, p.Y+h).Overlaps(keepOff) {
				return p.X, p.Y
			}
		}
	}
	for _, p := range spots {
		if fits(p) {
			return p.X, p.Y
		}
	}
	if anchor.Min.Y >= screenH-anchor.Max.Y {
		return clamp(cx, anchor.Min.Y-h-gap)
	}
	return clamp(cx, anchor.Max.Y+gap)
}
