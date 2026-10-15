package config

import (
	"context"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
)

// macInputSourceTimeout bounds one detection, both defaults calls together.
// It runs in the background, so a slow answer only delays the answer. A read
// that times out counts as not US until the next refresh, so the bound is
// generous: a first defaults call on a busy Mac can take a few hundred
// milliseconds.
const macInputSourceTimeout = 2 * time.Second

// macLayoutRefresh is how old an answer may get before a bare composed
// character asks for a new one. A user who switches input source sees the
// new layout at most this long after the first such character.
const macLayoutRefresh = 30 * time.Second

// The Mac's keyboard layout, as macLayoutIsUS reads it. The answer is filled
// in the background and never read through a subprocess on the input path.
var (
	// macLayoutUS is the last answer: 0 while none has arrived, 1 for a US
	// or ABC layout, 2 for any other layout or a failed read.
	macLayoutUS atomic.Int32
	// macLayoutCheckedAt is when the last detection started, in Unix
	// nanoseconds, zero before the first.
	macLayoutCheckedAt atomic.Int64
	// macLayoutRunning is set while a detection runs, so only one runs.
	macLayoutRunning atomic.Bool
	// macLayoutForced is set by ForceMacLayoutUS: 0 for none, 1 US, 2 not.
	macLayoutForced atomic.Int32
)

// StartMacLayoutDetection reads the Mac's keyboard layout in the background,
// unless an answer younger than macLayoutRefresh is there or a read is
// running. It never waits. Off macOS it does nothing. A client calls it at
// startup, so the answer is usually in before the first key.
//
// A tuios serving SSH or web clients from a Mac reads the Mac's layout, not
// the client's.
func StartMacLayoutDetection() {
	if macLayoutForced.Load() != 0 || !macLayoutReadable() {
		return
	}
	now := time.Now().UnixNano()
	if last := macLayoutCheckedAt.Load(); last != 0 && now-last < int64(macLayoutRefresh) {
		return
	}
	if !macLayoutRunning.CompareAndSwap(false, true) {
		return
	}
	macLayoutCheckedAt.Store(now)
	go func() {
		defer macLayoutRunning.Store(false)
		if inputSourceIsUS(readMacInputSource()) {
			macLayoutUS.Store(1)
		} else {
			macLayoutUS.Store(2)
		}
	}()
}

// macLayoutReadable reports whether there is a layout to read: on macOS, or
// in the end-to-end suite on its macOS paths.
func macLayoutReadable() bool {
	return runtime.GOOS == "darwin" || E2EPlatformDarwin()
}

// macLayoutIsUS reports whether the Mac's keyboard input source is US or ABC,
// the layouts the Option glyph tables describe. It is asked only for a bare
// composed character under option_glyphs = "auto". It answers from the last
// detection and never waits: before the first answer it is false, which sends
// the character to the pane. A stale answer starts a new detection in the
// background, for the next key.
func macLayoutIsUS() bool {
	switch macLayoutForced.Load() {
	case 1:
		return true
	case 2:
		return false
	}
	StartMacLayoutDetection()
	return macLayoutUS.Load() == 1
}

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
// not, with no detection, and returns the function that puts detection back.
// It is for tests.
func ForceMacLayoutUS(us bool) func() {
	prev := macLayoutForced.Load()
	if us {
		macLayoutForced.Store(1)
	} else {
		macLayoutForced.Store(2)
	}
	return func() { macLayoutForced.Store(prev) }
}
