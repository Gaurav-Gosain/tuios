package app

import (
	"image"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The rail and the panes as hover help explains them. The rail's parts are
// its sections, read from the bands the last frame drew them in; a pane's
// parts are its title bar and its body, read from the pane's own geometry.

// helpScopedKeys is the first key the config binds to each action of one
// table, with the description that action has in its scope. These keys act
// only while that scope has the keyboard, such as the rail after the leader
// then e.
func (m *OS) helpScopedKeys(table config.Table, scope string, actions ...string) []helpKey {
	if m.KeybindRegistry == nil {
		return nil
	}
	out := make([]helpKey, 0, len(actions))
	for _, action := range actions {
		for _, key := range m.KeybindRegistry.Keys(table, action) {
			if key == "" {
				continue
			}
			out = append(out, helpKey{
				press: helpPressLabel(key),
				desc:  config.ScopedDescription(scope, action, 0),
			})
			break
		}
	}
	return out
}

// helpRailBand is the primary rail's column on this frame, or the zero
// rectangle when the rail is hidden.
func (m *OS) helpRailBand() image.Rectangle {
	w := m.GetSidebarWidth()
	if w <= 0 {
		return image.Rectangle{}
	}
	x := sidebarEdgeX(m.legacySidebarEdge(), w, m.GetRenderWidth())
	top := m.viewReserve().Top
	return image.Rect(x, top, x+w, top+m.ViewUsableHeight())
}

// helpRailComponents is the rail and one part per section the last frame
// drew. A hidden rail keeps a component with no rectangle, so the walk still
// reaches the key that shows it.
func (m *OS) helpRailComponents() []helpComponent {
	rect := m.helpRailBand()
	position := m.Settings.SidebarPosition
	if position == "" {
		position = config.DefaultSidebarPosition
	}
	purpose := "The rail is the column beside the panes. It lists the sessions, the " +
		"panes of the session it follows, the agents and the files below the focused " +
		"pane, each in a section of its own. Its keys act once the leader then e has " +
		"moved the keyboard into it."
	if rect.Empty() {
		purpose = "The rail is hidden, so there is nothing on screen to point at. " +
			"The leader then b shows it. " + purpose
	}
	enter := m.helpKeys("prefix_explore", "prefix_toggle_sidebar")
	keys := append(append([]helpKey{}, enter...), m.helpScopedKeys(config.TableSidebar,
		config.ScopeSidebar, "cursor_down", "cursor_up", "section", "activate", "exit")...)
	all := append(append([]helpKey{}, enter...), m.helpKeys("prefix_toggle_other_rail")...)
	all = append(all, m.helpScopedKeys(config.TableSidebar, config.ScopeSidebar,
		"activate", "cursor_down", "cursor_up", "first", "last", "section", "collapse",
		"expand", "menu", "palette", "narrow", "widen", "switch_edge", "help", "exit")...)
	rail := helpComponent{
		id:      "rail",
		title:   "Rail",
		purpose: purpose,
		keys:    helpCap(keys, 7),
		allKeys: all,
		cli: []string{
			"tuios set-config appearance.sidebar.enabled true",
			"tuios set-config appearance.sidebar.position " + position,
			"tuios set-config appearance.sidebar.width 30",
			"tuios set-config appearance.sidebar.sections " + config.SidebarDefaultSections,
		},
		rect: rect,
	}
	out := []helpComponent{rail}
	if rect.Empty() {
		return out
	}
	for s := range sidebarSectionCount {
		band := m.sidebarSectionY[s]
		if band[1] <= band[0] {
			continue
		}
		r := image.Rect(rect.Min.X, band[0], rect.Max.X, band[1]).Intersect(rect)
		if r.Empty() {
			continue
		}
		out = append(out, m.helpRailSection(s, r))
	}
	return out
}

// helpRailSection explains one section of the rail.
func (m *OS) helpRailSection(s sidebarSection, rect image.Rectangle) helpComponent {
	c := helpComponent{parent: "rail", rect: rect}
	moves := m.helpScopedKeys(config.TableSidebar, config.ScopeSidebar,
		"cursor_down", "cursor_up", "section")
	switch s {
	case sidebarSectionSessions:
		c.id, c.title = "rail/sessions", "Sessions"
		c.purpose = "One row per session, on this machine and on the hosts it links to. " +
			"Enter attaches the session under the cursor; the number before a name is " +
			"its jump key. The + in the header makes a new one."
		c.keys = m.helpScopedKeys(config.TableSidebar, config.ScopeSidebar,
			"activate", "new_session", "jump_1", "reorder_down", "reorder_up", "menu")
		c.cli = []string{"tuios ls", "tuios new work", "tuios rename-session work", "tuios kill-session work"}
	case sidebarSectionTerminals:
		c.id, c.title = "rail/terminals", "Terminals"
		c.purpose = "The panes of the session the rail follows. Enter focuses one, r " +
			"renames it, and the + in the header opens another."
		c.keys = m.helpScopedKeys(config.TableSidebar, config.ScopeSidebar,
			"activate", "new_window", "rename", "accent", "kill", "menu")
		c.cli = []string{"tuios new-window", "tuios list-windows", "tuios focus-window <name>"}
	case sidebarSectionAgents:
		c.id, c.title = "rail/agents", "Agents"
		c.purpose = "The agents running in panes: working, waiting for a person, or " +
			"finished. The header's filter and sort pick which ones and in what order."
		c.keys = append(m.helpScopedKeys(config.TableSidebar, config.ScopeSidebar,
			"activate", "agents_filter", "agents_sort", "mail"),
			m.helpScopedKeys(config.TableSidebarAgents, config.ScopeSidebarAgents,
				"agent_reply", "agent_review", "agent_subagents")...)
		c.keys = append(c.keys, m.helpKeys("prefix_inbox", "prefix_next_attention")...)
		c.cli = []string{"tuios list-agents"}
	case sidebarSectionFiles:
		c.id, c.title = "rail/files", "Files"
		c.purpose = "What is in the focused pane's folder. Enter opens a folder, space " +
			"previews a file, and the header's cd sends the pane there."
		c.keys = m.helpScopedKeys(config.TableSidebarFiles, config.ScopeSidebarFiles,
			"file_open", "file_quick_look", "file_edit", "file_copy", "file_cut",
			"file_paste", "file_rename", "file_delete")
		c.keys = append(c.keys, m.helpScopedKeys(config.TableSidebar, config.ScopeSidebar,
			"file_search")...)
	case sidebarSectionGit:
		c.id, c.title = "rail/git", "Git"
		c.purpose = "The repository the focused pane is in, its branch, and how far " +
			"that branch has drifted from its upstream."
	case sidebarSectionCustom:
		c.id, c.title = "rail/custom", "Custom section"
		c.purpose = "Rows printed by a command of your own, set in " +
			"[appearance.sidebar.custom]."
		c.cli = []string{"tuios config edit"}
	case sidebarSectionPane:
		c.id, c.title = "rail/pane", "Rail pane"
		c.purpose = "A live terminal drawn in the rail, set in [appearance.sidebar.pane]. " +
			"Clicking it gives it the keyboard."
		c.cli = []string{"tuios config edit"}
	default:
		c.id, c.title = "rail/section-"+strconv.Itoa(int(s)), "Section"
		c.purpose = "A section of the rail."
	}
	if len(c.cli) == 0 {
		c.cli = []string{"tuios set-config appearance.sidebar.sections " + config.SidebarDefaultSections}
	}
	c.allKeys = append(append([]helpKey{}, c.keys...), moves...)
	c.keys = helpCap(c.keys, 7)
	return c
}

// helpPaneComponents is every pane on the current workspace, each with its
// title bar, when it draws one, and its body.
func (m *OS) helpPaneComponents() []helpComponent {
	var out []helpComponent
	for _, w := range m.Windows {
		if w == nil || w.Workspace != m.CurrentWorkspace || w.Minimized {
			continue
		}
		pane, title, body := m.helpPaneRects(w)
		if pane.Empty() {
			continue
		}
		first := len(out)
		out = append(out, m.helpPaneComponent(w, pane))
		if !title.Empty() {
			out = append(out, m.helpPaneTitleComponent(w, title))
		}
		if !body.Empty() {
			out = append(out, m.helpPaneBodyComponent(w, body))
		}
		// One above the desktop, so a pane at Z 0 still covers it.
		for i := first; i < len(out); i++ {
			out[i].layer = HitZ(w) + 1
		}
	}
	return out
}

// helpPaneRects is where a pane, its title bar and its body are on screen. A
// tiled pane is borderless and has no title bar of its own.
func (m *OS) helpPaneRects(w *terminal.Window) (pane, title, body image.Rectangle) {
	pane = image.Rect(w.X, w.Y, w.X+w.Width, w.Y+w.Height)
	off := w.BorderOffset()
	body = image.Rect(w.X+off, w.Y+off, w.X+off+w.ContentWidth(), w.Y+off+w.ContentHeight())
	if off > 0 && m.Settings.WindowTitlePosition != "hidden" && w.Height > 1 {
		if m.Settings.WindowTitlePosition == "bottom" {
			title = image.Rect(w.X, w.Y+w.Height-1, w.X+w.Width, w.Y+w.Height)
		} else {
			title = image.Rect(w.X, w.Y, w.X+w.Width, w.Y+1)
		}
	}
	if v := m.sessionView; v.on {
		shift := image.Pt(v.dx, v.dy)
		pane = pane.Add(shift).Intersect(v.clip)
		title = title.Add(shift).Intersect(v.clip)
		body = body.Add(shift).Intersect(v.clip)
	}
	return pane, title, body
}

// helpPaneName is the name a pane's pages call it by.
func helpPaneName(w *terminal.Window) string {
	if name := w.Title(); name != "" {
		return name
	}
	return w.ID
}

// helpShellQuote quotes a pane name for a shell line, so a name with spaces
// reaches the CLI as one argument.
func helpShellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (m *OS) helpPaneComponent(w *terminal.Window, rect image.Rectangle) helpComponent {
	name := helpPaneName(w)
	keys := m.helpKeys("prefix_fullscreen", "split_vertical", "split_horizontal",
		"close_window", "prefix_minimize", "prefix_next_window", "prefix_prev_window")
	return helpComponent{
		id:    "pane:" + w.ID,
		title: "Pane: " + name,
		purpose: "One terminal. It runs its own program; in terminal mode the keys go " +
			"to that program, and in window mode they move and arrange the panes. The " +
			"title bar names it, when it has one, and the body is the terminal.",
		keys: helpCap(keys, 7),
		allKeys: append(keys, m.helpKeys("prefix_window", "prefix_layout",
			"prefix_toggle_tiling", "prefix_rename_window")...),
		cli: []string{
			"tuios split-window vertical",
			"tuios set-layout <layout>",
			"tuios close-window " + helpShellQuote(name),
		},
		rect: rect,
	}
}

func (m *OS) helpPaneTitleComponent(w *terminal.Window, rect image.Rectangle) helpComponent {
	name := helpPaneName(w)
	keys := m.helpKeys("prefix_rename_window", "prefix_fullscreen", "prefix_minimize",
		"close_window")
	return helpComponent{
		id:     "pane:" + w.ID + "/title",
		parent: "pane:" + w.ID,
		title:  "Title bar: " + name,
		purpose: "The pane's title bar. It names the pane and carries its close, " +
			"minimize and zoom buttons. Dragging it moves a floating pane.",
		keys:    keys,
		allKeys: append(keys, m.helpKeys("prefix_window")...),
		cli: []string{
			"tuios set-window --name work",
			"tuios set-config appearance.window_title_position top",
			"tuios set-config appearance.window_title_style pill",
			"tuios set-config appearance.hide_window_buttons false",
			"tuios set-config appearance.window_button_position right",
		},
		rect: rect,
	}
}

func (m *OS) helpPaneBodyComponent(w *terminal.Window, rect image.Rectangle) helpComponent {
	name := helpPaneName(w)
	keys := m.helpKeys("prefix_selection", "prefix_scrollback", "prefix_screenshot",
		"split_vertical", "split_horizontal")
	return helpComponent{
		id:     "pane:" + w.ID + "/body",
		parent: "pane:" + w.ID,
		title:  "Terminal: " + name,
		purpose: "The terminal itself. In terminal mode every key but the leader goes " +
			"to its program. Copy mode moves over the scrollback without sending " +
			"anything.",
		keys:    keys,
		allKeys: append(keys, m.helpKeys("prefix_fullscreen", "close_window")...),
		cli: []string{
			"tuios send-keys <text>",
			"tuios capture-pane -w " + helpShellQuote(name),
			"tuios set-config appearance.dim_unfocused 20",
		},
		rect: rect,
	}
}
