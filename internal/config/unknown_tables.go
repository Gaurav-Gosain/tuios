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
