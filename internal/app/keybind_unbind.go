package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func (m *OS) keybindApply() tea.Cmd {
	m.KeybindRegistry.Reload(m.UserConfig)
	m.ListKeys = config.ListKeymap(m.UserConfig)
	m.keybinds.report = m.buildKeybindReport()
	m.keybinds.filtered = nil
	return m.persistSettings()
}

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
		m.ShowNotification(b.Action+" already has no key. Press ctrl+r to give it one.", "info", m.Settings.NotificationDuration)
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
	if rest := m.keybindOtherClaims(b.Key); rest != "" {
		msg += ". " + rest
	}
	m.ShowNotification(msg, "success", m.Settings.NotificationDuration)
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
		if held := m.keybindStillHeld(key); held != "" {
			m.ShowNotification("Nothing binds "+key+", but "+held, "warning", 0)
			return nil
		}
		m.ShowNotification("Nothing binds "+key+". The pane already gets it.", "info", m.Settings.NotificationDuration)
		return nil
	}

	var actions []string
	for _, r := range removals {
		actions = append(actions, r.Action)
	}
	msg := "Freed " + key + ", taken off " + strings.Join(actions, ", ")
	cmd := m.keybindApply()
	if held := m.keybindStillHeld(key); held != "" {
		m.ShowNotification(msg+". It still will not reach the pane: "+held, "warning", 0)
		return cmd
	}
	m.ShowNotification(msg+". The pane gets it now.", "success", m.Settings.NotificationDuration)
	return cmd
}

// KeybindSelectedConflict is the collision under the cursor on the Conflicts
// tab, and whether there is one.
func (m *OS) KeybindSelectedConflict() (config.Collision, bool) {
	if m.KeybindTab != KeybindTabConflicts {
		return config.Collision{}, false
	}
	all := m.keybinds.report.Collisions
	i := m.keybindList().Cursor
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
		c.Winner+" keeps it, which is what it already did.", "success", m.Settings.NotificationDuration)
	return cmd
}

// KeybindFreeSelectedKey frees the key of the binding under the cursor.
func (m *OS) KeybindFreeSelectedKey() tea.Cmd {
	if c, ok := m.KeybindSelectedConflict(); ok {
		return m.KeybindFreeKey(c.Key)
	}
	b, ok := m.KeybindSelectedBinding()
	if !ok {
		return nil
	}
	if b.Unbound {
		m.ShowNotification(b.Action+" has no key to free", "info", m.Settings.NotificationDuration)
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
	m.KeybindCapture(key)
	m.keybinds.armed = false
	return cmd
}

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
