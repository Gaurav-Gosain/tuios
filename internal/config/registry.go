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
	sections [tableCount]map[string]string
	// menus holds each declared menu (see MenuConfig), resolved the same
	// way, by name.
	menus map[string]map[string]string

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
// "," after the default moved to open_settings opened settings on roughly one
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
	// The config's tables hold each action by its own name (see
	// canonicalizeKeybindings), so an alias asks for that name.
	action = CanonicalAction(action)
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
	case ScopeSidebar, ScopeSidebarFiles, ScopeSidebarAgents, ScopeQuickLook, ScopeQuickLookIn, ScopeScript, ScopeLists, ScopeInbox, ScopeInboxPeek, ScopeMail, ScopeTransfers, ScopeCopyMode, ScopeReview:
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

// ActionDescriptions maps each action name to its description, for the help,
// the keybinds list and the readers that look a name up. It is derived from
// actionTable; change the table, not this map.
var ActionDescriptions = actionDescriptions()
