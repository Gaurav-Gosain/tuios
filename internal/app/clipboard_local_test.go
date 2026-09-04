package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// detectClipboardToolEnv built from explicit getenv/lookPath closures, so the
// whole Wayland / X11 / macOS / none decision table runs as a plain table test
// with no compositor, no display, and no clipboard binaries on PATH.
func envWith(g func(string) string, lp func(string) bool) detectClipboardToolEnv {
	if g == nil {
		g = func(string) string { return "" }
	}
	if lp == nil {
		lp = func(string) bool { return false }
	}
	return detectClipboardToolEnv{getenv: g, lookPath: lp}
}

// TestDetectClipboardToolTable pins the decision table. Each row names the
// environment, what the host looks like, and which tool (if any) must be
// found. This is the test Gaurav's review asked for: detection takes getenv and
// lookPath as parameters, so the entire table is exercised without a
// compositor and without the host machine's real environment leaking in.
func TestDetectClipboardToolTable(t *testing.T) {
	no := func(string) bool { return false }
	waylandEnv := map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000", "WAYLAND_DISPLAY": "wayland-0"}
	x11Env := map[string]string{"DISPLAY": ":0"}

	tests := []struct {
		name     string
		getenv   func(string) string
		lookPath func(string) bool
		want     *clipboardTool
	}{
		{
			name:     "wayland session with wl-clipboard",
			getenv:   func(k string) string { return waylandEnv[k] },
			lookPath: func(b string) bool { return b == "wl-copy" || b == "wl-paste" },
			want:     &clipboardTool{name: "wl-clipboard", copyCmd: []string{"wl-copy"}, pasteCmd: []string{"wl-paste", "-n"}},
		},
		{
			name:     "wayland session without wl-paste",
			getenv:   func(k string) string { return waylandEnv[k] },
			lookPath: func(b string) bool { return b == "wl-copy" },
			want:     nil,
		},
		{
			name:     "wayland env vars but no tool on path",
			getenv:   func(k string) string { return waylandEnv[k] },
			lookPath: no,
			want:     nil,
		},
		{
			name:     "x11 with xclip",
			getenv:   func(k string) string { return x11Env[k] },
			lookPath: func(b string) bool { return b == "xclip" },
			want:     &clipboardTool{name: "xclip", copyCmd: []string{"xclip", "-selection", "clipboard"}, pasteCmd: []string{"xclip", "-selection", "clipboard", "-o"}},
		},
		{
			name:     "x11 with xsel only",
			getenv:   func(k string) string { return x11Env[k] },
			lookPath: func(b string) bool { return b == "xsel" },
			want:     &clipboardTool{name: "xsel", copyCmd: []string{"xsel", "--clipboard", "--input"}, pasteCmd: []string{"xsel", "--clipboard", "--output"}},
		},
		{
			name:     "x11 without any tool",
			getenv:   func(k string) string { return x11Env[k] },
			lookPath: no,
			want:     nil,
		},
		{
			name:     "macOS with pbcopy/pbpaste",
			lookPath: func(b string) bool { return b == "pbcopy" || b == "pbpaste" },
			want:     &clipboardTool{name: "pbcopy/pbpaste", copyCmd: []string{"pbcopy"}, pasteCmd: []string{"pbpaste"}},
		},
		{
			name:     "headless box with wl-clipboard but no display env",
			getenv:   func(string) string { return "" },
			lookPath: func(b string) bool { return b == "wl-copy" || b == "wl-paste" },
			want:     nil, // no display socket -> tool unusable, must not be reported
		},
		{
			name: "remote-like: XDG_RUNTIME_DIR set but no WAYLAND_DISPLAY",
			getenv: func(k string) string {
				if k == "XDG_RUNTIME_DIR" {
					return "/run/user/1000"
				}
				return ""
			},
			lookPath: func(b string) bool { return b == "wl-copy" || b == "wl-paste" },
			want:     nil, // half a wayland env is not a wayland session
		},
		{
			name:     "nothing at all",
			getenv:   nil,
			lookPath: nil,
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectClipboardTool(envWith(tt.getenv, tt.lookPath))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("detectClipboardTool() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestNativeClipboardToolDecision pins the gating that prevents the data leak
// Gaurav flagged: the native path must be refused on any non-VTE host even when
// a tool exists, and refused when the config option is off. The getenv
// injection makes the whole matrix testable with no compositor and no real env.
func TestNativeClipboardToolDecision(t *testing.T) {
	waylandWithTool := envWith(
		func(k string) string {
			m := map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000", "WAYLAND_DISPLAY": "wayland-0"}
			return m[k]
		},
		func(b string) bool { return b == "wl-copy" || b == "wl-paste" },
	)
	noTool := envWith(
		func(k string) string {
			m := map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000", "WAYLAND_DISPLAY": "wayland-0"}
			return m[k]
		},
		func(string) bool { return false },
	)

	tests := []struct {
		name            string
		env             detectClipboardToolEnv
		hostIsVTE       bool
		fallbackEnabled bool
		want            bool
	}{
		{
			name:            "VTE host + tool + fallback on → native path",
			env:             waylandWithTool,
			hostIsVTE:       true,
			fallbackEnabled: true,
			want:            true,
		},
		{
			name:            "non-VTE host (kitty) + tool + fallback on → OSC 52 only",
			env:             waylandWithTool,
			hostIsVTE:       false,
			fallbackEnabled: true,
			want:            false, // kitty implements OSC 52; no spawned tool, no dup entries
		},
		{
			name:            "VTE host + no tool + fallback on → OSC 52 only",
			env:             noTool,
			hostIsVTE:       true,
			fallbackEnabled: true,
			want:            false,
		},
		{
			name:            "VTE host + tool + fallback OFF → OSC 52 only",
			env:             waylandWithTool,
			hostIsVTE:       true,
			fallbackEnabled: false,
			want:            false,
		},
		{
			name:            "headless + fallback on → false",
			env:             envWith(nil, nil),
			hostIsVTE:       true,
			fallbackEnabled: true,
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nativeClipboardToolFor(tt.env, tt.hostIsVTE, tt.fallbackEnabled) != nil
			if got != tt.want {
				t.Fatalf("nativeClipboardToolFor() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestClipboardToolWriteReadLogic exercises Write/Read against a fake tool
// built from shell commands that need no compositor (tee/cat to a temp file).
// This is the deterministic part of the contract: Write launches the keeper
// without blocking and reaps it, Read captures stdout. The real wl-copy round
// trip is covered by TestClipboardToolRoundTrip, which stubs wl-clipboard with
// shell scripts on a temp PATH and runs everywhere.
func TestClipboardToolWriteReadLogic(t *testing.T) {
	clipFile := filepath.Join(t.TempDir(), "clip.txt")

	fake := &clipboardTool{
		name:     "fake-file",
		copyCmd:  []string{"sh", "-c", "cat > " + clipFile},
		pasteCmd: []string{"cat", clipFile},
	}

	// Write must return without blocking (Start semantics) and the keeper
	// process must have been launched and reaped (no zombie).
	if err := fake.Write("hello-tuios-clipboard"); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// The fake keeper writes asynchronously (Start semantics, like wl-copy's
	// real keeper), so poll briefly for the content instead of sleeping: a
	// real wl-copy keeper stays alive until replaced, which is fine — we only
	// care that the content reached the clipboard.
	var got string
	for i := 0; i < 50; i++ {
		var err error
		got, err = fake.Read()
		if err == nil && strings.Contains(got, "hello-tuios-clipboard") {
			break
		}
		// tiny sleep to let the keeper land the bytes
		time.Sleep(10 * time.Millisecond)
		if i == 49 {
			t.Fatalf("round trip mismatch: wrote %q, read back %q (err=%v)", "hello-tuios-clipboard", got, err)
		}
	}

	// Write again with new content: the second keeper must replace the first
	// (for wl-clipboard this is a natural handoff; for the fake it is a new
	// write to the same file).
	if err := fake.Write("second-write"); err != nil {
		t.Fatalf("second Write failed: %v", err)
	}
	var got2 string
	for i := 0; i < 50; i++ {
		var err error
		got2, err = fake.Read()
		if err == nil && strings.Contains(got2, "second-write") {
			break
		}
		time.Sleep(10 * time.Millisecond)
		if i == 49 {
			t.Fatalf("second round trip mismatch: read back %q (err=%v)", got2, err)
		}
	}
}

// TestNativeClipboardToolGate pins the session half of the gate, which the
// table above cannot see: a remote session (SSH or browser client) must never
// reach for the native tool, because WAYLAND_DISPLAY / DISPLAY describe the
// operator's desktop and a native read or write would move data between two
// people. The tool must also be refused when the config option is off.
//
// The environment is set up so a local client WOULD reach the tool: VTE_VERSION
// is set and wl-clipboard is stubbed on PATH. That is what makes the rows below
// meaningful — an earlier draft ran in a bare tree where no local client could
// reach the tool either, so every row returned nil and deleting the session
// gate below changed nothing. Here the SSH, browser and fallback-off rows are
// the only thing that can return nil.
func TestNativeClipboardToolGate(t *testing.T) {
	clip := stubClipboardSession(t)
	if !config.DefaultSettings().ClipboardLocalFallback {
		t.Fatalf("the fallback is off by default; this gate test assumes it on")
	}

	local := &OS{Settings: config.DefaultSettings()}
	if got := local.nativeClipboardTool(); got == nil {
		t.Fatalf("a local VTE session with wl-clipboard on PATH did not reach the native tool (clip=%s); the gate rows below are vacuous", clip)
	}

	rows := []struct {
		name string
		os   *OS
	}{
		{"ssh session", &OS{Settings: config.DefaultSettings(), IsSSHMode: true}},
		{"browser client", &OS{Settings: config.DefaultSettings(), BrowserClient: true}},
		{"fallback off", &OS{Settings: func() config.Settings {
			s := config.DefaultSettings()
			s.ClipboardLocalFallback = false
			return s
		}()}},
	}
	for _, tt := range rows {
		if got := tt.os.nativeClipboardTool(); got != nil {
			t.Fatalf("%s reached for the native clipboard: %v", tt.name, got.name)
		}
	}
}

// TestClipboardToolRoundTrip writes a sentinel through a tool discovered the
// way the real one is — DetectClipboardTool walking PATH — and reads it back.
// The package TestMain points the environment at a throwaway tree, so
// detection can never see the host session's compositor or real tools; this
// test stubs wl-clipboard with shell scripts in a temp dir on PATH instead,
// which exercises the same detection → Write → Read plumbing with no
// compositor, and runs everywhere.
func TestClipboardToolRoundTrip(t *testing.T) {
	stubClipboardSession(t)

	tool := DetectClipboardTool()
	if tool == nil {
		t.Fatalf("detection did not find the stubbed wl-clipboard on PATH")
	}

	const sentinel = "tuios-round-trip-sentinel"
	if err := tool.Write(sentinel); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	// The stub keeper lands the bytes asynchronously (Start semantics, like
	// the real wl-copy), so poll briefly instead of sleeping a fixed amount.
	var got string
	var err error
	for i := 0; i < 100; i++ {
		got, err = tool.Read()
		if err == nil && got == sentinel {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("round trip mismatch: wrote %q, read back %q (err=%v)", sentinel, got, err)
}

// stubClipboardSession points the test process at a throwaway wl-clipboard:
// shell-script stubs on a temp PATH, a named Wayland session, and VTE_VERSION
// set, so config.HostIsVTE() and detection both see a local VTE session with a
// reachable tool. It returns the file the wl-copy stub writes to, so a test can
// assert the native path really ran. t.Setenv restores the real environment
// when the test ends, so no other test sees it.
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

// TestWriteClipboardReachesNativeTool connects the single write path to the
// native tool. TestClipboardToolWriteReadLogic exercises clipboardTool.Write on
// its own and TestNativeClipboardToolDecision pins the decision table, but
// nothing used to prove that WriteClipboard — the method every copy in the app
// goes through — actually runs the native write when one is available. Making
// the native write unreachable (nativeClipboardTool returning nil) leaves the
// wl-copy stub untouched, so this test is the guard that fails.
func TestWriteClipboardReachesNativeTool(t *testing.T) {
	clip := stubClipboardSession(t)
	m := &OS{Settings: config.DefaultSettings()}

	const sentinel = "tuios-writeclipboard-sentinel"
	cmd := m.WriteClipboard(sentinel)
	if cmd == nil {
		t.Fatalf("WriteClipboard returned no command")
	}
	cmd() // the Cmd runs the native write, then returns the OSC 52 message

	// The wl-copy stub keeper lands the bytes asynchronously (Start semantics),
	// so poll briefly instead of sleeping a fixed amount.
	for i := 0; i < 100; i++ {
		got, err := os.ReadFile(clip)
		if err == nil && string(got) == sentinel {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := os.ReadFile(clip)
	t.Fatalf("the native write never reached wl-copy: clip holds %q, want %q", got, sentinel)
}
