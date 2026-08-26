package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Unbinding from the overlay. Two verbs, because a user who wants a key gone
// means one of two different things:
//
//   - "tuios should stop taking this key, the program in my pane wants it."
//     That is KeybindFreeKey: the key comes off every action in every scope,
//     because a key still claimed by one of them still never reaches the pane.
//   - "this action should not be on this key." That is KeybindUnbindSelected:
//     one key, one action, and the action keeps whatever other keys it had.
//
// Both are written to config.toml as an empty list on the actions that ran out
// of keys, which is what stops the default coming back at the next load. See
// config/keybind_unbind.go for the encoding.

// keybindApply reloads the registry from the edited config, rebuilds the
// report, and returns the command that writes the file.
//
// The reload happens before the write is even started, so the overlay is
// showing the consequence of the edit (a freed key, a new conflict, an action
// left unbound) rather than the state before it.
func (m *OS) keybindApply() tea.Cmd {
	m.KeybindRegistry.Reload(m.UserConfig)
	m.keybinds.report = m.buildKeybindReport()
	m.keybinds.filtered = nil
	return m.persistSettings()
}

// keybindEditable reports whether there is a config and a registry to edit, and
// says so once if there is not.
func (m *OS) keybindEditable() bool {
	if m.UserConfig == nil || m.KeybindRegistry == nil {
		m.ShowNotification("No config is loaded, so keybindings cannot be changed here", "error", 0)
		return false
	}
	return true
}

// KeybindUnbindSelected takes the selected binding's key off its action.
//
// Only that one action. The key may still act in another scope, and the row
// says which; taking every one of them at once is KeybindFreeKey, on its own
// gesture, because the two are different requests and doing the wider one by
// accident is not recoverable from the overlay.
func (m *OS) KeybindUnbindSelected() tea.Cmd {
	b, ok := m.KeybindSelectedBinding()
	if !ok {
		return nil
	}
	if b.Unbound {
		m.ShowNotification(b.Action+" already has no key. Press ctrl+r to give it one.", "info", config.NotificationDuration)
		return nil
	}
	if !m.keybindEditable() {
		return nil
	}
	removal, changed := m.UserConfig.Keybindings.UnbindKey(b.Section, b.Action, b.Key)
	if !changed {
		m.ShowNotification("Could not unbind "+b.Key+" from "+b.Action, "error", 0)
		return nil
	}

	msg := "Unbound " + b.Key + " from " + b.Action
	if removal.LeftUnbound {
		msg += ". It now has no key"
	}
	cmd := m.keybindApply()
	// Said after the reload, so a key that is still taken elsewhere is reported
	// from the config as it now stands rather than as it was.
	if rest := m.keybindOtherClaims(b.Key); rest != "" {
		msg += ". " + rest
	}
	m.ShowNotification(msg, "success", config.NotificationDuration)
	return cmd
}

// KeybindFreeKey takes one key off every action in every scope, so the program
// in the pane gets it.
func (m *OS) KeybindFreeKey(key string) tea.Cmd {
	key = strings.TrimSpace(key)
	if key == "" || !m.keybindEditable() {
		return nil
	}
	removals := m.UserConfig.Keybindings.FreeKey(key)
	if len(removals) == 0 {
		// Not a failure. A key nothing binds is a key the pane already gets,
		// unless something built in takes it, which held says.
		if held := m.keybindStillHeld(key); held != "" {
			// A warning rather than a note: the user asked for the key back and
			// is not getting it. An "info" with no duration is also dropped
			// outright by notificationLifetime, so this would have been said to
			// nobody.
			m.ShowNotification("Nothing binds "+key+", but "+held, "warning", 0)
			return nil
		}
		m.ShowNotification("Nothing binds "+key+". The pane already gets it.", "info", config.NotificationDuration)
		return nil
	}

	var actions []string
	for _, r := range removals {
		actions = append(actions, r.Action)
	}
	msg := "Freed " + key + ", taken off " + strings.Join(actions, ", ")
	cmd := m.keybindApply()
	if held := m.keybindStillHeld(key); held != "" {
		// A partial result reported as a whole one is the worst outcome here:
		// the user would go back to their program and find the key still gone.
		m.ShowNotification(msg+". It still will not reach the pane: "+held, "warning", 0)
		return cmd
	}
	m.ShowNotification(msg+". The pane gets it now.", "success", config.NotificationDuration)
	return cmd
}

// KeybindSelectedConflict is the collision under the cursor on the Conflicts
// tab, and whether there is one.
func (m *OS) KeybindSelectedConflict() (config.Collision, bool) {
	if m.KeybindTab != KeybindTabConflicts {
		return config.Collision{}, false
	}
	all := m.keybinds.report.Collisions
	i := m.keybinds.selected
	if i < 0 || i >= len(all) {
		return config.Collision{}, false
	}
	return all[i], true
}

// KeybindResolveSelectedConflict takes the contested key off every action that
// loses it, leaving the one that actually runs.
//
// This is the one edit on this screen that changes no behaviour at all. The
// losing bindings never fire, so removing them cannot alter what any key does;
// it only makes config.toml say what the program was already doing. That is why
// it is the verb the Conflicts tab offers: a panel that reports a problem and
// gives the reader nothing to press is a panel that teaches them to distrust
// it, and the safest possible fix is the right one to put behind the key.
//
// Whoever wants the other action to win rebinds it, which is ctrl+r on the
// Bindings tab. That is a different decision and it is not made by accident
// from here.
func (m *OS) KeybindResolveSelectedConflict() tea.Cmd {
	c, ok := m.KeybindSelectedConflict()
	if !ok || len(c.Losers) == 0 || !m.keybindEditable() {
		return nil
	}
	var freed []string
	for _, l := range c.Losers {
		if _, changed := m.UserConfig.Keybindings.UnbindKey(l.Section, l.Action, c.Key); changed {
			freed = append(freed, l.Action)
		}
	}
	if len(freed) == 0 {
		m.ShowNotification("Could not take "+c.Key+" off "+c.Losers[0].Action, "error", 0)
		return nil
	}
	cmd := m.keybindApply()
	m.ShowNotification("Took "+c.Key+" off "+strings.Join(freed, ", ")+". "+
		c.Winner+" keeps it, which is what it already did.", "success", config.NotificationDuration)
	return cmd
}

// KeybindFreeSelectedKey frees the key of the binding under the cursor.
func (m *OS) KeybindFreeSelectedKey() tea.Cmd {
	// The Conflicts tab names a key too, and freeing it there means the same
	// thing it means on a binding row.
	if c, ok := m.KeybindSelectedConflict(); ok {
		return m.KeybindFreeKey(c.Key)
	}
	b, ok := m.KeybindSelectedBinding()
	if !ok {
		return nil
	}
	if b.Unbound {
		m.ShowNotification(b.Action+" has no key to free", "info", config.NotificationDuration)
		return nil
	}
	return m.KeybindFreeKey(b.Key)
}

// KeybindFreeCapturedKey frees the key the recorder last captured. This is the
// short path for the case the recorder exists to find: press ctrl+r, press the
// key your program wants, read that tuios takes it, take it back.
func (m *OS) KeybindFreeCapturedKey() tea.Cmd {
	key, _ := m.KeybindCaptured()
	if key == "" {
		return nil
	}
	cmd := m.KeybindFreeKey(key)
	// Re-asked so the recorder's own verdict for the key is the one after the
	// edit, not the one that prompted it.
	m.KeybindCapture(key)
	m.keybinds.armed = false
	return cmd
}

// keybindStillHeld is the sentence naming what takes a key from the pane once
// the config has stopped claiming it, or "" when nothing does.
func (m *OS) keybindStillHeld(key string) string {
	if m.KeybindRegistry == nil {
		return ""
	}
	held := m.KeybindRegistry.StillHeldBy(key)
	if len(held) == 0 {
		return ""
	}
	return strings.Join(held, ", and ")
}

// keybindOtherClaims names the scopes that still act on a key, for the message
// after a single-action unbind.
func (m *OS) keybindOtherClaims(key string) string {
	if m.KeybindRegistry == nil {
		return ""
	}
	fate := m.KeybindRegistry.Fate(key, m.keybinds.report.Pane)
	if len(fate.Acts) == 0 {
		return ""
	}
	seen := map[string]bool{}
	var scopes []string
	for _, a := range fate.Acts {
		name := scopeShortName(a.Scope)
		if seen[name] {
			continue
		}
		seen[name] = true
		scopes = append(scopes, name)
	}
	return key + " still acts in " + strings.Join(scopes, ", ") + ". Press ctrl+x to take it off every action"
}
