package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestDetachedCascadeClickReachesVisiblePane catches #1600: panes made while
// no client is attached can draw a TML button over the focused shell, yet a
// click keeps selecting that shell. The panes, placement, frame and input all
// come from the real daemon and client. The larger case also exceeds the
// compositor sort's small-stack path. No test assigns a window's Z.
func TestDetachedCascadeClickReachesVisiblePane(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		host  bool
	}{
		{name: "local-3", count: 3},
		{name: "local-15", count: 15},
		{name: "host-3", count: 3, host: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			killDaemon(t, base)
			configDir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
			if err := os.MkdirAll(configDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`[startup]
tiled = false
start_in_terminal_mode = true
`), 0o600); err != nil {
				t.Fatal(err)
			}
			const session = "cascade"
			if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
				t.Fatalf("create detached session: %v\n%s", err, out)
			}
			for i := 0; i < tc.count-3; i++ {
				if out, err := tuiosCLI(t, base, "new-window", "-s", session,
					fmt.Sprintf("under-%d", i), "--no-focus", "--", "/bin/sh", "-c", "exec sleep 120"); err != nil {
					t.Fatalf("create underlying pane: %v\n%s", err, out)
				}
			}
			doc := writeTMLDoc(t, base, "cascade.tml", `<tml version="1" title="Cascade target">
<col pad="1" gap="1"><text>VISIBLE_TOP_PANE</text>
<button on-press="emit('clicked')">CLICK_TARGET</button></col></tml>
`)
			events := filepath.Join(base, "events.jsonl")
			program := fmt.Sprintf("exec %q tml show %q --pane --events > %q", tuiosBin, doc, events)
			if out, err := tuiosCLI(t, base, "new-window", "-s", session, "owner", "--no-focus", "--", "/bin/sh", "-c", program); err != nil {
				t.Fatalf("start the surface owner: %v\n%s", err, out)
			}
			opened := waitFile(t, events, func(s string) bool {
				return strings.Contains(s, `"type":"tml_opened"`)
			}, "the detached owner did not open its TML pane")
			var surface struct {
				Window string `json:"window_id"`
			}
			if err := json.Unmarshal([]byte(strings.SplitN(opened, "\n", 2)[0]), &surface); err != nil || surface.Window == "" {
				t.Fatalf("invalid TML open result: %v\n%s", err, opened)
			}
			initialFocus := focusedWindowID(t, base, session)
			if initialFocus == surface.Window {
				t.Fatal("the pane-created surface took focus before attach")
			}
			clientBase := base
			opts := startOpts{cols: 140, rows: 60, args: []string{"attach", session}}
			if tc.host {
				clientBase = t.TempDir()
				ssh := writeFakeSSHTo(t, clientBase, base)
				writeOneHostConfig(t, clientBase, tuiosBin)
				clientConfig := filepath.Join(clientBase, "XDG_CONFIG_HOME", "tuios", "config.toml")
				body, err := os.ReadFile(clientConfig)
				if err != nil {
					t.Fatal(err)
				}
				body = append(body, []byte("\n[startup]\ntiled = false\nstart_in_terminal_mode = true\n")...)
				if err := os.WriteFile(clientConfig, body, 0o600); err != nil {
					t.Fatal(err)
				}
				opts.args = []string{"attach", "--host", "build", session}
				opts.env = []string{"TUIOS_SSH=" + ssh}
			}
			term := startIn(t, clientBase, opts)
			waitForAll(t, term, uiTimeout, "the top surface", "VISIBLE_TOP_PANE", "CLICK_TARGET")
			saveTMLFrame(t, term, "cascade-before-click")

			t.Run("floating", func(t *testing.T) {
				// A first click focuses an unfocused surface without activating
				// its button. Derive the point from the screen the user sees.
				x, y := tmlLocate(t, term, "CLICK_TARGET")
				mouseClick(t, term, x+1, y, tuitest.MouseLeft, 0)
				deadline := time.Now().Add(uiTimeout)
				for focusedWindowID(t, base, session) != surface.Window && time.Now().Before(deadline) {
					time.Sleep(50 * time.Millisecond)
				}
				if got := focusedWindowID(t, base, session); got != surface.Window {
					t.Errorf("click on the visible TML pane kept focus on %s; want %s", got, surface.Window)
				}
				time.Sleep(gestureGap)
				data, err := os.ReadFile(events)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(data), `"name":"clicked"`) {
					t.Error("the first click activated the unfocused surface")
				}
				x, y = tmlLocate(t, term, "CLICK_TARGET")
				mouseClick(t, term, x+1, y, tuitest.MouseLeft, 0)
				deadline = time.Now().Add(uiTimeout)
				for time.Now().Before(deadline) {
					data, err = os.ReadFile(events)
					if err != nil || strings.Contains(string(data), `"name":"clicked"`) {
						break
					}
					time.Sleep(50 * time.Millisecond)
				}
				saveTMLFrame(t, term, "cascade-after-click")
				if err != nil || !strings.Contains(string(data), `"name":"clicked"`) {
					t.Errorf("second click did not reach the visible TML button: %v\n%s", err, data)
				}
			})

			// Run the control even when floating fails. It uses the same owner,
			// surface and mouse transport, after the public tiling toggle.
			if tc.count == 3 {
				t.Run("tiled-control", func(t *testing.T) {
					before, err := os.ReadFile(events)
					if err != nil {
						t.Fatal(err)
					}
					prior := strings.Count(string(before), `"name":"clicked"`)
					oldX, oldY := tmlLocate(t, term, "CLICK_TARGET")
					if err := term.SendKeys(tuitest.Ctrl('b'), " "); err != nil {
						t.Fatal(err)
					}
					if err := term.WaitFor(func(s tuitest.Screen) bool {
						_, rows := s.Size()
						for y := range rows {
							if x := strings.Index(s.Line(y), "CLICK_TARGET"); x >= 0 {
								return y != oldY || len([]rune(s.Line(y)[:x])) != oldX
							}
						}
						return false
					}, uiTimeout); err != nil {
						t.Fatalf("tiling did not move the surface: %v", err)
					}
					waitForAll(t, term, uiTimeout, "the tiled surface", "VISIBLE_TOP_PANE", "CLICK_TARGET")
					pressTMLButton(t, term, "VISIBLE_TOP_PANE", "CLICK_TARGET")
					waitFile(t, events, func(s string) bool {
						return strings.Count(s, `"name":"clicked"`) > prior
					}, "the tiled button did not deliver a new event")
					saveTMLFrame(t, term, "tiled-control")
				})
			}
			if data, err := os.ReadFile(events); err == nil {
				if err := os.WriteFile(filepath.Join(artifactDir(t), "events.jsonl"), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
