package config

import (
	"sort"
	"strings"
)

// PaneFacts is what the caller observed about the pane the report is about.
// Every field is optional; a zero PaneFacts produces a report with no
// observed-tier findings rather than a report with wrong ones.
type PaneFacts struct {
	// Command is the foreground process group's command name, or "" when it
	// could not be read (no pane, not Linux, the process went away).
	Command string `json:"command,omitempty"`
	// AltScreen is whether the pane's program is on the alternate screen, which
	// is the closest thing to "a full-screen program is running" that a
	// terminal emulator can observe.
	AltScreen bool `json:"alt_screen"`
	// GuestKittyFlags are the kitty keyboard protocol flags the pane's program
	// pushed. Non-zero means the program explicitly asked to be sent keys in a
	// disambiguated form, which is the strongest statement a guest ever makes
	// about what it wants from the keyboard.
	GuestKittyFlags int `json:"guest_kitty_flags"`
	// HostDisambiguates is whether the host terminal granted tuios key
	// disambiguation. It decides whether the ambiguous pairs are separable
	// here.
	HostDisambiguates bool `json:"host_disambiguates"`
	// HasForeground is whether anything beyond the pane's shell is running.
	HasForeground bool `json:"has_foreground_process"`
}

// Observation is one thing the report can state about the pane as fact.
type Observation struct {
	What     string   `json:"what"`
	Detail   string   `json:"detail"`
	Evidence Evidence `json:"evidence"`
}

// Observations turns the pane facts into sentences, each carrying the tier it
// was arrived at. Only facts that were actually available produce a line: an
// absent signal is left out rather than reported as a negative, because "no
// program detected" and "detection is not available on this platform" are
// different things and only one of them is true here.
func (f PaneFacts) Observations() []Observation {
	var out []Observation
	if f.Command != "" {
		detail := f.Command + " is the foreground process in this pane"
		if p, ok := GuestProgramByComm(f.Command); ok {
			detail += "; the curated table has an entry for " + p.Name
		}
		out = append(out, Observation{What: "Running", Detail: detail, Evidence: EvidenceObserved})
	} else if f.HasForeground {
		out = append(out, Observation{
			What:     "Running",
			Detail:   "something beyond the shell is running, but its name could not be read",
			Evidence: EvidenceObserved,
		})
	}
	if f.AltScreen {
		out = append(out, Observation{
			What:     "Alt screen",
			Detail:   "the pane is on the alternate screen, so a full-screen program is drawing and is likely to want most of the keyboard",
			Evidence: EvidenceObserved,
		})
	}
	if f.GuestKittyFlags != 0 {
		out = append(out, Observation{
			What:     "Guest keyboard",
			Detail:   "the program in this pane pushed the kitty keyboard protocol, so it asked for keys tuios would otherwise flatten; keys it wants are keys tuios must not take",
			Evidence: EvidenceObserved,
		})
	}
	if f.HostDisambiguates {
		out = append(out, Observation{
			What:     "Host keyboard",
			Detail:   "this terminal reports disambiguated keys, so Ctrl+I, Ctrl+M and Ctrl+[ are separable from Tab, Enter and Esc",
			Evidence: EvidenceObserved,
		})
	} else {
		out = append(out, Observation{
			What:     "Host keyboard",
			Detail:   "this terminal has not granted key disambiguation, so Ctrl+I is Tab, Ctrl+M is Enter and Ctrl+[ is Esc",
			Evidence: EvidenceObserved,
		})
	}
	return out
}

// Collision is two tuios actions competing for one key inside one scope. It is
// the certain tier: it is decided entirely by tuios's own lookup order, so it
// is not a guess about anything.
type Collision struct {
	Scope     string `json:"scope"`
	ScopeName string `json:"scope_name"`
	Key       string `json:"key"`
	Press     string `json:"press"`
	// Winner is the action the key actually runs.
	Winner     string `json:"winner"`
	WinnerDesc string `json:"winner_description"`
	// Losers are the actions bound to the same key that never run.
	Losers []CollisionLoser `json:"losers"`
	// CrossSection is true when the competing actions came from different
	// tables in config.toml. Those are the ones worth flagging loudest: the two
	// bindings look unrelated in the file, and nothing in the TOML hints that
	// one of them is dead.
	CrossSection bool     `json:"cross_section"`
	Evidence     Evidence `json:"evidence"`
}

// CollisionLoser is one action that lost a key.
type CollisionLoser struct {
	Action  string `json:"action"`
	Desc    string `json:"description"`
	Section string `json:"section"`
}

// Collisions returns every key claimed more than once inside a single scope.
func (r *KeybindRegistry) Collisions() []Collision {
	scopeNames := map[string]Scope{}
	for _, s := range Scopes(r.config.Keybindings.LeaderKey) {
		scopeNames[s.ID] = s
	}

	type group struct {
		winner   Binding
		losers   []Binding
		sections map[string]bool
	}
	groups := map[string]*group{}
	var order []string

	for _, b := range r.Bindings() {
		id := b.Scope + "\x00" + lookupForm(b.Key)
		g := groups[id]
		if g == nil {
			g = &group{sections: map[string]bool{}}
			groups[id] = g
			order = append(order, id)
		}
		g.sections[b.Section] = true
		if b.Shadowed {
			g.losers = append(g.losers, b)
		} else {
			g.winner = b
		}
	}

	var out []Collision
	for _, id := range order {
		g := groups[id]
		if len(g.losers) == 0 {
			continue
		}
		scope := scopeNames[g.winner.Scope]
		c := Collision{
			Scope:        g.winner.Scope,
			ScopeName:    scope.Name,
			Key:          g.winner.Key,
			Press:        g.winner.Press,
			Winner:       g.winner.Action,
			WinnerDesc:   g.winner.Desc,
			CrossSection: len(g.sections) > 1,
			Evidence:     EvidenceCertain,
		}
		for _, l := range g.losers {
			c.Losers = append(c.Losers, CollisionLoser{Action: l.Action, Desc: l.Desc, Section: l.Section})
		}
		out = append(out, c)
	}

	// Cross-section first: those are the ones the config file gives no hint of.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CrossSection != out[j].CrossSection {
			return out[i].CrossSection
		}
		return false
	})
	return out
}

// KeyFate is what tuios does with one key, everywhere it could do something.
// It is what the recorder answers with, and it is deliberately the whole
// picture rather than the first match: a key can be a window-mode action and a
// prefix action and be swallowed in terminal mode all at once, and knowing only
// one of those is how a user ends up rebinding the wrong one.
type KeyFate struct {
	Key string `json:"key"`
	// Acts is every scope the key does something in.
	Acts []Binding `json:"acts"`
	// SwallowedInTerminal is set when the key does not reach the pane's program
	// while typing, with the reason.
	SwallowedInTerminal bool   `json:"swallowed_in_terminal"`
	SwallowReason       string `json:"swallow_reason,omitempty"`
	// Ambiguity is the terminal-level pair the key belongs to, if any.
	Ambiguity string `json:"ambiguity,omitempty"`
	// GuestWants is every curated program that binds this key.
	GuestWants []GuestClash `json:"guest_wants,omitempty"`
	// Free is true when nothing in tuios claims the key in any scope.
	Free bool `json:"free"`
}

// Fate returns everything tuios knows about one key.
func (r *KeybindRegistry) Fate(key string, facts PaneFacts) KeyFate {
	want := lookupForm(key)
	fate := KeyFate{Key: key}

	for _, b := range r.Bindings() {
		if lookupForm(b.Key) == want {
			fate.Acts = append(fate.Acts, b)
		}
	}

	for _, s := range r.TerminalModeSwallowed() {
		if lookupForm(s.Key) != want {
			continue
		}
		fate.SwallowedInTerminal = true
		fate.SwallowReason = s.Desc
		if s.Origin == "built-in" {
			fate.SwallowReason += " (built in, not configurable)"
		}
		break
	}

	fate.Ambiguity = AmbiguityVerdict(key, facts.HostDisambiguates)

	// Only the curated table is consulted here, and only for a key tuios keeps
	// from the pane: a key that is forwarded costs the guest nothing whoever
	// else binds it.
	if fate.SwallowedInTerminal {
		live, _ := GuestProgramByComm(facts.Command)
		for _, p := range GuestPrograms {
			use, wants := p.Keys[want]
			if !wants {
				continue
			}
			fate.GuestWants = append(fate.GuestWants, GuestClash{
				Key:         key,
				TuiosAction: fate.SwallowReason,
				Program:     p.Name,
				ProgramUse:  use,
				Note:        p.Note,
				Evidence:    EvidenceReference,
				Running:     p.Name == live.Name,
			})
		}
		sort.SliceStable(fate.GuestWants, func(i, j int) bool {
			if fate.GuestWants[i].Running != fate.GuestWants[j].Running {
				return fate.GuestWants[i].Running
			}
			return false
		})
	}

	fate.Free = len(fate.Acts) == 0 && !fate.SwallowedInTerminal
	return fate
}

// KeybindReport is the whole analysis as data. The overlay and `tuios keybinds
// doctor --json` render the same value, so what an agent reads and what a human
// sees cannot drift apart.
type KeybindReport struct {
	Leader string `json:"leader"`
	// EvidenceNote is the report explaining its own tiers. It ships inside the
	// payload because a consumer that only ever sees the JSON has nowhere else
	// to learn that one third of it is a curated list.
	EvidenceNote map[Evidence]string `json:"evidence_note"`
	Pane         PaneFacts           `json:"pane"`
	Observations []Observation       `json:"observations"`
	Bindings     []Binding           `json:"bindings"`
	Collisions   []Collision         `json:"collisions"`
	Swallowed    []Swallow           `json:"terminal_mode_swallowed"`
	GuestClashes []GuestClash        `json:"guest_clashes"`
	Ambiguous    []AmbiguousBinding  `json:"ambiguous_bindings"`
}

// AmbiguousBinding is a binding whose key is half of a pair the terminal cannot
// always tell apart.
type AmbiguousBinding struct {
	Scope    string   `json:"scope"`
	Action   string   `json:"action"`
	Key      string   `json:"key"`
	Partners []string `json:"partners"`
	Verdict  string   `json:"verdict"`
	Evidence Evidence `json:"evidence"`
}

// Report builds the whole analysis for the given pane.
func (r *KeybindRegistry) Report(facts PaneFacts) KeybindReport {
	leader := r.config.Keybindings.LeaderKey
	if leader == "" {
		leader = LeaderKey
	}
	rep := KeybindReport{
		Leader: leader,
		EvidenceNote: map[Evidence]string{
			EvidenceCertain:   "Derived from tuios's own keybind registry and dispatch order. If this is wrong, tuios has a bug.",
			EvidenceObserved:  "Read from the pane at the moment this report was built. True when read; possibly stale now.",
			EvidenceReference: "A curated list of what these programs bind by default. Nothing was detected or asked. A user who rebound the program has an entry that is wrong for them.",
		},
		Pane:         facts,
		Observations: facts.Observations(),
		Bindings:     r.Bindings(),
		Collisions:   r.Collisions(),
		Swallowed:    r.TerminalModeSwallowed(),
		GuestClashes: r.GuestClashes(facts.Command),
	}

	seen := map[string]bool{}
	for _, b := range rep.Bindings {
		// Only the surprising spelling: see AmbiguitySurprises.
		if !AmbiguitySurprises(b.Key) {
			continue
		}
		partners := AmbiguityPartners(b.Key)
		if len(partners) == 0 {
			continue
		}
		id := b.Scope + "\x00" + lookupForm(b.Key)
		if seen[id] {
			continue
		}
		seen[id] = true
		rep.Ambiguous = append(rep.Ambiguous, AmbiguousBinding{
			Scope:    b.Scope,
			Action:   b.Action,
			Key:      b.Key,
			Partners: partners,
			Verdict:  AmbiguityVerdict(b.Key, facts.HostDisambiguates),
			Evidence: EvidenceCertain,
		})
	}
	return rep
}

// Summary is the one line a report leads with.
func (rep KeybindReport) Summary() string {
	var parts []string
	if n := len(rep.Collisions); n > 0 {
		parts = append(parts, plural(n, "key claimed twice", "keys claimed twice"))
	}
	if n := len(rep.GuestClashes); n > 0 {
		parts = append(parts, plural(n, "key a guest wants", "keys a guest wants"))
	}
	if n := len(rep.Ambiguous); n > 0 {
		parts = append(parts, plural(n, "ambiguous key", "ambiguous keys"))
	}
	if len(parts) == 0 {
		return "No conflicts found"
	}
	return strings.Join(parts, ", ")
}

// plural renders a count with the right noun.
func plural(n int, one, many string) string {
	word := many
	if n == 1 {
		word = one
	}
	return itoa(n) + " " + word
}

// itoa avoids pulling strconv in for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
