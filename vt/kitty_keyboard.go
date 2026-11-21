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

const maxKittyKeyboardStack = 8

type kittyKeyboardState struct {
	main, alt []int
	onAlt     bool
}

func newKittyKeyboardState() *kittyKeyboardState {
	return &kittyKeyboardState{
		main: []int{0},
		alt:  []int{0},
	}
}

func (k *kittyKeyboardState) active() *[]int {
	if k.onAlt {
		return &k.alt
	}
	return &k.main
}

func (k *kittyKeyboardState) Stack() []int {
	return *k.active()
}

func (k *kittyKeyboardState) SetStack(stack []int) {
	setKittyStack(k.active(), stack)
}

func setKittyStack(dst *[]int, stack []int) {
	if len(stack) > maxKittyKeyboardStack {
		stack = stack[len(stack)-maxKittyKeyboardStack:]
	}
	if len(stack) == 0 {
		stack = []int{0}
	}
	*dst = slices.Clone(stack)
}

func (k *kittyKeyboardState) MainStack() []int {
	if !k.onAlt {
		return nil
	}
	return k.main
}

func (k *kittyKeyboardState) SetMainStack(stack []int) {
	setKittyStack(&k.main, stack)
}

func (k *kittyKeyboardState) SetAltScreen(on bool) {
	if on && !k.onAlt {
		k.alt = []int{0}
	}
	k.onAlt = on
}

func (k *kittyKeyboardState) SelectScreen(alt bool) {
	k.onAlt = alt
}

func (k *kittyKeyboardState) CurrentFlags() int {
	st := *k.active()
	if len(st) == 0 {
		return 0
	}
	return st[len(st)-1]
}

func (k *kittyKeyboardState) Push(flags int) {
	st := k.active()
	if len(*st) >= maxKittyKeyboardStack {
		*st = append((*st)[:0], (*st)[1:]...)
	}
	*st = append(*st, flags)
}

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

func (k *kittyKeyboardState) Reset() {
	k.main = []int{0}
	k.alt = []int{0}
	k.onAlt = false
}

func (k *kittyKeyboardState) HasDisambiguate() bool {
	return k.CurrentFlags()&ansi.KittyDisambiguateEscapeCodes != 0
}

func (k *kittyKeyboardState) HasReportEvents() bool {
	return k.CurrentFlags()&ansi.KittyReportEventTypes != 0
}

func (k *kittyKeyboardState) HasReportAlternateKeys() bool {
	return k.CurrentFlags()&ansi.KittyReportAlternateKeys != 0
}

func (k *kittyKeyboardState) HasReportAllKeys() bool {
	return k.CurrentFlags()&ansi.KittyReportAllKeysAsEscapeCodes != 0
}

// DisableKittyKeyboardProtocol makes the emulator stop offering the kitty
// keyboard protocol. Pushes, pops and sets change nothing, a CSI ? u query
// goes unanswered, and a saved stack is not restored.
//
// It is for a pane whose PTY is a Windows ConPTY. conhost before 1.22 drops
// the input CSIs it does not recognise, so the CSI u bytes for Escape,
// Ctrl+letters and modified Enter or Backspace never reach the guest. Per
// the kitty spec an unanswered query means the protocol is not supported,
// so a guest that queries first (pi, neovim) falls back to legacy keys on
// its own. Every encoder reads the flags, which stay at 0, so client key
// encoding, send-keys and key releases all agree.
//
// The daemon calls it before the pane's output starts. A client calls it
// from a snapshot that says the pane is a ConPTY one (see
// snapshot.State.KittyKbdUnavailable), under the lock that guards the
// emulator's writes. There is no way back: a pane keeps its PTY.
func (e *Emulator) DisableKittyKeyboardProtocol() {
	e.kkpUnavailable.Store(true)
	if e.kittyKbd != nil {
		e.kittyKbd.Reset()
	}
	e.cachedKittyFlags.Store(0)
}

// KittyKeyboardUnavailable reports whether DisableKittyKeyboardProtocol was
// called.
func (e *Emulator) KittyKeyboardUnavailable() bool { return e.kkpUnavailable.Load() }

func (e *Emulator) registerKittyKeyboardHandlers() {
	e.RegisterCsiHandler(ansi.Command('>', 0, 'u'), func(params ansi.Params) bool {
		if e.kkpUnavailable.Load() {
			return true
		}
		flags := 0
		if len(params) > 0 {
			flags = params[0].Param(0)
		}
		e.kittyKbd.Push(flags)
		e.updateKittyKeyboardCache()
		return true
	})

	e.RegisterCsiHandler(ansi.Command('<', 0, 'u'), func(params ansi.Params) bool {
		if e.kkpUnavailable.Load() {
			return true
		}
		count := 1
		if len(params) > 0 {
			count = params[0].Param(1)
		}
		e.kittyKbd.Pop(count)
		e.updateKittyKeyboardCache()
		return true
	})

	e.RegisterCsiHandler(ansi.Command('?', 0, 'u'), func(_ ansi.Params) bool {
		if e.kkpUnavailable.Load() {
			return true
		}
		flags := e.kittyKbd.CurrentFlags()
		response := fmt.Sprintf("\x1b[?%du", flags)
		_, _ = io.WriteString(e.pipe, response)
		return true
	})

	e.RegisterCsiHandler(ansi.Command('=', 0, 'u'), func(params ansi.Params) bool {
		if e.kkpUnavailable.Load() {
			return true
		}
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
	if e.kkpUnavailable.Load() || e.kittyKbd == nil || len(stack) == 0 {
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
	if e.kkpUnavailable.Load() || e.kittyKbd == nil || len(stack) == 0 {
		return
	}
	e.kittyKbd.SetStack(stack)
	e.updateKittyKeyboardCache()
}

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

	if flags&ansi.KittyReportAllKeysAsEscapeCodes == 0 {
		mods := key.Mod &^ (ModCapsLock | ModNumLock)
		if mods == 0 && code >= 0x20 && code < 0x7f {
			return ""
		}
		if mods == 0 && key.Text != "" && unicode.IsPrint(key.Code) {
			return ""
		}
		if key.Text != "" && mods == ModShift {
			return ""
		}
		if key.Text != "" && mods&^ModShift == 0 && isKittyKeypadKey(key.Code) {
			return ""
		}
	}

	form, ok := kittyKeyForm(key.Code)
	if !ok {
		return ""
	}
	code = form.num
	mods := kittyModField(key, flags)
	if form.final != 'u' {
		return encodeFormCSIu(form, mods)
	}

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

func kittyModField(key KeyPressEvent, flags int) string {
	field := strconv.Itoa(kittyModParamFor(key.Mod, flags))
	if key.IsRepeat && flags&ansi.KittyReportEventTypes != 0 {
		field += ":2"
	}
	return field
}

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

type csiuForm struct {
	num   int
	final byte
}

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
		return csiuForm{13, '~'}, true
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

var kittyFunctionalKeys = func() map[rune]int {
	m := map[rune]int{
		KeyKp0: 57399, KeyKp1: 57400, KeyKp2: 57401, KeyKp3: 57402, KeyKp4: 57403,
		KeyKp5: 57404, KeyKp6: 57405, KeyKp7: 57406, KeyKp8: 57407, KeyKp9: 57408,
		KeyKpDecimal: 57409, KeyKpDivide: 57410, KeyKpMultiply: 57411,
		KeyKpMinus: 57412, KeyKpPlus: 57413, KeyKpEnter: 57414, KeyKpEqual: 57415,
		KeyKpSep: 57416, KeyKpLeft: 57417, KeyKpRight: 57418, KeyKpUp: 57419,
		KeyKpDown: 57420, KeyKpPgUp: 57421, KeyKpPgDown: 57422, KeyKpHome: 57423,
		KeyKpEnd: 57424, KeyKpInsert: 57425, KeyKpDelete: 57426, KeyKpBegin: 57427,
		KeyKpComma: 57416,
	}
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

func isKittyModifierKey(code rune) bool {
	switch code {
	case KeyCapsLock, KeyScrollLock, KeyNumLock:
		return true
	}
	return code >= KeyLeftShift && code <= KeyIsoLevel5Shift
}

func isKittyKeypadKey(code rune) bool {
	num, ok := kittyFunctionalKeys[code]
	return ok && num >= 57399 && num <= 57427
}

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
		keyField = kittyAlternateKeys(form.num, key)
	}
	return fmt.Sprintf("\x1b[%s;%d:3%c", keyField, kittyModParamFor(key.Mod, flags), form.final)
}

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
