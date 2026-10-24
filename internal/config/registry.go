package config

import (
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
)

// KeybindRegistry manages the mapping between keys and actions
type KeybindRegistry struct {
	keyToAction map[string]string // Maps key string to action name
	// repeat is the set of actions that stay armed after the leader: the
	// config's keybindings.repeat, or DefaultPrefixRepeat when it is unset.
	repeat     map[string]bool
	config     *UserConfig
	normalizer *KeyNormalizer

	// sections holds one resolved key->action map per config section that is
	// looked up on its own rather than through keyToAction: every prefix, the
	// global and script scopes, the direct terminal-mode binds and the rail.
	//
	// A binding is resolved once here, at build time, and read per key like
	// the rest. Rebuilding from the config on every lookup would sort the
	// terminal-mode section, run the normalizer over each of its keys and
	// allocate a fresh map for every letter typed into a shell, three or four
	// times over as the key passes each gate on its way to the PTY. Measured
	// at 177 allocations and 19 us per typed key that way, against 7 and 0.65
	// us for a window-mode key that reads the flattened map.
	//
	// The maps are as stale as keyToAction is, and for the same reason: the
	// config is edited in place by the keybind manager and the unbind
	// commands, and each of those calls Reload when it is done. A section
	// changed without a Reload is not seen by any lookup, which was already
	// true of the seven flattened sections.
	sections sectionMaps

	// presses is PressesByAction's answer, kept until the next buildMappings.
	// The help overlay asks for it four times a frame while it is open, and
	// building it walks every binding in every scope: 2.9ms a frame, most of
	// the overlay's cost. It goes stale exactly when the maps above do, so a
	// rebound key shows as soon as the Reload that follows every edit runs.
	pressesMu sync.Mutex
	presses   map[string][]string
	// live is each scope's keys that run, for the which-key menus, kept
	// under pressesMu and dropped with presses. See liveScope.
	live map[string]*liveScope
}

// sectionMaps is the resolved form of every section the registry reads on its
// own. One field per section, so a lookup is a field read and a map access.
type sectionMaps struct {
	prefix, windowPrefix, minimizePrefix, workspacePrefix map[string]string
	debugPrefix, tapePrefix, layoutPrefix, tuiosPrefix    map[string]string
	terminalMode, global, script, sidebar, sidebarFiles   map[string]string
	sidebarAgents, inbox, inboxPeek, mail, copyMode       map[string]string
	transfers, lists                                      map[string]string
	// menus holds each declared menu (see MenuConfig), by name.
	menus map[string]map[string]string
}

// NewKeybindRegistry creates a new keybind registry from config
func NewKeybindRegistry(cfg *UserConfig) *KeybindRegistry {
	registry := &KeybindRegistry{
		keyToAction: make(map[string]string),
		config:      cfg,
		normalizer:  NewKeyNormalizer(),
	}
	registry.buildMappings()
	return registry
}

// buildMappings builds the reverse mapping from keys to actions
func (r *KeybindRegistry) buildMappings() {
	r.keyToAction = make(map[string]string)
	list := r.config.Keybindings.Repeat
	if list == nil {
		list = DefaultPrefixRepeat
	}
	r.repeat = make(map[string]bool, len(list))
	for _, a := range list {
		r.repeat[a] = true
	}
	r.pressesMu.Lock()
	r.presses = nil
	r.live = nil
	r.pressesMu.Unlock()

	// Build mappings for normal mode sections
	// Note: Prefix sections (PrefixMode, WindowPrefix, MinimizePrefix, WorkspacePrefix)
	// are NOT added here to avoid conflicts with normal mode keybindings
	// They will be looked up directly when in their respective prefix modes
	r.addSection(r.config.Keybindings.WindowManagement)
	r.addSection(r.config.Keybindings.Workspaces)
	r.addSection(r.config.Keybindings.Layout)
	r.addSection(r.config.Keybindings.ModeControl)
	r.addSection(r.config.Keybindings.System)
	r.addSection(r.config.Keybindings.Navigation)
	r.addSection(r.config.Keybindings.RestoreMinimized)
	// Prefix sections are handled separately. Don't add them to the main registry:
	// - PrefixMode (used after Ctrl+B)
	// - WindowPrefix (used after Ctrl+B, t)
	// - MinimizePrefix (used after Ctrl+B, m)
	// - WorkspacePrefix (used after Ctrl+B, w)
	// They are resolved here all the same, once, into their own maps.
	kb := &r.config.Keybindings
	r.sections = sectionMaps{
		prefix:          r.withCommands(r.sectionKeyMap(kb.PrefixMode), SectionPrefixMode),
		windowPrefix:    r.sectionKeyMap(kb.WindowPrefix),
		minimizePrefix:  r.sectionKeyMap(kb.MinimizePrefix),
		workspacePrefix: r.sectionKeyMap(kb.WorkspacePrefix),
		debugPrefix:     r.sectionKeyMap(kb.DebugPrefix),
		tapePrefix:      r.sectionKeyMap(kb.TapePrefix),
		layoutPrefix:    r.sectionKeyMap(kb.LayoutPrefix),
		tuiosPrefix:     r.sectionKeyMap(kb.TuiosPrefix),
		terminalMode:    r.sectionKeyMap(kb.TerminalMode),
		global:          r.withCommands(r.sectionKeyMap(kb.Global), SectionGlobal),
		script:          r.sectionKeyMap(kb.Script),
		lists:           r.sectionKeyMap(kb.Lists),
		sidebar:         r.sectionKeyMap(kb.Sidebar),
		sidebarFiles:    r.sectionKeyMap(kb.SidebarFiles),
		sidebarAgents:   r.sectionKeyMap(kb.SidebarAgents),
		inbox:           r.sectionKeyMap(kb.Inbox),
		inboxPeek:       r.sectionKeyMap(kb.InboxPeek),
		mail:            r.sectionKeyMap(kb.Mail),
		transfers:       r.sectionKeyMap(kb.Transfers),
		copyMode:        r.sectionKeyMap(kb.CopyMode),
		menus:           make(map[string]map[string]string, len(kb.Menus)),
	}
	for _, name := range kb.MenuNames() {
		r.sections.menus[name] = r.sectionKeyMap(kb.Menus[name].Keys)
	}
}

// addSection adds all keybindings from a section to the registry. Later
// sections still override earlier ones, which is how the section order in
// buildMappings decides a cross-section clash.
func (r *KeybindRegistry) addSection(section map[string][]string) {
	// Store keys exactly as normalized (preserves case for single letters)
	// Don't lowercase here: we need case sensitivity for M vs m, etc.
	maps.Copy(r.keyToAction, r.sectionKeyMap(section))
}

// sectionKeyMap is the key→action map for one section, with the normalizer's
// platform variants expanded (opt+N → unicode on macOS).
//
// Actions are visited in name order and the first claimant of a key keeps it.
// A Go map iterates in a different order every time, so when two actions in one
// section bind the same key, last-write-wins made the key resolve to a
// different action on each press: a config that kept prefix_rename_window on
// "," after the default moved to prefix_settings opened settings on roughly one
// press in five. Duplicates are repaired at load (see dropStaleDuplicateKeys);
// this makes whatever survives behave the same way every time.
func (r *KeybindRegistry) sectionKeyMap(section map[string][]string) map[string]string {
	actions := make([]string, 0, len(section))
	for action := range section {
		actions = append(actions, action)
	}
	sort.Strings(actions)

	keyMap := make(map[string]string, len(section))
	for _, action := range actions {
		r.expandInto(keyMap, action, section[action])
	}
	return keyMap
}

// expandInto adds the keys of one action to keyMap: every plain spelling of
// each key, then the spellings that hold only under an assumption about the
// keyboard, each in its own tier (see USLayoutKey and OptionGlyphKey). A key
// already in keyMap keeps the action that claimed it first.
//
// The tiers are why a binding the user writes for a key wins over a US alias
// of another binding. On AZERTY a binding on opt+& now runs, where before the
// alt+& alias of the default opt+shift+7 took it (issue #575).
func (r *KeybindRegistry) expandInto(keyMap map[string]string, action string, keys []string) {
	claim := func(key string) {
		if _, taken := keyMap[key]; !taken {
			keyMap[key] = action
		}
	}
	plain := r.normalizer.ExpandKeys(keys)
	for _, key := range plain {
		claim(key)
	}
	kb := &r.config.Keybindings
	if kb.KeyboardLayout == KeyboardLayoutOther {
		return
	}
	for _, key := range keys {
		for _, alias := range r.normalizer.USAliasKeys(key) {
			claim(USLayoutKey(alias))
		}
	}
	// An Option chord the terminal sent as the character a US layout composes
	// for it is looked up by the chord. With the Alt bit it is in one tier,
	// and with no modifier in another, which "bind" and "auto" fill: a bare
	// character can be text on another layout (issue #746), so under "auto"
	// lookupKey answers from it only on a US Mac layout. "type" leaves both
	// empty, so the character goes to the pane.
	for _, key := range plain {
		if !strings.HasPrefix(key, "alt+") {
			continue
		}
		if OptionGlyphRuns(kb.KeyboardLayout, kb.OptionGlyphs, true) {
			claim(OptionGlyphKey(key))
		}
		if bareGlyphTierFilled(kb.KeyboardLayout, kb.OptionGlyphs) {
			claim(BareOptionGlyphKey(key))
		}
	}
}

// withCommands adds the [[keybindings.command]] keys of one section to its
// key map. A command entry never takes a key a built-in action already has:
// the built-in keeps it, and tuios keybinds doctor reports the entry as
// shadowed (see Bindings).
func (r *KeybindRegistry) withCommands(keyMap map[string]string, section string) map[string]string {
	kb := &r.config.Keybindings
	// Config order, the order Bindings reports them in: of two entries on one
	// key, the first in the file runs.
	for _, c := range kb.Commands() {
		if c.Section() != section || kb.isLeader(c.BareKey()) {
			continue
		}
		r.expandInto(keyMap, c.Action(), []string{c.BareKey()})
	}
	return keyMap
}

// GetAction returns the action name for a given key in normal mode
func (r *KeybindRegistry) GetAction(key string) string {
	return r.lookupKey(key, r.keyToAction)
}

// LeaderTwiceGoesBack reports whether the leader pressed two times goes back
// to the last workspace rather than sending the leader. See
// KeybindingsConfig.LeaderTwice.
func (r *KeybindRegistry) LeaderTwiceGoesBack() bool {
	return r.config.Keybindings.LeaderTwice == LeaderTwiceLastWorkspace
}

// PrefixRepeats reports whether action stays armed after the leader. See
// KeybindingsConfig.Repeat.
func (r *KeybindRegistry) PrefixRepeats(action string) bool {
	return r.repeat[action]
}

// GetPrefixAction returns the action name for a given key in the main prefix mode (Ctrl+B)
func (r *KeybindRegistry) GetPrefixAction(key string) string {
	return r.lookupKey(key, r.sections.prefix)
}

// GetWindowPrefixAction returns the action name for a given key in window prefix mode (Ctrl+B, t)
func (r *KeybindRegistry) GetWindowPrefixAction(key string) string {
	return r.lookupKey(key, r.sections.windowPrefix)
}

// GetMinimizePrefixAction returns the action name for a given key in minimize prefix mode (Ctrl+B, m)
func (r *KeybindRegistry) GetMinimizePrefixAction(key string) string {
	return r.lookupKey(key, r.sections.minimizePrefix)
}

// GetWorkspacePrefixAction returns the action name for a given key in workspace prefix mode (Ctrl+B, w)
func (r *KeybindRegistry) GetWorkspacePrefixAction(key string) string {
	return r.lookupKey(key, r.sections.workspacePrefix)
}

// GetDebugPrefixAction returns the action name for a given key in debug prefix mode (Ctrl+B, D)
func (r *KeybindRegistry) GetDebugPrefixAction(key string) string {
	return r.lookupKey(key, r.sections.debugPrefix)
}

// GetTapePrefixAction returns the action name for a given key in tape prefix mode (Ctrl+B, T)
func (r *KeybindRegistry) GetTapePrefixAction(key string) string {
	return r.lookupKey(key, r.sections.tapePrefix)
}

// GetGlobalAction returns the action name for a key in the global scope, the
// binds that act in window mode and terminal mode alike. Kept out of
// buildMappings so a global bind cannot be overwritten by a same-key bind in one
// of the seven flattened window-mode sections.
func (r *KeybindRegistry) GetGlobalAction(key string) string {
	return r.lookupKey(key, r.sections.global)
}

// GetScriptAction returns the action name for a key while a tape script is
// playing back.
func (r *KeybindRegistry) GetScriptAction(key string) string {
	return r.lookupKey(key, r.sections.script)
}

// GetTuiosPrefixAction returns the action name for a given key in the tuios
// menu (the leader, then prefix_tuios's key).
func (r *KeybindRegistry) GetTuiosPrefixAction(key string) string {
	return r.lookupKey(key, r.sections.tuiosPrefix)
}

// GetMenuAction returns the action name for a given key in the declared menu
// with the given name (see MenuConfig).
func (r *KeybindRegistry) GetMenuAction(name, key string) string {
	return r.lookupKey(key, r.sections.menus[name])
}

// GetLayoutPrefixAction returns the action name for a given key in layout prefix
// mode (Ctrl+B, L).
func (r *KeybindRegistry) GetLayoutPrefixAction(key string) string {
	return r.lookupKey(key, r.sections.layoutPrefix)
}

// GetTerminalModeAction returns the action name for a given key among the
// direct terminal-mode binds (no prefix required).
func (r *KeybindRegistry) GetTerminalModeAction(key string) string {
	return r.lookupKey(key, r.sections.terminalMode)
}

// GetSidebarAction returns the action name for a given key in the rail's
// keyboard scope. Looked up in its own section rather than through the global
// keymap so rail keys (j/k/h/l/enter) never fire on a pane; only consulted while
// SidebarFocused.
func (r *KeybindRegistry) GetSidebarAction(key string) string {
	return r.lookupKey(key, r.sections.sidebar)
}

// GetSidebarFilesAction returns the action name for a key among the files
// section's own binds. Consulted before GetSidebarAction, and only while the
// rail's cursor is on a row of the listing, so the three keys the two sections
// share each mean the thing the row under the cursor is.
func (r *KeybindRegistry) GetSidebarFilesAction(key string) string {
	return r.lookupKey(key, r.sections.sidebarFiles)
}

// GetSidebarAgentsAction returns the action name for a key among the agent
// rows' own binds. Consulted before GetSidebarAction, and only while the
// rail's cursor is on an agent row.
func (r *KeybindRegistry) GetSidebarAgentsAction(key string) string {
	return r.lookupKey(key, r.sections.sidebarAgents)
}

// GetSidebarAgentsKeys is GetKeys for the agent rows' binds, for the help
// overlay.
func (r *KeybindRegistry) GetSidebarAgentsKeys(action string) []string {
	return r.config.Keybindings.SidebarAgents[action]
}

// GetInboxAction returns the action a key runs in the Inbox's list.
func (r *KeybindRegistry) GetInboxAction(key string) string {
	return r.lookupKey(key, r.sections.inbox)
}

// GetInboxPeekAction returns the action a key runs in the prompt open over
// the Inbox.
func (r *KeybindRegistry) GetInboxPeekAction(key string) string {
	return r.lookupKey(key, r.sections.inboxPeek)
}

// GetMailAction returns the action a key runs in the mailbox.
func (r *KeybindRegistry) GetMailAction(key string) string {
	return r.lookupKey(key, r.sections.mail)
}

// GetCopyModeAction returns the action a key runs in copy mode, or "" for a
// key the section does not bind. Copy mode reads its vim motions itself.
func (r *KeybindRegistry) GetCopyModeAction(key string) string {
	return r.lookupKey(key, r.sections.copyMode)
}

// GetCopyModeKeys is GetKeys for the copy_mode section, for the help overlay.
func (r *KeybindRegistry) GetCopyModeKeys(action string) []string {
	return r.config.Keybindings.CopyMode[action]
}

// GetInboxKeys is GetKeys for the Inbox, its peek and the mailbox, whose
// action names are their own: the key hints of those overlays read what the
// config binds rather than a letter written into the renderer.
func (r *KeybindRegistry) GetInboxKeys(action string) []string {
	kb := &r.config.Keybindings
	for _, section := range []map[string][]string{kb.Inbox, kb.InboxPeek, kb.Mail} {
		if keys, ok := section[action]; ok {
			return keys
		}
	}
	return nil
}

// GetTransfersAction returns the action a key runs in the Copies panel.
func (r *KeybindRegistry) GetTransfersAction(key string) string {
	return r.lookupKey(key, r.sections.transfers)
}

// GetTransfersKeys is GetKeys for the Copies panel, for its key hints and the
// help overlay.
func (r *KeybindRegistry) GetTransfersKeys(action string) []string {
	return r.config.Keybindings.Transfers[action]
}

// GetSidebarFilesKeys is GetKeys for the files section's binds, for the help
// overlay.
func (r *KeybindRegistry) GetSidebarFilesKeys(action string) []string {
	return r.config.Keybindings.SidebarFiles[action]
}

// GetSidebarKeys is GetKeys for the rail's scope. GetKeys deliberately does not
// search the sidebar section (its action names collide with the global ones),
// so the help overlay needs its own way to read what the rail is bound to.
func (r *KeybindRegistry) GetSidebarKeys(action string) []string {
	return r.config.Keybindings.Sidebar[action]
}

// lookupKey performs the actual key lookup with case handling
func (r *KeybindRegistry) lookupKey(key string, keyMap map[string]string) string {
	// Trim whitespace but preserve case for single letters
	// This is important for distinguishing m vs M (shift+m)
	key = strings.TrimSpace(key)

	// For single letters, preserve case exactly as received
	// For compound keys (ctrl+x, shift+tab), normalize to lowercase.
	// Rune-aware so multi-byte AZERTY letters (é/è/à/ç) match too.
	if isSingleRuneLetter(key) {
		// Single letter: check both exact case and lowercase.
		// This handles both "M" (shift+m) and "m" inputs
		if action, ok := keyMap[key]; ok {
			return action
		}
		// Fallback to lowercase for compatibility
		return keyMap[strings.ToLower(key)]
	}

	// A bare composed character under option_glyphs = "auto" runs its chord
	// only on a US Mac layout. Asked here, not when the tier is filled, so a
	// layout switched while tuios runs is seen. The answer comes from a cache
	// the client fills in the background (see StartMacLayoutDetection).
	if strings.HasPrefix(key, bareGlyphTier) {
		kb := &r.config.Keybindings
		if !OptionGlyphRuns(kb.KeyboardLayout, kb.OptionGlyphs, false) {
			return ""
		}
	}

	// For everything else (compound keys), normalize to lowercase
	normalizedKey := strings.ToLower(key)
	return keyMap[normalizedKey]
}

// bareGlyphTierFilled reports whether the registry fills the bare
// Option-glyph tier: for "bind", and for "auto" unless keyboard_layout is
// "other". Whether "auto" answers from it is up to lookupKey. A value outside
// OptionGlyphModes is "auto" here, as in OptionGlyphRuns and as the
// validation warning says, so the bindings and the leader agree on it.
func bareGlyphTierFilled(layout, glyphs string) bool {
	if layout == KeyboardLayoutOther {
		return false
	}
	return glyphs != OptionGlyphsAlt && glyphs != OptionGlyphsType
}

// GetKeys returns the keys bound to an action in the first section below that
// binds it, as bare keys with no chord. An action bound in two sections, such
// as launcher (global alt+space and prefix a), answers with one of them only.
// Use PressesByAction to show a binding to a person.
func (r *KeybindRegistry) GetKeys(action string) []string {
	// Search through all sections
	sections := []map[string][]string{
		r.config.Keybindings.WindowManagement,
		r.config.Keybindings.Workspaces,
		r.config.Keybindings.Layout,
		r.config.Keybindings.ModeControl,
		r.config.Keybindings.System,
		r.config.Keybindings.Navigation,
		r.config.Keybindings.RestoreMinimized,
		r.config.Keybindings.PrefixMode,
		r.config.Keybindings.WindowPrefix,
		r.config.Keybindings.MinimizePrefix,
		r.config.Keybindings.WorkspacePrefix,
		r.config.Keybindings.DebugPrefix,
		r.config.Keybindings.TapePrefix,
		r.config.Keybindings.LayoutPrefix,
		r.config.Keybindings.TuiosPrefix,
		r.config.Keybindings.TerminalMode,
		r.config.Keybindings.Global,
		r.config.Keybindings.Script,
		r.config.Keybindings.Lists,
	}

	for _, section := range sections {
		if keys, ok := section[action]; ok {
			return keys
		}
	}
	return nil
}

// PressesByAction maps every action to the whole thing a user presses to reach
// it, chord included: "1" for a window-mode binding, "ctrl+b L 1" for one that
// lives under a prefix. An action bound in several scopes gets every press:
// launcher is both "alt+space" and "ctrl+b a".
//
// GetKeys answers with the bare key of the first section that binds the
// action, which is the right answer for the keymap and the wrong one for
// anything that shows a binding to a human. launcher came back as "a", its key
// under the prefix, and not its global alt+space. An action
// reachable only under a chord would be listed as "1", and a help screen that
// says 1 snaps a window to a corner, while 1 selects a window, is the same
// class of lie this whole surface exists to catch.
//
// Built in one pass and returned as a map because the help overlay is on the
// render path: asking per action would rescan every section for each of eighty
// of them, once a frame. The registry keeps the map until its next Reload, so
// callers share it and must not change it. Each slice is clipped, so an append
// to one copies it rather than writing into the next.
func PressesByAction(r *KeybindRegistry) map[string][]string {
	r.pressesMu.Lock()
	defer r.pressesMu.Unlock()
	if r.presses == nil {
		r.presses = buildPressesByAction(r)
	}
	return r.presses
}

func buildPressesByAction(r *KeybindRegistry) map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	for _, b := range r.Bindings() {
		// A shadowed binding does not run, and every caller of this is telling
		// a reader what to press. Listing a key that another action already
		// took is the lie in its most direct form. An action whose every
		// binding is dead drops out entirely, which is what the keybind
		// manager's Conflicts tab is for.
		if b.Unbound || b.Shadowed || b.Press == "" {
			continue
		}
		// The rail's keymaps and the playback keymap are live only while
		// that context owns the keyboard, and they have no chord to say so.
		// new_window is n in window mode and t on the rail, and listing both
		// as plain keys tells a reader that t opens a window anywhere.
		if contextOnlyScope(b.Scope) {
			continue
		}
		id := b.Action + "\x00" + b.Press
		if seen[id] {
			continue
		}
		seen[id] = true
		out[b.Action] = append(out[b.Action], b.Press)
	}
	for action, presses := range out {
		out[action] = slices.Clip(presses)
	}
	return out
}

// contextOnlyScope reports whether a scope's keys act only inside one
// context (the rail, its files listing, or tape playback) and are shown with
// no chord that would say so.
func contextOnlyScope(scope string) bool {
	switch scope {
	case ScopeSidebar, ScopeSidebarFiles, ScopeSidebarAgents, ScopeScript, ScopeLists, ScopeInbox, ScopeInboxPeek, ScopeMail, ScopeTransfers, ScopeCopyMode:
		return true
	case ScopeGUI:
		// Only the GUI reads these keys. A terminal's help that listed
		// ctrl+shift+t for new_window would name a key that does nothing there.
		return true
	}
	return false
}

// HasAction checks if an action exists in the registry
func (r *KeybindRegistry) HasAction(action string) bool {
	return len(r.GetKeys(action)) > 0
}

// Reload reloads the keybind mappings from the config
func (r *KeybindRegistry) Reload(cfg *UserConfig) {
	r.config = cfg
	r.buildMappings()
}

// GetConfig returns the underlying config
func (r *KeybindRegistry) GetConfig() *UserConfig {
	return r.config
}

// ActionDescriptions maps action names to their descriptions for help menu generation.
var ActionDescriptions = map[string]string{
	"file_search": "Search files below the folder the sidebar shows",
	// The rail's files section. They act only while the cursor is on a row of
	// the listing, which is why three of them share a key with a rail binding
	// above and neither loses it.
	"file_create":         "Files: new file, or a folder with a trailing /",
	"file_rename":         "Files: rename this file",
	"file_delete":         "Files: delete this file",
	"file_delete_forever": "Files: delete for good, with no trash",
	"file_copy":           "Files: copy this file",
	"file_cut":            "Files: cut this file",
	"file_paste":          "Files: paste into this folder",
	"file_open":           "Files: open this folder, or copy this file's path",
	"file_edit":           "Files: edit this text file in a new pane",
	"file_copy_path":      "Files: copy the path of this file or folder",
	"file_send":           "Files: send this file or folder to a host",
	// The Copies panel: the copies between machines.
	"transfers_down":   "Copies: next copy",
	"transfers_up":     "Copies: previous copy",
	"transfers_pause":  "Copies: pause or go on with the copy",
	"transfers_cancel": "Copies: cancel the copy, after a question",
	"transfers_retry":  "Copies: try a failed copy again",
	"transfers_clear":  "Copies: remove the copies that ended",
	"transfers_answer": "Copies: answer a copy that waits for an answer",
	"transfers_close":  "Copies: close the panel",

	"list_up":        "Lists: up one row",
	"list_down":      "Lists: down one row",
	"list_page_up":   "Lists: up a page",
	"list_page_down": "Lists: down a page",
	"list_first":     "Lists: first row",
	"list_last":      "Lists: last row",

	// The agents view, the prompt open over it, and the mailbox. Each acts
	// only while its overlay is up. The action names keep the inbox_ prefix
	// they had before the Inbox became the agents view, so configs keep
	// working.
	"inbox_down":         "Agents view: next row",
	"inbox_up":           "Agents view: previous row",
	"inbox_page_down":    "Agents view: ten rows down",
	"inbox_page_up":      "Agents view: ten rows up",
	"inbox_first":        "Agents view: first row",
	"inbox_last":         "Agents view: last row",
	"inbox_go":           "Agents view: go to the agent's pane and close the view, or open the mail",
	"inbox_peek":         "Agents view: read the prompt here and answer it",
	"inbox_dismiss":      "Agents view: dismiss what needs you on the row",
	"inbox_reply":        "Agents view: reply to the agent or the mail, or reject the held approval",
	"inbox_resume":       "Agents view: resume the conversation in its pane",
	"inbox_pass_on":      "Agents view: pass held mail on to the agent it was for",
	"inbox_filter":       "Agents view: show only what needs you, then each kind, then every agent",
	"inbox_select":       "Agents view: search the agents, with selector terms such as harness:codex",
	"inbox_mailbox":      "Agents view: open the whole mailbox",
	"inbox_close":        "Agents view: close",
	"inbox_write":        "Agents view: write a reply to the selected agent",
	"inbox_sort":         "Agents view: step the sort",
	"inbox_view_pane":    "Agents view: zoom the selected agent's pane, and come back with the same key",
	"inbox_review":       "Agents view: review the changes in the agent's pane",
	"inbox_snooze":       "Agents view: snooze what needs you on the row, then 1 to 4 for how long",
	"inbox_undo":         "Agents view: undo the last dismiss or snooze",
	"inbox_show_snoozed": "Agents view: show or hide the snoozed rows",
	"inbox_deny_reason":  "Agents view: reject the approval or plan and say why",
	"inbox_new_agent":    "Agents view: start a new agent",
	"inbox_detail_down":  "Agents view: scroll the card down",
	"inbox_detail_up":    "Agents view: scroll the card up",
	"inbox_allow":        "Agents view: allow the held approval once, or approve the plan",
	"inbox_allow_always": "Agents view: always allow the held approval",
	"inbox_open":         "Agents view: open the focus on a narrow screen, or show every row of a long group",
	"inbox_back":         "Agents view: go back from the focus on a narrow screen",
	"inbox_help":         "Agents view: show every key",
	"inbox_mark":         "Agents view: mark the agent for a bulk answer",
	"inbox_mark_all":     "Agents view: mark every agent shown, or clear the marks",
	"inbox_tab_prev":     "Agents view: the focus's tab to the left",
	"inbox_tab_next":     "Agents view: the focus's tab to the right",
	"inbox_expand":       "Agents view: show every line of every card in the thread, or fold them again",
	"inbox_compare":      "Agents view: compare the attempts of the fan under the cursor, ask a judge, keep one",

	// The rail's agent rows. They act only while the cursor is on an agent
	// row, and a key they share with a rail binding keeps its rail meaning on
	// every other row.
	"agent_unread":        "Agents: mark the pane's finished turn unread",
	"agent_snooze":        "Agents: snooze what needs you in the pane",
	"agent_reply":         "Agents: reply to the agent",
	"agent_review":        "Agents: review the pane's changes",
	"agent_subagents":     "Agents: open or close the subagent rows",
	"agent_cancel_queued": "Agents: drop the newest queued message",
	"peek_approve":        "Prompt: approve",
	"peek_approve_always": "Prompt: approve and do not ask again",
	"peek_deny":           "Prompt: reject",
	"peek_type":           "Prompt: type an answer",
	"peek_read_again":     "Prompt: read the prompt again",
	"peek_go":             "Prompt: go to the pane",
	"peek_back":           "Prompt: back to the agents view",
	"mail_down":           "Mailbox: next thread",
	"mail_up":             "Mailbox: previous thread",
	"mail_page_down":      "Mailbox: ten threads down",
	"mail_page_up":        "Mailbox: ten threads up",
	"mail_open":           "Mailbox: open the thread, or reply in an open one",
	"mail_reply":          "Mailbox: reply in the open thread",
	"mail_focus_pane":     "Mailbox: go to the pane that last wrote",
	"mail_new":            "Mailbox: write a new message to an agent",
	"mail_back":           "Mailbox: back, or close",

	// The rail's reorder actions. They are here for the machine header's menu,
	// which resolves every row's label and key hint from this table.
	"reorder_up":   "Rail: move this session or machine up",
	"reorder_down": "Rail: move this session or machine down",

	// Window Management
	"new_window":     "New window",
	"new_window_ssh": "New window that runs the focused pane's ssh",
	"close_window":   "Close window",
	"rename_window":  "Rename window",
	"set_accent":     "Accent color",
	// Reached from the rail's session row and its menu; the row carries which
	// session, so it has no key of its own.
	"set_session_accent": "Session color",
	"minimize_window":    "Minimize window",
	"restore_all":        "Restore all minimized",
	"toggle_zoom":        "Toggle zoom (fullscreen)",
	"toggle_pip":         "Pin or unpin the picture-in-picture view",
	"start_screensaver":  "Start the screen saver now",
	"screenshot":         "Pick what to screenshot",
	"screenshot_window":  "Screenshot this window",
	"screenshot_screen":  "Screenshot the whole screen",
	"next_window":        "Next window",
	"prev_window":        "Previous window",
	"last_pane":          "Back to the previous pane",
	"select_window_1":    "Select window 1",
	"select_window_2":    "Select window 2",
	"select_window_3":    "Select window 3",
	"select_window_4":    "Select window 4",
	"select_window_5":    "Select window 5",
	"select_window_6":    "Select window 6",
	"select_window_7":    "Select window 7",
	"select_window_8":    "Select window 8",
	"select_window_9":    "Select window 9",

	// Multifocus. Neither action has a default key.
	"toggle_multifocus_active": "Add or remove the focused pane from multifocus",
	"toggle_multifocus_all":    "Add all panes on the workspace to multifocus, or clear it",
	"close_workspace":          "Close every pane on a workspace, after a confirmation",

	// Workspaces
	"next_workspace":     "Switch to the next workspace that has panes",
	"prev_workspace":     "Switch to the previous workspace that has panes",
	"switch_workspace_1": "Switch to workspace 1",
	"switch_workspace_2": "Switch to workspace 2",
	"switch_workspace_3": "Switch to workspace 3",
	"switch_workspace_4": "Switch to workspace 4",
	"switch_workspace_5": "Switch to workspace 5",
	"switch_workspace_6": "Switch to workspace 6",
	"switch_workspace_7": "Switch to workspace 7",
	"switch_workspace_8": "Switch to workspace 8",
	"switch_workspace_9": "Switch to workspace 9",
	"move_and_follow_1":  "Move to workspace 1 and follow",
	"move_and_follow_2":  "Move to workspace 2 and follow",
	"move_and_follow_3":  "Move to workspace 3 and follow",
	"move_and_follow_4":  "Move to workspace 4 and follow",
	"move_and_follow_5":  "Move to workspace 5 and follow",
	"move_and_follow_6":  "Move to workspace 6 and follow",
	"move_and_follow_7":  "Move to workspace 7 and follow",
	"move_and_follow_8":  "Move to workspace 8 and follow",
	"move_and_follow_9":  "Move to workspace 9 and follow",
	// Reached by the chord and by a dock pill's menu, so both name it the same.
	"workspace_prefix_rename": "Rename workspace",
	"workspace_pill_switch":   "Switch to workspace",

	// Layout
	"snap_left":                 "Snap or focus left",
	"snap_right":                "Snap or focus right",
	"focus_up":                  "Focus the window above",
	"focus_down":                "Focus the window below",
	"snap_fullscreen":           "Fullscreen",
	"unsnap":                    "Unsnap",
	"snap_corner_1":             "Snap to top-left",
	"snap_corner_2":             "Snap to top-right",
	"snap_corner_3":             "Snap to bottom-left",
	"snap_corner_4":             "Snap to bottom-right",
	"toggle_tiling":             "Toggle tiling mode",
	"swap_left":                 "Swap left",
	"swap_right":                "Swap right",
	"swap_up":                   "Swap up",
	"swap_down":                 "Swap down",
	"resize_master_shrink":      "Decrease master width",
	"resize_master_grow":        "Increase master width",
	"resize_height_shrink":      "Decrease focused window height",
	"resize_height_grow":        "Increase focused window height",
	"resize_master_shrink_left": "Decrease master width from left edge",
	"resize_master_grow_left":   "Increase master width from left edge",
	"resize_height_shrink_top":  "Decrease focused window height from top edge",
	"resize_height_grow_top":    "Increase focused window height from top edge",
	"resize_width_10":           "Set focused window width to 10%",
	"resize_width_20":           "Set focused window width to 20%",
	"resize_width_30":           "Set focused window width to 30%",
	"resize_width_40":           "Set focused window width to 40%",
	"resize_width_50":           "Set focused window width to 50%",
	"resize_width_60":           "Set focused window width to 60%",
	"resize_width_70":           "Set focused window width to 70%",
	"resize_width_80":           "Set focused window width to 80%",
	"resize_width_90":           "Set focused window width to 90%",
	"resize_height_10":          "Set focused window height to 10%",
	"resize_height_20":          "Set focused window height to 20%",
	"resize_height_30":          "Set focused window height to 30%",
	"resize_height_40":          "Set focused window height to 40%",
	"resize_height_50":          "Set focused window height to 50%",
	"resize_height_60":          "Set focused window height to 60%",
	"resize_height_70":          "Set focused window height to 70%",
	"resize_height_80":          "Set focused window height to 80%",
	"resize_height_90":          "Set focused window height to 90%",

	// BSP Tiling
	"split_horizontal":               "Split window horizontally (top/bottom)",
	"split_vertical":                 "Split window vertically (left/right)",
	"split_ssh_horizontal":           "Split top/bottom, and run the focused pane's ssh in the new pane",
	"split_ssh_vertical":             "Split left/right, and run the focused pane's ssh in the new pane",
	"rotate_split":                   "Rotate split",
	"equalize_splits":                "Equalize all split ratios",
	"preselect_left":                 "Preselect left for next window",
	"preselect_right":                "Preselect right for next window",
	"preselect_up":                   "Preselect up for next window",
	"preselect_down":                 "Preselect down for next window",
	"cycle_tiling_scheme":            "Cycle the workspace's tiling scheme: spiral, longest side, alternate, smart split",
	"set_tiling_scheme_spiral":       "Set the workspace's tiling scheme to spiral",
	"set_tiling_scheme_longest_side": "Set the workspace's tiling scheme to longest side",
	"set_tiling_scheme_alternate":    "Set the workspace's tiling scheme to alternate",
	"set_tiling_scheme_smart_split":  "Set the workspace's tiling scheme to smart split",

	// Master-stack
	"cycle_master_position":      "Move the master panes to the next side: left, right, top, bottom, center",
	"set_master_position_left":   "Put the master panes on the left",
	"set_master_position_right":  "Put the master panes on the right",
	"set_master_position_top":    "Put the master panes at the top",
	"set_master_position_bottom": "Put the master panes at the bottom",
	"set_master_position_center": "Put the master panes in the center, with the stack on both sides",
	"add_master":                 "Make one more pane a master pane",
	"remove_master":              "Make one pane fewer a master pane",
	"swap_with_master":           "Swap the focused pane with the master pane",
	"focus_master":               "Focus the master pane",

	// Mode Control
	"enter_terminal_mode": "Enter terminal mode",
	"enter_window_mode":   "Enter window management mode",
	"hold_window_mode":    "Hold for window management mode",
	"toggle_help":         "Toggle help",
	"open_settings":       "Open settings",
	"quit":                "Quit",
	"focus_sidebar":       "Focus sidebar",
	"next_session":        "Next session",
	"prev_session":        "Previous session",
	"switch_session_1":    "Switch to session 1",
	"switch_session_2":    "Switch to session 2",
	"switch_session_3":    "Switch to session 3",
	"switch_session_4":    "Switch to session 4",
	"switch_session_5":    "Switch to session 5",
	"switch_session_6":    "Switch to session 6",
	"switch_session_7":    "Switch to session 7",
	"switch_session_8":    "Switch to session 8",
	"switch_session_9":    "Switch to session 9",

	// Clipboard
	"copy_selection":  "Copy selection to clipboard",
	"paste_clipboard": "Paste from clipboard",
	"paste_image":     "Paste the clipboard image as a file path",
	"paste_buffer":    "Paste the newest paste buffer",
	"choose_buffer":   "Choose a paste buffer to paste",
	"clear_selection": "Clear the text selection",
	"hints":           "Label the text on the pane to copy it",
	"display_panes":   "Label the panes and focus one by its label",
	"toggle_scratch":  "Show or hide the scratch terminal",
	"hints_all_panes": "Label the text on all panes to copy it",

	// The scrollback in the editor (prefix E, ctrl+g in copy mode)
	ActionEditScrollback: "Open the scrollback in the editor",

	// Copy mode search (no default keybinding)
	ActionCopyModeSearchForward:  "Enter copy mode and search forward",
	ActionCopyModeSearchBackward: "Enter copy mode and search backward",
	ActionCopyModeLineStart:      "Copy mode: go to the start of the line",
	ActionCopyModeLineEnd:        "Copy mode: go to the end of the line",

	// Session lifecycle (context menu rows; no default keybinding)
	"settings_sidebar":  "Sidebar settings",
	"rename_session":    "Rename the session the menu was opened on",
	"rename_workspace":  "Rename the workspace the session is showing",
	"kill_session":      "Kill the session the menu was opened on",
	"kill_session_next": "Kill session, go to next",
	"kill_session_quit": "Kill session and quit",
	// The palette, the settings, the rail's menu for this machine and the
	// version notice; no default key.
	"restart_daemon": "Restart the daemon with the newest tuios installed",

	// System
	"toggle_logs":        "Toggle log viewer",
	"toggle_cache_stats": "Toggle cache statistics",
	"toggle_spotlight":   "Toggle spotlight",

	// Prefix Mode
	"prefix_new_window":         "Create new window",
	"prefix_close_window":       "Close current window",
	"prefix_rename_window":      "Rename window",
	"prefix_settings":           "Open settings",
	"prefix_keybinds":           "Open the keybind manager",
	"prefix_next_window":        "Next window",
	"prefix_prev_window":        "Previous window",
	"prefix_select_0":           "Jump to window 0",
	"prefix_select_1":           "Jump to window 1",
	"prefix_select_2":           "Jump to window 2",
	"prefix_select_3":           "Jump to window 3",
	"prefix_select_4":           "Jump to window 4",
	"prefix_select_5":           "Jump to window 5",
	"prefix_select_6":           "Jump to window 6",
	"prefix_select_7":           "Jump to window 7",
	"prefix_select_8":           "Jump to window 8",
	"prefix_select_9":           "Jump to window 9",
	"prefix_toggle_tiling":      "Toggle tiling mode",
	"prefix_workspace":          "Enter workspace prefix",
	"prefix_minimize":           "Enter minimize prefix",
	"prefix_window":             "Enter window prefix",
	"prefix_detach":             "Detach, leave running",
	"prefix_close_session":      "Close session and panes",
	"prefix_exit_mode":          "Leave terminal mode",
	"prefix_selection":          "Enter copy/scrollback mode",
	"prefix_help":               "Toggle help",
	"prefix_debug":              "Enter debug prefix",
	"prefix_tape":               "Enter tape manager prefix",
	"prefix_quit":               "Quit (daemon: kills session)",
	"prefix_fullscreen":         "Fullscreen current window",
	"prefix_split_horizontal":   "Split window horizontally",
	"prefix_split_vertical":     "Split window vertically",
	"mark_pane":                 "Mark the focused pane, or clear the mark",
	"join_marked_left":          "Join the marked pane left of this one",
	"join_marked_right":         "Join the marked pane right of this one",
	"join_marked_up":            "Join the marked pane above this one",
	"join_marked_down":          "Join the marked pane below this one",
	"resize_pane_left_5":        "Narrow the focused pane by 5",
	"resize_pane_right_5":       "Widen the focused pane by 5",
	"resize_pane_up_5":          "Shorten the focused pane by 5",
	"resize_pane_down_5":        "Heighten the focused pane by 5",
	"resize_pane_left_20":       "Narrow the focused pane by 20",
	"resize_pane_right_20":      "Widen the focused pane by 20",
	"resize_pane_up_20":         "Shorten the focused pane by 20",
	"resize_pane_down_20":       "Heighten the focused pane by 20",
	"new_workspace":             "Go to the first empty workspace",
	"last_workspace":            "Go back to the last workspace",
	"break_pane":                "Move the focused pane to the first empty workspace, and follow it",
	"last_session":              "Go back to the last session",
	"send_prefix":               "Send the leader key to the focused pane",
	"move_workspace_left":       "Swap this workspace with the one before it",
	"move_workspace_right":      "Swap this workspace with the one after it",
	"send_to_workspace_1":       "Send the focused pane to workspace 1",
	"send_to_workspace_2":       "Send the focused pane to workspace 2",
	"send_to_workspace_3":       "Send the focused pane to workspace 3",
	"send_to_workspace_4":       "Send the focused pane to workspace 4",
	"send_to_workspace_5":       "Send the focused pane to workspace 5",
	"send_to_workspace_6":       "Send the focused pane to workspace 6",
	"send_to_workspace_7":       "Send the focused pane to workspace 7",
	"send_to_workspace_8":       "Send the focused pane to workspace 8",
	"send_to_workspace_9":       "Send the focused pane to workspace 9",
	"reload_config":             "Reload the config file",
	"toggle_mouse":              "Turn mouse capture off or on (off: the terminal selects text)",
	"cycle_layout":              "Cycle the layout: BSP, master-stack, scrolling",
	"prefix_rotate_split":       "Rotate split",
	"prefix_equalize_splits":    "Equalize all splits",
	"prefix_scrollback":         "Open the scrollback browser",
	"prefix_screenshot":         "Take a screenshot",
	"prefix_command_palette":    "Open the command palette",
	"prefix_file_search":        "Search files below the focused pane's directory",
	"prefix_toggle_sidebar":     "Toggle the session sidebar",
	"prefix_toggle_other_rail":  "Toggle the opposite sidebar",
	"prefix_toggle_spotlight":   "Toggle spotlight",
	"prefix_explore":            "Focus/leave sidebar",
	"prefix_jump_notif":         "Jump to newest message",
	"prefix_last_message":       "Show the last message in full",
	"prefix_mail":               "Open the agents view on the mail for you",
	"prefix_inbox":              "Open the agents view",
	"prefix_transfers":          "Show the copies between machines",
	"prefix_next_attention":     "Jump to the oldest item needing you",
	"prefix_review":             "Review the focused pane's changes",
	"prefix_next_finished":      "Jump to the newest unseen finished turn",
	"prefix_agents_settings":    "Open the Agents settings",
	"prefix_session_switcher":   "Open the session switcher",
	"prefix_workspace_switcher": "Open the workspace switcher",
	"choose_tree":               "Find a pane in every session and go to it",
	"goto_picker":               "Go to an agent, session, workspace or pane",
	"prefix_layout":             "Enter layout prefix",

	// Tape Prefix
	"tape_prefix_manager": "Open tape manager",
	"tape_prefix_review":  "Review project tape",
	"tape_prefix_record":  "Start recording",
	"tape_prefix_stop":    "Stop recording",
	"tape_prefix_cancel":  "Cancel tape prefix",

	// Tape Actions
	"toggle_tape_manager": "Toggle tape manager",
	"stop_recording":      "Stop tape recording",

	// Debug Prefix
	"debug_prefix_logs":       "Toggle log viewer",
	"debug_prefix_cache":      "Toggle cache statistics",
	"debug_prefix_animations": "Toggle animations",
	"debug_prefix_showkeys":   "Toggle showkeys overlay",
	"debug_prefix_cancel":     "Cancel debug prefix",

	// Terminal Mode (direct keybinds, no prefix required)
	"terminal_next_window":      "Next window (terminal mode)",
	"terminal_prev_window":      "Previous window (terminal mode)",
	"terminal_exit_mode":        "Exit terminal mode (to window mode)",
	"terminal_focus_left":       "Focus the pane to the left",
	"terminal_focus_right":      "Focus the pane to the right",
	"terminal_focus_up":         "Focus the pane above",
	"terminal_focus_down":       "Focus the pane below",
	"terminal_scroll_up":        "Scroll into the pane's scrollback",
	"terminal_scroll_down":      "Scroll back down toward live output",
	"terminal_scroll_page_up":   "Scroll up one page",
	"terminal_scroll_page_down": "Scroll down one page",
	"terminal_paste_host":       "Paste from the host clipboard",

	// Global (window mode and terminal mode alike)
	"command_palette": "Open the command palette",
	"launcher":        "Open the app launcher",
	"new_session":     "Make a session, asking which machine",

	// Script playback
	"script_pause": "Pause or resume the playing tape",

	// Layout Prefix
	"layout_prefix_load":   "Open the layout picker",
	"layout_prefix_save":   "Save the current layout",
	"layout_prefix_cancel": "Cancel layout prefix",
	"window_prefix_new":    "Create a window",
	"window_prefix_close":  "Close the window",
	"window_prefix_rename": "Rename the window",
	"window_prefix_next":   "Focus the next window",
	"window_prefix_prev":   "Focus the previous window",
	"window_prefix_tiling": "Turn tiling on or off",
	"prefix_tuios":         "Open the tuios menu: the tuios keys a preset moved",
	"open_keybind_presets": "Choose the keys: tuios, tmux, screen or zellij",
}
