package integration

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestParseStatusLine reads Claude Code's and the opencode plugin's payloads,
// with every field optional: a missing, null or mistyped field is left out.
func TestParseStatusLine(t *testing.T) {
	cases := []struct {
		name    string
		harness string
		payload string
		want    StatusLineValues
	}{
		{
			name:    "claude, every field",
			harness: "claude-code",
			payload: `{"session_id":"s1","model":{"id":"claude-opus-4-7","display_name":"Opus 4.7"},"context_window":{"used_percentage":41.6,"context_window_size":200000},"cost":{"total_cost_usd":1.2049,"total_duration_ms":5000}}`,
			want:    StatusLineValues{Model: "Opus 4.7", Context: "42%", Cost: "$1.20", Session: "s1"},
		},
		{
			name:    "claude, model id when there is no display name",
			harness: "claude",
			payload: `{"model":{"id":"claude-sonnet-4-6"}}`,
			want:    StatusLineValues{Model: "claude-sonnet-4-6"},
		},
		{
			name:    "claude, nothing the rail shows",
			harness: "claude-code",
			payload: `{"session_id":"s1","cwd":"/src","workspace":{"current_dir":"/src"}}`,
			want:    StatusLineValues{Session: "s1"},
		},
		{
			name:    "claude, mistyped and null fields",
			harness: "claude-code",
			payload: `{"model":"opus","context_window":{"used_percentage":null},"cost":{"total_cost_usd":"1.20"}}`,
			want:    StatusLineValues{},
		},
		{
			name:    "claude, context clamped",
			harness: "claude-code",
			payload: `{"context_window":{"used_percentage":130}}`,
			want:    StatusLineValues{Context: "100%"},
		},
		{
			name:    "claude, zero cost is a cost",
			harness: "claude-code",
			payload: `{"cost":{"total_cost_usd":0}}`,
			want:    StatusLineValues{Cost: "$0.00"},
		},
		{
			name:    "opencode plugin",
			harness: "opencode",
			payload: `{"session_id":"ses_1","modelID":"claude-sonnet-4-6","providerID":"anthropic","cost":0.5}`,
			want:    StatusLineValues{Model: "claude-sonnet-4-6", Cost: "$0.50", Session: "ses_1"},
		},
		{
			name:    "kilo shares the plugin",
			harness: "kilo",
			payload: `{"modelID":"m"}`,
			want:    StatusLineValues{Model: "m"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseStatusLine(tc.harness, []byte(tc.payload))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
	for _, bad := range []struct{ harness, payload string }{
		{"codex", `{}`},
		{"claude-code", `not json`},
		{"claude-code", `[1]`},
	} {
		if _, err := ParseStatusLine(bad.harness, []byte(bad.payload)); err == nil {
			t.Errorf("ParseStatusLine(%s, %s) took it", bad.harness, bad.payload)
		}
	}
}

// settingsDoc parses a settings file for comparison.
func settingsDoc(t *testing.T, data string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatalf("settings do not parse: %v\n%s", err, data)
	}
	return m
}

const userStatusLineSettings = `{
  "theme": "dark",
  "statusLine": {"type": "command", "command": "~/.claude/statusline.sh", "padding": 0}
}
`

// TestStatusLineChainRoundTrip chains to the person's own command, keeps the
// entry's other keys, and puts their command back on uninstall.
func TestStatusLineChainRoundTrip(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, "claude-code")
	path := tg.Path(env)
	writeFile(t, path, userStatusLineSettings)
	before := settingsDoc(t, userStatusLineSettings)

	if _, err := tg.InstallStatusLine(env, "tuios", "~/.claude/statusline.sh"); err != nil {
		t.Fatal(err)
	}
	doc := settingsDoc(t, readFile(t, path))
	sl := doc["statusLine"].(map[string]any)
	if sl["command"] != StatusLineCommand("tuios", "~/.claude/statusline.sh") || sl["padding"] != float64(0) {
		t.Errorf("statusLine = %v", sl)
	}
	st := tg.StatusLineState(env, "tuios")
	if !st.Installed || st.Then != "~/.claude/statusline.sh" {
		t.Errorf("state = %+v", st)
	}
	// Installing again without --then keeps the chain: it is the person's.
	if res, err := tg.InstallStatusLine(env, "tuios", ""); err != nil || res.Changed {
		t.Errorf("reinstall: %+v %v", res, err)
	}
	if _, err := tg.UninstallStatusLine(env); err != nil {
		t.Fatal(err)
	}
	if after := settingsDoc(t, readFile(t, path)); !reflect.DeepEqual(after, before) {
		t.Errorf("uninstall did not put the status line back:\n got %v\nwant %v", after, before)
	}
}
