package config

import (
	"sort"
	"strings"
)

// A Scope is one keyboard context: a set of keys that are looked up together
// and therefore compete with each other. Two actions on one key only collide
// when they share a scope, so the scope is the unit every conflict is stated
// against.
//
// The scopes are not a re-description of the config's sections. Seven sections
// are flattened into one lookup map by buildMappings, so those seven share a
// scope and a key bound twice across them is a real collision even though the
// TOML shows it in two different tables. The prefix sections each stay their
// own scope because they are only ever consulted after their chord.
type Scope struct {
	ID string
	// Name is what the scope is called on screen.
	Name string
	// Chord is what has to be pressed to get into this scope, empty when the
	// scope is entered by being in a mode rather than by a chord. It prefixes
	// every key in the scope when the binding is shown as something to press.
	Chord string
	// Sections are the config tables the scope's keys are drawn from, in the
	// order they are consulted.
	Sections []string
	// Reaches is the guest-facing consequence of the scope: whether keys bound
	// here are taken out of the stream that would otherwise reach the program
	// running in the pane.
	Reaches Reach
}

// Reach says what a scope's bindings do to the program running in the pane.
type Reach int

const (
	// ReachModal means tuios owns the whole keyboard while the scope is active,
	// so no binding here can be said to steal anything: the guest is not being
	// typed at in the first place.
	ReachModal Reach = iota
	// ReachSteals means the scope is active while the user is typing into the
	// guest, so every key bound here is one the guest will never see.
	ReachSteals
	// ReachChorded means the scope is only reached after a chord, so it costs
	// the guest nothing beyond the chord's own first key.
	ReachChorded
)

// Section names as they appear in config.toml, used to name where a binding
// came from without exposing the Go field.
const (
	SectionWindowManagement = "window_management"
	SectionWorkspaces       = "workspaces"
	SectionLayout           = "layout"
	SectionModeControl      = "mode_control"
	SectionSystem           = "system"
	SectionNavigation       = "navigation"
	SectionRestoreMinimized = "restore_minimized"
	SectionPrefixMode       = "prefix_mode"
	SectionWindowPrefix     = "window_prefix"
	SectionMinimizePrefix   = "minimize_prefix"
	SectionWorkspacePrefix  = "workspace_prefix"
	SectionDebugPrefix      = "debug_prefix"
	SectionTapePrefix       = "tape_prefix"
	SectionLayoutPrefix     = "layout_prefix"
	SectionTerminalMode     = "terminal_mode"
	SectionSidebar          = "sidebar"
	SectionGlobal           = "global"
	SectionScript           = "script"
)

// Scope identifiers. Stable strings rather than an iota because they are part of
// the JSON an agent reads.
const (
	ScopeWindowMode     = "window"
	ScopeTerminalMode   = "terminal"
	ScopeSidebar        = "sidebar"
	ScopePrefix         = "prefix"
	ScopePrefixWindow   = "prefix.window"
	ScopePrefixMinimize = "prefix.minimize"
	ScopePrefixWorkspce = "prefix.workspace"
	ScopePrefixDebug    = "prefix.debug"
	ScopePrefixTape     = "prefix.tape"
	ScopePrefixLayout   = "prefix.layout"
	ScopeGlobal         = "global"
	ScopeScript         = "script"
)

// Scopes returns every keyboard context, in the order a reader should meet
// them: the two modes the user spends their time in, then the rail, then the
// chords.
//
// The chords are spelled with the configured leader rather than a literal
// ctrl+b, since rebinding the leader moves every one of them.
func Scopes(leader string) []Scope {
	if leader == "" {
		leader = LeaderKey
	}
	return []Scope{
		{
			// Global is listed first because it is consulted in both of the
			// modes below and wins nothing from them: a key bound here acts
			// wherever the user is, which is exactly why it must be visible.
			ID: ScopeGlobal, Name: "Global",
			Sections: []string{SectionGlobal},
			Reaches:  ReachSteals,
		},
		{
			ID: ScopeWindowMode, Name: "Window mode",
			Sections: []string{
				SectionWindowManagement, SectionWorkspaces, SectionLayout,
				SectionModeControl, SectionSystem, SectionNavigation,
				SectionRestoreMinimized,
			},
			Reaches: ReachModal,
		},
		{
			ID: ScopeTerminalMode, Name: "Terminal mode",
			Sections: []string{SectionTerminalMode},
			Reaches:  ReachSteals,
		},
		{
			ID: ScopeSidebar, Name: "Sidebar",
			Sections: []string{SectionSidebar},
			Reaches:  ReachModal,
		},
		{
			ID: ScopePrefix, Name: "Prefix", Chord: leader,
			Sections: []string{SectionPrefixMode},
			Reaches:  ReachChorded,
		},
		{
			ID: ScopePrefixWindow, Name: "Window prefix", Chord: leader + " t",
			Sections: []string{SectionWindowPrefix},
			Reaches:  ReachChorded,
		},
		{
			ID: ScopePrefixMinimize, Name: "Minimize prefix", Chord: leader + " m",
			Sections: []string{SectionMinimizePrefix},
			Reaches:  ReachChorded,
		},
		{
			ID: ScopePrefixWorkspce, Name: "Workspace prefix", Chord: leader + " w",
			Sections: []string{SectionWorkspacePrefix},
			Reaches:  ReachChorded,
		},
		{
			ID: ScopePrefixDebug, Name: "Debug prefix", Chord: leader + " D",
			Sections: []string{SectionDebugPrefix},
			Reaches:  ReachChorded,
		},
		{
			ID: ScopePrefixTape, Name: "Tape prefix", Chord: leader + " T",
			Sections: []string{SectionTapePrefix},
			Reaches:  ReachChorded,
		},
		{
			ID: ScopePrefixLayout, Name: "Layout prefix", Chord: leader + " L",
			Sections: []string{SectionLayoutPrefix},
			Reaches:  ReachChorded,
		},
		{
			// Only live while a .tape is playing back, so sharing ctrl+p with the
			// palette is not a conflict: the two contexts are never both active.
			ID: ScopeScript, Name: "Script playback",
			Sections: []string{SectionScript},
			Reaches:  ReachModal,
		},
	}
}

// section returns the config table with the given name, or nil.
func (k *KeybindingsConfig) section(name string) map[string][]string {
	switch name {
	case SectionWindowManagement:
		return k.WindowManagement
	case SectionWorkspaces:
		return k.Workspaces
	case SectionLayout:
		return k.Layout
	case SectionModeControl:
		return k.ModeControl
	case SectionSystem:
		return k.System
	case SectionNavigation:
		return k.Navigation
	case SectionRestoreMinimized:
		return k.RestoreMinimized
	case SectionPrefixMode:
		return k.PrefixMode
	case SectionWindowPrefix:
		return k.WindowPrefix
	case SectionMinimizePrefix:
		return k.MinimizePrefix
	case SectionWorkspacePrefix:
		return k.WorkspacePrefix
	case SectionDebugPrefix:
		return k.DebugPrefix
	case SectionTapePrefix:
		return k.TapePrefix
	case SectionLayoutPrefix:
		return k.LayoutPrefix
	case SectionGlobal:
		return k.Global
	case SectionScript:
		return k.Script
	case SectionTerminalMode:
		return k.TerminalMode
	case SectionSidebar:
		return k.Sidebar
	}
	return nil
}

// SectionFor returns the config table with the given name so a caller can add a
// binding to it, or nil for a name that is not a section. The map is the live
// one, which is the point: a recorder writing into it and then reloading the
// registry is how a newly bound key takes effect without a restart.
func (k *KeybindingsConfig) SectionFor(name string) map[string][]string {
	return k.section(name)
}

// SectionNames returns every section name in the order Scopes visits them, so a
// caller can iterate the config's tables without repeating the switch above.
func SectionNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range Scopes("") {
		for _, name := range s.Sections {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

// Binding is one key bound to one action inside one scope.
type Binding struct {
	Scope   string `json:"scope"`
	Section string `json:"section"`
	Action  string `json:"action"`
	Desc    string `json:"description"`
	// Key is the key as the lookup sees it, after the normalizer's platform
	// expansion has been undone: what the user wrote in config.toml.
	Key string `json:"key"`
	// Press is the whole thing to press, chord included.
	Press string `json:"press"`
	// Shadowed is set when an earlier claimant in the same scope already took
	// the key, so pressing it runs that other action instead of this one.
	Shadowed bool `json:"shadowed"`
	// ShadowedBy names the action that actually runs, when Shadowed.
	ShadowedBy string `json:"shadowed_by,omitempty"`
	// Unbound is set on the one row an action with no keys still gets. Key and
	// Press are empty on such a row.
	//
	// It is listed rather than left out because the alternative is a one-way
	// door: an action taken off its key would vanish from every surface that
	// reads this, and the overlay binds a key by selecting the action's row.
	// Unbinding something and then being unable to find it again is not an
	// unbind, it is a loss.
	Unbound bool `json:"unbound,omitempty"`
}

// press joins a chord and a key the way the rest of the interface spells a
// chord: space-separated, so "ctrl+b w 1" reads as three presses.
func press(chord, key string) string {
	if chord == "" {
		return key
	}
	return chord + " " + key
}

// Bindings returns every binding in every scope, in scope order, section order,
// and then action-name order.
//
// Which of two bindings on one key is the live one is not the same question
// inside a section as across them, and getting that backwards makes the whole
// report name the wrong action. Inside a section, sectionKeyMap hands the key to
// the first claimant in action-name order and later ones are skipped. Across
// sections, addSection does a maps.Copy into the shared map, so a later section
// overwrites what an earlier one put there. The two rules pull in opposite
// directions, which is why the winner is resolved in its own pass below rather
// than by whoever got there first.
func (r *KeybindRegistry) Bindings() []Binding {
	kb := &r.config.Keybindings
	var out []Binding
	for _, scope := range Scopes(kb.LeaderKey) {
		// The window-mode scope flattens seven sections into a single lookup, so
		// a key is contested across all of them at once. winner holds the index
		// into out of the binding that actually runs, per key.
		winner := map[string]int{}
		// claimedIn is the section index that currently owns the key, so a
		// second claim from the same section can be told from one that
		// overwrites from a later section.
		claimedIn := map[string]int{}

		for sectionIdx, name := range scope.Sections {
			section := kb.section(name)
			actions := make([]string, 0, len(section))
			for action := range section {
				actions = append(actions, action)
			}
			sort.Strings(actions)
			for _, action := range actions {
				// An action the config leaves with no keys gets one row saying
				// so. See Binding.Unbound, and keybind_unbind.go for how the
				// file spells it.
				if liveKeyCount(section[action]) == 0 {
					out = append(out, Binding{
						Scope:   scope.ID,
						Section: name,
						Action:  action,
						Desc:    describeAction(action),
						Unbound: true,
					})
					continue
				}
				for _, key := range section[action] {
					key = strings.TrimSpace(key)
					if key == "" {
						continue
					}
					idx := len(out)
					out = append(out, Binding{
						Scope:   scope.ID,
						Section: name,
						Action:  action,
						Desc:    describeAction(action),
						Key:     key,
						Press:   press(scope.Chord, key),
					})

					lookup := lookupForm(key)
					prev, taken := winner[lookup]
					switch {
					case !taken:
						winner[lookup] = idx
						claimedIn[lookup] = sectionIdx
					case out[prev].Action == action:
						// The same action bound to the same key twice is a
						// duplicate in the config, not a contest.
						out[idx].Shadowed = true
						out[idx].ShadowedBy = action
					case claimedIn[lookup] < sectionIdx:
						// A later section overwrites: the new claim wins and the
						// old one goes dark.
						out[prev].Shadowed = true
						out[prev].ShadowedBy = action
						winner[lookup] = idx
						claimedIn[lookup] = sectionIdx
					default:
						// Same section, and the earlier action name already took
						// it.
						out[idx].Shadowed = true
						out[idx].ShadowedBy = out[prev].Action
					}
				}
			}
		}
	}
	return out
}

// lookupForm is the shape lookupKey compares a key in: single letters keep
// their case, everything else is lowercased.
func lookupForm(key string) string {
	key = strings.TrimSpace(key)
	if isSingleRuneLetter(key) {
		return key
	}
	return strings.ToLower(key)
}

// describeAction is the human sentence for an action, falling back to the
// action name with its underscores opened up.
func describeAction(action string) string {
	if desc := ActionDescriptions[action]; desc != "" {
		return desc
	}
	return strings.ReplaceAll(action, "_", " ")
}
