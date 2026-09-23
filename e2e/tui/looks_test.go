package tuie2e

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The suite runs with the looks tuios shipped before v0.8.0, the way it runs
// standalone (see startIn): almost every test here was written against that
// screen. The dock row is the last line, the rail is off or on the left, a
// click on a pane starts typing in it, a zoomed pane fills the screen. Left to
// the v0.8.0 defaults, hundreds of assertions would quietly change what they
// check. startOpts.shippedLooks opts out, and shipped_looks_test.go is where
// the v0.8.0 defaults are driven for real.

// lookPin is one key the suite pins, with the value it pins it to, as TOML.
type lookPin struct {
	table, key, value string
	// legacy is a flat [appearance] key an older config spelled this one as.
	// A config that sets it has made its choice, so the pin stays out.
	legacy string
}

// preV080Pins are the seven defaults v0.8.0 changed, at their old values. It
// mirrors config.PinPreV080Appearance, which this module does not import.
var preV080Pins = []lookPin{
	{table: "appearance", key: "dockbar_position", value: `"bottom"`},
	{table: "appearance", key: "window_title_position", value: `"bottom"`},
	{table: "appearance", key: "zoom_size", value: `100`},
	{table: "appearance", key: "click_to_type", value: `"single"`},
	{table: "appearance.scrollbar", key: "style", value: `"thin"`},
	{table: "appearance.sidebar", key: "enabled", value: `false`, legacy: "sidebar_enabled"},
	{table: "appearance.sidebar", key: "position", value: `"left"`, legacy: "sidebar_position"},
	{table: "appearance.sidebar", key: "width", value: `28`, legacy: "sidebar_width"},
}

// shippedLooksBases holds the isolation roots whose tests asked for the
// shipped looks, so no tuios started against them gets the pins, whichever
// helper starts it.
var shippedLooksBases sync.Map

// useShippedLooks keeps the pins out of every tuios started against base.
func useShippedLooks(base string) { shippedLooksBases.Store(base, true) }

var (
	tomlHeaderRe = regexp.MustCompile(`^\s*\[\s*([A-Za-z0-9_.\-]+)\s*\]\s*(#.*)?$`)
	tomlArrayRe  = regexp.MustCompile(`^\s*\[\[`)
	tomlKeyRe    = regexp.MustCompile(`^\s*([A-Za-z0-9_.\-]+)\s*=`)
)

// pinPreV080Looks makes the config under base carry every pin the test's own
// config does not already set. A test that writes a key keeps its value; a
// test that writes none gets the whole set. It reads the file line by line
// rather than decoding it, which is enough for the plain tables tests write,
// and it inserts each pin right under its table's header so the file stays
// valid TOML.
func pinPreV080Looks(t *testing.T, base string) {
	t.Helper()
	pinPreV080LooksIn(t, base, xdgDir(base, "XDG_CONFIG_HOME"))
}

// pinPreV080LooksIn is pinPreV080Looks for a config home other than the
// root's own, which is what a second client started with its own
// XDG_CONFIG_HOME reads.
func pinPreV080LooksIn(t *testing.T, base, configHome string) {
	t.Helper()
	if _, ok := shippedLooksBases.Load(base); ok {
		return
	}
	path := filepath.Join(configHome, "tuios", "config.toml")
	data, err := os.ReadFile(path)
	replace := false
	if errors.Is(err, fs.ErrNotExist) {
		// No file is a first run, and a first run writes the whole default
		// config: startup.tiled and startup.daemon are on only because that
		// file says so, and a file of pins alone would turn them off. So the
		// binary writes its own first-run file, and the pins replace the
		// looks in it.
		data, err = firstRunConfig(t, base, configHome, path)
		replace = true
	}
	if err != nil {
		t.Fatalf("pin looks: read %s: %v", path, err)
	}
	out := withPreV080Pins(string(data), replace)
	if out == string(data) {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("pin looks: mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatalf("pin looks: write %s: %v", path, err)
	}
}

// firstRunConfig has the binary under test write the config file a first run
// writes, by running a subcommand that loads the config, and returns it.
func firstRunConfig(t *testing.T, base, configHome, path string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command(tuiosBin, "keybinds", "list")
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	// Last, so it wins over the root's own: exec keeps the last value of a
	// repeated key.
	cmd.Env = append(cmd.Env, "XDG_CONFIG_HOME="+configHome)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pin looks: first-run config: %v\n%s", err, out)
	}
	return os.ReadFile(path)
}

// withPreV080Pins is src with the missing pins added. With replace, the pinned
// keys src already sets are dropped first, so every pin lands; that is for a
// first-run file, whose values are the defaults rather than a test's choice.
func withPreV080Pins(src string, replace bool) string {
	lines := strings.Split(src, "\n")
	if replace {
		pinned := map[string]bool{}
		for _, p := range preV080Pins {
			pinned[p.table+"."+p.key] = true
		}
		kept := lines[:0:0]
		table := ""
		for _, ln := range lines {
			if tomlArrayRe.MatchString(ln) {
				table = "\x00array"
			} else if m := tomlHeaderRe.FindStringSubmatch(ln); m != nil {
				table = m[1]
			} else if m := tomlKeyRe.FindStringSubmatch(ln); m != nil && pinned[table+"."+m[1]] {
				continue
			}
			kept = append(kept, ln)
		}
		lines = kept
	}
	set := map[string]bool{}
	// implicit holds the tables a dotted key defined, which no header may
	// open afterwards.
	implicit := map[string]bool{}
	headerAt := map[string]int{}
	table := ""
	for i, ln := range lines {
		if tomlArrayRe.MatchString(ln) {
			table = "\x00array"
			continue
		}
		if m := tomlHeaderRe.FindStringSubmatch(ln); m != nil {
			table = m[1]
			if _, ok := headerAt[table]; !ok {
				headerAt[table] = i
			}
			continue
		}
		if m := tomlKeyRe.FindStringSubmatch(ln); m != nil {
			full := m[1]
			if table != "" {
				full = table + "." + m[1]
			}
			set[full] = true
			for i := len(full) - 1; i > len(table); i-- {
				if full[i] == '.' {
					implicit[full[:i]] = true
				}
			}
		}
	}

	// A table written as an inline table or as dotted keys cannot be given
	// a header afterwards, so its pins are left out rather than breaking the
	// file.
	inline := func(tbl string) bool { return set[tbl] || implicit[tbl] }

	missing := map[string][]string{}
	var order []string
	for _, p := range preV080Pins {
		if set[p.table+"."+p.key] || (p.legacy != "" && set["appearance."+p.legacy]) || inline(p.table) {
			continue
		}
		if _, ok := missing[p.table]; !ok {
			order = append(order, p.table)
		}
		missing[p.table] = append(missing[p.table], p.key+" = "+p.value)
	}
	if len(order) == 0 {
		return src
	}

	// Insert under existing headers from the bottom up, so earlier indexes
	// stay good, then append the tables the file never opened.
	var appended []string
	type insertion struct {
		at   int
		rows []string
	}
	var inserts []insertion
	for _, tbl := range order {
		if at, ok := headerAt[tbl]; ok {
			inserts = append(inserts, insertion{at: at, rows: missing[tbl]})
			continue
		}
		appended = append(appended, "", "["+tbl+"]")
		appended = append(appended, missing[tbl]...)
	}
	sort.Slice(inserts, func(i, j int) bool { return inserts[i].at < inserts[j].at })
	for i := len(inserts) - 1; i >= 0; i-- {
		ins := inserts[i]
		rest := append([]string{}, lines[ins.at+1:]...)
		lines = append(append(lines[:ins.at+1], ins.rows...), rest...)
	}
	out := strings.Join(lines, "\n")
	if len(appended) > 0 {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += strings.Join(appended, "\n") + "\n"
	}
	return out
}

// TestPinsKeepWhatTheTestWrote checks the pin merge on the shapes of config
// the suite writes: nothing, tables that the pins share, and keys that the
// test set itself, which the pins must leave alone.
func TestPinsKeepWhatTheTestWrote(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		replace   bool
		want      []string
		wantNot   []string
		unchanged bool
	}{
		{
			name:    "a first-run file, replaced",
			src:     "# dockbar_position: bottom, top, hidden\n[appearance]\nclick_to_type = 'double'\ndockbar_position = 'top'\nzoom_size = 95\n\n[appearance.scrollbar]\nstyle = 'track'\n\n[appearance.sidebar]\nenabled = true\nposition = 'right'\nwidth = 24\n\n[startup]\ntiled = true\ndaemon = true\n",
			replace: true,
			want: []string{
				"# dockbar_position: bottom, top, hidden",
				"dockbar_position = \"bottom\"", "window_title_position = \"bottom\"", "zoom_size = 100", "click_to_type = \"single\"",
				"style = \"thin\"", "enabled = false", "position = \"left\"", "width = 28",
				"[startup]\ntiled = true\ndaemon = true",
			},
			wantNot: []string{"'top'", "'double'", "= 95", "'track'", "enabled = true", "'right'", "= 24"},
		},
		{
			name: "no file",
			src:  "",
			want: []string{"[appearance]\ndockbar_position = \"bottom\"", "[appearance.sidebar]\nenabled = false", "[appearance.scrollbar]\nstyle = \"thin\""},
		},
		{
			name:    "the rail turned on by the test",
			src:     "[appearance.sidebar]\nenabled = true\nwidth = 40\n",
			want:    []string{"[appearance.sidebar]\nposition = \"left\"\nenabled = true\nwidth = 40", "click_to_type = \"single\""},
			wantNot: []string{"enabled = false", "width = 28"},
		},
		{
			name:    "a dotted key inside the appearance table",
			src:     "[appearance]\nsidebar.enabled = true\n",
			wantNot: []string{"[appearance.sidebar]"},
			want:    []string{"dockbar_position = \"bottom\""},
		},
		{
			name:    "the legacy flat key",
			src:     "[appearance]\nsidebar_enabled = true\n",
			want:    []string{"position = \"left\""},
			wantNot: []string{"enabled = false"},
		},
		{
			name:      "everything already set",
			src:       "[appearance]\ndockbar_position = \"top\"\nwindow_title_position = \"top\"\nzoom_size = 95\nclick_to_type = \"double\"\n[appearance.scrollbar]\nstyle = \"track\"\n[appearance.sidebar]\nenabled = true\nposition = \"right\"\nwidth = 24\n",
			unchanged: true,
		},
		{
			name: "other tables and an array of tables",
			src:  "[startup]\ntiled = true\n\n[[hooks.after-new-window]]\ncommand = \"true\"\n",
			want: []string{"[startup]\ntiled = true", "[appearance]\ndockbar_position = \"bottom\""},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := withPreV080Pins(c.src, c.replace)
			if c.unchanged && got != c.src {
				t.Fatalf("a config that sets every key was rewritten:\n%s", got)
			}
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("merged config lacks %q:\n%s", w, got)
				}
			}
			for _, w := range c.wantNot {
				if strings.Contains(got, w) {
					t.Errorf("merged config carries %q:\n%s", w, got)
				}
			}
		})
	}
}
