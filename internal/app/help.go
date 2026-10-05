package app

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

// HelpBinding represents a single keybinding for the help menu
type HelpBinding struct {
	Action      string   // Action name (e.g., "new_window")
	Keys        []string // Keybindings (e.g., ["n", "ctrl+n"])
	Description string   // Human-readable description
	Category    string   // Category name
}

// HelpCategory represents a category of keybindings
type HelpCategory struct {
	Name     string        // Display name
	Bindings []HelpBinding // Bindings in this category
}

// GetHelpCategories generates all help categories from the keybind registry
func GetHelpCategories(registry *config.KeybindRegistry, s *config.Settings) []HelpCategory {
	categories := []HelpCategory{
		{
			Name: "Window Management",
			Bindings: generateCategoryBindings(registry, "Window Management", []string{
				"new_window", "new_window_ssh", "close_window", "rename_window",
				"minimize_window", "restore_all", "toggle_zoom", "toggle_pip", "toggle_spotlight",
				"next_window", "prev_window",
				"terminal_next_window", "terminal_prev_window",
				"terminal_focus_left", "terminal_focus_right",
				"terminal_focus_up", "terminal_focus_down",
				"copy_selection", "focus_sidebar",
				"next_session", "prev_session",
				"toggle_multifocus_active", "toggle_multifocus_all", "close_workspace",
			}),
		},
		{
			Name:     "Workspaces",
			Bindings: generateWorkspaceBindings(registry, s),
		},
		{
			Name: "Layout",
			Bindings: generateCategoryBindings(registry, "Layout", []string{
				"snap_left", "snap_right", "focus_up", "focus_down",
				"snap_fullscreen", "unsnap",
				"snap_corner_1", "snap_corner_2", "snap_corner_3", "snap_corner_4",
			}),
		},
		{
			Name: "Tiling",
			Bindings: generateCategoryBindings(registry, "Tiling", []string{
				"toggle_tiling", "swap_left", "swap_right", "swap_up", "swap_down",
				"resize_master_shrink", "resize_master_grow", "resize_height_shrink", "resize_height_grow",
				"resize_master_shrink_left", "resize_master_grow_left", "resize_height_shrink_top", "resize_height_grow_top",
				"cycle_master_position", "swap_with_master", "focus_master", "add_master", "remove_master",
			}),
		},
		{
			Name: "BSP",
			Bindings: generateCategoryBindings(registry, "BSP", []string{
				"split_horizontal", "split_vertical", "split_ssh_horizontal", "split_ssh_vertical",
				"rotate_split", "equalize_splits",
				"preselect_left", "preselect_right", "preselect_up", "preselect_down",
			}),
		},
		{
			Name:     "Mouse",
			Bindings: generateMouseBindings(),
		},
		{
			Name:     "Sidebar",
			Bindings: generateSidebarBindings(registry, s),
		},
		{
			Name:     HelpCategoryAgents,
			Bindings: generateAgentBindings(registry, s),
		},
		{
			// Hints mode's keys ride along here rather than in a tab of their
			// own, for the reason the list keys ride in Modes below.
			Name:     HelpCategoryCopyMode,
			Bindings: append(generateCopyModeBindings(s), generateHintsBindings(registry, s)...),
		},
		{
			Name: "Modes",
			// The list keys ride along here rather than in a tab of their own:
			// the strip is one row wide at a desktop width, and there is no
			// room for another tab on it.
			Bindings: append(append(generateCategoryBindings(registry, "Modes", []string{
				"enter_terminal_mode", "enter_window_mode",
				"terminal_exit_mode",
				"toggle_help", "quit",
			}), generateSpotlightOffBindings(registry, s)...), append(messageViewKeys(), generateListBindings()...)...),
		},
		{
			Name:     "Debug",
			Bindings: generateDebugBindings(s),
		},
		{
			Name:     "Tape",
			Bindings: generateTapeBindings(s),
		},
		{
			Name:     "Prefix",
			Bindings: generatePrefixBindings(registry, s),
		},
	}

	// Filter out empty categories
	filteredCategories := []HelpCategory{}
	for _, cat := range categories {
		if len(cat.Bindings) > 0 {
			filteredCategories = append(filteredCategories, cat)
		}
	}

	return filteredCategories
}

// HelpCategories is the help overlay's sections for this client. The Agents
// section waits until an agent has been seen, like the prefix menu's Inbox
// lines: its keys mean nothing before, and the tab strip is one row wide
// without it.
//
// On a daemon that cannot review a pane's changes, the review's keys are left
// out of the Agents section, since each would do what an unbound key does.
func (m *OS) HelpCategories() []HelpCategory {
	cats := GetHelpCategories(m.KeybindRegistry, &m.Settings)
	if !m.agentsOn() {
		cats = withoutAgentHelp(cats)
	}
	if !m.agentsSeen() {
		return slices.DeleteFunc(cats, func(c HelpCategory) bool { return c.Name == HelpCategoryAgents })
	}
	if !m.reviewSupported() {
		for i := range cats {
			if cats[i].Name == HelpCategoryAgents {
				cats[i].Bindings = slices.DeleteFunc(slices.Clone(cats[i].Bindings), isReviewHelpBinding)
			}
		}
	}
	// The Agents settings key is left out where the tab is. See
	// agentsPageAvailable.
	if !m.agentsPageAvailable() {
		for i := range cats {
			if cats[i].Name == HelpCategoryAgents {
				cats[i].Bindings = slices.DeleteFunc(slices.Clone(cats[i].Bindings), func(b HelpBinding) bool {
					return b.Description == config.ActionDescriptions["prefix_agents_settings"]
				})
			}
		}
	}
	return cats
}

// The help lines of the review's keys outside the overlay. The overlay's own
// keys are the lines starting helpReviewPrefix.
const (
	helpRailReview   = "Rail agent row: review the pane's changes"
	helpInboxReview  = "Inbox: review the changes in the item's pane"
	helpReviewPrefix = "Review: "
)

// isReviewHelpBinding reports whether a help line is one of the review's.
func isReviewHelpBinding(b HelpBinding) bool {
	return b.Description == config.ActionDescriptions[config.ActionPrefixReview] ||
		b.Description == helpRailReview || b.Description == helpInboxReview ||
		strings.HasPrefix(b.Description, helpReviewPrefix)
}

// HelpCategoryAgents is the section gathering every key that deals with
// agents: the prefix chords that reach the Inbox, the rail's agent controls,
// the palette's state filter, and the keys inside the Inbox, its prompt and
// the mailbox. They were spread over Prefix and Rail, where the same letter
// means different things, and the Inbox's own keys were in no section.
const HelpCategoryAgents = "Agents"

// generateAgentBindings lists the Agents section. Every key is read from the
// config, chord included, so a rebound key shows as it is bound.
func generateAgentBindings(registry *config.KeybindRegistry, s *config.Settings) []HelpBinding {
	const cat = HelpCategoryAgents
	presses := config.PressesByAction(registry)
	var bindings []HelpBinding
	add := func(keys []string, desc string) {
		var live []string
		for _, k := range keys {
			if k = strings.TrimSpace(k); k != "" {
				live = append(live, k)
			}
		}
		if len(live) > 0 {
			bindings = append(bindings, HelpBinding{Keys: live, Description: desc, Category: cat})
		}
	}
	for _, action := range []string{"prefix_inbox", "prefix_next_attention", "prefix_next_finished", "prefix_review", "prefix_mail", "prefix_jump_notif", "prefix_agents_settings"} {
		desc := config.ActionDescriptions[action]
		add(presses[action], desc)
	}
	if keys := presses["command_palette"]; len(keys) > 0 {
		add([]string{keys[0] + ", @"}, "Palette: panes running an agent; @n needs you, @w working, @d done, @i idle, @e errored, @a needs you or errored")
	}
	railKey := func(action string) []string {
		var out []string
		for _, k := range registry.GetSidebarKeys(action) {
			out = append(out, "rail "+k)
		}
		return out
	}
	add(railKey("agents_filter"), "Rail agents: all sessions, or this one")
	add(railKey("agents_sort"), "Rail agents: needs you, priority, or recency")
	add(railKey("mail"), "Rail: the mailbox of the pane under the cursor")
	agentRowKey := func(action string) []string {
		var out []string
		for _, k := range registry.GetSidebarAgentsKeys(action) {
			out = append(out, "rail "+k)
		}
		return out
	}
	add(agentRowKey(config.ActionAgentReply), "Rail agent row: reply to the agent")
	add(agentRowKey(config.ActionAgentReview), helpRailReview)
	add(agentRowKey(config.ActionAgentUnread), "Rail agent row: mark its finished turn unread")
	add(agentRowKey(config.ActionAgentSnooze), "Rail agent row: snooze its Inbox item")
	add(agentRowKey(config.ActionAgentCancelQueued), "Rail agent row: drop the newest queued message")

	inbox := func(action, desc string) {
		add(registry.GetInboxKeys(action), desc)
	}
	add([]string{"1-9"}, "Inbox: answer a held approval or a question by its number")
	inbox(config.ActionInboxPeek, "Inbox: read an approval's or a question's prompt and answer it")
	inbox(config.ActionInboxGo, "Inbox: go to the item's pane, or open its mail")
	inbox(config.ActionInboxDismiss, "Inbox: dismiss the item")
	inbox(config.ActionInboxReply, "Inbox: reply to mail")
	inbox(config.ActionInboxResume, "Inbox: resume a conversation a restart left")
	inbox(config.ActionInboxPassOn, "Inbox: pass held mail on to its agent")
	inbox(config.ActionInboxFilter, "Inbox: show one kind, then the next")
	inbox(config.ActionInboxSelect, "Inbox: narrow the list with a selector")
	inbox(config.ActionInboxMailbox, "Inbox: open the whole mailbox")
	inbox(config.ActionInboxReview, helpInboxReview)
	inbox(config.ActionInboxSnooze, "Inbox: snooze the item, then 1 to 4 for how long")
	inbox(config.ActionInboxUndo, "Inbox: undo the last dismiss or snooze")
	inbox(config.ActionInboxShowSnoozed, "Inbox: show or hide snoozed items")
	inbox(config.ActionInboxDenyReason, "Inbox: deny an approval or a plan with a reason")
	inbox(config.ActionPeekApprove, "Prompt: approve")
	inbox(config.ActionPeekApproveAlways, "Prompt: approve and do not ask again")
	inbox(config.ActionPeekDeny, "Prompt: deny")
	inbox(config.ActionPeekType, "Prompt: type an answer")
	inbox(config.ActionMailReply, "Mailbox: reply in the open thread")
	inbox(config.ActionMailFocusPane, "Mailbox: go to the pane that last wrote")
	inbox(config.ActionMailNew, "Mailbox: write a new message to an agent")
	// The review overlay's own keys, which are not bindings, like the
	// scrollback browser's. See review_input.go.
	add([]string{"] [", "} {"}, "Review: next or previous hunk, next or previous file")
	add([]string{"c", "C"}, "Review: a note on the line, or on the whole hunk")
	add([]string{"e", "x"}, "Review: edit or resolve the note under the cursor")
	add([]string{"S"}, "Review: send the unsent notes to the agent, typed when it is at rest")
	add([]string{"u", "b"}, "Review: uncommitted changes only, or another base")
	add([]string{"w"}, "Review: compare a fan's attempts; m marks, d diffs two, V checks all, K keeps one")
	return bindings
}

// HelpCategorySidebar is the name of the section listing the rail's keys, used
// by the rail's own help key to open the overlay already on it.
const HelpCategorySidebar = "Sidebar"

// OpenHelpAtCategory shows the help overlay with a named section selected. The
// rail has its own keys and no way to discover them from inside it, so its help
// key opens this one overlay on the rail's section rather than a second surface
// that would have to be kept in step with it. An unknown name falls back to the
// usual auto-selection.
func (m *OS) OpenHelpAtCategory(name string) {
	m.ShowHelp = true
	m.HelpScrollOffset = 0
	m.HelpSearchMode = false
	m.HelpSearchQuery = ""
	m.HelpCategory = -1
	for i, cat := range m.HelpCategories() {
		if cat.Name == name {
			m.HelpCategory = i
			return
		}
	}
}

// HelpStepCategory moves the help's section strip by delta under the list
// rule, so left on the first section goes to the last. A section not yet
// chosen (-1, before the first frame picked one) steps from the first.
func (m *OS) HelpStepCategory(delta int) {
	n := len(m.HelpCategories())
	m.HelpCategory = m.listStep(max(m.HelpCategory, 0), delta, n)
}

// generateCategoryBindings generates bindings for a specific category
func generateCategoryBindings(registry *config.KeybindRegistry, categoryName string, actions []string) []HelpBinding {
	presses := config.PressesByAction(registry)
	bindings := []HelpBinding{}
	for _, action := range actions {
		// The whole chord, not the bare key: corner snapping is reached with
		// the layout prefix, and listing its key as "1" would tell the reader
		// that 1 snaps a window when 1 selects one.
		keys := presses[action]
		if len(keys) == 0 {
			continue // Skip unbound actions
		}

		desc := config.ActionDescriptions[action]
		if desc == "" {
			desc = formatActionName(action)
		}

		bindings = append(bindings, HelpBinding{
			Action:      action,
			Keys:        keys,
			Description: desc,
			Category:    categoryName,
		})
	}
	return bindings
}

// generateWorkspaceBindings generates all workspace-related bindings
func generateWorkspaceBindings(registry *config.KeybindRegistry, s *config.Settings) []HelpBinding {
	bindings := []HelpBinding{}

	// Add all 9 workspace switches
	for i := 1; i <= 9; i++ {
		action := fmt.Sprintf("switch_workspace_%d", i)
		keys := registry.GetKeys(action)
		if len(keys) > 0 {
			bindings = append(bindings, HelpBinding{
				Action:      action,
				Keys:        keys,
				Description: fmt.Sprintf("Switch to workspace %d", i),
				Category:    "Workspaces",
			})
		}
	}

	// Add all 9 move and follow actions
	for i := 1; i <= 9; i++ {
		action := fmt.Sprintf("move_and_follow_%d", i)
		keys := registry.GetKeys(action)
		if len(keys) > 0 {
			bindings = append(bindings, HelpBinding{
				Action:      action,
				Keys:        keys,
				Description: fmt.Sprintf("Move to workspace %d and follow", i),
				Category:    "Workspaces",
			})
		}
	}

	// Renaming a workspace is a chord rather than a plain key, so its row is
	// built from the same whole-chord hint the pill menu shows.
	if chord := contextMenuHint(registry, "workspace_prefix_rename", s); chord != "" {
		bindings = append(bindings, HelpBinding{
			Action:      "workspace_prefix_rename",
			Keys:        []string{chord},
			Description: "Rename workspace",
			Category:    "Workspaces",
		})
	}

	return bindings
}

// generateMouseBindings lists the pointer gestures. They are written out rather
// than derived because there is no registry for them: the gestures are decided
// by internal/input's press/motion/release handlers, and this list is the only
// place a user can read what those handlers do. Rail gestures live in the
// Sidebar section, next to the rail's keys.
func generateMouseBindings() []HelpBinding {
	const cat = "Mouse"
	return []HelpBinding{
		{Keys: []string{"click"}, Description: "Focus a pane and start typing in it", Category: cat},
		{Keys: []string{"double / triple click"}, Description: "Select the word / line under the pointer", Category: cat},
		{Keys: []string{"drag title bar"}, Description: "Move a pane", Category: cat},
		{Keys: []string{"alt+drag"}, Description: "Move a pane, even while typing in it", Category: cat},
		{Keys: []string{"alt+right-drag"}, Description: "Resize a pane. Never opens the menu", Category: cat},
		{Keys: []string{"ctrl+drag"}, Description: "Move a pane. Drops when you release ctrl", Category: cat},
		{Keys: []string{"ctrl+shift+click"}, Description: "Add or remove a pane from multi-select", Category: cat},
		{Keys: []string{"drag pane border"}, Description: "Resize one edge: divider or floating side", Category: cat},
		{Keys: []string{"right-drag"}, Description: "Resize a pane from the nearest corner", Category: cat},
		{Keys: []string{"right-click"}, Description: "Pane menu, if the program ignores the mouse", Category: cat},
		// One badge rather than two: the key column drops the second when a row
		// carries more than it can hold, and losing the shift half would read as
		// if ctrl were the only way in.
		{Keys: []string{"ctrl/shift + right-click"}, Description: "Pane menu, even if the program uses the mouse", Category: cat},
		{Keys: []string{"ctrl+click a link"}, Description: "Open the link. See appearance.link_click", Category: cat},
		{Keys: []string{"hover a link"}, Description: "Show where the link goes", Category: cat},
		{Keys: []string{"wheel"}, Description: "Scroll the scrollback, or the program", Category: cat},
		{Keys: []string{"drag right edge"}, Description: "Drag the scrollbar, if there is one", Category: cat},
		{Keys: []string{"right-click desktop"}, Description: "Desktop menu", Category: cat},
		{Keys: []string{"click dock entry"}, Description: "Restore that minimized window", Category: cat},
		{Keys: []string{"right-click dock"}, Description: "Dock menu, or the entry's own menu", Category: cat},
		{Keys: []string{"right-click workspace tab"}, Description: "Switch or rename that workspace", Category: cat},
		{Keys: []string{"drag a panel"}, Description: "Move the panel. Click outside to close", Category: cat},
	}
}

// generateSidebarBindings lists the rail: how to reach its keyboard scope, the
// keys inside it, and the gestures that do the same jobs with the pointer. The
// keys come from the [keybindings.sidebar] section, which the global keymap
// deliberately does not carry, so they are read through GetSidebarKeys.
func generateSidebarBindings(registry *config.KeybindRegistry, s *config.Settings) []HelpBinding {
	const cat = "Sidebar"
	// The descriptions are read from config.ScopedDescription, which `tuios
	// keybinds list` reads too, so the two say the same thing.
	row := func(action string) HelpBinding {
		return HelpBinding{
			Action:      action,
			Keys:        registry.GetSidebarKeys(action),
			Description: config.ScopedDescription(config.ScopeSidebar, action),
			Category:    cat,
		}
	}

	bindings := generateCategoryBindings(registry, cat, []string{"focus_sidebar"})
	for _, action := range []string{"prefix_explore", "prefix_toggle_sidebar"} {
		for _, key := range registry.GetKeys(action) {
			desc := config.ActionDescriptions[action]
			if desc == "" {
				desc = formatActionName(action)
			}
			bindings = append(bindings, HelpBinding{
				Action:      action,
				Keys:        []string{s.LeaderKey + ", " + key},
				Description: desc,
				Category:    cat,
			})
		}
	}

	bindings = append(bindings,
		row("exit"),
		row("cursor_down"),
		row("cursor_up"),
		row("first"),
		row("last"),
		row("collapse"),
		row("expand"),
		row("activate"),
	)
	if first, last := registry.GetSidebarKeys("jump_1"), registry.GetSidebarKeys("jump_9"); len(first) > 0 && len(last) > 0 {
		bindings = append(bindings, HelpBinding{
			Keys:        []string{first[0] + "-" + last[0]},
			Description: "Jump to a session by its position in the rail",
			Category:    cat,
		})
	}
	bindings = append(bindings,
		row("reorder_down"),
		row("reorder_up"),
		row("section"),
		row("agents_filter"),
		row("agents_sort"),
		row("file_search"),
		row("mail"),
		row("palette"),
		row("narrow"),
		row("widen"),
		row("new_session"),
		row("new_window"),
		row("menu"),
		row("kill"),
		row("rename"),
		row("accent"),
		row("help"),
	)

	// The files section's own keys, which act only on a row of the listing.
	// They are read from their own section for the reason that section exists:
	// three of them share a key with a rail binding above, so the map the rail
	// uses cannot hold them. See getDefaultSidebarFilesKeybinds.
	fileRow := func(action, desc string) HelpBinding {
		return HelpBinding{
			Action:      action,
			Keys:        registry.GetSidebarFilesKeys(action),
			Description: desc,
			Category:    cat,
		}
	}
	bindings = append(bindings,
		fileRow("file_create", "Files: make a file, or a folder with a / at the end"),
		fileRow("file_rename", "Files: rename the file under the cursor"),
		fileRow("file_delete", "Files: delete the file under the cursor"),
		fileRow("file_delete_forever", "Files: delete for good, with no trash"),
		fileRow("file_copy", "Files: copy the file under the cursor"),
		fileRow("file_cut", "Files: cut the file under the cursor"),
		fileRow("file_paste", "Files: paste into the folder on screen"),
		fileRow("file_open", "Files: open the folder, or copy the file path"),
		fileRow("file_edit", "Files: edit a text file in the configured editor"),
		fileRow("file_copy_path", "Files: copy the path of the file or folder"),
	)

	// Drop rows whose action is unbound, exactly as generateCategoryBindings does.
	bound := bindings[:0]
	for _, b := range bindings {
		if len(b.Keys) > 0 {
			bound = append(bound, b)
		}
	}
	bindings = bound

	return append(bindings, sidebarGestures()...)
}

// sidebarGestures are the rail's pointer gestures. They are not bindings.
func sidebarGestures() []HelpBinding {
	const cat = "Sidebar"
	return []HelpBinding{
		{Keys: []string{"click a row"}, Description: "Attach to that session, or focus that pane", Category: cat},
		{Keys: []string{"hover a session"}, Description: "Preview its panes in the terminals section", Category: cat},
		{Keys: []string{"drag a session"}, Description: "Reorder the sessions", Category: cat},
		{Keys: []string{"right-click a row"}, Description: "Open that row's menu", Category: cat},
		{Keys: []string{"click a header's +"}, Description: "New session, or new terminal in the one shown", Category: cat},
		{Keys: []string{"click blank rail"}, Description: "Give the rail the keyboard", Category: cat},
		{Keys: []string{"drag the rail edge"}, Description: "Resize the rail", Category: cat},
		{Keys: []string{"hover a clipped row"}, Description: "Scroll its text past the edge to read the rest", Category: cat},
		{Keys: []string{"wheel"}, Description: "Scroll the rail", Category: cat},
	}
}

// generateListBindings is the keys every list and panel shares (see
// internal/listnav) and the settings page's own. They are fixed keys of the
// panels rather than registry actions, so they are written out here. They are
// listed in the Modes section; see GetHelpCategories.
func generateListBindings() []HelpBinding {
	const cat = "Modes"
	return []HelpBinding{
		{Keys: []string{"up", "down", "ctrl+p", "ctrl+n"}, Description: "Lists: move; at either end, wrap round", Category: cat},
		{Keys: []string{"home", "end"}, Description: "Lists: first / last row", Category: cat},
		{Keys: []string{"pgup", "pgdown"}, Description: "Lists: a page, stopping at the ends", Category: cat},
		{Keys: []string{"k", "j", "g", "G"}, Description: "Lists: up, down, first, last, with no filter", Category: cat},
		{Keys: []string{"ctrl+u"}, Description: "Lists: clear a typed filter", Category: cat},
		{Keys: []string{"wheel"}, Description: "Lists: scroll the list under the pointer", Category: cat},
		{Keys: []string{"/"}, Description: "Settings: search every tab", Category: cat},
		{Keys: []string{"1-9"}, Description: "Settings: go to that tab", Category: cat},
		{Keys: []string{"backspace"}, Description: "Settings: reset the row to its default", Category: cat},
		{Keys: []string{"ctrl+z"}, Description: "Settings: undo the last change", Category: cat},
		{Keys: []string{"tab"}, Description: "Settings search: go to the row's tab", Category: cat},
	}
}

// generateCopyModeBindings generates copy mode keybindings
func generateCopyModeBindings(s *config.Settings) []HelpBinding {
	return copyModeKeys(s.LeaderKey)
}

// copyModeKeys are copy mode's keys. They are not bindings: copy mode reads
// them itself (see internal/input/copymode_handlers.go).
func copyModeKeys(leader string) []HelpBinding {
	return []HelpBinding{
		{Keys: []string{leader + ", ["}, Description: "Enter copy mode", Category: "Copy Mode"},
		{Keys: []string{"h, j, k, l"}, Description: "Move cursor", Category: "Copy Mode"},
		{Keys: []string{"w, b, e"}, Description: "Word fwd/back/end", Category: "Copy Mode"},
		{Keys: []string{"f, F, t, T"}, Description: "Jump to a character on the line", Category: "Copy Mode"},
		{Keys: []string{"%"}, Description: "Matching bracket", Category: "Copy Mode"},
		{Keys: []string{"0, ^, $"}, Description: "Line start/first/end", Category: "Copy Mode"},
		{Keys: []string{"gg, G"}, Description: "Jump top/bottom", Category: "Copy Mode"},
		{Keys: []string{"ctrl+u, ctrl+d"}, Description: "Half page up/down", Category: "Copy Mode"},
		{Keys: []string{"/, ?"}, Description: "Search forward/backward", Category: "Copy Mode"},
		{Keys: []string{"n, N"}, Description: "Repeat search/reverse", Category: "Copy Mode"},
		{Keys: []string{"v, V"}, Description: "Visual char/line", Category: "Copy Mode"},
		{Keys: []string{"y, c"}, Description: "Yank to clipboard", Category: "Copy Mode"},
		{Keys: []string{"i, q, Esc"}, Description: "Exit copy mode", Category: "Copy Mode"},
	}
}

// HelpCategoryCopyMode is the section listing copy mode's keys and hints
// mode's. The ? key in hints mode opens the help on it, because the dock's
// legend has room for only some of hints mode's keys on a narrow screen.
const HelpCategoryCopyMode = "Copy Mode"

// helpHintsPrefix starts every hints mode line of the Copy Mode section, so
// HintsShowKeys can find the first of them.
const helpHintsPrefix = "Hints: "

// generateHintsBindings lists hints mode's keys. The keys that open it are
// read from the config. The keys inside it are not bindings (see
// handleHintsKey in internal/input).
func generateHintsBindings(registry *config.KeybindRegistry, s *config.Settings) []HelpBinding {
	const cat = HelpCategoryCopyMode
	var bindings []HelpBinding
	presses := config.PressesByAction(registry)
	for _, open := range []struct{ action, desc string }{
		{"hints", helpHintsPrefix + "label the text on the pane"},
		{"hints_all_panes", helpHintsPrefix + "label the text on all panes"},
	} {
		if keys := presses[open.action]; len(keys) > 0 {
			bindings = append(bindings, HelpBinding{Action: open.action, Keys: keys, Description: open.desc, Category: cat})
		}
	}
	return append(bindings, hintsModeKeys(s.LeaderKey)...)
}

// hintsModeKeys are the keys inside hints mode. They are not bindings (see
// handleHintsKey in internal/input).
func hintsModeKeys(leader string) []HelpBinding {
	const cat = HelpCategoryCopyMode
	return []HelpBinding{
		{Keys: []string{"label"}, Description: helpHintsPrefix + "copy the text the label is on", Category: cat},
		{Keys: []string{"shift+label"}, Description: helpHintsPrefix + "copy the text and type it into the pane", Category: cat},
		{Keys: []string{"ctrl+label"}, Description: helpHintsPrefix + "open the link or the file, or copy other text", Category: cat},
		{Keys: []string{"backspace"}, Description: helpHintsPrefix + "remove the last letter you typed", Category: cat},
		{Keys: []string{hintsHelpKey}, Description: helpHintsPrefix + "close hints and show these keys", Category: cat},
		{Keys: []string{"esc", "q", leader}, Description: helpHintsPrefix + "close hints. q closes only when no label uses q", Category: cat},
	}
}

// generateDebugBindings generates debug keybindings
func generateDebugBindings(s *config.Settings) []HelpBinding {
	return []HelpBinding{
		{Keys: []string{s.LeaderKey + ", D, l"}, Description: "Toggle log viewer", Category: "Debug"},
		{Keys: []string{s.LeaderKey + ", D, c"}, Description: "Toggle cache stats", Category: "Debug"},
		{Keys: []string{s.LeaderKey + ", D, k"}, Description: "Toggle showkeys", Category: "Debug"},
		{Keys: []string{s.LeaderKey + ", D, a"}, Description: "Toggle animations", Category: "Debug"},
	}
}

// generateTapeBindings generates tape scripting bindings
func generateTapeBindings(s *config.Settings) []HelpBinding {
	bindings := []HelpBinding{}

	// Add tape commands with prefix notation
	bindings = append(bindings, HelpBinding{
		Action:      "tape_manager",
		Keys:        []string{s.LeaderKey + ", T, m"},
		Description: "Open tape manager",
		Category:    "Tape Scripting",
	})
	bindings = append(bindings, HelpBinding{
		Action:      "tape_record",
		Keys:        []string{s.LeaderKey + ", T, r"},
		Description: "Start recording",
		Category:    "Tape Scripting",
	})
	bindings = append(bindings, HelpBinding{
		Action:      "tape_stop",
		Keys:        []string{s.LeaderKey + ", T, s"},
		Description: "Stop recording",
		Category:    "Tape Scripting",
	})

	return bindings
}

// generateSpotlightOffBindings lists the ways out of the spotlight: esc in
// window mode, the leader chord in either mode, and a click on the dock's
// chip. See spotlight_exit.go.
func generateSpotlightOffBindings(registry *config.KeybindRegistry, s *config.Settings) []HelpBinding {
	fixed := spotlightOffKeys()
	out := []HelpBinding{fixed[0]}
	if keys := registry.GetKeys("prefix_toggle_spotlight"); len(keys) > 0 {
		out = append(out, HelpBinding{
			Action:      "spotlight_off",
			Keys:        []string{s.LeaderKey + ", " + keys[0]},
			Description: "Turn off spotlight, in any mode",
			Category:    "Modes",
		})
	}
	return append(out, fixed[1:]...)
}

// spotlightOffKeys are the ways out of the spotlight that are not bindings.
func spotlightOffKeys() []HelpBinding {
	return []HelpBinding{
		{Action: "spotlight_off", Keys: []string{"esc"}, Description: "Turn off spotlight, in window mode", Category: "Modes"},
		{Action: "spotlight_off", Keys: []string{"click the Spotlight chip"}, Description: "Turn off spotlight, in any mode", Category: "Modes"},
	}
}

// messageViewKeys are the message view's keys (ctrl+b N, or a click on a long
// message). They are not bindings: see handleMessageViewKey in internal/input.
func messageViewKeys() []HelpBinding {
	const cat = "Modes"
	return []HelpBinding{
		{Keys: []string{"j", "k", "up", "down"}, Description: "Message view: scroll one line", Category: cat},
		{Keys: []string{"ctrl+d", "ctrl+u", "space", "pgdown", "pgup"}, Description: "Message view: scroll half a page", Category: cat},
		{Keys: []string{"g", "G", "home", "end"}, Description: "Message view: first or last line", Category: cat},
		{Keys: []string{"y"}, Description: "Message view: copy the message", Category: cat},
		{Keys: []string{"enter"}, Description: "Message view: go to the pane the message came from", Category: cat},
		{Keys: []string{"esc", "q"}, Description: "Message view: close", Category: cat},
	}
}

// FixedKeyGroup is one context's keys that are not registry bindings: copy
// mode's motions, hints mode's labels, the message view, the list keys, and
// the pointer gestures. ID is stable, for JSON.
type FixedKeyGroup struct {
	ID       string
	Name     string
	Bindings []HelpBinding
}

// FixedKeyGroups is every key the help overlay lists that no config section
// binds, grouped by the context it acts in. `tuios keybinds list` prints them
// after the registry's bindings, so the list and the overlay read the same
// rows. leader is spelled into the rows that name it.
func FixedKeyGroups(leader string) []FixedKeyGroup {
	if leader == "" {
		leader = config.DefaultLeaderKey
	}
	return []FixedKeyGroup{
		{ID: "copy_mode", Name: "Copy mode", Bindings: copyModeKeys(leader)},
		{ID: "hints_mode", Name: "Hints mode", Bindings: hintsModeKeys(leader)},
		{ID: "message_view", Name: "Message view", Bindings: messageViewKeys()},
		{ID: "spotlight", Name: "Spotlight", Bindings: spotlightOffKeys()},
		{ID: "lists", Name: "Lists and panels", Bindings: generateListBindings()},
		{ID: "mouse", Name: "Mouse", Bindings: generateMouseBindings()},
		{ID: "mouse.sidebar", Name: "Sidebar mouse", Bindings: sidebarGestures()},
	}
}

// generatePrefixBindings generates prefix command bindings
func generatePrefixBindings(registry *config.KeybindRegistry, s *config.Settings) []HelpBinding {
	bindings := []HelpBinding{}

	// Get all prefix actions from the config
	prefixActions := []string{
		"prefix_new_window", "prefix_close_window", "prefix_rename_window",
		"prefix_next_window", "prefix_prev_window",
		"prefix_select_0", "prefix_select_1", "prefix_select_2",
		"prefix_select_3", "prefix_select_4", "prefix_select_5",
		"prefix_select_6", "prefix_select_7", "prefix_select_8", "prefix_select_9",
		"prefix_toggle_tiling", "prefix_workspace", "prefix_minimize",
		"prefix_window", "prefix_detach", "prefix_close_session", "prefix_selection",
		"prefix_help", "prefix_quit", "prefix_fullscreen", "prefix_settings",
		"prefix_split_horizontal", "prefix_split_vertical", "prefix_rotate_split",
		"prefix_equalize_splits", "prefix_layout",
		"prefix_scrollback", "prefix_screenshot", "prefix_command_palette", "prefix_session_switcher",
		"prefix_workspace_switcher", "choose_tree",
		"prefix_toggle_sidebar", "prefix_toggle_spotlight", "prefix_explore",
		"prefix_jump_notif", "prefix_last_message", "prefix_mail", "prefix_inbox", "prefix_next_attention",
		"toggle_scratch", "paste_image",
		"hints", "hints_all_panes", "display_panes", config.ActionCopyModeSearchForward, config.ActionCopyModeSearchBackward,
		// prefix_review and prefix_next_finished are listed only in the
		// Agents section, which waits for an agent to have been seen.
	}

	// Debug commands are deliberately not listed here. They used to be, built
	// from action names by slicing off a prefix, which rendered them as the
	// action name rather than the key ("ctrl+b, d, cache_stats" for what is
	// actually ctrl+b, D, c). The Debug category above lists the real keys.

	for _, action := range prefixActions {
		keys := registry.GetKeys(action)
		if len(keys) == 0 {
			continue
		}

		desc := config.ActionDescriptions[action]
		if desc == "" {
			desc = formatActionName(action)
		}

		// Prefix all keys with the leader key for display
		prefixedKeys := []string{}
		for _, key := range keys {
			prefixedKeys = append(prefixedKeys, s.LeaderKey+", "+key)
		}

		bindings = append(bindings, HelpBinding{
			Action:      action,
			Keys:        prefixedKeys,
			Description: desc,
			Category:    "Prefix Commands",
		})
	}

	return bindings
}

// formatActionName formats an action name for display
func formatActionName(action string) string {
	// Remove prefix_ if present
	action = strings.TrimPrefix(action, "prefix_")
	// Replace underscores with spaces and title case
	parts := strings.Split(action, "_")
	for i, part := range parts {
		if len(part) > 0 {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, " ")
}

// SearchBindings ranks every binding whose description, keys or action name
// matches the query, best match first.
//
// The three fields are matched as one string. Searching them separately let a
// binding be appended twice when its keys and its action both matched, and
// gave no way to say which of three independent hits was the better one.
func SearchBindings(query string, categories []HelpCategory) []HelpBinding {
	if query == "" {
		return []HelpBinding{}
	}

	bindings := []HelpBinding{}
	for _, category := range categories {
		bindings = append(bindings, category.Bindings...)
	}

	var m fuzzy.Matcher
	hits := m.FilterIndex(query, len(bindings), func(i int) string {
		b := bindings[i]
		return b.Description + " " + strings.Join(b.Keys, " ") + " " + b.Action
	})

	results := make([]HelpBinding, len(hits))
	for i, h := range hits {
		results[i] = bindings[h.Index]
	}
	return results
}

// Help overlay layout constants. These are the preferred sizes; a screen
// narrower or shorter than the panel prefers gets a panel fitted to it (see
// overlay_fit.go).
const (
	helpPanelInnerWidth = 74
	helpVisibleRows     = 14
	helpKeyColMax       = 30

	// helpCategoryTagWidth is the narrowest inner width that still has room for
	// the right-aligned category tag next to a search result. Below it the tag
	// is dropped so the description keeps the space.
	helpCategoryTagWidth = 48
)

// helpTabNames maps full category names to short tab labels.
// The strip is one row at the panel's preferred width and has to stay that way,
// so a label is only as long as it needs to be to pick its section out from the
// neighbours: "Snap" says which of the three layout sections it is, and "Rail"
// is what the sidebar is called everywhere else.
var helpTabNames = map[string]string{
	"Window Management": "Windows",
	"Workspaces":        "Spaces",
	"Layout":            "Snap",
	"Tiling":            "Tiling",
	"BSP":               "BSP",
	"Mouse":             "Mouse",
	"Sidebar":           "Rail",
	"Agents":            "Agents",
	"Copy Mode":         "Copy",
	"Modes":             "Modes",
	"Debug":             "Debug",
	"Tape":              "Tape",
	"Prefix":            "Prefix",
	"Selection":         "Selection",
	"System":            "System",
	"Prefix Commands":   "Prefix",
}

func helpTabLabel(name string) string {
	if short, ok := helpTabNames[name]; ok {
		return short
	}
	return name
}

// RenderHelpMenu renders the keybindings overlay on the shared panel grammar.
func (m *OS) RenderHelpMenu() (string, overlay.Geometry) {
	categories := m.HelpCategories()
	if len(categories) == 0 {
		return "", overlay.Geometry{}
	}

	// Auto-select an appropriate category based on mode when first opened.
	if m.HelpCategory < 0 {
		m.HelpCategory = 0
		if m.Mode == TerminalMode {
			for i, cat := range categories {
				if cat.Name == "Modes" {
					m.HelpCategory = i
					break
				}
			}
		}
	}
	if m.HelpCategory >= len(categories) {
		m.HelpCategory = len(categories) - 1
	}

	pal := theme.UI()
	inSearch := m.HelpSearchMode

	var bindings []HelpBinding
	showCategoryTag := false
	if inSearch {
		bindings = SearchBindings(m.HelpSearchQuery, categories)
		showCategoryTag = true
	} else {
		bindings = categories[m.HelpCategory].Bindings
	}

	var tabs []string
	if !inSearch {
		tabs = make([]string, len(categories))
		for i, cat := range categories {
			tabs[i] = helpTabLabel(cat.Name)
		}
	}

	width := m.panelWidth(helpPanelInnerWidth)
	hints := helpHints(inSearch)
	// Body lines that are not binding rows: the scroll indicator, plus the
	// search prompt and its rule when searching.
	extra := 1
	if inSearch {
		extra += 2
	}
	rows, hints := m.panelBody(helpVisibleRows, extra, width, tabs, hints)
	if width < helpCategoryTagWidth {
		showCategoryTag = false
	}

	body := m.renderHelpBody(bindings, inSearch, showCategoryTag, pal, width, rows)

	panel := overlay.Panel{
		Title: "Keybindings",
		Width: width,
		Body:  body,
		Hints: hints,
	}
	if !inSearch {
		panel.Tabs = tabs
		panel.ActiveTab = m.HelpCategory
	}

	return panel.Render(pal)
}

// renderHelpBody builds the multi-line body: an optional search box, the
// scrolling list of binding rows, and a scroll indicator, padded to a fixed
// height so the panel never jumps.
func (m *OS) renderHelpBody(bindings []HelpBinding, inSearch, showCategoryTag bool, pal overlay.Palette, width, visibleRows int) string {
	bg := pal.Surface
	var lines []string

	if inSearch {
		cursor := overlay.Style(bg).Foreground(pal.Accent).Render("█")
		prompt := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("Search ") +
			overlay.Style(bg).Foreground(pal.Fg).Render(m.HelpSearchQuery) + cursor
		lines = append(lines, prompt, overlay.Rule(width, bg, pal))
	}

	// Clamp scroll to the row count.
	maxScroll := max(len(bindings)-visibleRows, 0)
	m.HelpScrollOffset = max(0, min(m.HelpScrollOffset, maxScroll))

	// Compute a stable key column width from the visible window. On a narrow
	// panel the badges may not leave a usable description column, so the key
	// column never takes more than half the width.
	keyColW := 0
	end := min(m.HelpScrollOffset+visibleRows, len(bindings))
	for i := m.HelpScrollOffset; i < end; i++ {
		w := lipgloss.Width(overlay.KeyBadges(bindings[i].Keys, bg, pal))
		keyColW = max(keyColW, w)
	}
	keyColW = min(keyColW, min(helpKeyColMax, max(width/2, 6)))

	if len(bindings) == 0 {
		msg := "No matching keybindings"
		if !inSearch {
			msg = "No keybindings in this section"
		}
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgDim).Italic(true).Render("  "+msg))
	}

	rowCount := 0
	for i := m.HelpScrollOffset; i < end; i++ {
		lines = append(lines, helpBindingRow(bindings[i], keyColW, showCategoryTag, pal, width))
		rowCount++
	}
	// Pad to a fixed number of rows so the panel height is stable.
	for rowCount < visibleRows {
		lines = append(lines, overlay.Style(bg).Render(" "))
		rowCount++
	}

	// Scroll indicator.
	if len(bindings) > visibleRows {
		info := fmt.Sprintf("%d-%d of %d", m.HelpScrollOffset+1, end, len(bindings))
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgDim).Italic(true).Render("  "+info))
	} else {
		lines = append(lines, overlay.Style(bg).Render(" "))
	}

	return strings.Join(lines, "\n")
}

// helpBindingRow renders one keybinding row: key badges in a fixed-width gutter,
// the description, and an optional right-aligned category tag (in search view).
func helpBindingRow(b HelpBinding, keyColW int, showCategoryTag bool, pal overlay.Palette, width int) string {
	bg := pal.Surface
	// A binding with more key combos than the column can hold drops the extra
	// combos, then shortens the last one, rather than pushing the description
	// off the panel.
	keys := b.Keys
	badges := overlay.KeyBadges(keys, bg, pal)
	for len(keys) > 1 && lipgloss.Width(badges) > keyColW {
		keys = keys[:len(keys)-1]
		badges = overlay.KeyBadges(keys, bg, pal)
	}
	if len(keys) == 1 && lipgloss.Width(badges) > keyColW {
		badges = overlay.KeyBadge(overlay.Truncate(keys[0], max(keyColW-2, 1)), pal)
	}
	bw := lipgloss.Width(badges)
	if bw < keyColW {
		badges += overlay.Style(bg).Render(strings.Repeat(" ", keyColW-bw))
	}

	// Reserve space for a right-aligned category tag when searching.
	tag := ""
	tagW := 0
	if showCategoryTag && b.Category != "" {
		label := helpTabLabel(b.Category)
		tag = overlay.Style(bg).Foreground(pal.FgMute).Render(label)
		tagW = lipgloss.Width(label) + 2
	}

	descMax := width - keyColW - 2 - tagW
	desc := b.Description
	if lipgloss.Width(desc) > descMax {
		desc = overlay.Truncate(desc, descMax)
	}

	line := badges + overlay.Style(bg).Render("  ") + overlay.Style(bg).Foreground(pal.Fg).Render(desc)
	if tag != "" {
		used := lipgloss.Width(line)
		gap := width - used - lipgloss.Width(tag)
		if gap > 0 {
			line += overlay.Style(bg).Render(strings.Repeat(" ", gap)) + tag
		}
	}
	return line
}

// helpHints returns the footer key hints for the current help mode.
func helpHints(inSearch bool) []overlay.Hint {
	if inSearch {
		return []overlay.Hint{
			{Key: "type", Label: "filter"},
			{Key: "↑↓", Label: "scroll"},
			{Key: "esc", Label: "clear"},
			{Key: "?", Label: "close"},
		}
	}
	return []overlay.Hint{
		{Key: "/", Label: "search"},
		{Key: "←→", Label: "section"},
		{Key: "↑↓", Label: "scroll"},
		{Key: "?", Label: "close"},
	}
}

// agentHelpActions are the keys outside the Agents tab that act only on an
// agent feature: the rail's agent controls and mailbox, and the prefix keys
// of the Inbox and the mail.
var agentHelpActions = map[string]bool{
	"agents_filter": true, "agents_sort": true, "mail": true,
	"prefix_mail": true, "prefix_inbox": true, "prefix_next_attention": true,
}

// withoutAgentHelp leaves the agent keys out of every tab, for the agent
// features turned off. See agents_off.go.
func withoutAgentHelp(cats []HelpCategory) []HelpCategory {
	out := make([]HelpCategory, 0, len(cats))
	for _, c := range cats {
		c.Bindings = slices.DeleteFunc(slices.Clone(c.Bindings), func(b HelpBinding) bool { return agentHelpActions[b.Action] })
		for i := range c.Bindings {
			if c.Name == HelpCategorySidebar {
				if c.Bindings[i].Action == "section" {
					c.Bindings[i].Description = "Cycle the rail's sections"
				}
			}
		}
		if len(c.Bindings) > 0 {
			out = append(out, c)
		}
	}
	return out
}
