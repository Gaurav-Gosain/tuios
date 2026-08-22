package session

import (
	"strings"
	"testing"
)

// An agent told to make a session look like Catppuccin Mocha has to get from
// that sentence to a theme id, and the id it will guess is the one with a
// hyphen. Before list-themes there was nothing to ask: the option published no
// accepted set, so the name was a guess, and setting a name that did not
// resolve was reported as a set that worked.
func TestListThemesFindsAThemeByName(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "rice")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"verb":"list-themes","params":{"session":"rice","filter":"catppuccin"}}`))
	ids, _ := res["themes"].([]any)
	if len(ids) == 0 {
		t.Fatal("no catppuccin theme matched")
	}
	found := false
	for _, id := range ids {
		if id == "catppuccin_mocha" {
			found = true
		}
	}
	if !found {
		t.Errorf("catppuccin_mocha is not in %v", ids)
	}
	if total, _ := res["total"].(float64); total < 100 {
		t.Errorf("total reported %v registered themes", total)
	}
	// Where a custom theme goes is the other half of the answer, and it was
	// knowable only by reading the source.
	if dir, _ := res["themes_dir"].(string); dir == "" {
		t.Error("list-themes did not say where a custom theme file goes")
	}
}

// The palette is the feedback half: an agent cannot see the screen, so the
// colours and their contrast are the only way it can tell a theme that applied
// from one that did not.
func TestListThemesDescribesAPalette(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "rice")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"verb":"list-themes","params":{"session":"rice","theme":"catppuccin_mocha"}}`))
	pal, ok := res["palette"].(map[string]any)
	if !ok {
		t.Fatal("no palette in the result")
	}
	if pal["id"] != "catppuccin_mocha" {
		t.Errorf("palette is for %v", pal["id"])
	}
	if bg, _ := pal["bg"].(string); !strings.HasPrefix(bg, "#") {
		t.Errorf("background is %q, want a hex literal", bg)
	}
	swatches, _ := pal["swatches"].([]any)
	if len(swatches) != 18 {
		t.Fatalf("got %d swatches, want the foreground, the cursor and the sixteen", len(swatches))
	}
	for _, s := range swatches {
		row := s.(map[string]any)
		for _, field := range []string{"name", "hex", "ratio", "floor", "passes"} {
			if _, ok := row[field]; !ok {
				t.Fatalf("swatch %v is missing %s", row["name"], field)
			}
		}
	}
	// Naming one theme is a question about that theme; answering with a hundred
	// other ids buries it.
	if _, ok := res["themes"]; ok {
		t.Error("describing one theme also returned the roster")
	}
}

// The whole point of the check: a name that does not resolve fails, and says
// the name that would have.
func TestSetOptionRejectsAThemeThatDoesNotExist(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "rice")
	c := dialVerb(t, sp)

	resp := c.call(t, `{"verb":"set-option","params":{"session":"rice","key":"appearance.theme","value":"catppuccin-mocha"}}`)
	e := errorOf(t, resp)
	if e["code"] != ErrVerbInvalidParams {
		t.Fatalf("code is %v, want %s", e["code"], ErrVerbInvalidParams)
	}
	hint, _ := e["hint"].(map[string]any)
	if hint["did_you_mean"] != "catppuccin_mocha" {
		t.Errorf("did_you_mean is %v, want catppuccin_mocha", hint["did_you_mean"])
	}
}

// Empty is not a name that has to resolve: it is how a session says it wants
// the terminal's own colours, and clearing a theme has to stay reachable.
func TestSetOptionAcceptsAnEmptyTheme(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "rice")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"verb":"set-option","params":{"session":"rice","key":"appearance.theme","value":""}}`))
	if res["key"] != "appearance.theme" {
		t.Fatalf("set the wrong key: %v", res["key"])
	}
}
