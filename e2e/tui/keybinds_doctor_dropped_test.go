package tuie2e

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// TestDoctorNamesAKeyADefaultTook is a config that gives four keys after the
// prefix to the swap actions as well as their arrows. Each key is the default
// of another prefix action, and on load the default keeps it: the swap is
// left with its arrow. That is meant for a key an older tuios wrote into the
// file, but it takes a key the user chose the same way, so keybinds doctor
// has to say so. The JSON lists each dropped key with the action that runs it
// now, in the same order on every run, and the text report prints them under
// DROPPED BINDINGS with the keys quoted.
//
// Negative controls: the dropped keys from dropStaleDuplicateKeys left out of
// RetiredKeybinds (the JSON then has no entry for ","), and the sort of the
// drops removed (the map walk then reorders them between runs).
func TestDoctorNamesAKeyADefaultTook(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `[keybindings.prefix_mode]
swap_down = ['shift+down', ',']
swap_up = ['shift+up', 'c']
swap_left = ['shift+left', 'd']
swap_right = ['shift+right', 'x']
`)
	killDaemon(t, base)

	out, err := tuiosCLI(t, base, "keybinds", "explain", ",", "--json")
	if err != nil {
		t.Fatalf("keybinds explain: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"prefix_settings"`) {
		t.Fatalf("explain does not say , runs prefix_settings after the prefix:\n%s", out)
	}

	type retired struct {
		Section string   `json:"section"`
		Action  string   `json:"action"`
		Keys    []string `json:"keys"`
		Note    string   `json:"note"`
	}
	want := []string{"swap_down ,", "swap_left d", "swap_right x", "swap_up c"}
	// Each run walks the maps afresh, so five runs in one order rule out an
	// unsorted list passing by luck.
	for run := range 5 {
		out, err = tuiosCLI(t, base, "keybinds", "doctor", "--json")
		if err != nil {
			t.Fatalf("keybinds doctor: %v\n%s", err, out)
		}
		var rep struct {
			Retired []retired `json:"retired"`
		}
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatalf("keybinds doctor json: %v\n%s", err, out)
		}
		var got []string
		found := false
		for _, r := range rep.Retired {
			if r.Section != "prefix_mode" || len(r.Keys) != 1 {
				continue
			}
			got = append(got, r.Action+" "+r.Keys[0])
			if r.Action == "swap_down" && slices.Equal(r.Keys, []string{","}) {
				found = strings.Contains(r.Note, `"," is the default key of prefix_settings`)
			}
		}
		if !found {
			t.Fatalf("the doctor does not report , taken from swap_down by prefix_settings:\n%s", out)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("run %d: the dropped keys are %q, want %q in that order", run+1, got, want)
		}
	}

	out, err = tuiosCLI(t, base, "keybinds", "doctor")
	if err != nil {
		t.Fatalf("keybinds doctor: %v\n%s", err, out)
	}
	_, dropped, ok := strings.Cut(out, "DROPPED BINDINGS")
	if !ok || !strings.Contains(dropped, `swap_down`) || !strings.Contains(dropped, `"," in [keybindings.prefix_mode]`) {
		t.Fatalf("the text report does not list swap_down's quoted , under DROPPED BINDINGS:\n%s", out)
	}
}
