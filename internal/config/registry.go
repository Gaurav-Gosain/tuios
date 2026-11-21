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
	keyToAction map[string]string
	repeat      map[string]bool
	config      *UserConfig
	normalizer  *KeyNormalizer

	sections [tableCount]map[string]string
	menus    map[string]map[string]string
	modes    map[string]map[string]string

	pressesMu sync.Mutex
	presses   map[string][]string
	live      map[string]*liveScope
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

	r.addSection(r.config.Keybindings.WindowManagement)
	r.addSection(r.config.Keybindings.Workspaces)
	r.addSection(r.config.Keybindings.Layout)
	r.addSection(r.config.Keybindings.ModeControl)
	r.addSection(r.config.Keybindings.System)
	r.addSection(r.config.Keybindings.Navigation)
	r.addSection(r.config.Keybindings.RestoreMinimized)
	kb := &r.config.Keybindings
	for t := range keybindTables {
		tbl := &keybindTables[t]
		keys := r.sectionKeyMap(*tbl.field(kb))
		if Table(t) == TablePrefixMode || Table(t) == TableGlobal {
			keys = r.withCommands(keys, tbl.name)
		}
		r.sections[t] = keys
	}
	r.menus = make(map[string]map[string]string, len(kb.Menus))
	for _, name := range kb.MenuNames() {
		r.menus[name] = r.sectionKeyMap(kb.Menus[name].Keys)
	}
	r.modes = make(map[string]map[string]string, len(kb.Modes))
	for _, name := range kb.ModeNames() {
		r.modes[name] = r.sectionKeyMap(kb.Modes[name].Keys)
	}
}

func (r *KeybindRegistry) addSection(section map[string][]string) {
	maps.Copy(r.keyToAction, r.sectionKeyMap(section))
}

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

func (r *KeybindRegistry) withCommands(keyMap map[string]string, section string) map[string]string {
	kb := &r.config.Keybindings
	for _, e := range r.config.keyedEntries() {
		if e.section != section || kb.isLeader(e.key) {
			continue
		}
		r.expandInto(keyMap, e.action, []string{e.key})
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
	return r.repeat[CanonicalAction(action)]
}

// GetMenuAction returns the action name for a given key in the declared menu
// with the given name (see MenuConfig).
func (r *KeybindRegistry) GetMenuAction(name, key string) string {
	return r.lookupKey(key, r.menus[name])
}

// GetModeAction returns the action name for a given key in the declared key
// mode with the given name (see ModeConfig).
func (r *KeybindRegistry) GetModeAction(name, key string) string {
	return r.lookupKey(key, r.modes[name])
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

// Action returns the action a key runs in table t, or "" when the table does
// not bind the key. Prefix and global include their command binds.
func (r *KeybindRegistry) Action(t Table, key string) string {
	return r.lookupKey(key, r.sections[t])
}

// Keys returns the keys table t binds to action, as the config writes them.
// GetKeys searches the window-mode tables only; this reads one table, for the
// key hints and the help overlay of a scope with its own action names.
func (r *KeybindRegistry) Keys(t Table, action string) []string {
	return (*keybindTables[t].field(&r.config.Keybindings))[CanonicalAction(action)]
}

func (r *KeybindRegistry) lookupKey(key string, keyMap map[string]string) string {
	key = strings.TrimSpace(key)

	if isSingleRuneLetter(key) {
		if action, ok := keyMap[key]; ok {
			return action
		}
		return keyMap[strings.ToLower(key)]
	}

	if strings.HasPrefix(key, bareGlyphTier) {
		kb := &r.config.Keybindings
		if !OptionGlyphRuns(kb.KeyboardLayout, kb.OptionGlyphs, false) {
			return ""
		}
	}

	normalizedKey := strings.ToLower(key)
	return keyMap[normalizedKey]
}

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
	action = CanonicalAction(action)
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
		if b.Unbound || b.Shadowed || b.Press == "" {
			continue
		}
		if contextOnlyScope(b.Scope) {
			continue
		}
		if r.hidesWithoutWindowMode(b) {
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

func contextOnlyScope(scope string) bool {
	switch scope {
	case ScopeSidebar, ScopeSidebarFiles, ScopeSidebarAgents, ScopeQuickLook, ScopeQuickLookIn, ScopeScript, ScopeLists, ScopeInbox, ScopeInboxPeek, ScopeMail, ScopeTransfers, ScopeCopyMode, ScopeReview:
		return true
	case ScopeGUI:
		return true
	}
	return false
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

// ActionDescriptions maps each action name to its description, for the help,
// the keybinds list and the readers that look a name up. It is derived from
// actionTable; change the table, not this map.
var ActionDescriptions = actionDescriptions()
