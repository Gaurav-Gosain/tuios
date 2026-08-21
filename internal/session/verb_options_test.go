package session

import (
	"strings"
	"testing"
)

// TestListOptionsIsEnoughToFindASetting drives the discovery verb. Finding a
// setting used to mean reading the configuration docs and hoping the runtime
// understood the path, which for everything under sidebar it did not.
func TestListOptionsIsEnoughToFindASetting(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "opts")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"verb":"list-options","params":{"session":"opts"}}`))
	all, _ := res["options"].([]any)
	if len(all) < 50 {
		t.Fatalf("list-options reported %d options, want the whole surface", len(all))
	}
	sections, _ := res["sections"].([]any)
	if len(sections) == 0 {
		t.Error("list-options reported no sections")
	}

	// Every row has to carry enough for a caller to set it without a second call.
	for _, row := range all {
		o := row.(map[string]any)
		for _, field := range []string{"path", "type", "section", "description"} {
			if o[field] == nil || o[field] == "" {
				t.Fatalf("option %v is missing %s", o["path"], field)
			}
		}
		// default is present but may be empty, which is itself the value: a
		// colour that is unset falls back to the theme.
		if _, ok := o["default"]; !ok {
			t.Fatalf("option %v is missing default", o["path"])
		}
	}

	// The section filter is what makes the surface navigable rather than a wall.
	res = result(t, c.call(t, `{"verb":"list-options","params":{"session":"opts","section":"sidebar"}}`))
	sidebar, _ := res["options"].([]any)
	if len(sidebar) == 0 {
		t.Fatal("no sidebar options: the section that prompted this work is missing")
	}
	for _, row := range sidebar {
		o := row.(map[string]any)
		if o["section"] != "sidebar" {
			t.Errorf("section filter returned %v", o["path"])
		}
	}

	// A closed value set is reported, so a caller never has to guess a spelling.
	res = result(t, c.call(t, `{"verb":"list-options","params":{"session":"opts","prefix":"appearance.sidebar.position"}}`))
	pos := res["options"].([]any)[0].(map[string]any)
	accepted, _ := pos["accepted"].([]any)
	if len(accepted) == 0 {
		t.Error("sidebar.position reported no accepted values")
	}
}

// TestSetOptionValidatesBothHalves is the change that matters. set-option took
// any string at all, so a misspelled path was recorded, reported as set, and did
// nothing.
func TestSetOptionValidatesBothHalves(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "valid")
	c := dialVerb(t, sp)

	t.Run("a path that names nothing is refused", func(t *testing.T) {
		resp := c.call(t, `{"verb":"set-option","params":{"session":"valid","key":"appearance.totally_made_up","value":"purple"}}`)
		if code := errCode(t, resp); code != ErrVerbOptionNotFound {
			t.Fatalf("code = %q, want %q", code, ErrVerbOptionNotFound)
		}
	})

	t.Run("a near miss suggests the real path", func(t *testing.T) {
		resp := c.call(t, `{"verb":"set-option","params":{"session":"valid","key":"appearance.sidebar.positon","value":"right"}}`)
		hint := resp["error"].(map[string]any)["hint"].(map[string]any)
		if hint["did_you_mean"] != "appearance.sidebar.position" {
			t.Errorf("did_you_mean = %v", hint["did_you_mean"])
		}
	})

	t.Run("a value outside the accepted set is refused", func(t *testing.T) {
		resp := c.call(t, `{"verb":"set-option","params":{"session":"valid","key":"appearance.sidebar.position","value":"sideways"}}`)
		if code := errCode(t, resp); code != ErrVerbInvalidParams {
			t.Fatalf("code = %q, want %q", code, ErrVerbInvalidParams)
		}
		// The refusal has to say what would have worked.
		hint := resp["error"].(map[string]any)["hint"].(map[string]any)
		accepted, _ := hint["accepted"].([]any)
		if len(accepted) == 0 {
			t.Error("the refusal listed no accepted values")
		}
	})

	t.Run("a non-numeric int is refused", func(t *testing.T) {
		resp := c.call(t, `{"verb":"set-option","params":{"session":"valid","key":"appearance.sidebar.width","value":"wide"}}`)
		if code := errCode(t, resp); code != ErrVerbInvalidParams {
			t.Errorf("code = %q, want %q", code, ErrVerbInvalidParams)
		}
	})

	t.Run("a good value is recorded and says why it is not live", func(t *testing.T) {
		res := result(t, c.call(t, `{"verb":"set-option","params":{"session":"valid","key":"appearance.sidebar.enabled","value":"true"}}`))
		if res["applied"] != false {
			t.Errorf("applied = %v, want false with no client attached", res["applied"])
		}
		// applied false used to mean both "nobody attached" and "that key means
		// nothing", which a caller could not tell apart and so could not act on.
		reason, _ := res["reason"].(string)
		if !strings.Contains(reason, "attach") {
			t.Errorf("reason = %q, want it to say nobody is attached", reason)
		}
	})
}

// TestSetOptionStillTakesTheBareSpelling guards a break dressed up as a fix. The
// runtime setter has always taken "border_style" as well as the full path, and
// validation that only knew the long form would have made the short one an error
// for the first time.
func TestSetOptionStillTakesTheBareSpelling(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "bare")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"verb":"set-option","params":{"session":"bare","key":"border_style","value":"double"}}`))
	// Normalised on the way in, so one option never becomes two entries that can
	// disagree.
	if res["key"] != "appearance.border_style" {
		t.Errorf("key = %v, want the full path", res["key"])
	}

	got := result(t, c.call(t, `{"verb":"get-option","params":{"session":"bare","key":"border_style"}}`))
	if got["value"] != "double" {
		t.Errorf("value = %v, want double", got["value"])
	}
}

// TestGetOptionAnswersWithTheValueInEffect pins the read. It used to read only
// this session's own overrides, so the ordinary question failed on every session
// that had never set one, which is most of them.
func TestGetOptionAnswersWithTheValueInEffect(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "read")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"verb":"get-option","params":{"session":"read","key":"appearance.sidebar.position"}}`))
	if res["source"] != "default" {
		t.Errorf("source = %v, want default for an option nothing has set", res["source"])
	}
	if res["value"] == nil || res["value"] == "" {
		t.Error("an untouched option read back empty")
	}

	c.call(t, `{"verb":"set-option","params":{"session":"read","key":"appearance.sidebar.position","value":"right"}}`)
	res = result(t, c.call(t, `{"verb":"get-option","params":{"session":"read","key":"appearance.sidebar.position"}}`))
	if res["value"] != "right" {
		t.Errorf("value = %v, want right", res["value"])
	}
	// source is what lets a caller tell an override from a default it happens to
	// match, which is the whole reason it is reported.
	if res["source"] != "session" {
		t.Errorf("source = %v, want session after setting it", res["source"])
	}

	resp := c.call(t, `{"verb":"get-option","params":{"session":"read","key":"appearance.nonsense"}}`)
	if code := errCode(t, resp); code != ErrVerbOptionNotFound {
		t.Errorf("code = %q, want %q", code, ErrVerbOptionNotFound)
	}
}
