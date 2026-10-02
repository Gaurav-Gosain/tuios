package config

import "testing"

// TestStartupDefaultFollowsTheConfigFile: list-options must show the value that
// applies. A file without [startup] reads tiled and daemon as false.
func TestStartupDefaultFollowsTheConfigFile(t *testing.T) {
	tiled, _ := LookupOption("startup.tiled")
	daemon, _ := LookupOption("startup.daemon")
	cases := []struct {
		name, src, wantTiled, wantDaemon string
	}{
		{"no startup table", "[appearance]\nborder_style = \"rounded\"\n", "false", "false"},
		{"startup table sets both", "[startup]\ntiled = true\ndaemon = true\n", "true", "true"},
		{"startup table sets one", "[startup]\ntiled = true\n", "true", "false"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := effectiveStartupDefault(tiled, []byte(c.src)); got != c.wantTiled {
				t.Errorf("startup.tiled = %s, want %s", got, c.wantTiled)
			}
			if got := effectiveStartupDefault(daemon, []byte(c.src)); got != c.wantDaemon {
				t.Errorf("startup.daemon = %s, want %s", got, c.wantDaemon)
			}
		})
	}
	other, _ := LookupOption("startup.layout")
	if got := EffectiveDefault(other); got != other.Default {
		t.Errorf("startup.layout = %s, want %s", got, other.Default)
	}
}
