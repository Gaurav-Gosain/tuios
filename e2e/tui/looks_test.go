package tuie2e

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

type lookPin struct {
	table, key, value string
	legacy            string
}

var preV080Pins = []lookPin{
	{table: "appearance", key: "dockbar_position", value: `"bottom"`},
	{table: "appearance", key: "window_title_position", value: `"bottom"`},
	{table: "appearance", key: "zoom_size", value: `100`},
	{table: "appearance", key: "click_to_type", value: `"single"`},
	{table: "appearance.scrollbar", key: "style", value: `"thin"`},
	{table: "appearance.sidebar", key: "enabled", value: `false`, legacy: "sidebar_enabled"},
	{table: "appearance.sidebar", key: "position", value: `"left"`, legacy: "sidebar_position"},
	{table: "appearance.sidebar", key: "width", value: `28`, legacy: "sidebar_width"},
	{table: "appearance", key: "modal_dim", value: `0`},
}

var shippedLooksBases sync.Map

func useShippedLooks(base string) { shippedLooksBases.Store(base, true) }

var (
	tomlHeaderRe = regexp.MustCompile(`^\s*\[\s*([A-Za-z0-9_.\-]+)\s*\]\s*(#.*)?$`)
	tomlArrayRe  = regexp.MustCompile(`^\s*\[\[`)
	tomlKeyRe    = regexp.MustCompile(`^\s*([A-Za-z0-9_.\-]+)\s*=`)
)

func pinPreV080Looks(t *testing.T, base string) {
	t.Helper()
	pinPreV080LooksIn(t, base, xdgDir(base, "XDG_CONFIG_HOME"))
}

var pinnedConfigs sync.Map

const tuiosConfigHeader = "# TUIOS Configuration File\n"

func writtenByTuios(data []byte) bool {
	s := string(data)
	return strings.HasPrefix(s, tuiosConfigHeader) || strings.HasPrefix(tuiosConfigHeader, s)
}

func pinPreV080LooksIn(t *testing.T, base, configHome string) {
	t.Helper()
	if _, ok := shippedLooksBases.Load(base); ok {
		return
	}
	path := filepath.Join(configHome, "tuios", "config.toml")
	data, err := os.ReadFile(path)
	if _, seen := pinnedConfigs.Load(path); seen && err == nil && writtenByTuios(data) {
		return
	}
	replace := false
	if errors.Is(err, fs.ErrNotExist) {
		data, err = firstRunConfig(t, base, configHome, path)
		replace = true
	}
	if err != nil {
		t.Fatalf("pin looks: read %s: %v", path, err)
	}
	out := withPreV080Pins(string(data), replace)
	if out != string(data) {
		writeConfigAtomically(t, path, []byte(out))
	}
	pinnedConfigs.Store(path, true)
}

func writeConfigAtomically(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("pin looks: mkdir: %v", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.toml.pin.*")
	if err != nil {
		t.Fatalf("pin looks: write %s: %v", path, err)
	}
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		t.Fatalf("pin looks: write %s: %v", path, werr)
	}
}

func firstRunConfig(t *testing.T, base, configHome, path string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command(tuiosBin, "keybinds", "list")
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Env = append(cmd.Env, "XDG_CONFIG_HOME="+configHome)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pin looks: first-run config: %v\n%s", err, out)
	}
	return os.ReadFile(path)
}

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
	for _, ins := range slices.Backward(inserts) {
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
			src:       "[appearance]\nmodal_dim = 0\ndockbar_position = \"top\"\nwindow_title_position = \"top\"\nzoom_size = 95\nclick_to_type = \"double\"\n[appearance.scrollbar]\nstyle = \"track\"\n[appearance.sidebar]\nenabled = true\nposition = \"right\"\nwidth = 24\n",
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
