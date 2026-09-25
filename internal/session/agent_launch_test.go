package session

import (
	"slices"
	"strings"
	"testing"
)

func TestSplitAgentWords(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
		bad  bool
	}{
		{in: "claude", want: []string{"claude"}},
		{in: "  codex   --model o5 ", want: []string{"codex", "--model", "o5"}},
		{in: `aider --message 'fix the "bug"'`, want: []string{"aider", "--message", `fix the "bug"`}},
		{in: `x "a \"b\" \\ c" d\ e`, want: []string{"x", `a "b" \ c`, "d e"}},
		{in: `x ''`, want: []string{"x", ""}},
		// Nothing is expanded: these are literal words.
		{in: `x $HOME $(id) ; rm`, want: []string{"x", "$HOME", "$(id)", ";", "rm"}},
		{in: `x 'open`, bad: true},
		{in: `x \`, bad: true},
	} {
		got, err := splitAgentWords(tc.in)
		if tc.bad {
			if err == nil {
				t.Errorf("splitAgentWords(%q) = %q, want an error", tc.in, got)
			}
			continue
		}
		if err != nil || strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") || len(got) != len(tc.want) {
			t.Errorf("splitAgentWords(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestCallerEnvRefusesWhatItMayNotSet(t *testing.T) {
	for _, env := range []map[string]string{
		{"TUIOS_PANE_ID": "x"},
		{"TUIOS_SOCKET": "/tmp/s"},
		{"TMUX": "/tmp/t"},
		{"1BAD": "x"},
		{"A=B": "x"},
		{"NUL": "a\x00b"},
	} {
		if _, _, verr := callerEnv(nil, env); verr == nil || verr.Code != ErrVerbInvalidParams {
			t.Errorf("callerEnv(%v) = %v, want invalid_params", env, verr)
		}
	}
	for _, cs := range []*connState{{viaLink: true}, {paneOnly: true}} {
		if _, _, verr := callerEnv(cs, map[string]string{"PATH": "/usr/bin"}); verr == nil || verr.Code != ErrVerbForbidden {
			t.Errorf("env from another machine (%+v) = %v, want forbidden", cs, verr)
		}
	}
	got, path, verr := callerEnv(nil, map[string]string{"PATH": "/opt/bin:/usr/bin", "ANTHROPIC_MODEL": "o"})
	if verr != nil || path != "/opt/bin:/usr/bin" || strings.Join(got, ",") != "ANTHROPIC_MODEL=o,PATH=/opt/bin:/usr/bin" {
		t.Errorf("callerEnv = %q, %q, %v", got, path, verr)
	}
}

// TestBuildEnvWithPutsTheCallersVariablesUnderTuios: the caller's value
// replaces the daemon's, and cannot change what tuios sets after it.
func TestBuildEnvWithPutsTheCallersVariablesUnderTuios(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "env")
	t.Setenv("FAN_DAEMON_ONLY", "daemon")
	env := sess.buildEnvWith("w1", false, []string{"FAN_DAEMON_ONLY=caller", "TERM=dumb"})
	if got := envValue(env, "FAN_DAEMON_ONLY"); got != "caller" {
		t.Errorf("FAN_DAEMON_ONLY = %q, want the caller's", got)
	}
	count := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "FAN_DAEMON_ONLY=") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("FAN_DAEMON_ONLY appears %d times", count)
	}
	// TERM is set after the caller's, so the last one, which exec keeps, is
	// tuios's.
	last := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, "TERM=") {
			last = kv
		}
	}
	if last == "TERM=dumb" {
		t.Error("the caller's TERM won over the one tuios sets")
	}
}

// envValue returns the last value env holds for key, which is the one exec
// keeps.
func envValue(env []string, key string) string {
	prefix := key + "="
	for _, kv := range slices.Backward(env) {
		if len(kv) > len(prefix) && kv[:len(prefix)] == prefix {
			return kv[len(prefix):]
		}
	}
	return ""
}
