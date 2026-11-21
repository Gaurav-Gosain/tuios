package config

import (
	"context"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

const macInputSourceTimeout = 2 * time.Second

const macLayoutRefresh = 30 * time.Second

var (
	macLayoutUS        atomic.Int32
	macLayoutCheckedAt atomic.Int64
	macLayoutRunning   atomic.Bool
	macLayoutForced    atomic.Int32
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

func macLayoutReadable() bool {
	return runtime.GOOS == "darwin" || E2EPlatformDarwin()
}

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

func readMacInputSource() string {
	if E2EPlatformDarwin() {
		return testknob.String("mac_input_source")
	}
	if runtime.GOOS != "darwin" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), macInputSourceTimeout)
	defer cancel()
	for _, key := range []string{"AppleCurrentKeyboardLayoutInputSourceID", "AppleSelectedInputSources"} {
		out, err := childproc.CommandContext(ctx, "/usr/bin/defaults", "read", "com.apple.HIToolbox", key).Output()
		if err == nil && len(out) > 0 {
			return string(out)
		}
	}
	return ""
}

const layoutNameKey = `"KeyboardLayout Name" = `

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
