package config

import (
	"reflect"
	"slices"
	"strings"
	"sync"
)

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

func isBindableAction(table, name string) bool {
	if strings.HasPrefix(name, CommandActionPrefix) || strings.HasPrefix(name, OpenMenuPrefix) || strings.HasPrefix(name, EnterModePrefix) {
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

var retiredActions = map[string]bool{
	"nav_up":                 true,
	"nav_down":               true,
	"nav_left":               true,
	"nav_right":              true,
	"extend_up":              true,
	"extend_down":            true,
	"extend_left":            true,
	"extend_right":           true,
	"debug_prefix_keyevents": true,
	"prefix_focus_sidebar":   true,
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
