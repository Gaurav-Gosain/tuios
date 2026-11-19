package session

import (
	"strings"
	"testing"
)

// TestRigRefusal is the rule that keeps TUIOS_RIG off the person's own
// daemon: a rig needs a config and a state folder named on purpose and not
// the home's defaults, and a socket outside the person's socket folders.
func TestRigRefusal(t *testing.T) {
	const home = "/home/p"
	person := []string{"/run/user/1000/tuios", "/tmp/tuios-1000"}
	rigEnv := map[string]string{
		"XDG_CONFIG_HOME": "/tmp/rig/config",
		"XDG_STATE_HOME":  "/tmp/rig/state",
	}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range rigEnv {
			m[a] = b
		}
		m[k] = v
		return m
	}
	for _, c := range []struct {
		name   string
		socket string
		env    map[string]string
		want   string // a word of the refusal, or "" for a rig
	}{
		{"throwaway", "/tmp/rig/run/tuios/tuios.sock", rigEnv, ""},
		{"config unset", "/tmp/rig/run/tuios/tuios.sock", with("XDG_CONFIG_HOME", ""), "XDG_CONFIG_HOME"},
		{"config is the home's", "/tmp/rig/run/tuios/tuios.sock", with("XDG_CONFIG_HOME", "/home/p/.config/"), "XDG_CONFIG_HOME"},
		{"config relative", "/tmp/rig/run/tuios/tuios.sock", with("XDG_CONFIG_HOME", "rig/config"), "XDG_CONFIG_HOME"},
		{"state unset", "/tmp/rig/run/tuios/tuios.sock", with("XDG_STATE_HOME", ""), "XDG_STATE_HOME"},
		{"state is the home's", "/tmp/rig/run/tuios/tuios.sock", with("XDG_STATE_HOME", "/home/p/.local/state"), "XDG_STATE_HOME"},
		{"the person's socket", "/run/user/1000/tuios/tuios.sock", rigEnv, "socket"},
		{"a named server of the person", "/run/user/1000/tuios/servers/x/tuios.sock", rigEnv, "socket"},
		{"the person's legacy socket", "/tmp/tuios-1000/tuios.sock", rigEnv, "socket"},
		{"a folder that only starts like the person's", "/tmp/tuios-10000/tuios.sock", rigEnv, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := rigRefusal(c.socket, func(k string) string { return c.env[k] }, home, person)
			if c.want == "" && got != "" {
				t.Fatalf("refused a throwaway rig: %s", got)
			}
			if c.want != "" && !strings.Contains(got, c.want) {
				t.Fatalf("refusal = %q, want one that names %s", got, c.want)
			}
		})
	}
}
