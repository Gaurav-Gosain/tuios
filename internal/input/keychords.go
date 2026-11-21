package input

import (
	"slices"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

const lockMods = tea.ModCapsLock | tea.ModNumLock | tea.ModScrollLock

func bindingKeys(msg tea.KeyPressMsg, base rune) []string {
	typed, typedOK := optionTypedKey(msg, base)
	msg = withoutChordText(msg)
	orig := msg
	msg = producedKey(msg)
	key := msg.String()
	keys := []string{key}
	if stroke := msg.Keystroke(); stroke != key {
		keys = append(keys, stroke)
	}
	if pos, ok := baseLayoutKey(orig); ok && !slices.Contains(keys, pos) {
		keys = append(keys, pos)
	}
	if shifted, ok := shiftedKey(msg); ok && !slices.Contains(keys, shifted) {
		keys = append(keys, shifted)
	}
	plain := len(keys)

	mods := msg.Mod &^ lockMods
	if config.KeyFitsUSLayout(msg.Code, msg.ShiftedCode, base, mods&tea.ModShift != 0) {
		for _, k := range keys[:plain] {
			keys = append(keys, config.USLayoutKey(k))
		}
	}
	tier := config.BareOptionGlyphKey
	if mods&tea.ModAlt != 0 {
		tier = config.OptionGlyphKey
	}
	for _, chord := range composedChords(msg, base) {
		if chord != key {
			keys = append(keys, tier(chord))
		}
	}
	if typedOK && !slices.Contains(keys, typed) {
		keys = append(keys, typed)
	}
	return keys
}

func composedChords(msg tea.KeyPressMsg, base rune) []string {
	chord, ok := macOptionChord(msg)
	if !ok {
		return nil
	}
	mods := msg.Mod &^ lockMods
	if base != 0 {
		if mods&tea.ModAlt == 0 {
			return nil
		}
		return []string{tea.Key{Code: base, Mod: mods}.Keystroke()}
	}
	if mods&tea.ModShift != 0 && !strings.Contains(chord, "shift+") {
		return []string{strings.Replace(chord, "alt+", "alt+shift+", 1), chord}
	}
	return []string{chord}
}

func optionGlyphsApply(msg tea.KeyPressMsg, layout, glyphs string) bool {
	return config.OptionGlyphRuns(layout, glyphs, msg.Mod&tea.ModAlt != 0)
}

func shiftedKey(msg tea.KeyPressMsg) (string, bool) {
	mods := msg.Mod &^ lockMods
	if mods&tea.ModShift == 0 || msg.ShiftedCode == 0 || msg.ShiftedCode == msg.Code {
		return "", false
	}
	if unicode.IsLetter(msg.ShiftedCode) || !unicode.IsPrint(msg.ShiftedCode) {
		return "", false
	}
	k := tea.Key{Code: msg.ShiftedCode, Mod: mods &^ tea.ModShift}
	return k.Keystroke(), true
}

func lookupAction(o *app.OS, msg tea.KeyPressMsg, get func(string) string) string {
	base := msg.BaseCode
	if base == 0 && o != nil {
		base = o.HostBaseCode(msg)
	}
	for _, key := range bindingKeys(msg, base) {
		if action := get(key); action != "" {
			return action
		}
	}
	return ""
}

func tableAction(o *app.OS, msg tea.KeyPressMsg, reg *config.KeybindRegistry, table config.Table) string {
	if !config.MenuOverlayTable(table) {
		return lookupAction(o, msg, func(key string) string { return reg.Action(table, key) })
	}
	if h := o.MenuAction; h.Set && h.Table == table {
		return h.Action
	}
	if o.Menu != "" && o.MenuState().Origin.Set {
		return actionMenuOpened
	}
	action := lookupAction(o, msg, func(key string) string { return reg.Action(table, key) })
	if name, ok := config.MenuNameOf(action); ok {
		if _, declared := declaredMenu(o, name); declared {
			o.OpenMenuFrom(name, table, false)
			return actionMenuOpened
		}
	}
	return action
}

const actionMenuOpened = "menu_opened"

func macOptionChord(msg tea.KeyPressMsg) (string, bool) {
	if !runtimeIsDarwin() {
		return "", false
	}
	if mods := msg.Mod &^ lockMods; mods&^(tea.ModAlt|tea.ModShift) != 0 {
		return "", false
	}
	r := chordRune(msg)
	if r == 0 {
		return "", false
	}
	return config.MacOSOptionChord(r)
}

func chordRune(msg tea.KeyPressMsg) rune {
	if msg.Code != 0 && msg.Code != tea.KeyExtended {
		return msg.Code
	}
	runes := []rune(msg.Text)
	if len(runes) != 1 {
		return 0
	}
	return runes[0]
}

var macRewrittenAltArrowKeys = map[string]string{
	"alt+b": "alt+left",
	"alt+f": "alt+right",
}

func macRewrittenAltArrow(msg tea.KeyPressMsg) (got, arrow string, ok bool) {
	if !runtimeIsDarwin() {
		return "", "", false
	}
	if mods := msg.Mod &^ lockMods; mods != tea.ModAlt {
		return "", "", false
	}
	got = msg.Keystroke()
	arrow, ok = macRewrittenAltArrowKeys[got]
	return got, arrow, ok
}
