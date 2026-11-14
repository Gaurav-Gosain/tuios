package config

import (
	"reflect"
	"slices"
	"strings"
	"sync"
)

// keybindTableNames are the keys the [keybindings] table takes: its own
// settings and the names of its tables. It is read from the toml tags of
// KeybindingsConfig, so a new table is known here as soon as it is added.
var keybindTableNames = sync.OnceValue(func() []string {
	t := reflect.TypeFor[KeybindingsConfig]()
	names := make([]string, 0, t.NumField())
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
		if name != "" && name != "-" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
})

// UnknownKeybindTables returns a warning for each key under [keybindings]
// that tuios does not read, such as [keybindings.terminal] for
// [keybindings.terminal_mode]. The decoder drops such a key without a word,
// so every binding in it is lost and the keys stay free (issue #1734). Each
// warning names the file and, when one is close, the table that was meant.
// lc may be nil.
func UnknownKeybindTables(lc *LayeredConfig) []string {
	if lc == nil || len(lc.Layers) == 0 {
		return nil
	}
	kb, ok := lc.Merged()["keybindings"].(map[string]any)
	if !ok {
		return nil
	}
	known := keybindTableNames()
	var unknown []string
	for name := range kb {
		if _, found := slices.BinarySearch(known, name); !found {
			unknown = append(unknown, name)
		}
	}
	slices.Sort(unknown)
	out := make([]string, 0, len(unknown))
	for _, name := range unknown {
		file := lc.DisplayPath(lc.Main)
		if holders := lc.Holders([]string{"keybindings", name}); len(holders) > 0 {
			file = lc.DisplayPath(holders[len(holders)-1].Path)
		}
		what, near := "[keybindings."+name+"] is not a keybinding table", nearestName(name, known)
		if _, isTable := kb[name].(map[string]any); !isTable {
			what = "keybindings." + name + " is not a keybinding setting"
		}
		line := file + ": " + what + ", so tuios ignores it."
		if near != "" {
			line += " Did you mean [keybindings." + near + "]?"
		}
		out = append(out, line)
	}
	return out
}

// keybindActionTables are the [keybindings] tables that map an action name
// to its keys, read from KeybindingsConfig like keybindTableNames.
var keybindActionTables = sync.OnceValue(func() []string {
	t := reflect.TypeFor[KeybindingsConfig]()
	var names []string
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
		if name == "" || name == "-" || f.Type != reflect.TypeFor[map[string][]string]() {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
})

// knownActionNames are the names a keybinding table may bind besides the
// [[keybindings.command]] and open_menu: families: every action of the
// action table, and the actions only a GUI client runs. Sorted, for
// nearestName.
var knownActionNames = sync.OnceValue(func() []string {
	names := make([]string, 0, len(actionIndex())+len(GUIActionDescriptions))
	for name := range actionIndex() {
		names = append(names, name)
	}
	for name := range GUIActionDescriptions {
		names = append(names, name)
	}
	slices.Sort(names)
	return slices.Compact(names)
})

// isBindableAction reports whether the keybinding table named table may
// bind name: an action, a [[keybindings.command]] or open_menu: name, or a
// name only that table knows, such as the rail's "exit" and jump_1 (see
// scopedDescriptions).
func isBindableAction(table, name string) bool {
	if strings.HasPrefix(name, CommandActionPrefix) || strings.HasPrefix(name, OpenMenuPrefix) {
		return true
	}
	if _, found := slices.BinarySearch(knownActionNames(), name); found {
		return true
	}
	if _, scoped := scopedDescriptions[table][name]; scoped {
		return true
	}
	for prefix := range numberedDescriptions[table] {
		if n, ok := strings.CutPrefix(name, prefix); ok && len(n) == 1 && n[0] >= '1' && n[0] <= '9' {
			return true
		}
	}
	return false
}

// tableActionNames are the names nearestAction may suggest for table: the
// actions, and the names only that table knows.
func tableActionNames(table string) []string {
	scoped := scopedDescriptions[table]
	if len(scoped) == 0 {
		return knownActionNames()
	}
	names := slices.Clone(knownActionNames())
	for name := range scoped {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// retiredActions are action names that were deleted after an older tuios wrote
// them into the config.toml it generated. A person's file still holds them, on
// lines the person did not write, so naming them as unknown would put a
// warning on the first start after an upgrade for something tuios did itself.
// The loader skips them without a word. A name that was never an action is
// still a typo and still warns. Add a name here only when a release wrote it
// into a generated config before the action was deleted.
var retiredActions = map[string]bool{
	"nav_up":                 true, // deleted in fc6678d09, no-ops on every path
	"nav_down":               true,
	"nav_left":               true,
	"nav_right":              true,
	"extend_up":              true, // selection mode, replaced by copy mode in c13a329ee
	"extend_down":            true,
	"extend_left":            true,
	"extend_right":           true,
	"debug_prefix_keyevents": true, // key events overlay, removed in d1749a97a
	"prefix_focus_sidebar":   true, // removed by 6e731a17d
}

// UnknownKeybindActions returns a warning for each action name in a
// keybinding table that no action has, such as copy_mode_wrod for
// copy_mode_word_forward. The registry skips such a name without a word, so
// its keys do nothing. Each warning names the file, the table and, when one
// is close, the action that was meant. lc may be nil.
func UnknownKeybindActions(lc *LayeredConfig) []string {
	if lc == nil || len(lc.Layers) == 0 {
		return nil
	}
	kb, ok := lc.Merged()["keybindings"].(map[string]any)
	if !ok {
		return nil
	}
	var out []string
	for _, table := range keybindActionTables() {
		binds, ok := kb[table].(map[string]any)
		if !ok {
			continue
		}
		var unknown []string
		for name := range binds {
			if !isBindableAction(table, name) && !retiredActions[name] {
				unknown = append(unknown, name)
			}
		}
		slices.Sort(unknown)
		for _, name := range unknown {
			file := lc.DisplayPath(lc.Main)
			if holders := lc.Holders([]string{"keybindings", table, name}); len(holders) > 0 {
				file = lc.DisplayPath(holders[len(holders)-1].Path)
			}
			line := file + ": [keybindings." + table + "] " + name + " is not an action, so its keys do nothing."
			if near := nearestAction(name, tableActionNames(table)); near != "" {
				line += " Did you mean " + near + "?"
			}
			out = append(out, line)
		}
	}
	return out
}

// nearestName is the name in names that name most likely meant: one that
// starts with it or that it starts with, or else one at most two edits away.
// It is "" when none is close.
func nearestName(name string, names []string) string {
	for _, n := range names {
		if strings.HasPrefix(n, name+"_") || strings.HasPrefix(name, n+"_") {
			return n
		}
	}
	best, bestDist := "", 3
	for _, n := range names {
		if d := editDistance(name, n); d < bestDist {
			best, bestDist = n, d
		}
	}
	return best
}

// nearestAction is the action in names that name most likely meant. Action
// names are long and share their beginnings, so a typo is measured against
// the start of each name as long as the typo, with two swapped letters as one
// edit: copy_mode_wrod is one edit from the copy_mode_word actions. Among the
// names that close, the one nearest in full wins, then the first in order.
// It is "" when none is within two edits.
func nearestAction(name string, names []string) string {
	best, bestHead, bestFull := "", 3, 0
	for _, n := range names {
		head := n[:min(len(n), len(name))]
		dh := swapDistance(name, head)
		if dh >= 3 {
			continue
		}
		df := swapDistance(name, n)
		if dh < bestHead || (dh == bestHead && df < bestFull) {
			best, bestHead, bestFull = n, dh, df
		}
	}
	return best
}

// swapDistance is the edit distance between a and b in which two adjacent
// letters swapped count as one edit (optimal string alignment), by byte.
func swapDistance(a, b string) int {
	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(a)][len(b)]
}

// editDistance is the Levenshtein distance between a and b, by byte. The
// names it compares are ASCII.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
