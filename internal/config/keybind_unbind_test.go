package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// These cases are about one claim: an unbind has to look different in the file
// from an action nobody mentioned, or the default comes back at the next load
// and the unbind was theatre.

// loadFromTOML parses text the way LoadUserConfig does, without the XDG lookup:
// unmarshal, then fill the defaults in. That order is the whole point of the
// encoding, so a test that skipped the fill would prove nothing, and it is the
// one parse function's order rather than a copy of it.
func loadFromTOML(t *testing.T, text string) *UserConfig {
	t.Helper()
	cfg, err := ParseUserConfig([]byte(text))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return cfg
}

// TestAbsentActionGetsItsDefaultBack is the control the encoding is measured
// against: a config that says nothing about an action is a config that wants
// the default.
//
// Negative control: make fillMapDefaults skip nothing, or delete the
// fillMapDefaults call, and this fails.
func TestAbsentActionGetsItsDefaultBack(t *testing.T) {
	cfg := loadFromTOML(t, "[keybindings.window_management]\nnew_window = [\"n\"]\n")
	if got := cfg.Keybindings.WindowManagement["close_window"]; len(got) == 0 {
		t.Fatalf("an action the file does not mention must come back from the defaults, got %#v", got)
	}
}

// TestEmptyListIsNotRefilledFromDefaults is the encoding itself.
//
// Negative control: change fillMapDefaults's test from "the action is present"
// to "the action has keys" (len(target[k]) == 0) and this fails, which is the
// shape the bug would take if anyone tried to tidy that line.
func TestEmptyListIsNotRefilledFromDefaults(t *testing.T) {
	cfg := loadFromTOML(t, "[keybindings.window_management]\nclose_window = []\n")
	keys, present := cfg.Keybindings.WindowManagement["close_window"]
	if !present {
		t.Fatal("the unbound action must stay in the map, or the next save drops it back to absent")
	}
	if len(keys) != 0 {
		t.Fatalf("close_window came back bound to %v: the default overwrote a deliberate unbind", keys)
	}
}

// TestUnbindSurvivesASaveAndReload is the round trip a restart makes: unbind,
// write the file, read it back.
//
// Negative control: make UnbindKey delete the action instead of emptying it and
// this fails at the reload, because the action is then absent and the default
// returns. That is exactly the bug the empty list exists to prevent, and it is
// the tidier-looking of the two implementations.
func TestUnbindSurvivesASaveAndReload(t *testing.T) {
	cfg := DefaultConfig()
	before := slices.Clone(cfg.Keybindings.WindowManagement["close_window"])
	if len(before) == 0 {
		t.Fatal("close_window must be bound by default for this case to mean anything")
	}
	for _, key := range before {
		if _, ok := cfg.Keybindings.UnbindKey(SectionWindowManagement, "close_window", key); !ok {
			t.Fatalf("UnbindKey did not remove %q", key)
		}
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := WriteConfigFile(cfg, path); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(path) // #nosec G304 - path is the test's own temp dir
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// The bytes matter, not just the struct: an encoding that only exists in
	// memory is one a restart loses.
	if !strings.Contains(string(data), "close_window = []") {
		t.Errorf("the file does not spell the unbind as an empty list:\n%s", keybindSectionOf(string(data), "window_management"))
	}

	reloaded := loadFromTOML(t, string(data))
	if got := reloaded.Keybindings.WindowManagement["close_window"]; len(got) != 0 {
		t.Errorf("after a save and a reload close_window is bound to %v again", got)
	}
}

// keybindSectionOf is the named table out of a rendered config, for a failure
// message that shows the relevant lines rather than the whole file.
func keybindSectionOf(file, section string) string {
	head := "[keybindings." + section + "]"
	i := strings.Index(file, head)
	if i < 0 {
		return "no [" + head + "] in the file"
	}
	rest := file[i:]
	if j := strings.Index(rest[len(head):], "\n["); j >= 0 {
		rest = rest[:len(head)+j]
	}
	return rest
}

// TestUnbindKeyLeavesTheActionsOtherKeys: unbinding is per key, not per action.
// close_window has two keys by default and taking one must leave the other.
//
// Negative control: make UnbindKey set the action to []string{} outright and
// this fails.
func TestUnbindKeyLeavesTheActionsOtherKeys(t *testing.T) {
	cfg := DefaultConfig()
	keys := slices.Clone(cfg.Keybindings.WindowManagement["close_window"])
	if len(keys) < 2 {
		t.Skip("close_window no longer has two default keys")
	}
	r, ok := cfg.Keybindings.UnbindKey(SectionWindowManagement, "close_window", keys[0])
	if !ok {
		t.Fatal("UnbindKey reported no change")
	}
	if r.LeftUnbound {
		t.Error("removing one of two keys must not report the action left unbound")
	}
	got := cfg.Keybindings.WindowManagement["close_window"]
	if len(got) != len(keys)-1 || slices.Contains(got, keys[0]) {
		t.Errorf("close_window = %v, want %v without %q", got, keys, keys[0])
	}
}

// TestUnbindActionEmptiesEveryKey.
//
// Negative control: have UnbindAction delete the map entry and the reload half
// of TestUnbindSurvivesASaveAndReload fails; have it remove only the first key
// and this one fails.
func TestUnbindActionEmptiesEveryKey(t *testing.T) {
	cfg := DefaultConfig()
	removals, ok := cfg.Keybindings.UnbindAction(SectionWindowManagement, "close_window")
	if !ok || len(removals) == 0 {
		t.Fatal("UnbindAction reported no change on an action that has keys")
	}
	keys, present := cfg.Keybindings.WindowManagement["close_window"]
	if !present || len(keys) != 0 {
		t.Fatalf("close_window = %v, present=%v; want present and empty", keys, present)
	}
	for _, r := range removals {
		if !r.LeftUnbound {
			t.Errorf("removal of %q must report the action left unbound", r.Key)
		}
	}
}

// TestFreeKeyTakesTheKeyOutOfEveryScope is the case the feature exists for: a
// key a program in the pane wants has to come off every table, because one
// table still holding it means the key still never arrives.
//
// Negative control: have FreeKey iterate one section (say only
// SectionTerminalMode) and this fails on the prefix_mode claim.
func TestFreeKeyTakesTheKeyOutOfEveryScope(t *testing.T) {
	cfg := DefaultConfig()
	// Bound in two tables that are consulted in different scopes, which is the
	// arrangement a single-section implementation gets wrong.
	cfg.Keybindings.TerminalMode["terminal_next_window"] = []string{"ctrl+alt+j"}
	cfg.Keybindings.PrefixMode["prefix_next_window"] =
		append(cfg.Keybindings.PrefixMode["prefix_next_window"], "ctrl+alt+j")

	removals := cfg.Keybindings.FreeKey("ctrl+alt+j")
	if len(removals) != 2 {
		t.Fatalf("freed %d bindings, want 2: %+v", len(removals), removals)
	}
	for _, section := range SectionNames() {
		for action, keys := range cfg.Keybindings.section(section) {
			if slices.Contains(keys, "ctrl+alt+j") {
				t.Errorf("[%s] %s still holds ctrl+alt+j", section, action)
			}
		}
	}
	// The action that had no other key is left present and empty, so the
	// default does not return.
	if keys, present := cfg.Keybindings.TerminalMode["terminal_next_window"]; !present || len(keys) != 0 {
		t.Errorf("terminal_next_window = %v, present=%v; want present and empty", keys, present)
	}
}

// TestFreeKeyIsCaseFoldedTheWayLookupIs: the registry looks a compound key up
// lowercased, so freeing "Ctrl+Alt+J" must remove a binding written
// "ctrl+alt+j". Anything else leaves a binding the user cannot see and cannot
// remove.
//
// Negative control: compare keys with == instead of through lookupForm and this
// fails.
func TestFreeKeyIsCaseFoldedTheWayLookupIs(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Keybindings.TerminalMode["terminal_next_window"] = []string{"ctrl+alt+j"}
	if got := cfg.Keybindings.FreeKey("Ctrl+Alt+J"); len(got) != 1 {
		t.Fatalf("freed %d bindings, want 1", len(got))
	}
}

// TestFreedKeyStopsBeingWithheldFromThePane joins the edit to the report the
// user reads. Freeing a key must take it out of TerminalModeSwallowed, or the
// overlay goes on saying the pane will not get it.
//
// Negative control: skip the registry Reload after the edit and this fails,
// which is the mistake a caller makes when it writes the config and forgets the
// live registry still holds the old one.
func TestFreedKeyStopsBeingWithheldFromThePane(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Keybindings.TerminalMode["terminal_next_window"] = []string{"ctrl+alt+j"}
	r := NewKeybindRegistry(cfg)
	if !swallows(r, "ctrl+alt+j") {
		t.Fatal("a terminal_mode bind must be reported as withheld from the pane")
	}

	cfg.Keybindings.FreeKey("ctrl+alt+j")
	r.Reload(cfg)
	if swallows(r, "ctrl+alt+j") {
		t.Error("ctrl+alt+j is still withheld after being freed")
	}
	if fate := r.Fate("ctrl+alt+j", PaneFacts{}); !fate.Free {
		t.Errorf("Fate says ctrl+alt+j is not free: %+v", fate)
	}
}

func swallows(r *KeybindRegistry, key string) bool {
	for _, s := range r.TerminalModeSwallowed() {
		if strings.EqualFold(s.Key, key) {
			return true
		}
	}
	return false
}

// TestFreeKeyCannotTakeTheLeader, and says so. The leader is
// keybindings.leader_key rather than an entry in a table, so freeing it removes
// nothing; reporting success would send the user back to their program to find
// the key still gone.
//
// Negative control: return nil from StillHeldBy and this fails.
func TestFreeKeyCannotTakeTheLeader(t *testing.T) {
	cfg := DefaultConfig()
	r := NewKeybindRegistry(cfg)
	leader := cfg.Keybindings.LeaderKey

	if got := cfg.Keybindings.FreeKey(leader); len(got) != 0 {
		t.Errorf("freeing the leader removed %+v; it is not in any table", got)
	}
	held := r.StillHeldBy(leader)
	if len(held) == 0 {
		t.Fatal("StillHeldBy must say the leader still takes the key")
	}
	if !strings.Contains(strings.Join(held, " "), "leader_key") {
		t.Errorf("StillHeldBy(%q) = %v; it must name the setting that moves it", leader, held)
	}
}

// TestStillHeldBySaysNothingAboutAnOrdinaryKey: the sentence is only for the
// keys the config cannot reach, so a key that really was freed does not get a
// warning attached to it.
//
// Negative control: drop the Origin == "built-in" test in StillHeldBy and this
// fails, because every configurable swallow would be reported as unremovable.
func TestStillHeldBySaysNothingAboutAnOrdinaryKey(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Keybindings.TerminalMode["terminal_next_window"] = []string{"ctrl+alt+j"}
	r := NewKeybindRegistry(cfg)
	if held := r.StillHeldBy("ctrl+alt+j"); len(held) != 0 {
		t.Errorf("StillHeldBy = %v for a key an ordinary binding holds", held)
	}
}

// TestUnboundActionIsStillListed. An action taken off its key must stay in the
// report, because the overlay binds a key by selecting the action's row and a
// row that is gone is an unbind nobody can undo.
//
// Negative control: this fails on the tree before Binding.Unbound existed,
// where Bindings() skipped an action with no keys outright.
func TestUnboundActionIsStillListed(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Keybindings.UnbindAction(SectionWindowManagement, "close_window")
	r := NewKeybindRegistry(cfg)

	var row Binding
	for _, b := range r.Bindings() {
		if b.Action == "close_window" {
			row = b
			break
		}
	}
	if row.Action == "" {
		t.Fatal("close_window vanished from the report after being unbound")
	}
	if !row.Unbound {
		t.Error("the row must be marked unbound, or it reads as a binding with no key")
	}
	if row.Key != "" || row.Press != "" {
		t.Errorf("an unbound row must carry no key, got Key=%q Press=%q", row.Key, row.Press)
	}
}

// TestUnboundActionsDoNotCollide. Two actions with no keys share no key, so
// there is no contest between them.
//
// Negative control, run and confirmed failing: make Bindings treat an action
// with no keys as a binding on the empty key (iterate []string{""} for it and
// drop the blank-key skip) instead of emitting an Unbound row. Collisions then
// reports "close_window beats new_window" over a key that is not there. That is
// the shape of the mistake, and it is a plausible one: it is the smaller diff.
func TestUnboundActionsDoNotCollide(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Keybindings.UnbindAction(SectionWindowManagement, "close_window")
	cfg.Keybindings.UnbindAction(SectionWindowManagement, "new_window")
	r := NewKeybindRegistry(cfg)

	for _, c := range r.Collisions() {
		if c.Key == "" || c.Press == "" {
			t.Errorf("a collision was reported over no key at all: %+v", c)
		}
	}
}

// TestUnbindClearsTheCollisionItWasFor is the point of reaching unbind from the
// Conflicts tab: taking the losing action's key away has to make the conflict
// go.
//
// Negative control: have UnbindKey edit a copy of the section map rather than
// the live one and this fails, since the registry would reload the unchanged
// config.
func TestUnbindClearsTheCollisionItWasFor(t *testing.T) {
	cfg := DefaultConfig()
	// Two actions in one scope on one key. window_management and navigation are
	// both flattened into window mode, so this is a real contest.
	cfg.Keybindings.WindowManagement["new_window"] = []string{"ctrl+alt+k"}
	cfg.Keybindings.Navigation["next_window"] = []string{"ctrl+alt+k"}
	r := NewKeybindRegistry(cfg)
	if !hasCollisionOn(r, "ctrl+alt+k") {
		t.Fatal("two actions on one key in one scope must be reported as a collision")
	}

	cfg.Keybindings.UnbindKey(SectionNavigation, "next_window", "ctrl+alt+k")
	r.Reload(cfg)
	if hasCollisionOn(r, "ctrl+alt+k") {
		t.Error("the collision survived the unbind that resolved it")
	}
}

func hasCollisionOn(r *KeybindRegistry, key string) bool {
	for _, c := range r.Collisions() {
		if strings.EqualFold(c.Key, key) {
			return true
		}
	}
	return false
}

// TestDeliberateUnbindDoesNotWarnAtLoad. The config header tells users to write
// [] to unbind. Warning about it every start taught them the unbind had not
// worked.
//
// Negative control: this fails on the unfixed tree, where validateSection
// appended a warning for every action with no keys.
func TestDeliberateUnbindDoesNotWarnAtLoad(t *testing.T) {
	cfg := loadFromTOML(t, "[keybindings.terminal_mode]\nterminal_focus_left = []\n")
	for _, line := range ConfigWarnings(cfg) {
		if strings.Contains(line, "terminal_focus_left") {
			t.Errorf("unbinding an action warns at load: %q", line)
		}
	}
}

// TestABadKeyStillFailsValidation guards the other half: dropping the empty
// warning must not drop the check that a key is spelled correctly.
//
// Negative control: make validateSection skip the whole action rather than just
// the empty case and this fails.
func TestABadKeyStillFailsValidation(t *testing.T) {
	cfg := loadFromTOML(t, "[keybindings.window_management]\nnew_window = [\"ctrl+nosuchkey\"]\n")
	if !ValidateConfig(cfg).HasErrors() {
		t.Error("a misspelled key must still be an error")
	}
}

// TestUnboundActionsListsWhatWasRemoved, so a caller can report the file's
// state without re-deriving it.
//
// Negative control: have UnboundActions count entries with len(keys) == 0 only,
// and a whitespace-only key would hide an action that is effectively unbound;
// this catches that through liveKeyCount.
func TestUnboundActionsListsWhatWasRemoved(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Keybindings.UnbindAction(SectionWindowManagement, "close_window")
	cfg.Keybindings.WindowManagement["new_window"] = []string{"  "}

	got := cfg.Keybindings.UnboundActions(SectionWindowManagement)
	if !slices.Contains(got, "close_window") {
		t.Errorf("UnboundActions = %v, want it to contain close_window", got)
	}
	if !slices.Contains(got, "new_window") {
		t.Errorf("UnboundActions = %v, want it to contain new_window, whose only key is blank", got)
	}
}
