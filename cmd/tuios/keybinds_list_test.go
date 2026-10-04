package main

import (
	"encoding/json"
	"io"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestKeybindsListCoversEveryActionAndDefault is a consistency check on
// `tuios keybinds list`. The list used to print a hand-picked set of
// actions ("the common keybindings"), so every action added later was
// missing from it: the rail, the Inbox, the prefix sub-menus, the master-stack
// keys and the agent keys among them. This fails when an action the registry
// describes, or a key the defaults bind, is not in the list, on Linux and on
// macOS, in the text and in --json.
//
// The expectation is read from the default config tables and
// config.ActionDescriptions, not from keybindRows, so a scope or a section
// that the rows leave out shows up here.
func TestKeybindsListCoversEveryActionAndDefault(t *testing.T) {
	for _, mac := range []bool{false, true} {
		name := "linux"
		if mac {
			name = "macos"
		}
		t.Run(name, func(t *testing.T) {
			restore := config.ForceMacOSHost(mac)
			defer restore()
			cfg := config.DefaultConfig()
			leader := cfg.Keybindings.LeaderKey
			if leader == "" {
				leader = config.DefaultLeaderKey
			}

			text := captureLargeStdout(t, func() { printKeybindingsTable(keybindRows(cfg), leader) })
			textCells := parseKeybindTables(t, text)

			var rows []keybindRow
			out := captureLargeStdout(t, func() {
				enc := json.NewEncoder(os.Stdout)
				_ = enc.Encode(keybindRows(cfg))
			})
			if err := json.Unmarshal([]byte(out), &rows); err != nil {
				t.Fatalf("decode --json rows: %v", err)
			}
			jsonCells := map[[2]string][]string{}
			for _, r := range rows {
				k := [2]string{r.ScopeName, r.Action}
				jsonCells[k] = append(jsonCells[k], strings.Join(r.Keys, ", "))
			}

			// Every described action has a row.
			for action := range config.ActionDescriptions {
				if !hasAction(textCells, action) {
					t.Errorf("text list has no row for action %q", action)
				}
				if !hasAction(jsonCells, action) {
					t.Errorf("--json list has no row for action %q", action)
				}
			}

			// Every default key of every section is on its action's row, in
			// the scope the section belongs to, with the scope's chord.
			scopeOf := map[string]config.Scope{}
			for _, s := range config.Scopes(leader) {
				for _, sec := range s.Sections {
					scopeOf[sec] = s
				}
			}
			for _, sec := range keybindingSectionTags(t) {
				scope, ok := scopeOf[sec]
				if !ok {
					t.Errorf("section [keybindings.%s] belongs to no scope, so no listing can show it", sec)
					continue
				}
				for action, keys := range cfg.Keybindings.SectionFor(sec) {
					for _, key := range keys {
						if strings.TrimSpace(key) == "" {
							continue
						}
						press := key
						if scope.Chord != "" {
							press = scope.Chord + " " + key
						}
						k := [2]string{scope.Name, action}
						if !cellsHold(textCells[k], press) {
							t.Errorf("text list: %s %s lacks default key %q (cells %q)", scope.Name, action, press, textCells[k])
						}
						if !cellsHold(jsonCells[k], press) {
							t.Errorf("--json list: %s %s lacks default key %q (cells %q)", scope.Name, action, press, jsonCells[k])
						}
					}
				}
			}
		})
	}
}

// keybindingSectionTags is the toml name of every key table in
// KeybindingsConfig, read from the struct so a new table is covered.
func keybindingSectionTags(t *testing.T) []string {
	t.Helper()
	var out []string
	typ := reflect.TypeFor[config.KeybindingsConfig]()
	for f := range typ.Fields() {
		if f.Type != reflect.TypeFor[map[string][]string]() {
			continue
		}
		tag, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
		out = append(out, tag)
	}
	if len(out) < 20 {
		t.Fatalf("found %d key tables in KeybindingsConfig, want at least 20", len(out))
	}
	return out
}

func hasAction(cells map[[2]string][]string, action string) bool {
	for k := range cells {
		if k[1] == action {
			return true
		}
	}
	return false
}

// cellsHold reports whether one of the Keys cells lists press. A cell joins
// keys with ", ", and a key can itself be ",", so the match is on the
// separators around it.
func cellsHold(cells []string, press string) bool {
	for _, c := range cells {
		if strings.Contains(", "+c+", ", ", "+press+", ") {
			return true
		}
	}
	return false
}

var (
	ansiCSI    = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	titleChord = regexp.MustCompile(` \(.*\)$`)
)

// parseKeybindTables reads the text list into its Keys cells, by table title
// and action.
func parseKeybindTables(t *testing.T, text string) map[[2]string][]string {
	t.Helper()
	cells := map[[2]string][]string{}
	title := ""
	for line := range strings.SplitSeq(ansiCSI.ReplaceAllString(text, ""), "\n") {
		line = strings.TrimRight(line, " ")
		switch {
		case line == "":
		case strings.HasPrefix(line, "│"):
			f := strings.Split(line, "│")
			if len(f) < 4 {
				continue
			}
			keys, action := strings.TrimSpace(f[1]), strings.TrimSpace(f[2])
			if keys == "Keys" {
				continue
			}
			k := [2]string{title, action}
			cells[k] = append(cells[k], keys)
		case strings.HasPrefix(line, "╭"), strings.HasPrefix(line, "├"), strings.HasPrefix(line, "╰"):
		default:
			title = titleChord.ReplaceAllString(line, "")
		}
	}
	if len(cells) == 0 {
		t.Fatalf("the text list has no table rows:\n%s", text)
	}
	return cells
}

// captureLargeStdout is captureStdout for output larger than a pipe holds: it
// reads while fn writes.
func captureLargeStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	done := make(chan []byte)
	go func() {
		data, _ := io.ReadAll(r)
		done <- data
	}()
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	fn()
	_ = w.Close()
	return string(<-done)
}
