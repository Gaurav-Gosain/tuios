package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// TestGetOptionAnswersWithTheValueInEffect pins the read: the session's
// override, then config.toml, then the default, the order the client draws by.
// It used to read only this session's own overrides, so the ordinary question
// failed on every session that had never set one. Later it skipped
// config.toml, so appearance.background = "theme" there read back as off while
// the client painted the theme.
func TestGetOptionAnswersWithTheValueInEffect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[appearance]\nbackground = \"theme\"\nanimations_enabled = false\n\n[notifications.agent]\ncommand = \"curl -H 'Authorization: Bearer s3cret' https://example.invalid\"\n"), 0o600); err != nil {
		t.Fatalf("write the config: %v", err)
	}
	d, sp := startTestDaemonWith(t, &DaemonConfig{ConfigPath: path})
	makeSessionWithWindow(t, d, "read")
	c := dialVerb(t, sp)
	get := func(key string) map[string]any {
		t.Helper()
		return result(t, c.call(t, `{"verb":"get-option","params":{"session":"read","key":"`+key+`"}}`))
	}

	res := get("appearance.sidebar.position")
	if res["source"] != "default" {
		t.Errorf("source = %v, want default for an option nothing has set", res["source"])
	}
	if res["value"] == nil || res["value"] == "" {
		t.Error("an untouched option read back empty")
	}

	res = get("appearance.background")
	if res["value"] != "theme" || res["source"] != "config" {
		t.Errorf("value, source = %v, %v, want theme, config from config.toml", res["value"], res["source"])
	}

	// A legacy key the parse folds into a new path is config.toml's too: the
	// client draws by it although no file names the new path.
	res = get("appearance.motion")
	if res["value"] != "none" || res["source"] != "config" {
		t.Errorf("value, source = %v, %v, want none, config from animations_enabled = false", res["value"], res["source"])
	}

	// A machine linked to this one reads no config file: config.toml holds
	// command strings that can carry tokens, and get-option needs only the
	// list capability on a link. It gets the session's override, then the
	// default, as it did before the file was read.
	link := dialLink(t, sp)
	for _, key := range []string{"appearance.background", "notifications.agent.command"} {
		res = result(t, link.call(t, `{"verb":"get-option","params":{"session":"read","key":"`+key+`"}}`))
		if res["source"] != "default" {
			t.Errorf("over a link, %s: value, source = %v, %v, want the default and nothing from config.toml", key, res["value"], res["source"])
		}
		if v, _ := res["value"].(string); strings.Contains(v, "s3cret") {
			t.Errorf("over a link, %s leaked a config.toml value: %q", key, v)
		}
	}

	result(t, c.call(t, `{"verb":"set-option","params":{"session":"read","key":"appearance.background","value":"off"}}`))
	res = get("appearance.background")
	if res["value"] != "off" || res["source"] != "session" {
		t.Errorf("value, source = %v, %v, want the session's off over config.toml", res["value"], res["source"])
	}
}
