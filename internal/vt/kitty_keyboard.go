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

// kittyKeyboardState tracks the kitty keyboard protocol state for a terminal.
// The protocol uses a stack of flag sets that can be pushed/popped by applications.
type kittyKeyboardState struct {
	stack []int // Stack of keyboard flag bitmasks
}

// newKittyKeyboardState creates a new kitty keyboard state with an empty stack.
func newKittyKeyboardState() *kittyKeyboardState {
	return &kittyKeyboardState{
		stack: []int{0}, // Always have at least one entry (the base)
	}
}

// CurrentFlags returns the currently active keyboard flags.
func (k *kittyKeyboardState) CurrentFlags() int {
	if len(k.stack) == 0 {
		return 0
	}
	return k.stack[len(k.stack)-1]
}

// Push pushes a new set of flags onto the stack.
func (k *kittyKeyboardState) Push(flags int) {
	k.stack = append(k.stack, flags)
}

// Pop removes n entries from the top of the stack.
// It always keeps at least one entry (the base).
func (k *kittyKeyboardState) Pop(n int) {
	if n <= 0 {
		n = 1
	}
	for range n {
		if len(k.stack) <= 1 {
			break
		}
		k.stack = k.stack[:len(k.stack)-1]
	}
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
	if len(k.stack) == 0 {
		k.stack = append(k.stack, current)
	} else {
		k.stack[len(k.stack)-1] = current
	}
}

// Reset clears the stack back to the base entry.
func (k *kittyKeyboardState) Reset() {
	k.stack = []int{0}
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
	// CSI > flags u  - Push keyboard mode
	e.RegisterCsiHandler(ansi.Command('>', 0, 'u'), func(params ansi.Params) bool {
		flags := 0
		if len(params) > 0 {
			flags = params[0].Param(0)
		}
		e.kittyKbd.Push(flags)
		e.updateKittyKeyboardCache()
		e.logf("kitty keyboard: push flags=%d, stack depth=%d", flags, len(e.kittyKbd.stack))
		return true
	})

	// CSI < count u  - Pop keyboard mode
	e.RegisterCsiHandler(ansi.Command('<', 0, 'u'), func(params ansi.Params) bool {
		count := 1
		if len(params) > 0 {
			count = params[0].Param(1)
		}
		e.kittyKbd.Pop(count)
		e.updateKittyKeyboardCache()
		e.logf("kitty keyboard: pop count=%d, stack depth=%d, flags=%d", count, len(e.kittyKbd.stack), e.kittyKbd.CurrentFlags())
		return true
	})

	// CSI ? u  - Query keyboard mode
	e.RegisterCsiHandler(ansi.Command('?', 0, 'u'), func(_ ansi.Params) bool {
		flags := e.kittyKbd.CurrentFlags()
		// Respond with CSI ? flags u
		response := fmt.Sprintf("\x1b[?%du", flags)
		_, _ = io.WriteString(e.pipe, response)
		e.logf("kitty keyboard: query, responding with flags=%d", flags)
		return true
	})

	// CSI = flags ; mode u  - Set keyboard mode
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
		e.logf("kitty keyboard: set flags=%d mode=%d, result=%d", flags, mode, e.kittyKbd.CurrentFlags())
		return true
	})
}

// KittyKeyboardFlags returns the current kitty keyboard protocol flags.
// Thread-safe: reads from an atomic cache updated on push/pop/set/reset.
func (e *Emulator) KittyKeyboardFlags() int {
	return int(e.cachedKittyFlags.Load())
}

// KittyKeyboardStack returns a copy of the kitty keyboard flag stack, base
// entry first. It exists for daemon state sync: a guest negotiates the
// protocol once (CSI > u push or CSI = u set) and never repeats it, so a
// reattaching client must be handed the stack rather than rediscover it from
// the output buffer. Call from the goroutine that feeds the emulator, or with
// the same lock that serializes writes to it.
func (e *Emulator) KittyKeyboardStack() []int {
	if e.kittyKbd == nil {
		return nil
	}
	return slices.Clone(e.kittyKbd.stack)
}

// RestoreKittyKeyboardState replaces the kitty keyboard flag stack from a
// saved state and refreshes the cache KittyKeyboardFlags reads. Used when
// reconnecting to a daemon session; a nil or empty stack is a no-op so state
// from an older daemon leaves the default (empty) state untouched.
func (e *Emulator) RestoreKittyKeyboardState(stack []int) {
	if e.kittyKbd == nil || len(stack) == 0 {
		return
	}
	e.kittyKbd.stack = slices.Clone(stack)
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

	code := int(key.Code)

	// Don't encode basic printable characters without modifiers
	// (unless report-all-keys flag is set)
	if flags&ansi.KittyReportAllKeysAsEscapeCodes == 0 {
		if key.Mod == 0 && code >= 0x20 && code < 0x7f {
			return ""
		}
		// For Shift+printable that produces different text (e.g., Shift+a → 'A'),
		// the kitty spec says to send the text directly, not CSI u.
		// Only use CSI u when there are other modifiers (Ctrl, Alt) besides Shift.
		if key.Text != "" && key.Mod == 1 { // Shift only (ModShift = 1)
			return ""
		}
	}

	// Map special keys to their CSI u key codes
	form := kittyKeyForm(key.Code)
	code = form.num
	if form.final != 'u' {
		return encodeFormCSIu(form, key.Mod)
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
	modParam := kittyModParam(key.Mod)
	if flags&ansi.KittyReportAssociatedKeys != 0 {
		if text := kittyAssociatedText(key.Text); text != "" {
			return fmt.Sprintf("\x1b[%d;%d;%su", code, modParam, text)
		}
	}
	if modParam > 1 {
		return fmt.Sprintf("\x1b[%d;%du", code, modParam)
	}
	return fmt.Sprintf("\x1b[%du", code)
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
// modifier field, then a terminator. Three shapes exist -- \x1b[<code>u for
// ordinary keys, \x1b[1;<mods><letter> for the arrows and F1-F4, and
// \x1b[<n>;<mods>~ for Insert through F12 -- and they differ only in those two
// values. Presses and releases read the same table, so a release can never name
// a different key than the press it ends.
type csiuForm struct {
	num   int
	final byte
}

// kittyKeyForm returns the CSI u spelling of a key code. Anything not named
// here is its own code terminated by 'u', which is what the protocol says for
// every ordinary character.
func kittyKeyForm(code rune) csiuForm {
	switch code {
	case KeyEnter:
		return csiuForm{13, 'u'}
	case KeyTab:
		return csiuForm{9, 'u'}
	case KeyBackspace:
		return csiuForm{127, 'u'}
	case KeyEscape:
		return csiuForm{27, 'u'}
	case KeySpace:
		return csiuForm{32, 'u'}
	case KeyUp:
		return csiuForm{1, 'A'}
	case KeyDown:
		return csiuForm{1, 'B'}
	case KeyRight:
		return csiuForm{1, 'C'}
	case KeyLeft:
		return csiuForm{1, 'D'}
	case KeyHome:
		return csiuForm{1, 'H'}
	case KeyEnd:
		return csiuForm{1, 'F'}
	case KeyF1:
		return csiuForm{1, 'P'}
	case KeyF2:
		return csiuForm{1, 'Q'}
	case KeyF3:
		return csiuForm{1, 'R'}
	case KeyF4:
		return csiuForm{1, 'S'}
	case KeyInsert:
		return csiuForm{2, '~'}
	case KeyDelete:
		return csiuForm{3, '~'}
	case KeyPgUp:
		return csiuForm{5, '~'}
	case KeyPgDown:
		return csiuForm{6, '~'}
	case KeyF5:
		return csiuForm{15, '~'}
	case KeyF6:
		return csiuForm{17, '~'}
	case KeyF7:
		return csiuForm{18, '~'}
	case KeyF8:
		return csiuForm{19, '~'}
	case KeyF9:
		return csiuForm{20, '~'}
	case KeyF10:
		return csiuForm{21, '~'}
	case KeyF11:
		return csiuForm{23, '~'}
	case KeyF12:
		return csiuForm{24, '~'}
	}
	return csiuForm{int(code), 'u'}
}

// encodeFormCSIu spells a press of one of the letter- or tilde-terminated keys.
// With no modifiers the sequence is the bare legacy one, which is what every
// terminal sends and every application already reads.
func encodeFormCSIu(form csiuForm, mod KeyMod) string {
	modParam := kittyModParam(mod)
	if modParam > 1 {
		return fmt.Sprintf("\x1b[%d;%d%c", form.num, modParam, form.final)
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
// held and xkb repeating it forever. The press form is unchanged by the flag --
// kitty sends a bare \x1b[97u for the press and \x1b[97;1:3u for its release --
// so the release always carries the modifier field, even when empty, because the
// event type rides on it as a subparameter.
func EncodeKeyReleaseCSIu(key KeyPressEvent, flags int) string {
	if flags&ansi.KittyReportEventTypes == 0 {
		return ""
	}
	form := kittyKeyForm(key.Code)
	return fmt.Sprintf("\x1b[%d;%d:3%c", form.num, kittyModParam(key.Mod), form.final)
}

// kittyModParam converts modifier flags to the CSI parameter format.
// The format is 1 + bitwise OR of: 1=shift, 2=alt, 4=ctrl, 8=super
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
	if mod&ModMeta != 0 {
		param += 8
	}
	return param
}
