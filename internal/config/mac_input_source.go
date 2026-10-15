package config

import (
	"context"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
)

// macInputSourceTimeout bounds the one call to defaults. A slow answer is a
// failed detection, which is the safe reading.
const macInputSourceTimeout = time.Second

// macLayoutIsUS reports whether the Mac's keyboard input source is US or ABC,
// the layouts the Option glyph tables describe. It is asked the first time a
// bare composed character meets option_glyphs = "auto", and never again: a
// layout switched while tuios runs is not seen. Off macOS it is false at no
// cost.
//
// A tuios serving SSH or web clients from a Mac reads the Mac's layout, not
// the client's.
var macLayoutIsUS = sync.OnceValue(func() bool {
	return inputSourceIsUS(readMacInputSource())
})

// readMacInputSource returns what macOS says about the selected keyboard
// layout, or "" when it cannot be read. The end-to-end suite fakes it with
// TUIOS_E2E_MAC_INPUT_SOURCE, alongside TUIOS_E2E_PLATFORM=darwin.
func readMacInputSource() string {
	if E2EPlatformDarwin() {
		return os.Getenv("TUIOS_E2E_MAC_INPUT_SOURCE")
	}
	if runtime.GOOS != "darwin" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), macInputSourceTimeout)
	defer cancel()
	// The current layout's ID, such as com.apple.keylayout.British. Some
	// systems do not write it; the list of selected sources is the fallback.
	for _, key := range []string{"AppleCurrentKeyboardLayoutInputSourceID", "AppleSelectedInputSources"} {
		out, err := childproc.CommandContext(ctx, "/usr/bin/defaults", "read", "com.apple.HIToolbox", key).Output()
		if err == nil && len(out) > 0 {
			return string(out)
		}
	}
	return ""
}

// layoutNameKey starts each keyboard layout's name in the property list that
// defaults prints for AppleSelectedInputSources.
const layoutNameKey = `"KeyboardLayout Name" = `

// inputSourceIsUS reads what readMacInputSource returned. A layout ID counts
// when it is the US or ABC layout. A list of selected sources counts only
// when every keyboard layout in it is US or ABC: with British also selected,
// a bare £ may be Shift and 3. Anything else, empty included, is not US.
func inputSourceIsUS(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	if id, ok := strings.CutPrefix(raw, "com.apple.keylayout."); ok {
		return usLayoutName(id)
	}
	found := false
	for _, entry := range strings.Split(raw, layoutNameKey)[1:] {
		name, _, ok := strings.Cut(entry, ";")
		if !ok || !usLayoutName(strings.Trim(strings.TrimSpace(name), `"`)) {
			return false
		}
		found = true
	}
	return found
}

// usLayoutName reports whether a layout ID suffix or name is US or ABC.
func usLayoutName(name string) bool {
	switch name {
	case "US", "U.S.", "ABC":
		return true
	}
	return false
}

// ForceMacLayoutUS makes option_glyphs = "auto" read the Mac layout as US or
// not, and returns the function that puts detection back. It is for tests.
func ForceMacLayoutUS(us bool) func() {
	prev := macLayoutIsUS
	macLayoutIsUS = func() bool { return us }
	return func() { macLayoutIsUS = prev }
}
