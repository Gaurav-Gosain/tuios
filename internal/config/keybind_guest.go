package config

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// Evidence says how much weight a finding carries. tuios sits between the
// keyboard and someone else's program, and the three tiers are the three
// genuinely different things it can say about that program: what tuios itself
// does (it decided it), what this pane has asserted (it was told), and what a
// program of that name usually binds (nobody checked).
//
// The tier travels with every finding rather than being implied by the section
// it is printed under, because the whole point of the guest half of this
// analysis is that its weakest tier must never be read as its strongest.
type Evidence string

const (
	// EvidenceCertain is a fact about tuios's own routing, derived from the
	// registry and the dispatch order. If it is wrong, tuios has a bug.
	EvidenceCertain Evidence = "certain"
	// EvidenceObserved is a fact the pane asserted at the moment it was read:
	// the alternate screen is on, the kitty keyboard protocol was pushed with
	// these flags, this is the foreground process group's command name. True
	// when read, and possibly stale a second later.
	EvidenceObserved Evidence = "observed"
	// EvidenceReference is a curated list of what a program of that name binds
	// by default. It is not detection: the program is not asked, its config is
	// not read, and a user who rebound it is not accounted for. It is here
	// because a curated list of the six prefixes that actually collide is worth
	// more than silence, and it is labelled so it is never mistaken for the
	// tier above.
	EvidenceReference Evidence = "reference"
)

// hardcodedTerminalKeys are the keys terminal mode takes out of the stream
// without going through the registry, listed by hand because a report that only
// covered the configurable ones would tell the user their ctrl+p reaches fish.
//
// It is empty now, and staying empty is the point: the palette, the launcher,
// the scrollback scroll and the host-paste chords all resolve through the
// registry, so the report derives them. Anything added here is a key nobody can
// rebind, and should be a binding instead.
//
// Keep in step with HandleTerminalModeKey and handleTerminalModeBinds.
var hardcodedTerminalKeys = map[string]string{}

// Swallow is one key terminal mode takes before the pane's program can see it.
type Swallow struct {
	Key    string `json:"key"`
	Action string `json:"action"`
	Desc   string `json:"description"`
	// Origin says where the claim comes from: a config section, or "built-in"
	// for the keys the input path spells literally.
	Origin string `json:"origin"`
}

// TerminalModeSwallowed returns every key that does not reach the program
// running in the focused pane while the user is typing into it.
//
// This is the honest half of the guest question. tuios cannot know what the
// guest wants, but it knows exactly what it withholds, and that set is small
// enough to read: the leader, the terminal_mode table, the handful of
// navigation actions that survive into terminal mode on a reserved chord, and
// the literals above. Everything absent from this list is forwarded.
func (r *KeybindRegistry) TerminalModeSwallowed() []Swallow {
	kb := &r.config.Keybindings
	seen := map[string]bool{}
	var out []Swallow
	add := func(key, action, desc, origin string) {
		key = strings.TrimSpace(key)
		if key == "" || seen[lookupForm(key)] {
			return
		}
		seen[lookupForm(key)] = true
		out = append(out, Swallow{Key: key, Action: action, Desc: desc, Origin: origin})
	}

	leader := kb.LeaderKey
	if leader == "" {
		leader = DefaultLeaderKey
	}
	add(leader, "leader", "Start a prefix chord", "built-in")

	// The global scope acts in terminal mode too, so everything bound in it is
	// withheld from the pane exactly like a terminal_mode bind.
	for _, name := range []string{SectionGlobal, SectionTerminalMode} {
		section := kb.section(name)
		actions := make([]string, 0, len(section))
		for action := range section {
			actions = append(actions, action)
		}
		sort.Strings(actions)
		for _, action := range actions {
			for _, key := range section[action] {
				add(key, action, describeAction(action), name)
			}
		}
	}

	// The main sections reach terminal mode only through isTerminalSafeAction,
	// and only on a chord the shell would never want as literal input. Applying
	// both filters here is what keeps the report from claiming tuios eats the
	// plain letters window mode binds.
	for _, name := range []string{
		SectionWindowManagement, SectionWorkspaces, SectionLayout,
		SectionModeControl, SectionSystem, SectionNavigation,
		SectionRestoreMinimized,
	} {
		section := kb.section(name)
		actions := make([]string, 0, len(section))
		for action := range section {
			actions = append(actions, action)
		}
		sort.Strings(actions)
		for _, action := range actions {
			if !terminalSafeAction(action) {
				continue
			}
			for _, key := range section[action] {
				if !reservedTerminalChord(key) {
					continue
				}
				add(key, action, describeAction(action), name)
			}
		}
	}

	for _, key := range sortedKeys(hardcodedTerminalKeys) {
		add(key, "", hardcodedTerminalKeys[key], "built-in")
	}

	// The hold trigger is only inert when no terminal reports releases, and the
	// report cannot know that from here, so it is listed: a held modifier that
	// does reach the input path is one the guest does not get.
	for _, key := range kb.section(SectionModeControl)["hold_window_mode"] {
		add(key, "hold_window_mode", describeAction("hold_window_mode"), SectionModeControl)
	}

	return out
}

// terminalSafeAction mirrors input.isTerminalSafeAction. The two are separate
// because this package cannot import the input path, and they are checked
// against each other by TestSwallowSetMatchesTheInputPath.
func terminalSafeAction(action string) bool {
	return strings.HasPrefix(action, "switch_workspace_") ||
		strings.HasPrefix(action, "move_and_follow_") ||
		action == "next_session" || action == "prev_session"
}

// reservedTerminalChord mirrors input.isReservedTerminalChord for a key written
// in config.toml rather than for a decoded key event: a real Alt or Ctrl
// modifier, or a bare macOS Option glyph.
func reservedTerminalChord(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	for _, mod := range []string{"alt+", "opt+", "option+", "ctrl+", "control+"} {
		if strings.Contains(lower, mod) {
			return true
		}
	}
	if utf8.RuneCountInString(key) == 1 {
		r, _ := utf8.DecodeRuneInString(key)
		if _, ok := MacOSOptionChord(r); ok {
			return true
		}
	}
	return false
}

// GuestProgram is one entry in the curated table: a program that runs inside a
// pane and the keys it claims by default.
type GuestProgram struct {
	// Name is what to call it on screen.
	Name string
	// Comms are the process names it appears under in the foreground process
	// group, used to raise an entry from reference to "this is what is running".
	Comms []string
	// Keys maps a key to what the program does with it. Deliberately short: the
	// entries earn their place by colliding with something tuios binds or by
	// being the program's prefix, not by being complete. A complete keymap for
	// vim would be a lie told at length.
	Keys map[string]string
	// Note is the one sentence a user needs about this program's claim.
	Note string
}

// GuestPrograms is the curated table.
//
// It is a curated list of defaults, not detection. Nothing here is read from
// the program, its config, or its runtime state, so a user who moved tmux's
// prefix to ctrl+a has an entry that is wrong for them. It is worth carrying
// anyway: the collisions that actually bite are a short list, they are stable
// across years, and the alternative is an interface that shows a user their
// leader is ctrl+b without ever mentioning that so is tmux's.
var GuestPrograms = []GuestProgram{
	{
		Name:  "tmux",
		Comms: []string{"tmux", "tmux: client", "tmux: server"},
		Keys: map[string]string{
			"ctrl+b": "prefix (every tmux command starts here)",
		},
		Note: "tmux's default prefix is the same key as tuios's. Nested, the outer one wins and the inner multiplexer is unreachable.",
	},
	{
		Name:  "GNU screen",
		Comms: []string{"screen", "SCREEN"},
		Keys: map[string]string{
			"ctrl+a": "prefix (every screen command starts here)",
		},
		Note: "screen's prefix is ctrl+a, which is also readline's start-of-line.",
	},
	{
		Name:  "zellij",
		Comms: []string{"zellij"},
		Keys: map[string]string{
			"ctrl+p": "pane mode",
			"ctrl+t": "tab mode",
			"ctrl+n": "resize mode",
			"ctrl+s": "search / scroll mode",
			"ctrl+o": "session mode",
			"ctrl+g": "lock the whole keyboard",
		},
		Note: "zellij claims a row of bare ctrl+letter chords rather than one prefix, so it collides with more of tuios than tmux does.",
	},
	{
		Name:  "wlterm",
		Comms: []string{"wlterm"},
		Keys: map[string]string{
			"ctrl+b": "prefix",
		},
		Note: "Another ctrl+b prefix, and a compositor in a pane also wants key releases.",
	},
	{
		Name:  "vim / neovim",
		Comms: []string{"vim", "nvim", "vi", "view"},
		Keys: map[string]string{
			"ctrl+w": "window commands",
			"ctrl+v": "visual block",
			"ctrl+o": "jump back",
			"ctrl+i": "jump forward (the same byte as Tab)",
			"ctrl+r": "redo",
			"ctrl+a": "increment number",
			"ctrl+d": "half page down",
			"ctrl+u": "half page up",
			"ctrl+p": "keyword completion",
			"ctrl+n": "keyword completion",
		},
		Note: "vim binds most of the control range. ctrl+i is the one worth knowing: it is Tab's byte, so a terminal that does not disambiguate cannot tell them apart.",
	},
	{
		Name:  "emacs",
		Comms: []string{"emacs", "emacsclient"},
		Keys: map[string]string{
			"ctrl+x": "the C-x prefix (half of emacs lives here)",
			"ctrl+c": "the C-c prefix (mode-specific commands)",
			"ctrl+a": "start of line",
			"ctrl+e": "end of line",
			"ctrl+k": "kill line",
			"ctrl+s": "incremental search",
			"ctrl+g": "cancel",
			"ctrl+h": "help prefix",
		},
		Note: "emacs treats ctrl+x and ctrl+c as prefixes, so taking either one away removes a large part of the program.",
	},
	{
		Name:  "readline (bash, zsh, and most REPLs)",
		Comms: []string{"bash", "zsh", "sh", "dash", "ksh", "python", "python3", "irb", "node", "psql", "sqlite3"},
		Keys: map[string]string{
			"ctrl+a": "start of line",
			"ctrl+e": "end of line",
			"ctrl+k": "kill to end of line",
			"ctrl+u": "kill to start of line",
			"ctrl+w": "kill previous word",
			"ctrl+r": "reverse history search",
			"ctrl+l": "clear screen",
			"ctrl+d": "end of input",
			"ctrl+y": "yank",
			"ctrl+t": "transpose characters",
		},
		Note: "Every readline program shares this set, so a tuios binding on one of these keys is felt in the shell and in every REPL launched from it.",
	},
	{
		Name:  "fish",
		Comms: []string{"fish"},
		Keys: map[string]string{
			"ctrl+f": "accept the autosuggestion",
			"ctrl+r": "history search",
			"ctrl+p": "history back",
			"ctrl+n": "history forward",
			"alt+up": "history for the current token",
		},
		Note: "fish's ctrl+f is the autosuggestion, which is the thing people notice missing first.",
	},
	{
		Name:  "less / man / git pager",
		Comms: []string{"less", "man", "more", "pager"},
		Keys: map[string]string{
			"ctrl+f": "page forward",
			"ctrl+b": "page back",
			"ctrl+d": "half page down",
			"ctrl+u": "half page up",
		},
		Note: "less binds ctrl+b to page back, so a leader on ctrl+b is felt in every man page.",
	},
	{
		Name:  "fzf",
		Comms: []string{"fzf"},
		Keys: map[string]string{
			"ctrl+j": "move down",
			"ctrl+k": "move up",
			"ctrl+t": "file widget",
			"ctrl+r": "history widget",
		},
		Note: "fzf is usually reached through a shell widget, so its keys are live for as long as the picker is up and not before.",
	},
	{
		Name:  "htop",
		Comms: []string{"htop", "btop", "top"},
		Keys:  map[string]string{},
		Note:  "Function keys and letters rather than control chords, so it collides with tuios rarely.",
	},
	{
		Name:  "yazi / ranger / mc",
		Comms: []string{"yazi", "ranger", "mc", "nnn", "lf"},
		Keys: map[string]string{
			"ctrl+a": "select all",
			"ctrl+r": "refresh",
		},
		Note: "File managers lean on plain letters, which terminal mode forwards untouched.",
	},
	{
		Name:  "nano",
		Comms: []string{"nano", "pico"},
		Keys: map[string]string{
			"ctrl+o": "write out",
			"ctrl+x": "exit",
			"ctrl+w": "search",
			"ctrl+k": "cut line",
			"ctrl+g": "help",
		},
		Note: "nano's whole command set is control chords, and it prints them along the bottom, so a stolen one is visibly broken.",
	},
}

// GuestProgramByComm returns the curated entry whose process names include comm,
// and whether one was found. The comparison is on the base name lowercased, so
// /usr/bin/nvim and NVIM both land on the vim entry.
func GuestProgramByComm(comm string) (GuestProgram, bool) {
	comm = strings.ToLower(strings.TrimSpace(comm))
	if i := strings.LastIndexByte(comm, '/'); i >= 0 {
		comm = comm[i+1:]
	}
	if comm == "" {
		return GuestProgram{}, false
	}
	for _, p := range GuestPrograms {
		for _, c := range p.Comms {
			if strings.ToLower(c) == comm {
				return p, true
			}
		}
	}
	return GuestProgram{}, false
}

// GuestClash is one key tuios withholds that a curated program is known to want.
type GuestClash struct {
	Key string `json:"key"`
	// TuiosAction is what tuios does with the key instead.
	TuiosAction string `json:"tuios_action"`
	TuiosDesc   string `json:"tuios_description"`
	Program     string `json:"program"`
	// ProgramUse is what the program would have done with it.
	ProgramUse string `json:"program_use"`
	Note       string `json:"note"`
	// Evidence is EvidenceReference for every entry here, and it is carried
	// explicitly so a consumer never has to infer it from the field's name.
	Evidence Evidence `json:"evidence"`
	// Running is true when the pane that was inspected is actually running this
	// program, which lifts the finding from "some day" to "right now". The
	// clash itself stays reference-tier either way: knowing nvim is running is
	// observation, knowing nvim wants ctrl+w is not.
	Running bool `json:"running"`
}

// GuestClashes crosses the terminal-mode swallow set with the curated table.
//
// Only the swallow set is crossed, because a key tuios forwards costs the guest
// nothing no matter who else binds it. That is what keeps the output to the
// handful of rows that are actually about a program being broken.
//
// running is the foreground process name of the pane being inspected, or "" when
// it is not known; it only marks rows, it does not filter them.
func (r *KeybindRegistry) GuestClashes(running string) []GuestClash {
	live, _ := GuestProgramByComm(running)
	var out []GuestClash
	for _, s := range r.TerminalModeSwallowed() {
		key := lookupForm(s.Key)
		for _, p := range GuestPrograms {
			use, wants := p.Keys[key]
			if !wants {
				continue
			}
			out = append(out, GuestClash{
				Key:         s.Key,
				TuiosAction: s.Action,
				TuiosDesc:   s.Desc,
				Program:     p.Name,
				ProgramUse:  use,
				Note:        p.Note,
				Evidence:    EvidenceReference,
				Running:     p.Name == live.Name,
			})
		}
	}
	// The running program's clashes first: they are the ones the user can see
	// being wrong without leaving the pane.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Running != out[j].Running {
			return out[i].Running
		}
		return false
	})
	return out
}

// sortedKeys returns a map's keys in order, so output is stable.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
