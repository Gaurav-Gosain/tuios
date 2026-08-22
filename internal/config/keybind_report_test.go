package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// registryFor builds a registry over a config with only the sections a case
// needs, so a test states its own world rather than inheriting every default.
func registryFor(t *testing.T, mutate func(*UserConfig)) *KeybindRegistry {
	t.Helper()
	cfg := DefaultConfig()
	if mutate != nil {
		mutate(cfg)
	}
	return NewKeybindRegistry(cfg)
}

func TestBindingsCoverEveryScope(t *testing.T) {
	r := registryFor(t, nil)
	seen := map[string]bool{}
	for _, b := range r.Bindings() {
		seen[b.Scope] = true
	}
	// Every scope in the table should have at least one default binding, or the
	// scope is describing a context that does not exist.
	for _, s := range Scopes("") {
		if !seen[s.ID] {
			t.Errorf("scope %q has no bindings in the default config", s.ID)
		}
	}
}

// The strongest check available: for every key the report calls contested, the
// action it names as the live one must be the action the registry's own lookup
// returns. Anything else means the overlay is describing a different program
// than the one running.
func TestReportAgreesWithTheRegistryOnEveryContestedKey(t *testing.T) {
	r := registryFor(t, nil)
	for _, c := range r.Collisions() {
		if c.Scope != ScopeWindowMode {
			continue // only the window-mode scope is reachable through GetAction
		}
		if got := r.GetAction(c.Key); got != c.Winner {
			t.Errorf("key %q: registry runs %q, report names %q as the winner", c.Key, got, c.Winner)
		}
	}
}

// The shipped defaults bind 1-4 to both select_window_N and snap_corner_N.
// layout is copied over window_management, so the snap wins and select_window_1
// is dead. This is exactly the kind of finding the overlay exists to surface,
// so it is pinned rather than treated as a bug in the test.
func TestDefaultConfigDigitKeysAreContested(t *testing.T) {
	r := registryFor(t, nil)
	found := map[string]bool{}
	for _, c := range r.Collisions() {
		if c.Scope != ScopeWindowMode || c.Key != "1" {
			continue
		}
		found["1"] = true
		if c.Winner != "snap_corner_1" {
			t.Errorf("winner = %q, want snap_corner_1: layout is copied over window_management", c.Winner)
		}
		if !c.CrossSection {
			t.Error("the two claims are in different tables, so this is a cross-section collision")
		}
		var sawLoser bool
		for _, l := range c.Losers {
			if l.Action == "select_window_1" {
				sawLoser = true
			}
		}
		if !sawLoser {
			t.Errorf("select_window_1 lost the key and must be listed: %v", c.Losers)
		}
	}
	if !found["1"] {
		t.Fatal("1 is bound to select_window_1 and snap_corner_1 in the defaults; that must be reported")
	}
}

// The direction of the cross-section rule, stated on its own so a change to
// buildMappings' section order cannot pass quietly.
func TestLaterSectionWinsAcrossSections(t *testing.T) {
	r := registryFor(t, func(c *UserConfig) {
		// window_management is visited before system.
		c.Keybindings.WindowManagement["zzz_early_section"] = []string{"ctrl+alt+k"}
		c.Keybindings.System["aaa_late_section"] = []string{"ctrl+alt+k"}
	})
	if got := r.GetAction("ctrl+alt+k"); got != "aaa_late_section" {
		t.Fatalf("registry runs %q; this test's premise about section order is stale", got)
	}
	for _, c := range r.Collisions() {
		if c.Key != "ctrl+alt+k" {
			continue
		}
		if c.Winner != "aaa_late_section" {
			t.Errorf("winner = %q, want the later section's action even though its name sorts first", c.Winner)
		}
		return
	}
	t.Fatal("no collision reported for a key claimed by two sections")
}

// A key bound twice in one scope is the collision the whole overlay exists to
// show, and the winner must be the one the registry's own lookup picks: first
// in action-name order.
func TestCollisionNamesTheActionThatActuallyRuns(t *testing.T) {
	r := registryFor(t, func(c *UserConfig) {
		c.Keybindings.WindowManagement["zzz_last_action"] = []string{"ctrl+alt+q"}
		c.Keybindings.WindowManagement["aaa_first_action"] = []string{"ctrl+alt+q"}
	})

	var got *Collision
	for i, c := range r.Collisions() {
		if c.Key == "ctrl+alt+q" {
			got = &r.Collisions()[i]
			break
		}
	}
	if got == nil {
		t.Fatal("a key bound to two actions in one section produced no collision")
	}
	if got.Winner != "aaa_first_action" {
		t.Errorf("winner = %q, want aaa_first_action (first in action-name order)", got.Winner)
	}
	if len(got.Losers) != 1 || got.Losers[0].Action != "zzz_last_action" {
		t.Errorf("losers = %v, want just zzz_last_action", got.Losers)
	}
	if got.CrossSection {
		t.Error("both actions were in window_management, so this is not a cross-section collision")
	}
	// The registry itself must agree, or the report is describing a different
	// program than the one running.
	if action := r.GetAction("ctrl+alt+q"); action != got.Winner {
		t.Errorf("registry resolves the key to %q but the report names %q", action, got.Winner)
	}
}

// The seven window-mode sections are flattened into one lookup map, so a key
// bound in two of them collides even though the TOML shows them apart. This is
// the case a user cannot see by reading their config file.
func TestCrossSectionCollisionIsFlagged(t *testing.T) {
	r := registryFor(t, func(c *UserConfig) {
		c.Keybindings.WindowManagement["custom_wm"] = []string{"ctrl+alt+z"}
		c.Keybindings.System["custom_sys"] = []string{"ctrl+alt+z"}
	})
	for _, c := range r.Collisions() {
		if c.Key != "ctrl+alt+z" {
			continue
		}
		if !c.CrossSection {
			t.Error("a key bound in window_management and system is a cross-section collision")
		}
		if c.Scope != ScopeWindowMode {
			t.Errorf("scope = %q, want %q", c.Scope, ScopeWindowMode)
		}
		return
	}
	t.Fatal("no collision reported for a key bound in two window-mode sections")
}

// The same key in two different scopes is not a conflict: they are never looked
// up together. Reporting it would bury the real ones.
func TestSameKeyInDifferentScopesIsNotACollision(t *testing.T) {
	r := registryFor(t, func(c *UserConfig) {
		c.Keybindings.WindowManagement["custom_wm"] = []string{"ctrl+alt+y"}
		c.Keybindings.PrefixMode["custom_prefix"] = []string{"ctrl+alt+y"}
	})
	for _, c := range r.Collisions() {
		if c.Key == "ctrl+alt+y" {
			t.Fatalf("window mode and prefix mode are separate scopes; %s should not collide", c.Press)
		}
	}
}

func TestLeaderIsSwallowedInTerminalMode(t *testing.T) {
	r := registryFor(t, func(c *UserConfig) { c.Keybindings.LeaderKey = "ctrl+b" })
	for _, s := range r.TerminalModeSwallowed() {
		if s.Key == "ctrl+b" {
			return
		}
	}
	t.Fatal("the leader key must be reported as swallowed: it never reaches the pane")
}

// Rebinding the leader moves the swallow, and moves every prefix chord's
// spelling with it.
func TestRebindingTheLeaderMovesTheSwallowAndTheChords(t *testing.T) {
	r := registryFor(t, func(c *UserConfig) { c.Keybindings.LeaderKey = "ctrl+a" })
	var sawA, sawB bool
	for _, s := range r.TerminalModeSwallowed() {
		switch s.Key {
		case "ctrl+a":
			sawA = true
		case "ctrl+b":
			sawB = true
		}
	}
	if !sawA {
		t.Error("ctrl+a is the leader now and must be swallowed")
	}
	if sawB {
		t.Error("ctrl+b is no longer the leader and must not be reported as swallowed")
	}
	for _, b := range r.Bindings() {
		if b.Scope == ScopePrefix && !strings.HasPrefix(b.Press, "ctrl+a ") {
			t.Fatalf("prefix binding %q should be spelled with the configured leader", b.Press)
			break
		}
	}
}

// A window-mode binding on a plain letter must not be reported as stolen from
// the shell: terminal mode never consults that section for it.
func TestPlainLetterWindowModeBindIsNotSwallowed(t *testing.T) {
	r := registryFor(t, nil)
	for _, s := range r.TerminalModeSwallowed() {
		if s.Key == "n" || s.Key == "x" {
			t.Errorf("%q is a window-mode key; terminal mode forwards it to the shell", s.Key)
		}
	}
}

// Workspace switching does reach terminal mode, but only on a reserved chord.
func TestWorkspaceSwitchIsSwallowedOnlyOnAReservedChord(t *testing.T) {
	r := registryFor(t, func(c *UserConfig) {
		c.Keybindings.Workspaces["switch_workspace_1"] = []string{"alt+1", "F1"}
	})
	var sawChord, sawBare bool
	for _, s := range r.TerminalModeSwallowed() {
		switch s.Key {
		case "alt+1":
			sawChord = true
		case "F1":
			sawBare = true
		}
	}
	if !sawChord {
		t.Error("alt+1 has a real Alt modifier, so terminal mode takes it")
	}
	if sawBare {
		t.Error("F1 has no Alt or Ctrl, so isReservedTerminalChord rejects it and the shell gets it")
	}
}

func TestHardcodedTerminalKeysAreReported(t *testing.T) {
	r := registryFor(t, nil)
	want := map[string]bool{"ctrl+p": false, "shift+up": false, "ctrl+shift+v": false}
	for _, s := range r.TerminalModeSwallowed() {
		if _, ok := want[s.Key]; ok {
			want[s.Key] = true
			if s.Origin != "built-in" {
				t.Errorf("%s is a literal in the input path, so its origin should be built-in, got %q", s.Key, s.Origin)
			}
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("%s is taken by the input path but the report does not say so", key)
		}
	}
}

// The default leader is tmux's default prefix, so a fresh install has a real
// guest clash to show. If this ever stops being true the overlay's headline
// example changes, and that is worth being told about.
func TestDefaultLeaderClashesWithTmux(t *testing.T) {
	r := registryFor(t, nil)
	for _, c := range r.GuestClashes("") {
		if c.Key == "ctrl+b" && c.Program == "tmux" {
			if c.Evidence != EvidenceReference {
				t.Errorf("evidence = %q, want reference: nothing detected tmux", c.Evidence)
			}
			return
		}
	}
	t.Fatal("ctrl+b is tuios's leader and tmux's prefix; that clash must be reported")
}

// A clash is only worth reporting for a key tuios actually withholds. vim binds
// ctrl+r, but terminal mode forwards it, so there is nothing to warn about.
func TestForwardedKeysProduceNoGuestClash(t *testing.T) {
	r := registryFor(t, nil)
	swallowed := map[string]bool{}
	for _, s := range r.TerminalModeSwallowed() {
		swallowed[lookupForm(s.Key)] = true
	}
	for _, c := range r.GuestClashes("") {
		if !swallowed[lookupForm(c.Key)] {
			t.Errorf("%s is forwarded to the pane, so %s still gets it; no clash to report", c.Key, c.Program)
		}
	}
}

// Knowing nvim is running is observation; knowing nvim wants ctrl+w is not.
// The row is marked, but its evidence tier does not move.
func TestRunningProgramMarksTheRowWithoutPromotingIt(t *testing.T) {
	r := registryFor(t, func(c *UserConfig) {
		c.Keybindings.TerminalMode["custom_steal"] = []string{"ctrl+w"}
	})
	var found bool
	for _, c := range r.GuestClashes("nvim") {
		if c.Key != "ctrl+w" || c.Program != "vim / neovim" {
			continue
		}
		found = true
		if !c.Running {
			t.Error("nvim was named as the foreground process, so the vim row is the live one")
		}
		if c.Evidence != EvidenceReference {
			t.Errorf("evidence = %q, want reference: the keymap was never read from nvim", c.Evidence)
		}
	}
	if !found {
		t.Fatal("binding ctrl+w in terminal mode takes vim's window prefix and must be reported")
	}
}

func TestGuestProgramByComm(t *testing.T) {
	cases := []struct{ comm, want string }{
		{"nvim", "vim / neovim"},
		{"NVIM", "vim / neovim"},
		{"/usr/bin/nvim", "vim / neovim"},
		{"tmux: server", "tmux"},
		{"fish", "fish"},
		{"", ""},
		{"some-program-nobody-curated", ""},
	}
	for _, tc := range cases {
		p, ok := GuestProgramByComm(tc.comm)
		if tc.want == "" {
			if ok {
				t.Errorf("GuestProgramByComm(%q) matched %q, want no match", tc.comm, p.Name)
			}
			continue
		}
		if !ok || p.Name != tc.want {
			t.Errorf("GuestProgramByComm(%q) = %q/%v, want %q", tc.comm, p.Name, ok, tc.want)
		}
	}
}

// The three pairs a user actually trips over, and the fact that the host's
// answer is what decides them.
func TestAmbiguityVerdictTurnsOnTheHostsAnswer(t *testing.T) {
	for _, key := range []string{"ctrl+i", "ctrl+m", "ctrl+["} {
		if v := AmbiguityVerdict(key, false); !strings.Contains(v, "also binds") {
			t.Errorf("without disambiguation %s must say it binds its partner too, got %q", key, v)
		}
		if v := AmbiguityVerdict(key, true); !strings.Contains(v, "separate here") {
			t.Errorf("with disambiguation %s must say the pair separates, got %q", key, v)
		}
	}
	if v := AmbiguityVerdict("ctrl+g", false); v != "" {
		t.Errorf("ctrl+g is in no ambiguous pair, got %q", v)
	}
}

func TestAmbiguityPartnersAreSymmetric(t *testing.T) {
	if got := AmbiguityPartners("tab"); len(got) != 1 || got[0] != "ctrl+i" {
		t.Errorf("AmbiguityPartners(tab) = %v, want [ctrl+i]", got)
	}
	if got := AmbiguityPartners("ctrl+i"); len(got) != 1 || got[0] != "tab" {
		t.Errorf("AmbiguityPartners(ctrl+i) = %v, want [tab]", got)
	}
}

// The recorder's whole job: press a key, get everything tuios does with it.
func TestFateReportsEveryScopeAKeyActsIn(t *testing.T) {
	r := registryFor(t, func(c *UserConfig) {
		c.Keybindings.WindowManagement["custom_wm"] = []string{"ctrl+alt+w"}
		c.Keybindings.PrefixMode["custom_prefix"] = []string{"ctrl+alt+w"}
	})
	fate := r.Fate("ctrl+alt+w", PaneFacts{})
	scopes := map[string]bool{}
	for _, a := range fate.Acts {
		scopes[a.Scope] = true
	}
	if !scopes[ScopeWindowMode] || !scopes[ScopePrefix] {
		t.Errorf("a key bound in window mode and prefix mode must report both, got %v", scopes)
	}
	if fate.Free {
		t.Error("the key is bound twice; it is not free")
	}
}

func TestFateReportsAFreeKey(t *testing.T) {
	r := registryFor(t, nil)
	fate := r.Fate("ctrl+alt+shift+f19", PaneFacts{})
	if !fate.Free {
		t.Errorf("nothing binds this key, so Fate must call it free: %+v", fate)
	}
	if fate.SwallowedInTerminal {
		t.Error("an unbound key is forwarded to the pane")
	}
}

func TestFateCarriesTheAmbiguityVerdict(t *testing.T) {
	r := registryFor(t, nil)
	if fate := r.Fate("ctrl+i", PaneFacts{HostDisambiguates: false}); !strings.Contains(fate.Ambiguity, "tab") {
		t.Errorf("Fate(ctrl+i) must mention tab, got %q", fate.Ambiguity)
	}
}

func TestObservationsOnlyStateWhatWasActuallyRead(t *testing.T) {
	// A zero PaneFacts knows nothing about the pane, so it must not claim a
	// program is absent; the only line it can honestly print is the host's
	// keyboard answer, which is known either way.
	for _, o := range (PaneFacts{}).Observations() {
		if o.What == "Running" || o.What == "Alt screen" {
			t.Errorf("nothing was read about the pane, so %q must not be reported: %q", o.What, o.Detail)
		}
	}

	facts := PaneFacts{Command: "nvim", AltScreen: true, GuestKittyFlags: 1, HasForeground: true}
	var sawRunning, sawAlt, sawGuest bool
	for _, o := range facts.Observations() {
		switch o.What {
		case "Running":
			sawRunning = strings.Contains(o.Detail, "nvim")
		case "Alt screen":
			sawAlt = true
		case "Guest keyboard":
			sawGuest = true
		}
		if o.Evidence != EvidenceObserved {
			t.Errorf("%q came from the pane, so it is observed, got %q", o.What, o.Evidence)
		}
	}
	if !sawRunning || !sawAlt || !sawGuest {
		t.Errorf("observed facts missing: running=%v alt=%v guest=%v", sawRunning, sawAlt, sawGuest)
	}
}

// The report is the agent's copy of the analysis, so it has to survive a
// round-trip and it has to explain its own tiers to a reader who only ever sees
// the JSON.
func TestReportRoundTripsAndCarriesItsEvidenceNote(t *testing.T) {
	r := registryFor(t, nil)
	rep := r.Report(PaneFacts{Command: "nvim"})

	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back KeybindReport
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.Bindings) != len(rep.Bindings) {
		t.Errorf("bindings survived as %d, want %d", len(back.Bindings), len(rep.Bindings))
	}
	for _, tier := range []Evidence{EvidenceCertain, EvidenceObserved, EvidenceReference} {
		if back.EvidenceNote[tier] == "" {
			t.Errorf("the report must explain the %q tier to a JSON-only reader", tier)
		}
	}
	if !strings.Contains(back.EvidenceNote[EvidenceReference], "curated") {
		t.Error("the reference tier's note must say it is curated, not detected")
	}
}

func TestSummaryCountsWhatItFound(t *testing.T) {
	clean := registryFor(t, nil).Report(PaneFacts{})
	if got := clean.Summary(); !strings.Contains(got, "No conflicts") && len(clean.Collisions) == 0 && len(clean.GuestClashes) == 0 && len(clean.Ambiguous) == 0 {
		t.Errorf("a clean report should say so, got %q", got)
	}

	dirty := registryFor(t, func(c *UserConfig) {
		c.Keybindings.WindowManagement["a_custom"] = []string{"ctrl+alt+v"}
		c.Keybindings.System["z_custom"] = []string{"ctrl+alt+v"}
	}).Report(PaneFacts{})
	if got := dirty.Summary(); !strings.Contains(got, "claimed twice") {
		t.Errorf("summary should count the collision, got %q", got)
	}
}
