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

// TestDockMetersFollowLiveSettings covers both ways of changing the same
// registry settings: the settings page and set-config. A visible label alone
// is not enough: the listing must carry a value and a sampling interval, and
// last_run must advance without further input. Switching off must remove the
// schedule, and switching back on must restore it without a manual config reload.
//
// Negative control: remove DockMetersSyncCmd from Update. Both paths then
// show enabled meters with no interval or sampled text.
func TestDockMetersFollowLiveSettings(t *testing.T) {
	for _, method := range []string{"settings", "set-config"} {
		t.Run(method, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, "[appearance]\nshow_cpu = false\nshow_ram = false\nshow_clock = false\n")
			if out, err := tuiosCLI(t, base, "new", "e2e", "--detach"); err != nil {
				t.Fatalf("create session: %v\n%s", err, out)
			}
			killDaemon(t, base)
			term := startIn(t, base, startOpts{args: []string{"attach", "e2e"}})
			waitWindowCount(t, term, 1, "attaching the meter client")
			windowManagementMode(t, term)

			setMeter := func(name string, on bool) {
				t.Helper()
				if method == "set-config" {
					value := "false"
					if on {
						value = "true"
					}
					if out, err := tuiosCLI(t, base, "set-config", "appearance.show_"+name, value, "-s", "e2e"); err != nil {
						t.Fatalf("set %s=%s: %v\n%s", name, value, err, out)
					}
					return
				}
				openSettings(t, term)
				label := strings.ToUpper(name) + " meter"
				if err := term.SendKeys("/", strings.ToLower(label)); err != nil {
					t.Fatalf("search %s: %v", label, err)
				}
				if err := term.WaitFor(func(s tuitest.Screen) bool {
					return selectedSettingsRow(s, label) != ""
				}, uiTimeout); err != nil {
					t.Fatalf("select %s: %v\n%s", label, err, term.Snapshot())
				}
				if err := term.SendKeys(tuitest.Enter); err != nil {
					t.Fatalf("toggle %s: %v", label, err)
				}
				state := "off ]"
				if on {
					state = "on ]"
				}
				if err := term.WaitFor(func(s tuitest.Screen) bool {
					return strings.Contains(selectedSettingsRow(s, label), state)
				}, uiTimeout); err != nil {
					t.Fatalf("%s never became %s: %v\n%s", label, state, err, term.Snapshot())
				}
				if err := term.SendKeys(tuitest.Esc); err != nil {
					t.Fatalf("clear search: %v", err)
				}
				if err := term.WaitForText("Focused border color", uiTimeout); err != nil {
					t.Fatalf("search did not clear: %v\n%s", err, term.Snapshot())
				}
				if err := term.SendKeys(tuitest.Esc); err != nil {
					t.Fatalf("close settings: %v", err)
				}
				if err := term.WaitFor(func(s tuitest.Screen) bool {
					return !strings.Contains(s.Text(), "Focused border color")
				}, uiTimeout); err != nil {
					t.Fatalf("settings did not close: %v\n%s", err, term.Snapshot())
				}
			}

			for _, name := range []string{"cpu", "ram"} {
				waitDockMeter(t, base, name, false)
				setMeter(name, true)
				waitDockMeter(t, base, name, true)
			}
			// No keys or config writes here: the sampler itself must keep running.
			for _, name := range []string{"cpu", "ram"} {
				first := waitDockMeter(t, base, name, true)
				deadline := time.Now().Add(uiTimeout)
				for readDockMeters(t, base)[name].LastRun == first.LastRun {
					if time.Now().After(deadline) {
						t.Fatalf("%s stopped sampling after its initial fill", name)
					}
					time.Sleep(100 * time.Millisecond)
				}
			}
			saveArtifact(t, term, artifactDir(t), "meters-on")
			saveDockMeterListing(t, base, "meters-on")
			for _, name := range []string{"cpu", "ram"} {
				setMeter(name, false)
				waitDockMeter(t, base, name, false)
				setMeter(name, true)
				waitDockMeter(t, base, name, true)
				setMeter(name, false)
				waitDockMeter(t, base, name, false)
			}
			saveArtifact(t, term, artifactDir(t), "meters-off")
			saveDockMeterListing(t, base, "meters-off")
		})
	}
}

// TestDockMeterSyncDoesNotReloadAnUnchangedPlan guards the quiet path. A once
// component records each start, so needless engine rebuilds cannot hide behind
// an unchanged frame. Enabled meters absent from the plan must not poll. The
// same fixture then places and enables CPU, proving that a real change starts
// its sampler and restarts the once component.
func TestDockMeterSyncDoesNotReloadAnUnchangedPlan(t *testing.T) {
	for _, placed := range []bool{false, true} {
		t.Run(fmt.Sprintf("placed=%v", placed), func(t *testing.T) {
			base := t.TempDir()
			starts := filepath.Join(base, "starts")
			right := "['custom/probe', 'session-controls']"
			if placed {
				right = "['cpu', 'ram', 'custom/probe', 'session-controls']"
			}
			command := fmt.Sprintf("printf x >> %q; echo METERGUARD", starts)
			writeConfig(t, base, fmt.Sprintf("[dock]\nright = %s\n[dock.custom.probe]\ncommand = %q\nrefresh = 'once'\n", right, command))
			if out, err := tuiosCLI(t, base, "new", "e2e", "--detach"); err != nil {
				t.Fatalf("create session: %v\n%s", err, out)
			}
			killDaemon(t, base)
			term := startIn(t, base, startOpts{args: []string{"attach", "e2e"}})
			waitWindowCount(t, term, 1, "attaching the quiet client")
			deadline := time.Now().Add(uiTimeout)
			for readDockMeters(t, base)["custom/probe"].Text != "METERGUARD" {
				if time.Now().After(deadline) {
					t.Fatal("the once component never ran")
				}
				time.Sleep(100 * time.Millisecond)
			}
			for range 2 {
				for _, name := range []string{"cpu", "ram", "clock"} {
					value := "false"
					if !placed && name != "clock" {
						value = "true"
					}
					if out, err := tuiosCLI(t, base, "set-config", "appearance.show_"+name, value, "-s", "e2e"); err != nil {
						t.Fatalf("set %s: %v\n%s", name, err, out)
					}
				}
			}
			// Exercise the post-handler sync over more than one meter interval.
			deadline = time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				meters := readDockMeters(t, base)
				for _, name := range []string{"cpu", "ram"} {
					c, found := meters[name]
					if found != placed || c.Interval != "" || c.LastRun != "" {
						t.Fatalf("unexpected %s sampler: found=%v, %+v", name, found, c)
					}
				}
				time.Sleep(100 * time.Millisecond)
			}
			if got, err := os.ReadFile(starts); err != nil || string(got) != "x" {
				t.Fatalf("unchanged sampler membership restarted the once component: %q, %v", got, err)
			}
			saveArtifact(t, term, artifactDir(t), "unchanged-plan")
			saveDockMeterListing(t, base, "unchanged-plan")

			// Positive half: the same engine must rebuild when a meter really
			// joins its sampler set. Placement changes use the config watcher;
			// an already placed meter uses the live setting handled by the sync.
			if placed {
				if out, err := tuiosCLI(t, base, "set-config", "appearance.show_cpu", "true", "-s", "e2e"); err != nil {
					t.Fatalf("enable placed CPU: %v\n%s", err, out)
				}
			} else {
				writeConfig(t, base, fmt.Sprintf("[appearance]\nshow_cpu = true\nshow_ram = false\nshow_clock = false\n[dock]\nright = ['cpu', 'custom/probe', 'session-controls']\n[dock.custom.probe]\ncommand = %q\nrefresh = 'once'\n", command))
			}
			waitDockMeter(t, base, "cpu", true)
			deadline = time.Now().Add(uiTimeout)
			for {
				got, err := os.ReadFile(starts)
				if err != nil {
					t.Fatalf("read starts after membership change: %v", err)
				}
				if string(got) == "xx" {
					break
				}
				if len(got) > 2 || time.Now().After(deadline) {
					t.Fatalf("membership change should restart the once component once: %q", got)
				}
				time.Sleep(100 * time.Millisecond)
			}
			saveArtifact(t, term, artifactDir(t), "changed-plan")
			saveDockMeterListing(t, base, "changed-plan")
		})
	}
}

func saveDockMeterListing(t *testing.T, base, name string) {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-dock-components", "--json", "-s", "e2e")
	if err != nil {
		t.Fatalf("artifact listing: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(artifactDir(t), name+".json"), []byte(out), 0o644); err != nil {
		t.Fatalf("save listing: %v", err)
	}
}

type liveDockMeter struct {
	Name     string `json:"name"`
	Visible  bool   `json:"visible"`
	Refresh  string `json:"refresh"`
	Interval string `json:"interval"`
	Text     string `json:"text"`
	LastRun  string `json:"last_run"`
	Off      string `json:"off"`
}

func readDockMeters(t *testing.T, base string) map[string]liveDockMeter {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-dock-components", "--json", "-s", "e2e")
	if err != nil {
		t.Fatalf("list meters: %v\n%s", err, out)
	}
	var payload struct {
		Components []liveDockMeter `json:"components"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode meters: %v\n%s", err, out)
	}
	meters := make(map[string]liveDockMeter)
	for _, c := range payload.Components {
		meters[c.Name] = c
	}
	return meters
}

func waitDockMeter(t *testing.T, base, name string, on bool) liveDockMeter {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		c, found := readDockMeters(t, base)[name]
		if found && c.Visible == on {
			if on && c.Refresh == "interval" && c.Interval == "2s" && c.LastRun != "" &&
				strings.HasPrefix(c.Text, strings.ToUpper(name)+":") && c.Off == "" {
				return c
			}
			if !on && c.Refresh == "render" && c.Interval == "" && c.LastRun == "" && c.Text == "" && c.Off != "" {
				return c
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s on=%v did not update its sampler: found=%v, %+v", name, on, found, c)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
