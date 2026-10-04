package tuie2e

import (
	"encoding/json"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestDockListSaysWhichSwitchTurnsAComponentOff: a meter or the clock placed in
// the dock layout but switched off by show_cpu, show_ram or show_clock draws
// nothing, and list-dock-components says it is off and names the switch.
//
// Negative control: make dockSwitchedOff in internal/app/dock_components.go
// return "". The listing then reports cpu, ram and clock as drawn, with no
// switch named, and the test fails on the first of them.
func TestDockListSaysWhichSwitchTurnsAComponentOff(t *testing.T) {
	cfg := "[appearance]\nshow_cpu = false\nshow_ram = true\nshow_clock = false\n\n" +
		"[dock]\nright = ['cpu', 'ram', 'clock']\n"
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, cfg)
	if out, err := tuiosCLI(t, base, "new", "e2e", "--detach"); err != nil {
		t.Fatalf("create e2e: %v: %s", err, out)
	}
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "e2e"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}

	out, err := tuiosCLI(t, base, "list-dock-components", "--json", "-s", "e2e")
	if err != nil {
		t.Fatalf("list-dock-components: %v\n%s", err, out)
	}
	var payload struct {
		Components []struct {
			Name    string `json:"name"`
			Visible bool   `json:"visible"`
			Off     string `json:"off"`
		} `json:"components"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("list-dock-components output: %v\n%s", err, out)
	}
	want := map[string]string{"cpu": "show_cpu = false", "ram": "", "clock": "show_clock = false"}
	seen := map[string]bool{}
	for _, c := range payload.Components {
		off, ok := want[c.Name]
		if !ok {
			continue
		}
		seen[c.Name] = true
		if c.Off != off {
			t.Errorf("%s: off = %q, want %q\n%s", c.Name, c.Off, off, out)
		}
		if c.Visible != (off == "") {
			t.Errorf("%s: visible = %v with off %q\n%s", c.Name, c.Visible, c.Off, out)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("%s is not listed\n%s", name, out)
		}
	}

	table, err := tuiosCLI(t, base, "list-dock-components", "-s", "e2e")
	if err != nil {
		t.Fatalf("list-dock-components: %v\n%s", err, table)
	}
	if !containsAll(table, "off", "show_cpu = false", "show_clock = false") {
		t.Errorf("the table does not say which components are off\n%s", table)
	}
}
