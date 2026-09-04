package input

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// stubClipboardSession points the test process at a throwaway wl-clipboard:
// shell-script stubs on a temp PATH, a named Wayland session, and VTE_VERSION
// set. It returns the file the wl-copy stub writes to. The app package keeps
// the same helper for its own clipboard tests; a test in this package cannot
// import it, so it lives here too.
func stubClipboardSession(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	clip := filepath.Join(bin, "clipboard")
	stubs := map[string]string{
		filepath.Join(bin, "wl-copy"):  "#!/bin/sh\ncat > " + clip + "\n",
		filepath.Join(bin, "wl-paste"): "#!/bin/sh\nexec cat " + clip + "\n",
	}
	for path, body := range stubs {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatalf("stub %s: %v", path, err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_RUNTIME_DIR", bin)
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("VTE_VERSION", "1")
	// VTE_VERSION is the LAST marker detectHostTerminal consults, after
	// TERM_PROGRAM, KITTY_WINDOW_ID, WEZTERM_EXECUTABLE, TERM, etc. A dev
	// machine's real terminal would otherwise win over the stubbed session, so
	// blank every earlier marker and leave VTE_VERSION as the only one.
	for _, k := range []string{
		"TERM_PROGRAM", "KITTY_WINDOW_ID", "GHOSTTY_RESOURCES_DIR",
		"GHOSTTY_BIN_DIR", "ALACRITTY_WINDOW_ID", "ALACRITTY_SOCKET",
		"WEZTERM_EXECUTABLE", "TERM",
	} {
		t.Setenv(k, "")
	}
	return clip
}

var yankedRe = regexp.MustCompile(`Yanked (\d+) chars`)

// TestCopyModeYankReachesNativeTool pins copy mode's yank to the native write
// path. "y" routes through copyModeEffects.SetClipboard, and apply hands the
// text to app.OS.WriteClipboard, which runs wl-copy on a VTE host. If apply
// regressed to a bare tea.SetClipboard (OSC 52 only), the yank would never
// touch the native tool and the wl-copy stub would receive nothing — which is
// exactly what this test fails on. The units and the decision table are covered
// elsewhere; this is the wiring test between them.
func TestCopyModeYankReachesNativeTool(t *testing.T) {
	clip := stubClipboardSession(t)
	win := newCopyModeWindow(t, "copymode-clip-0001")
	o := &app.OS{Settings: config.DefaultSettings(), Mode: app.WindowManagementMode}

	// Jump to the top of the buffer so the cursor sits on real content
	// ("alpha beta gamma delta..."), then select in visual char mode and yank
	// the way a user would. Without the jump the cursor starts past the last
	// written cell and the yank comes back empty.
	HandleCopyModeKey(key("g"), o, win) // gg: top of the buffer
	HandleCopyModeKey(key("g"), o, win)
	HandleCopyModeKey(key("v"), o, win)
	HandleCopyModeKey(key("l"), o, win)
	_, cmd := HandleCopyModeKey(key("y"), o, win)
	if cmd == nil {
		t.Fatalf("y in visual mode returned no command")
	}

	// The handler reports how many chars it yanked; the native write must land
	// exactly that text in the stub.
	var yanked int
	for _, msg := range notificationMessages(o) {
		if m := yankedRe.FindStringSubmatch(msg); m != nil {
			yanked, _ = strconv.Atoi(m[1])
		}
	}
	if yanked == 0 {
		t.Fatalf("no yank notification; copy mode did not yank: %v", notificationMessages(o))
	}

	cmd() // apply's WriteClipboard Cmd: native write to the stub, then OSC 52

	// The wl-copy stub keeper lands the bytes asynchronously (Start semantics),
	// so poll briefly instead of sleeping a fixed amount.
	for i := 0; i < 100; i++ {
		got, err := os.ReadFile(clip)
		if err == nil && len(got) == yanked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := os.ReadFile(clip)
	t.Fatalf("the yank never reached wl-copy: stub holds %d chars (%q), want %d", len(got), got, yanked)
}
