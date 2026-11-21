package app

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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
				"minimize_window", "restore_all", "toggle_zoom", "toggle_pip", "focus_rail_pane", "toggle_spotlight",
				"next_window", "prev_window",
				"terminal_next_window", "terminal_prev_window",
				"terminal_focus_left", "terminal_focus_right",
				"focus_up", "focus_down",
				"copy_selection", "focus_sidebar",
				"next_session", "prev_session", "next_host", "prev_host",
				"next_agent", "prev_agent",
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
			Name:     HelpCategoryCopyMode,
			Bindings: append(generateCopyModeBindings(registry, s), generateHintsBindings(registry, s)...),
		},
		{
			Name: "Modes",
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

const (
	helpRailReview   = "Rail agent row: review the pane's changes"
	helpInboxReview  = "Agents view: review the changes in the agent's pane"
	helpReviewPrefix = "Review: "
)

// HelpCategoryAgents is the section gathering every key that deals with
// agents: the prefix chords that reach the Inbox, the rail's agent controls,
// the palette's state filter, and the keys inside the Inbox, its prompt and
// the mailbox. They were spread over Prefix and Rail, where the same letter
// means different things, and the Inbox's own keys were in no section.
const HelpCategoryAgents = "Agents"

//go:noinline
func appendHelpBinding(bindings *[]HelpBinding, keys []string, desc, cat string) {
	var live []string
	for _, k := range keys {
		if k = strings.TrimSpace(k); k != "" {
			live = append(live, k)
		}
	}
	if len(live) > 0 {
		*bindings = append(*bindings, HelpBinding{Keys: live, Description: desc, Category: cat})
	}
}

func generateAgentBindings(registry *config.KeybindRegistry, s *config.Settings) []HelpBinding {
	const cat = HelpCategoryAgents
	presses := config.PressesByAction(registry)
	var bindings []HelpBinding
	add := func(keys []string, desc string) { appendHelpBinding(&bindings, keys, desc, cat) }
	for _, action := range []string{"prefix_inbox", "prefix_next_attention", "prefix_next_finished", "prefix_review", "prefix_mail", "prefix_jump_notif", "prefix_agents_settings"} {
		desc := config.ActionDescriptions[action]
		add(presses[action], desc)
	}
	if keys := presses["command_palette"]; len(keys) > 0 {
		add([]string{keys[0] + ", @"}, "Palette: panes running an agent; @n needs you, @w working, @d done, @i idle, @e errored, @a needs you or errored")
	}
	railKey := func(action string) []string {
		var out []string
		for _, k := range registry.Keys(config.TableSidebar, action) {
			out = append(out, "rail "+k)
		}
		return out
	}
	add(railKey("agents_filter"), "Rail agents: all sessions, or this one")
	add(railKey("agents_sort"), "Rail agents: needs you, priority, or recency")
	add(railKey("mail"), "Rail: the mailbox of the pane under the cursor")
	agentRowKey := func(action string) []string {
		var out []string
		for _, k := range registry.Keys(config.TableSidebarAgents, action) {
			out = append(out, "rail "+k)
		}
		return out
	}
	add(agentRowKey(config.ActionAgentReply), "Rail agent row: reply to the agent")
	add(agentRowKey(config.ActionAgentReview), helpRailReview)
	add(agentRowKey(config.ActionAgentUnread), "Rail agent row: mark its finished turn unread")
	add(agentRowKey(config.ActionAgentSnooze), "Rail agent row: snooze what needs you in its pane")
	add(agentRowKey(config.ActionAgentCancelQueued), "Rail agent row: drop the newest queued message")
	add(agentRowKey(config.ActionAgentSubagents), "Rail agent row: open or close its subagent rows")

	inbox := func(action, desc string) {
		add(registry.GetInboxKeys(action), desc)
	}
	add([]string{"1-9"}, "Agents view: answer a held approval or a question by its number")
	for _, action := range []string{
		config.ActionInboxAllow, config.ActionInboxAllowAlways, config.ActionInboxReject, config.ActionInboxDenyReason, config.ActionInboxReply,
		config.ActionInboxPeek, config.ActionInboxGo, config.ActionInboxWrite, config.ActionInboxNewAgent, config.ActionInboxCompare, config.ActionInboxArtifact,
		config.ActionInboxBoard, config.ActionInboxBoardLayout, config.ActionInboxViewList, config.ActionInboxTake, config.ActionInboxShip,
		config.ActionInboxStagePrev, config.ActionInboxStageNext,
		config.ActionInboxDismiss, config.ActionInboxResume, config.ActionInboxPassOn, config.ActionInboxFilter,
		config.ActionInboxSelect, config.ActionInboxMailbox, config.ActionInboxReview, config.ActionInboxSnooze,
		config.ActionInboxSettle, config.ActionInboxUndo, config.ActionInboxShowSnoozed, config.ActionInboxMark, config.ActionInboxMarkAll,
		config.ActionInboxViewPane, config.ActionInboxOpen, config.ActionInboxBack,
		config.ActionInboxTabPrev, config.ActionInboxTabNext, config.ActionInboxExpand, config.ActionInboxCodeWrap, config.ActionInboxCodeLeft, config.ActionInboxCodeRight, config.ActionInboxCodeCopy, config.ActionInboxBlockSource, config.ActionInboxBlockPicture, config.ActionInboxFold, config.ActionInboxPin,
		config.ActionInboxPromptPrev, config.ActionInboxPromptNext, config.ActionInboxChild, config.ActionInboxJumpBack,
		config.ActionInboxDetailDown, config.ActionInboxDetailUp,
		config.ActionInboxRegionNext, config.ActionInboxRegionPrev, config.ActionInboxEscape, config.ActionInboxNextNeeds,
		config.ActionInboxListNarrower, config.ActionInboxListWider, config.ActionInboxSide, config.ActionInboxZoom,
	} {
		inbox(action, config.ActionDescriptions[action])
	}
	inbox(config.ActionPeekApprove, "Prompt: approve")
	inbox(config.ActionPeekApproveAlways, "Prompt: approve and do not ask again")
	inbox(config.ActionPeekDeny, "Prompt: reject")
	inbox(config.ActionPeekType, "Prompt: type an answer")
	inbox(config.ActionMailReply, "Mailbox: reply in the open thread")
	inbox(config.ActionMailFocusPane, "Mailbox: go to the pane that last wrote")
	inbox(config.ActionMailNew, "Mailbox: write a new message to an agent")
	review := func(action, desc string) { add(registry.Keys(config.TableReview, action), desc) }
	review(config.ActionReviewNextHunk, "Review: next hunk")
	review(config.ActionReviewPrevHunk, "Review: previous hunk")
	review(config.ActionReviewNextFile, "Review: next file")
	review(config.ActionReviewPrevFile, "Review: previous file")
	for _, action := range []string{config.ActionReviewStageHunk, config.ActionReviewUnstageHunk,
		config.ActionReviewStageFile, config.ActionReviewUnstageFile, config.ActionReviewUndoStage} {
		review(action, config.ActionDescriptions[action])
	}
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
	m.helpSearch.Set("")
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

func generateCategoryBindings(registry *config.KeybindRegistry, categoryName string, actions []string) []HelpBinding {
	presses := config.PressesByAction(registry)
	bindings := []HelpBinding{}
	for _, action := range actions {
		keys := presses[action]
		if len(keys) == 0 {
			continue
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

func generateWorkspaceBindings(registry *config.KeybindRegistry, s *config.Settings) []HelpBinding {
	bindings := []HelpBinding{}
	presses := config.PressesByAction(registry)

	for i := 1; i <= 9; i++ {
		action := fmt.Sprintf("switch_workspace_%d", i)
		keys := presses[action]
		if len(keys) > 0 {
			bindings = append(bindings, HelpBinding{
				Action:      action,
				Keys:        keys,
				Description: fmt.Sprintf("Switch to workspace %d", i-s.WorkspaceIndexOffset),
				Category:    "Workspaces",
			})
		}
	}

	for i := 1; i <= 9; i++ {
		action := fmt.Sprintf("move_and_follow_%d", i)
		keys := presses[action]
		if len(keys) > 0 {
			bindings = append(bindings, HelpBinding{
				Action:      action,
				Keys:        keys,
				Description: fmt.Sprintf("Move to workspace %d and follow", i-s.WorkspaceIndexOffset),
				Category:    "Workspaces",
			})
		}
	}

	for i := 1; i <= 9; i++ {
		action := fmt.Sprintf("send_to_workspace_%d", i)
		keys := presses[action]
		if len(keys) > 0 {
			bindings = append(bindings, HelpBinding{
				Action:      action,
				Keys:        keys,
				Description: fmt.Sprintf("Send the focused pane to workspace %d", i-s.WorkspaceIndexOffset),
				Category:    "Workspaces",
			})
		}
	}

	if chord := contextMenuHint(registry, "workspace_prefix_rename", s, WindowManagementMode); chord != "" {
		bindings = append(bindings, HelpBinding{
			Action:      "workspace_prefix_rename",
			Keys:        []string{chord},
			Description: "Rename workspace",
			Category:    "Workspaces",
		})
	}

	return bindings
}

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
		{Keys: []string{"ctrl/shift + right-click"}, Description: "Pane menu, even if the program uses the mouse", Category: cat},
		{Keys: []string{"ctrl+click a link"}, Description: "Open the link. See appearance.link_click", Category: cat},
		{Keys: []string{"hover a link"}, Description: "Show where the link goes", Category: cat},
		{Keys: []string{"wheel"}, Description: "Scroll the scrollback, or the program", Category: cat},
		{Keys: []string{"drag right edge"}, Description: "Drag the scrollbar, if there is one", Category: cat},
		{Keys: []string{"right-click desktop"}, Description: "Desktop menu", Category: cat},
		{Keys: []string{"click dock entry"}, Description: "Restore that minimized window", Category: cat},
		{Keys: []string{"right-click dock"}, Description: "Dock menu, or the entry's own menu", Category: cat},
		{Keys: []string{"right-click workspace tab"}, Description: "Switch to, rename or close that workspace", Category: cat},
		{Keys: []string{"drag a panel"}, Description: "Move the panel. Click outside to close", Category: cat},
	}
}

func generateSidebarBindings(registry *config.KeybindRegistry, s *config.Settings) []HelpBinding {
	const cat = "Sidebar"
	row := func(action string) HelpBinding {
		return HelpBinding{
			Action:      action,
			Keys:        registry.Keys(config.TableSidebar, action),
			Description: config.ScopedDescription(config.ScopeSidebar, action, s.WorkspaceIndexOffset),
			Category:    cat,
		}
	}

	bindings := generateCategoryBindings(registry, cat, []string{"focus_sidebar"})
	for _, action := range []string{"prefix_explore", "prefix_toggle_sidebar", "prefix_toggle_other_rail"} {
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
	if first, last := registry.Keys(config.TableSidebar, "jump_1"), registry.Keys(config.TableSidebar, "jump_9"); len(first) > 0 && len(last) > 0 {
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
		row("switch_edge"),
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

	fileRow := func(action, desc string) HelpBinding {
		return HelpBinding{
			Action:      action,
			Keys:        registry.Keys(config.TableSidebarFiles, action),
			Description: desc,
			Category:    cat,
		}
	}
	quickLookRow := func(action, desc string) HelpBinding {
		return HelpBinding{Action: action, Keys: registry.Keys(config.TableQuickLook, action), Description: desc, Category: cat}
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
		fileRow("file_send", "Files: send the file or folder to a host"),
		fileRow("file_insert_path", "Files: paste the path into the focused pane"),
		fileRow("file_quick_look", "Files: show a preview of the file or folder"),
		fileRow("file_open_system", "Files: open with the system opener"),
		quickLookRow("seek_down", "Preview: move forward in the file"),
		quickLookRow("seek_up", "Preview: move back in the file"),
		quickLookRow("go_inside", "Preview: move the keyboard inside the file"),
		quickLookRow("full_size", "Preview: fill the area of the panes"),
	)

	copyRow := func(action, desc string) HelpBinding {
		return HelpBinding{
			Action:      action,
			Keys:        registry.Keys(config.TableTransfers, action),
			Description: desc,
			Category:    cat,
		}
	}
	bindings = append(bindings,
		copyRow(config.ActionTransfersPause, "Copies: pause the copy, or go on with it"),
		copyRow(config.ActionTransfersCancel, "Copies: cancel the copy, after a question"),
		copyRow(config.ActionTransfersRetry, "Copies: try a failed copy again"),
		copyRow(config.ActionTransfersClear, "Copies: remove the copies that ended"),
		copyRow(config.ActionTransfersAnswer, "Copies: answer a copy that waits for an answer"),
	)

	bound := bindings[:0]
	for _, b := range bindings {
		if len(b.Keys) > 0 {
			bound = append(bound, b)
		}
	}
	bindings = bound

	return append(bindings, sidebarGestures()...)
}

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

func generateListBindings() []HelpBinding {
	const cat = "Modes"
	return []HelpBinding{
		{Keys: []string{"ctrl+u"}, Description: "Lists: clear a typed filter", Category: cat},
		{Keys: []string{"alt+backspace", "ctrl+w"}, Description: "Text fields: delete the last word", Category: cat},
		{Keys: []string{"wheel"}, Description: "Lists: scroll the list under the pointer", Category: cat},
		{Keys: []string{"/"}, Description: "Settings: search every tab", Category: cat},
		{Keys: []string{"1-9"}, Description: "Settings: go to that tab", Category: cat},
		{Keys: []string{"backspace"}, Description: "Settings: reset the row to its default", Category: cat},
		{Keys: []string{"ctrl+z"}, Description: "Settings: undo the last change", Category: cat},
		{Keys: []string{"tab"}, Description: "Settings search: go to the row's tab", Category: cat},
	}
}

func generateCopyModeBindings(registry *config.KeybindRegistry, s *config.Settings) []HelpBinding {
	if registry == nil {
		registry = config.NewKeybindRegistry(config.DefaultConfig())
	}
	var enter []string
	for _, key := range registry.GetConfig().Keybindings.PrefixMode["prefix_selection"] {
		enter = append(enter, s.LeaderKey+", "+key)
	}
	out := copyModeKeys(enter...)
	at := min(len(enter), 1)
	out = slices.Insert(out, at, copyModeMotionBindings(registry)...)
	for _, row := range []struct{ action, desc string }{
		{config.ActionCopyModeExit, "Leave copy mode"},
		{config.ActionEditScrollback, "Open the scrollback in the editor"},
		{config.ActionHints, "Label the text in view to copy it"},
	} {
		if keys := registry.Keys(config.TableCopyMode, row.action); len(keys) > 0 {
			out = append(out, HelpBinding{Action: row.action, Keys: keys, Description: row.desc, Category: HelpCategoryCopyMode})
		}
	}
	return out
}

var copyModeMotionRows = []struct {
	actions     []string
	fixed       map[string]string
	lead        string
	desc, desc2 string
}{
	{actions: []string{config.ActionCopyModeLeft, config.ActionCopyModeDown, config.ActionCopyModeUp, config.ActionCopyModeRight}, desc: "Move cursor"},
	{actions: []string{config.ActionCopyModeWordForward, config.ActionCopyModeWordBackward, config.ActionCopyModeWordEnd}, fixed: map[string]string{config.ActionCopyModeWordForward: "w", config.ActionCopyModeWordBackward: "b"}, desc: "Word fwd/back/end", desc2: "Word fwd/back"},
	{actions: []string{config.ActionCopyModeBigWordForward, config.ActionCopyModeBigWordBackward, config.ActionCopyModeBigWordEnd}, desc: "Word fwd/back/end, split only at spaces"},
	{actions: []string{config.ActionCopyModeLineStart, config.ActionCopyModeFirstNonBlank, config.ActionCopyModeLineEnd}, fixed: map[string]string{config.ActionCopyModeLineStart: "0", config.ActionCopyModeFirstNonBlank: "^", config.ActionCopyModeLineEnd: "$"}, desc: "Line start/first/end", desc2: "Line start/end"},
	{actions: []string{config.ActionCopyModeBottom}, lead: "gg", desc: "Jump top/bottom"},
	{actions: []string{config.ActionCopyModeHalfPageUp, config.ActionCopyModeHalfPageDown}, desc: "Half page up/down"},
	{actions: []string{config.ActionCopyModePageUp, config.ActionCopyModePageDown}, fixed: map[string]string{config.ActionCopyModePageUp: "ctrl+b"}, desc: "Page up/down"},
	{actions: []string{config.ActionCopyModeScreenTop, config.ActionCopyModeScreenMiddle, config.ActionCopyModeScreenBottom}, desc: "Top/middle/bottom of the view"},
	{actions: []string{config.ActionCopyModeParagraphUp, config.ActionCopyModeParagraphDown}, desc: "Paragraph up/down"},
	{actions: []string{config.ActionCopyModeMatchingBracket}, desc: "Matching bracket"},
	{actions: []string{config.ActionCopyModeRepeatCharSearch, config.ActionCopyModeRepeatCharSearchBack}, desc: "Repeat the last f/F/t/T forward/back"},
}

func copyModeMotionBindings(registry *config.KeybindRegistry) []HelpBinding {
	var out []HelpBinding
	for _, row := range copyModeMotionRows {
		var first, second []string
		if row.lead != "" {
			first = append(first, row.lead)
		}
		for _, action := range row.actions {
			keys := registry.Keys(config.TableCopyMode, action)
			if f := row.fixed[action]; f != "" {
				keys = append([]string{f}, keys...)
			}
			if len(keys) > 0 {
				first = append(first, keys[0])
			}
			if len(keys) > 1 {
				second = append(second, keys[1])
			}
		}
		if len(first) == 0 {
			continue
		}
		out = append(out, HelpBinding{Action: row.actions[0], Keys: []string{strings.Join(first, ", ")}, Description: row.desc, Category: HelpCategoryCopyMode})
		if len(second) > 0 {
			out = append(out, HelpBinding{Action: row.actions[0], Keys: []string{strings.Join(second, ", ")}, Description: cmp.Or(row.desc2, row.desc), Category: HelpCategoryCopyMode})
		}
	}
	return out
}

func copyModeKeys(enter ...string) []HelpBinding {
	var out []HelpBinding
	if len(enter) > 0 {
		out = append(out, HelpBinding{Keys: enter, Description: "Enter copy mode", Category: "Copy Mode"})
	}
	return append(out, []HelpBinding{
		{Keys: []string{"f, F, t, T"}, Description: "Jump to a character on the line", Category: "Copy Mode"},
		{Keys: []string{"/, ?"}, Description: "Search forward/backward", Category: "Copy Mode"},
		{Keys: []string{"n, N"}, Description: "Repeat search/reverse", Category: "Copy Mode"},
		{Keys: []string{"ctrl+l"}, Description: "Clear the search highlights", Category: "Copy Mode"},
		{Keys: []string{"v, V"}, Description: "Visual char/line", Category: "Copy Mode"},
		{Keys: []string{"ctrl+v"}, Description: "Visual block", Category: "Copy Mode"},
		{Keys: []string{"o"}, Description: "Other end of selection, the opposite corner of a block", Category: "Copy Mode"},
		{Keys: []string{"y, c"}, Description: "Yank to clipboard", Category: "Copy Mode"},
		{Keys: []string{"i, q, Esc"}, Description: "Exit copy mode", Category: "Copy Mode"},
		{Keys: []string{"ctrl+c"}, Description: "Exit copy mode from any state. Nothing is copied", Category: "Copy Mode"},
	}...)
}

// HelpCategoryCopyMode is the section listing copy mode's keys and hints
// mode's. The ? key in hints mode opens the help on it, because the dock's
// legend has room for only some of hints mode's keys on a narrow screen.
const HelpCategoryCopyMode = "Copy Mode"

const helpHintsPrefix = "Hints: "

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

func generateDebugBindings(s *config.Settings) []HelpBinding {
	return []HelpBinding{
		{Keys: []string{s.LeaderKey + ", D, l"}, Description: "Toggle log viewer", Category: "Debug"},
		{Keys: []string{s.LeaderKey + ", D, c"}, Description: "Toggle cache stats", Category: "Debug"},
		{Keys: []string{s.LeaderKey + ", D, k"}, Description: "Toggle showkeys", Category: "Debug"},
		{Keys: []string{s.LeaderKey + ", D, a"}, Description: "Toggle animations", Category: "Debug"},
	}
}

func generateTapeBindings(s *config.Settings) []HelpBinding {
	bindings := []HelpBinding{}

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

func generateSpotlightOffBindings(registry *config.KeybindRegistry, s *config.Settings) []HelpBinding {
	fixed := spotlightOffKeys()
	out := []HelpBinding{fixed[0]}
	if keys := registry.GetKeys("toggle_spotlight"); len(keys) > 0 {
		out = append(out, HelpBinding{
			Action:      "spotlight_off",
			Keys:        []string{s.LeaderKey + ", " + keys[0]},
			Description: "Turn off spotlight, in any mode",
			Category:    "Modes",
		})
	}
	return append(out, fixed[1:]...)
}

func spotlightOffKeys() []HelpBinding {
	return []HelpBinding{
		{Action: "spotlight_off", Keys: []string{"esc"}, Description: "Turn off spotlight, in window mode", Category: "Modes"},
		{Action: "spotlight_off", Keys: []string{"click the Spotlight chip"}, Description: "Turn off spotlight, in any mode", Category: "Modes"},
	}
}

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
		{ID: "copy_mode", Name: "Copy mode", Bindings: copyModeKeys(leader + ", [")},
		{ID: "hints_mode", Name: "Hints mode", Bindings: hintsModeKeys(leader)},
		{ID: "message_view", Name: "Message view", Bindings: messageViewKeys()},
		{ID: "spotlight", Name: "Spotlight", Bindings: spotlightOffKeys()},
		{ID: "lists", Name: "Lists and panels", Bindings: generateListBindings()},
		{ID: "mouse", Name: "Mouse", Bindings: generateMouseBindings()},
		{ID: "mouse.sidebar", Name: "Sidebar mouse", Bindings: sidebarGestures()},
	}
}

func generatePrefixBindings(registry *config.KeybindRegistry, s *config.Settings) []HelpBinding {
	bindings := []HelpBinding{}

	prefixActions := []string{
		"new_window", "close_window", "prefix_rename_window",
		"prefix_next_window", "prefix_prev_window",
		"prefix_select_0", "prefix_select_1", "prefix_select_2",
		"prefix_select_3", "prefix_select_4", "prefix_select_5",
		"prefix_select_6", "prefix_select_7", "prefix_select_8", "prefix_select_9",
		"prefix_toggle_tiling", "prefix_workspace", "prefix_minimize",
		"prefix_window", "prefix_detach", "prefix_close_session", "prefix_selection",
		"toggle_help", "prefix_quit", "prefix_fullscreen", "open_settings",
		"split_horizontal", "split_vertical", "rotate_split",
		"equalize_splits", "prefix_layout",
		"prefix_scrollback", "prefix_screenshot", "command_palette", "prefix_session_switcher",
		"prefix_workspace_switcher", "choose_tree", "goto_picker", "new_worktree", "set_note",
		"prefix_toggle_sidebar", "toggle_spotlight", "prefix_explore",
		"prefix_jump_notif", "prefix_last_message", "prefix_mail", "prefix_inbox", "prefix_next_attention",
		"toggle_scratch", "toggle_calm", "paste_image", "paste_buffer", "choose_buffer", "prefix_transfers",
		config.ActionEditScrollback, config.ActionClearScrollback,
		"hints", "hints_all_panes", "display_panes", "pick_path", config.ActionCopyModeSearchForward, config.ActionCopyModeSearchBackward,
	}

	for _, action := range prefixActions {
		keys := registry.GetKeys(action)
		if len(keys) == 0 {
			continue
		}

		desc := config.ActionDescriptions[action]
		if desc == "" {
			desc = formatActionName(action)
		}

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

func formatActionName(action string) string {
	action = strings.TrimPrefix(action, "prefix_")
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

const (
	helpPanelInnerWidth = 74
	helpVisibleRows     = 14
	helpKeyColMax       = 30

	helpCategoryTagWidth = 48
)

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

	pal := m.overlayUI()
	inSearch := m.HelpSearchMode

	var bindings []HelpBinding
	showCategoryTag := false
	if inSearch {
		bindings = SearchBindings(m.helpSearch.String(), categories)
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

func (m *OS) renderHelpBody(bindings []HelpBinding, inSearch, showCategoryTag bool, pal overlay.Palette, width, visibleRows int) string {
	bg := pal.Surface
	var lines []string

	if inSearch {
		label := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("Search ")
		before, at, after := m.helpSearch.Parts()
		prompt := label + overlay.FieldLine(before, at, after, max(width-lipgloss.Width(label), 1), bg, pal.Fg)
		lines = append(lines, prompt, overlay.Rule(width, bg, pal))
	}

	maxScroll := max(len(bindings)-visibleRows, 0)
	m.HelpScrollOffset = max(0, min(m.HelpScrollOffset, maxScroll))

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
	for rowCount < visibleRows {
		lines = append(lines, overlay.Style(bg).Render(" "))
		rowCount++
	}

	if len(bindings) > visibleRows {
		info := fmt.Sprintf("%d-%d of %d", m.HelpScrollOffset+1, end, len(bindings))
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgDim).Italic(true).Render("  "+info))
	} else {
		lines = append(lines, overlay.Style(bg).Render(" "))
	}

	return strings.Join(lines, "\n")
}

func helpBindingRow(b HelpBinding, keyColW int, showCategoryTag bool, pal overlay.Palette, width int) string {
	bg := pal.Surface
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

	tag := ""
	tagW := 0
	if showCategoryTag && b.Category != "" {
		label := helpTabLabel(b.Category)
		tag = overlay.Style(bg).Foreground(pal.FgMute).Render(label)
		tagW = lipgloss.Width(label) + 2
	}

	descMax := width - keyColW - 2 - tagW
	desc := overlay.Truncate(b.Description, descMax)

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

var agentHelpActions = map[string]bool{
	"agents_filter": true, "agents_sort": true, "mail": true,
	"prefix_mail": true, "prefix_inbox": true, "prefix_next_attention": true,
}

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

// HelpSearchQuery is the help search line's text.
func (m *OS) HelpSearchQuery() string { return m.helpSearch.String() }

// SetHelpSearchQuery replaces the help search line's text.
func (m *OS) SetHelpSearchQuery(q string) { m.helpSearch.Set(q) }

// HelpSearchKey applies one key press to the help search line (see lineKey).
// Left and right are not here: they change the section, as outside a search.
func (m *OS) HelpSearchKey(key, text string) (changed, consumed bool) {
	return lineKey(&m.helpSearch, key, text, cleanControl)
}

// HelpSearchPaste types a paste at the help search line's caret.
func (m *OS) HelpSearchPaste(s string) bool {
	return lineInsert(&m.helpSearch, s, cleanControl)
}
