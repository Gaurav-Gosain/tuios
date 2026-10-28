package tuie2e

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// TestDoctorNamesAKeyADefaultTook is a config that gives "," after the prefix
// to swap_down as well as shift+down. "," is the default key of
// prefix_settings, and on load the default keeps it: swap_down is left with
// shift+down. That is meant for a key an older tuios wrote into the file, but
// it takes a key the user chose the same way, so keybinds doctor has to say
// so. The JSON lists the dropped key with the action that runs it now, and the
// text report prints it under DROPPED BINDINGS.
//
// Negative control: the dropped keys from dropStaleDuplicateKeys left out of
// RetiredKeybinds. The JSON then has no entry for "," and this fails there.
func TestDoctorNamesAKeyADefaultTook(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `[keybindings.prefix_mode]
swap_down = ['shift+down', ',']
`)
	killDaemon(t, base)

	out, err := tuiosCLI(t, base, "keybinds", "explain", ",", "--json")
	if err != nil {
		t.Fatalf("keybinds explain: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"prefix_settings"`) {
		t.Fatalf("explain does not say , runs prefix_settings after the prefix:\n%s", out)
	}

	out, err = tuiosCLI(t, base, "keybinds", "doctor", "--json")
	if err != nil {
		t.Fatalf("keybinds doctor: %v\n%s", err, out)
	}
	var rep struct {
		Retired []struct {
			Section string   `json:"section"`
			Action  string   `json:"action"`
			Keys    []string `json:"keys"`
			Note    string   `json:"note"`
		} `json:"retired"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("keybinds doctor json: %v\n%s", err, out)
	}
	found := false
	for _, r := range rep.Retired {
		if r.Section == "prefix_mode" && r.Action == "swap_down" && slices.Equal(r.Keys, []string{","}) {
			found = strings.Contains(r.Note, "prefix_settings")
		}
	}
	if !found {
		t.Fatalf("the doctor does not report , taken from swap_down by prefix_settings:\n%s", out)
	}

	out, err = tuiosCLI(t, base, "keybinds", "doctor")
	if err != nil {
		t.Fatalf("keybinds doctor: %v\n%s", err, out)
	}
	_, dropped, ok := strings.Cut(out, "DROPPED BINDINGS")
	if !ok || !strings.Contains(dropped, "swap_down") || !strings.Contains(dropped, "[keybindings.prefix_mode]") {
		t.Fatalf("the text report does not list swap_down's , under DROPPED BINDINGS:\n%s", out)
	}
}
