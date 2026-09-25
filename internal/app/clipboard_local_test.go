package app

import "testing"

// detectEnv builds an injectable environment: getenv answers from vars, and a
// tool is "installed" when its name is in tools.
func detectEnv(vars map[string]string, tools ...string) detectClipboardToolEnv {
	have := make(map[string]bool, len(tools))
	for _, t := range tools {
		have[t] = true
	}
	return detectClipboardToolEnv{
		getenv:   func(k string) string { return vars[k] },
		lookPath: func(name string) bool { return have[name] },
	}
}

func TestDetectClipboardTool(t *testing.T) {
	wayland := map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000", "WAYLAND_DISPLAY": "wayland-0"}
	x11 := map[string]string{"DISPLAY": ":0"}

	cases := []struct {
		name string
		env  detectClipboardToolEnv
		want string // "" means no tool
	}{
		{"wayland names wl-clipboard", detectEnv(wayland, "wl-copy", "wl-paste"), "wl-clipboard"},
		{"wayland needs both wl tools", detectEnv(wayland, "wl-copy"), ""},
		{"wayland without a runtime dir is not a route", detectEnv(map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, "wl-copy", "wl-paste"), ""},
		{"wayland with no helper falls through to x11", detectEnv(map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000", "WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}, "xclip"), "xclip"},
		{"x11 names xclip", detectEnv(x11, "xclip"), "xclip"},
		{"x11 names xsel when xclip is absent", detectEnv(x11, "xsel"), "xsel"},
		{"xclip wins over xsel", detectEnv(x11, "xclip", "xsel"), "xclip"},
		{"macos names pbcopy", detectEnv(map[string]string{}, "pbcopy", "pbpaste"), "pbcopy/pbpaste"},
		{"pbpaste without pbcopy is not a route", detectEnv(map[string]string{}, "pbpaste"), ""},
		{"nothing reachable", detectEnv(map[string]string{}), ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectClipboardTool(tc.env)
			gotName := ""
			if got != nil {
				gotName = got.name
			}
			if gotName != tc.want {
				t.Fatalf("detectClipboardTool = %q, want %q", gotName, tc.want)
			}
		})
	}
}

func TestShouldUseNativeClipboard(t *testing.T) {
	wayland := map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000", "WAYLAND_DISPLAY": "wayland-0"}

	cases := []struct {
		name     string
		env      detectClipboardToolEnv
		hostVTE  bool
		loopback bool
		want     bool
	}{
		{"a VTE host on a box with a tool uses the native path", detectEnv(wayland, "wl-copy", "wl-paste"), true, false, true},
		{"a loopback SSH session counts even without VTE detection", detectEnv(wayland, "wl-copy", "wl-paste"), false, true, true},
		{"a plain local non-VTE terminal keeps OSC 52", detectEnv(wayland, "wl-copy", "wl-paste"), false, false, false},
		{"VTE but no tool falls back to OSC 52", detectEnv(wayland), true, false, false},
		{"loopback but no tool falls back to OSC 52", detectEnv(wayland), false, true, false},
		{"neither VTE nor loopback, even with a tool, is refused", detectEnv(wayland, "wl-copy", "wl-paste"), false, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldUseNativeClipboard(tc.env, tc.hostVTE, tc.loopback); got != tc.want {
				t.Fatalf("shouldUseNativeClipboard(vte=%v, loopback=%v) = %v, want %v", tc.hostVTE, tc.loopback, got, tc.want)
			}
		})
	}
}
