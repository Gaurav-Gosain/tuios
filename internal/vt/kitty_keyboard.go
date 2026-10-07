package vt

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// maxKittyKeyboardStack is how many entries one screen's flag stack holds.
// The protocol asks for a limit so a guest that pushes without popping cannot
// grow it without bound. kitty and ghostty both hold eight, and evict the
// oldest entry to make room for a new one, which is what Push does here.
const maxKittyKeyboardStack = 8

// kittyKeyboardState tracks the kitty keyboard protocol state for a terminal.
// The protocol uses a stack of flag sets that can be pushed/popped by
// applications, and the main and alternate screens each have their own: a
// full-screen program that pushes flags on the alternate screen and exits
// without popping them leaves the shell's keyboard as it found it.
type kittyKeyboardState struct {
	main, alt []int // flag stacks, oldest first; never empty
	onAlt     bool  // the alternate screen is the active one
}

// newKittyKeyboardState creates a new kitty keyboard state with an empty stack.
func newKittyKeyboardState() *kittyKeyboardState {
	return &kittyKeyboardState{
		main: []int{0}, // Always have at least one entry (the base)
		alt:  []int{0},
	}
}

// active returns the stack of the screen in use.
func (k *kittyKeyboardState) active() *[]int {
	if k.onAlt {
		return &k.alt
	}
	return &k.main
}

// Stack returns the active screen's stack, oldest entry first. The caller
// must not modify it.
func (k *kittyKeyboardState) Stack() []int {
	return *k.active()
}

// SetStack replaces the active screen's stack, keeping the newest entries
// when there are more than the limit allows. An empty stack is the base entry.
func (k *kittyKeyboardState) SetStack(stack []int) {
	setKittyStack(k.active(), stack)
}

// setKittyStack stores a copy of stack in dst, keeping the newest entries
// past the limit, and the base entry for an empty stack.
func setKittyStack(dst *[]int, stack []int) {
	if len(stack) > maxKittyKeyboardStack {
		stack = stack[len(stack)-maxKittyKeyboardStack:]
	}
	if len(stack) == 0 {
		stack = []int{0}
	}
	*dst = slices.Clone(stack)
}

// MainStack returns the main screen's stack while the alternate screen is in
// use, and nil while the main screen is: the stack a snapshot taken on the
// alternate screen has to carry besides the active one. The caller must not
// modify it.
func (k *kittyKeyboardState) MainStack() []int {
	if !k.onAlt {
		return nil
	}
	return k.main
}

// SetMainStack replaces the main screen's stack, with the limits SetStack
// applies, whichever screen is in use.
func (k *kittyKeyboardState) SetMainStack(stack []int) {
	setKittyStack(&k.main, stack)
}

// SetAltScreen follows a switch between the main and the alternate screen.
// Entering the alternate screen starts it with an empty stack, as it starts
// with an empty screen: flags a previous program left there are not the next
// one's. The main screen's stack is kept as it was.
func (k *kittyKeyboardState) SetAltScreen(on bool) {
	if on && !k.onAlt {
		k.alt = []int{0}
	}
	k.onAlt = on
}

// SelectScreen points the state at the main or the alternate screen without
// touching either stack. It is for a restore, which puts back a stack that
// was saved on whichever screen was in use.
func (k *kittyKeyboardState) SelectScreen(alt bool) {
	k.onAlt = alt
}

// CurrentFlags returns the currently active keyboard flags.
func (k *kittyKeyboardState) CurrentFlags() int {
	st := *k.active()
	if len(st) == 0 {
		return 0
	}
	return st[len(st)-1]
}

// Push pushes a new set of flags onto the stack. A full stack drops its
// oldest entry first.
func (k *kittyKeyboardState) Push(flags int) {
	st := k.active()
	if len(*st) >= maxKittyKeyboardStack {
		*st = append((*st)[:0], (*st)[1:]...)
	}
	*st = append(*st, flags)
}

// Pop removes n entries from the top of the stack. Popping every entry, or
// more, leaves no flags set, as in kitty, where popping past the bottom is
// how a program gets back to the legacy encoding whatever was pushed.
func (k *kittyKeyboardState) Pop(n int) {
	if n <= 0 {
		n = 1
	}
	st := k.active()
	if n >= len(*st) {
		*st = append((*st)[:0], 0)
		return
	}
	*st = (*st)[:len(*st)-n]
}

// Set modifies the current flags based on the mode:
//
//	1 = set given flags, unset all others
//	2 = set given flags, keep existing unchanged
//	3 = unset given flags, keep existing unchanged
func (k *kittyKeyboardState) Set(flags, mode int) {
	current := k.CurrentFlags()
	switch mode {
	case 1:
		current = flags
	case 2:
		current |= flags
	case 3:
		current &^= flags
	default:
		current = flags
	}
	st := k.active()
	if len(*st) == 0 {
		*st = append(*st, current)
	} else {
		(*st)[len(*st)-1] = current
	}
}

// Reset clears both stacks back to the base entry and selects the main
// screen, which is where a hard reset leaves the terminal.
func (k *kittyKeyboardState) Reset() {
	k.main = []int{0}
	k.alt = []int{0}
	k.onAlt = false
}

// HasDisambiguate returns true if the disambiguate flag is set.
func (k *kittyKeyboardState) HasDisambiguate() bool {
	return k.CurrentFlags()&ansi.KittyDisambiguateEscapeCodes != 0
}

// HasReportEvents returns true if the report events flag is set.
func (k *kittyKeyboardState) HasReportEvents() bool {
	return k.CurrentFlags()&ansi.KittyReportEventTypes != 0
}

// HasReportAlternateKeys returns true if the report alternate keys flag is set.
func (k *kittyKeyboardState) HasReportAlternateKeys() bool {
	return k.CurrentFlags()&ansi.KittyReportAlternateKeys != 0
}

// HasReportAllKeys returns true if the report all keys flag is set.
func (k *kittyKeyboardState) HasReportAllKeys() bool {
	return k.CurrentFlags()&ansi.KittyReportAllKeysAsEscapeCodes != 0
}

// registerKittyKeyboardHandlers registers CSI handlers for kitty keyboard protocol.
func (e *Emulator) registerKittyKeyboardHandlers() {
	// CSI > flags u: Push keyboard mode
	e.RegisterCsiHandler(ansi.Command('>', 0, 'u'), func(params ansi.Params) bool {
		flags := 0
		if len(params) > 0 {
			flags = params[0].Param(0)
		}
		e.kittyKbd.Push(flags)
		e.updateKittyKeyboardCache()
		return true
	})

	// CSI < count u: Pop keyboard mode
	e.RegisterCsiHandler(ansi.Command('<', 0, 'u'), func(params ansi.Params) bool {
		count := 1
		if len(params) > 0 {
			count = params[0].Param(1)
		}
		e.kittyKbd.Pop(count)
		e.updateKittyKeyboardCache()
		return true
	})

	// CSI ? u: Query keyboard mode
	e.RegisterCsiHandler(ansi.Command('?', 0, 'u'), func(_ ansi.Params) bool {
		flags := e.kittyKbd.CurrentFlags()
		// Respond with CSI ? flags u
		response := fmt.Sprintf("\x1b[?%du", flags)
		_, _ = io.WriteString(e.pipe, response)
		return true
	})

	// CSI = flags ; mode u: Set keyboard mode
	e.RegisterCsiHandler(ansi.Command('=', 0, 'u'), func(params ansi.Params) bool {
		flags := 0
		mode := 1
		if len(params) > 0 {
			flags = params[0].Param(0)
		}
		if len(params) > 1 {
			mode = params[1].Param(1)
		}
		e.kittyKbd.Set(flags, mode)
		e.updateKittyKeyboardCache()
		return true
	})
}

// KittyKeyboardFlags returns the current kitty keyboard protocol flags.
// Thread-safe: reads from an atomic cache updated on push/pop/set/reset.
func (e *Emulator) KittyKeyboardFlags() int {
	return int(e.cachedKittyFlags.Load())
}

// KittyKeyboardStack returns a copy of the kitty keyboard flag stack of the
// screen in use, base entry first. It exists for daemon state sync: a guest negotiates the
// protocol once (CSI > u push or CSI = u set) and never repeats it, so a
// reattaching client must be handed the stack rather than rediscover it from
// the output buffer. Call from the goroutine that feeds the emulator, or with
// the same lock that serializes writes to it.
func (e *Emulator) KittyKeyboardStack() []int {
	if e.kittyKbd == nil {
		return nil
	}
	return slices.Clone(e.kittyKbd.Stack())
}

// KittyKeyboardMainStack returns a copy of the main screen's kitty keyboard
// flag stack while the alternate screen is in use, and nil otherwise. A
// snapshot carries it with KittyKeyboardStack: a client that restores only
// the alternate screen's stack has the main one empty when the program quits,
// and encodes keys in legacy form for a shell that negotiated the protocol.
func (e *Emulator) KittyKeyboardMainStack() []int {
	if e.kittyKbd == nil {
		return nil
	}
	return slices.Clone(e.kittyKbd.MainStack())
}

// RestoreKittyKeyboardMainStack replaces the main screen's flag stack from a
// saved state. A nil or empty stack is a no-op.
func (e *Emulator) RestoreKittyKeyboardMainStack(stack []int) {
	if e.kittyKbd == nil || len(stack) == 0 {
		return
	}
	e.kittyKbd.SetMainStack(stack)
	e.updateKittyKeyboardCache()
}

// RestoreKittyKeyboardState replaces the kitty keyboard flag stack from a
// saved state and refreshes the cache KittyKeyboardFlags reads. Used when
// reconnecting to a daemon session; a nil or empty stack is a no-op so state
// from an older daemon leaves the default (empty) state untouched. The stack
// goes to the screen in use, so restore the alternate screen first.
func (e *Emulator) RestoreKittyKeyboardState(stack []int) {
	if e.kittyKbd == nil || len(stack) == 0 {
		return
	}
	e.kittyKbd.SetStack(stack)
	e.updateKittyKeyboardCache()
}

// updateKittyKeyboardCache updates the thread-safe cached flags.
// Must be called from the VT processing goroutine after any stack change.
func (e *Emulator) updateKittyKeyboardCache() {
	flags := 0
	if e.kittyKbd != nil {
		flags = e.kittyKbd.CurrentFlags()
	}
	e.cachedKittyFlags.Store(int32(flags))
}

// EncodeKeyCSIu encodes a key event in the CSI u format used by the kitty keyboard protocol.
// Returns the encoded sequence, or empty string if the key should use legacy encoding.
func EncodeKeyCSIu(key KeyPressEvent, flags int) string {
	// Only encode if at least disambiguate or report-all-keys flag is set
	if flags&(ansi.KittyDisambiguateEscapeCodes|ansi.KittyReportAllKeysAsEscapeCodes) == 0 {
		return ""
	}

	if isKittyModifierKey(key.Code) && flags&ansi.KittyReportAllKeysAsEscapeCodes == 0 {
		return ""
	}
	if isKittyLegacyTextKey(key, flags) {
		return ""
	}

	code := int(key.Code)

	// Don't encode basic printable characters without modifiers
	// (unless report-all-keys flag is set)
	if flags&ansi.KittyReportAllKeysAsEscapeCodes == 0 {
		// Caps Lock and Num Lock change the text, not the chord: NumLock+a is
		// still text, and CapsLock+ш types Ш as text.
		mods := key.Mod &^ (ModCapsLock | ModNumLock)
		if mods == 0 && code >= 0x20 && code < 0x7f {
			return ""
		}
		// The same for a key that types a non-ASCII character, such as "ш" on
		// a Ukrainian layout: text is sent as text. Only report-all-keys turns
		// a plain text key into an escape code.
		if mods == 0 && key.Text != "" && unicode.IsPrint(key.Code) {
			return ""
		}
		// For Shift+printable that produces different text (e.g., Shift+a → 'A'),
		// the kitty spec says to send the text directly, not CSI u.
		// Only use CSI u when there are other modifiers (Ctrl, Alt) besides Shift.
		if key.Text != "" && mods == ModShift {
			return ""
		}
		// A keypad key that types text is text too: keypad 1 with NumLock on
		// is "1". Its code is one of ultraviolet's private ones, which the
		// printable-code test above cannot see, so it is named here. Only the
		// keypad keys that type nothing are CSI u under disambiguate.
		if key.Text != "" && mods&^ModShift == 0 && isKittyKeypadKey(key.Code) {
			return ""
		}
	}

	// Map special keys to their CSI u key codes
	form, ok := kittyKeyForm(key.Code)
	if !ok {
		return ""
	}
	code = form.num
	mods := kittyModField(key, flags)
	if form.final != 'u' {
		return encodeFormCSIu(form, mods)
	}

	// For regular keys, encode as CSI code ; modifiers u.
	//
	// When the pane requested the associated-text flag, a key that produces
	// text must carry that text as the third CSI-u field so the app inserts the
	// character the user actually typed. Without it, an app that honours the
	// flag it asked for (terminal-browser escalates to CSI >27u on text focus,
	// awrit pushes CSI >31u) has only the base key code to work with: it inserts
	// the unshifted character, so Shift+A types "a", ":" types ";", and any
	// non-ASCII text is dropped. The field is a colon-separated list of the
	// produced text's Unicode code points; see the kitty keyboard protocol.
	keyField := strconv.Itoa(code)
	if flags&ansi.KittyReportAlternateKeys != 0 {
		keyField = kittyAlternateKeys(code, key)
	}
	if flags&ansi.KittyReportAssociatedKeys != 0 {
		if text := kittyAssociatedText(key.Text); text != "" {
			return fmt.Sprintf("\x1b[%s;%s;%su", keyField, mods, text)
		}
	}
	if mods != "1" {
		return fmt.Sprintf("\x1b[%s;%su", keyField, mods)
	}
	return fmt.Sprintf("\x1b[%su", keyField)
}

// kittyModField renders the modifier field of a key press for a pane with
// these flags: the modifier parameter, then ":2" for a repeat when the pane
// asked for event types. kitty marks a held key's repeats that way, and a pane
// that tracks presses and releases (a Wayland compositor does) would otherwise
// read each repeat as a second press of a key that is already down.
func kittyModField(key KeyPressEvent, flags int) string {
	field := strconv.Itoa(kittyModParamFor(key.Mod, flags))
	if key.IsRepeat && flags&ansi.KittyReportEventTypes != 0 {
		field += ":2"
	}
	return field
}

// kittyAlternateKeys renders the key field for a pane that asked for alternate
// keys: code[:shifted[:base]]. The shifted key is only there with Shift held,
// and the base-layout key (the US-layout key at the same position) only when it
// differs from the code. A pane that did not ask gets the bare code, so the
// alternate keys tuios asks the host for never reach it.
func kittyAlternateKeys(code int, key KeyPressEvent) string {
	field := strconv.Itoa(code)
	shifted := 0
	if key.Mod&ModShift != 0 && key.ShiftedCode != 0 && int(key.ShiftedCode) != code {
		shifted = int(key.ShiftedCode)
	}
	base := 0
	if key.BaseCode != 0 && int(key.BaseCode) != code {
		base = int(key.BaseCode)
	}
	if shifted != 0 {
		field += ":" + strconv.Itoa(shifted)
	}
	if base != 0 {
		if shifted == 0 {
			field += ":"
		}
		field += ":" + strconv.Itoa(base)
	}
	return field
}

// kittyAssociatedText renders a key's produced text as the colon-separated
// decimal code-point list the kitty keyboard protocol uses for its associated
// text field. It returns "" when there is no text to report: an empty string,
// or text that is a lone control character (Enter, Tab, Backspace and Escape
// carry their semantics in the key code, not as insertable text, and a kitty
// app expects no text field for them).
func kittyAssociatedText(text string) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range text {
		if unicode.IsControl(r) {
			return ""
		}
		if b.Len() > 0 {
			b.WriteByte(':')
		}
		b.WriteString(strconv.Itoa(int(r)))
	}
	return b.String()
}

// csiuForm is how one key is spelled in the CSI u family: a number, then the
// modifier field, then a terminator. Three shapes exist: \x1b[<code>u for
// ordinary keys, \x1b[1;<mods><letter> for the arrows and F1-F4, and
// \x1b[<n>;<mods>~ for Insert through F12. They differ only in those two
// values. Presses and releases read the same table, so a release can never name
// a different key than the press it ends.
type csiuForm struct {
	num   int
	final byte
}

// kittyKeyForm returns the CSI u spelling of a key code. Anything not named
// here is its own code terminated by 'u', which is what the protocol says for
// every ordinary character. ok is false for one of ultraviolet's private codes
// the protocol has no number for (F36 and up, say): putting the private code on
// the wire names a key no application knows, so the caller leaves it to the
// legacy encoder instead.
func kittyKeyForm(code rune) (form csiuForm, ok bool) {
	switch code {
	case KeyEnter:
		return csiuForm{13, 'u'}, true
	case KeyTab:
		return csiuForm{9, 'u'}, true
	case KeyBackspace:
		return csiuForm{127, 'u'}, true
	case KeyEscape:
		return csiuForm{27, 'u'}, true
	case KeySpace:
		return csiuForm{32, 'u'}, true
	case KeyUp:
		return csiuForm{1, 'A'}, true
	case KeyDown:
		return csiuForm{1, 'B'}, true
	case KeyRight:
		return csiuForm{1, 'C'}, true
	case KeyLeft:
		return csiuForm{1, 'D'}, true
	case KeyHome:
		return csiuForm{1, 'H'}, true
	case KeyEnd:
		return csiuForm{1, 'F'}, true
	case KeyF1:
		return csiuForm{1, 'P'}, true
	case KeyF2:
		return csiuForm{1, 'Q'}, true
	case KeyF3:
		return csiuForm{1, 'R'}, true
	case KeyF4:
		return csiuForm{1, 'S'}, true
	case KeyInsert:
		return csiuForm{2, '~'}, true
	case KeyDelete:
		return csiuForm{3, '~'}, true
	case KeyPgUp:
		return csiuForm{5, '~'}, true
	case KeyPgDown:
		return csiuForm{6, '~'}, true
	case KeyF5:
		return csiuForm{15, '~'}, true
	case KeyF6:
		return csiuForm{17, '~'}, true
	case KeyF7:
		return csiuForm{18, '~'}, true
	case KeyF8:
		return csiuForm{19, '~'}, true
	case KeyF9:
		return csiuForm{20, '~'}, true
	case KeyF10:
		return csiuForm{21, '~'}, true
	case KeyF11:
		return csiuForm{23, '~'}, true
	case KeyF12:
		return csiuForm{24, '~'}, true
	case KeyBegin:
		// KP_BEGIN in the protocol's table: the legacy CSI E, like the arrows.
		return csiuForm{1, 'E'}, true
	}
	if n, ok := kittyModifierKeyCodes[code]; ok {
		return csiuForm{n, 'u'}, true
	}
	if num, ok := kittyFunctionalKeys[code]; ok {
		return csiuForm{num, 'u'}, true
	}
	if code > unicode.MaxRune {
		return csiuForm{}, false
	}
	return csiuForm{int(code), 'u'}, true
}

// kittyModifierKeyCodes are the kitty protocol's codes for the modifier and
// lock keys, which a terminal reports as keys of their own in report-all-keys
// mode. The decoder turns them into its own key codes, which are not what a
// pane expects on the wire.
var kittyModifierKeyCodes = map[rune]int{
	KeyCapsLock:       57358,
	KeyScrollLock:     57359,
	KeyNumLock:        57360,
	KeyLeftShift:      57441,
	KeyLeftCtrl:       57442,
	KeyLeftAlt:        57443,
	KeyLeftSuper:      57444,
	KeyLeftHyper:      57445,
	KeyLeftMeta:       57446,
	KeyRightShift:     57447,
	KeyRightCtrl:      57448,
	KeyRightAlt:       57449,
	KeyRightSuper:     57450,
	KeyRightHyper:     57451,
	KeyRightMeta:      57452,
	KeyIsoLevel3Shift: 57453,
	KeyIsoLevel5Shift: 57454,
}

// kittyFunctionalKeys holds the kitty protocol's numbers for the other keys
// that are not characters: the keypad, F13 and up, Print Screen, Pause and
// Menu, and the media keys. The decoder hands these over as ultraviolet's
// private codes, which sit past unicode.MaxRune, so falling through to "the
// code is the number" put \x1b[1114126u on the wire for keypad Enter -- a key
// no application has ever heard of. See "Functional key definitions" in the
// kitty keyboard protocol for the table.
var kittyFunctionalKeys = func() map[rune]int {
	m := map[rune]int{
		KeyKp0: 57399, KeyKp1: 57400, KeyKp2: 57401, KeyKp3: 57402, KeyKp4: 57403,
		KeyKp5: 57404, KeyKp6: 57405, KeyKp7: 57406, KeyKp8: 57407, KeyKp9: 57408,
		KeyKpDecimal: 57409, KeyKpDivide: 57410, KeyKpMultiply: 57411,
		KeyKpMinus: 57412, KeyKpPlus: 57413, KeyKpEnter: 57414, KeyKpEqual: 57415,
		KeyKpSep: 57416, KeyKpLeft: 57417, KeyKpRight: 57418, KeyKpUp: 57419,
		KeyKpDown: 57420, KeyKpPgUp: 57421, KeyKpPgDown: 57422, KeyKpHome: 57423,
		KeyKpEnd: 57424, KeyKpInsert: 57425, KeyKpDelete: 57426, KeyKpBegin: 57427,
		// The protocol has no keypad comma; KP_SEPARATOR is the same key on
		// the layouts that have one.
		KeyKpComma: 57416,
	}
	// These blocks are declared in the protocol's own order, so they range.
	blocks := []struct {
		first, last rune
		num         int
	}{
		{KeyPrintScreen, KeyMenu, 57361},
		{KeyF13, KeyF35, 57376},
		{KeyMediaPlay, KeyMute, 57428},
	}
	for _, b := range blocks {
		for c := b.first; c <= b.last; c++ {
			m[c] = b.num + int(c-b.first)
		}
	}
	return m
}()

// isKittyModifierKey reports whether code is a modifier pressed on its own,
// which the protocol reports only under the report-all-keys flag. The lock keys
// count, as they do in kitty's own is_modifier_key: without them every CapsLock
// press reached a disambiguate pane as \x1b[57358u.
func isKittyModifierKey(code rune) bool {
	switch code {
	case KeyCapsLock, KeyScrollLock, KeyNumLock:
		return true
	}
	return code >= KeyLeftShift && code <= KeyIsoLevel5Shift
}

// isKittyKeypadKey reports whether code is one of the keypad keys the protocol
// numbers 57399 (KP_0) through 57427 (KP_BEGIN).
func isKittyKeypadKey(code rune) bool {
	num, ok := kittyFunctionalKeys[code]
	return ok && num >= 57399 && num <= 57427
}

// encodeFormCSIu spells a press of one of the letter- or tilde-terminated keys.
// With no modifiers the sequence is the bare legacy one, which is what every
// terminal sends and every application already reads.
func encodeFormCSIu(form csiuForm, mods string) string {
	if mods != "1" {
		return fmt.Sprintf("\x1b[%d;%s%c", form.num, mods, form.final)
	}
	if form.final == '~' {
		return fmt.Sprintf("\x1b[%d~", form.num)
	}
	return fmt.Sprintf("\x1b[%c", form.final)
}

// EncodeKeyReleaseCSIu encodes a key release for a pane that asked to be told
// about them, and returns "" for one that did not.
//
// Only the event-type flag makes a release reportable, and it is the flag a
// compositor running in a pane cannot do without: a Wayland client is told a key
// is down and waits to be told it came up, so a dropped release leaves the key
// held and xkb repeating it forever. The press form is unchanged by the flag
// (kitty sends a bare \x1b[97u for the press and \x1b[97;1:3u for its release),
// so the release always carries the modifier field, even when empty, because the
// event type rides on it as a subparameter.
func EncodeKeyReleaseCSIu(key KeyPressEvent, flags int) string {
	if flags&ansi.KittyReportEventTypes == 0 {
		return ""
	}
	if isKittyModifierKey(key.Code) && flags&ansi.KittyReportAllKeysAsEscapeCodes == 0 {
		return ""
	}
	if isKittyLegacyTextKey(key, flags) {
		return ""
	}
	form, ok := kittyKeyForm(key.Code)
	if !ok {
		return ""
	}
	keyField := strconv.Itoa(form.num)
	if form.final == 'u' && flags&ansi.KittyReportAlternateKeys != 0 {
		// The release names the same alternate keys as its press, as kitty's
		// does: CSI 97:65 ; 2:3 u for Shift+a.
		keyField = kittyAlternateKeys(form.num, key)
	}
	return fmt.Sprintf("\x1b[%s;%d:3%c", keyField, kittyModParamFor(key.Mod, flags), form.final)
}

// isKittyLegacyTextKey reports whether key is Enter, Tab or Backspace with no
// modifier for a pane that did not ask for every key. kitty sends those three
// as their legacy bytes (CR, HT, DEL) under disambiguate and event types, and
// sends no release or repeat event for them. Only the report-all-keys flag, or
// a modifier, turns them into escape codes.
func isKittyLegacyTextKey(key KeyPressEvent, flags int) bool {
	if flags&ansi.KittyReportAllKeysAsEscapeCodes != 0 {
		return false
	}
	if key.Mod&^(ModCapsLock|ModNumLock) != 0 {
		return false
	}
	switch key.Code {
	case KeyEnter, KeyTab, KeyBackspace:
		return true
	}
	return false
}

// kittyModParamFor is kittyModParam with the lock modifiers added for a pane
// that asked for every key as an escape code: Caps Lock is 64 and Num Lock is
// 128, as kitty sends them. Such a pane treats the lock keys as keys, and a
// compositor among them keeps its own lock state, which it can only bring in
// line with the host's from these bits: the desktop may have turned Num Lock on
// before the pane ever had the keyboard. Other panes keep the field they always
// got, so a Num Lock left on does not turn a plain arrow into CSI 1;129A for
// them.
func kittyModParamFor(mod KeyMod, flags int) int {
	param := kittyModParam(mod)
	if flags&ansi.KittyReportAllKeysAsEscapeCodes == 0 {
		return param
	}
	if mod&ModCapsLock != 0 {
		param += 64
	}
	if mod&ModNumLock != 0 {
		param += 128
	}
	return param
}

// kittyModParam converts modifier flags to the CSI parameter format.
// The format is 1 + bitwise OR of: 1=shift, 2=alt, 4=ctrl, 8=super, 16=hyper,
// 32=meta. Super used to be read from ModMeta, which is where the decoder put
// it for a key sent in the legacy form, but a CSI u key decodes Super as
// ModSuper and lost it here. The input path now corrects the legacy form (see
// app.fixKittyLegacyMods), so each bit is read from its own modifier.
func kittyModParam(mod KeyMod) int {
	param := 1
	if mod&ModShift != 0 {
		param += 1
	}
	if mod&ModAlt != 0 {
		param += 2
	}
	if mod&ModCtrl != 0 {
		param += 4
	}
	if mod&ModSuper != 0 {
		param += 8
	}
	if mod&ModHyper != 0 {
		param += 16
	}
	if mod&ModMeta != 0 {
		param += 32
	}
	return param
}
