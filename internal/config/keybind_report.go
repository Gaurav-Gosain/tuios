package config

import (
	"sort"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/plural"
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
			detail += ". The program list has an entry for " + p.Name
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
			Detail:   "a full-screen program is drawing in this pane. It probably wants most of the keyboard",
			Evidence: EvidenceObserved,
		})
	}
	if f.GuestKittyFlags != 0 {
		out = append(out, Observation{
			What:     "Guest keyboard",
			Detail:   "the program in this pane uses the kitty keyboard protocol. It asked for keys tuios must not take",
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
			Detail:   "this terminal does not separate these keys. Ctrl+I is Tab, Ctrl+M is Enter and Ctrl+[ is Esc",
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
		// An unbound action has no key, so it contests nothing. Left in, every
		// one of them would group under the empty key and the first would be
		// reported as winning a contest that does not exist.
		if b.Unbound {
			continue
		}
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
	// ReadAs is the spelling tuios matches the key in, when it differs from
	// Key by more than case: opt+f12 is read as alt+f12.
	ReadAs string `json:"read_as,omitempty"`
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
	fate := KeyFate{Key: key, ReadAs: readAs(key)}

	for _, b := range r.Bindings() {
		if !b.Unbound && lookupForm(b.Key) == want {
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

	fate.Ambiguity = AmbiguityVerdict(want, facts.HostDisambiguates)

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
	// LeaderReadAs is the spelling tuios matches the leader in, when it
	// differs from Leader by more than case.
	LeaderReadAs string `json:"leader_read_as,omitempty"`
	// KeyProblems are the keys in config.toml that tuios cannot read.
	KeyProblems []KeyProblem `json:"key_problems"`
	// CommandProblems are the [[keybindings.command]] entries tuios ignores
	// or warns about, in the validator's words.
	CommandProblems []CommandProblem `json:"command_problems"`
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
	// Yielded are new default bindings tuios left off because the key was
	// already bound to another action in the same table.
	Yielded []YieldedDefault `json:"yielded_defaults,omitempty"`
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
		leader = DefaultLeaderKey
	}
	rep := KeybindReport{
		Leader: leader,
		EvidenceNote: map[Evidence]string{
			EvidenceCertain:   "Derived from tuios's own keybind registry and dispatch order. If this is wrong, tuios has a bug.",
			EvidenceObserved:  "Read from the pane when this report was built. True then, and possibly stale now.",
			EvidenceReference: "A fixed list of what these programs bind by default. Nothing was detected. If you rebound the program, this entry is wrong for you.",
		},
		Pane:         facts,
		Yielded:      r.config.YieldedDefaults,
		Observations: facts.Observations(),
		Bindings:     r.Bindings(),
		Collisions:   r.Collisions(),
		Swallowed:    r.TerminalModeSwallowed(),
		GuestClashes: r.GuestClashes(facts.Command),
		LeaderReadAs: readAs(leader),
		KeyProblems:  r.KeyProblems(),

		CommandProblems: r.config.Keybindings.CommandProblems(),
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
	if n := len(rep.KeyProblems); n > 0 {
		parts = append(parts, plural.CountAs(n, "key tuios cannot read", "keys tuios cannot read"))
	}
	ignored := 0
	for _, p := range rep.CommandProblems {
		if p.Ignored {
			ignored++
		}
	}
	if ignored > 0 {
		parts = append(parts, plural.CountAs(ignored, "command entry tuios ignores", "command entries tuios ignores"))
	}
	if n := len(rep.Collisions); n > 0 {
		parts = append(parts, plural.CountAs(n, "key claimed twice", "keys claimed twice"))
	}
	if n := len(rep.GuestClashes); n > 0 {
		parts = append(parts, plural.CountAs(n, "key a guest wants", "keys a guest wants"))
	}
	if n := len(rep.Ambiguous); n > 0 {
		parts = append(parts, plural.CountAs(n, "ambiguous key", "ambiguous keys"))
	}
	if len(parts) == 0 {
		return "No conflicts found"
	}
	return strings.Join(parts, ", ")
}

// readAs returns the canonical spelling of key when it differs from what the
// user wrote by more than case, and "" when it does not. A key the validator
// rejects on this platform gets "" too: opt+f12 on Linux is not read as
// anything, and the doctor lists it as a key tuios cannot read.
func readAs(key string) string {
	trimmed := strings.TrimSpace(key)
	if ok, _ := (&KeyNormalizer{isMacOS: macOSHost}).ValidateKey(trimmed); !ok {
		return ""
	}
	canonical := CanonicalKey(trimmed)
	if canonical == trimmed || canonical == strings.ToLower(trimmed) {
		return ""
	}
	return canonical
}

// KeyProblem is one key in config.toml that tuios cannot read, so it never
// matches a key press.
type KeyProblem struct {
	// Section is the config table, or "keybindings" for the leader.
	Section string `json:"section"`
	// Action is the action the key is bound to, or "leader_key".
	Action string `json:"action"`
	Key    string `json:"key"`
	// Problem is what is wrong, in the validator's words.
	Problem  string   `json:"problem"`
	Evidence Evidence `json:"evidence"`
}

// KeyProblems returns every key in the leader and the binding tables that the
// validator rejects. Such a key is loaded but no key press ever matches it, so
// without this the only sign of it is a binding that does nothing.
func (r *KeybindRegistry) KeyProblems() []KeyProblem {
	kb := &r.config.Keybindings
	normalizer := &KeyNormalizer{isMacOS: macOSHost}
	var out []KeyProblem
	check := func(section, action, key string) {
		if strings.TrimSpace(key) == "" {
			return
		}
		if ok, msg := normalizer.ValidateKey(key); !ok {
			out = append(out, KeyProblem{
				Section: section, Action: action, Key: key,
				Problem: msg, Evidence: EvidenceCertain,
			})
		}
	}
	if kb.LeaderKey != "" {
		check("keybindings", "leader_key", kb.LeaderKey)
	}
	for _, name := range SectionNames() {
		section := kb.section(name)
		actions := make([]string, 0, len(section))
		for action := range section {
			actions = append(actions, action)
		}
		sort.Strings(actions)
		for _, action := range actions {
			for _, key := range section[action] {
				check(name, action, key)
			}
		}
	}
	return out
}
