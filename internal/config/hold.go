package config

import (
	"maps"
	"slices"
	"strings"
)

type optionHolds struct {
	groups map[string][]heldKey
	force  map[string][]string
}

type heldKey struct {
	path  []string
	value string
	known bool
}

// HoldOption runs change, which sets the option at path, and holds every key
// the change touched, and path itself, out of each later save. The value
// stays held until ReleaseOption for the same path.
//
// The keys are found by comparing the config before and after the change,
// because a setter may move a key other than the one it is named for: the
// legacy animations switch sets appearance.motion. A nil config only runs
// change.
func (cfg *UserConfig) HoldOption(path string, change func() error) error {
	if cfg == nil {
		return change()
	}
	before, berr := MarshalUserConfig(cfg)
	if err := change(); err != nil {
		return err
	}
	keys := [][]string{strings.Split(path, ".")}
	if after, err := MarshalUserConfig(cfg); berr == nil && err == nil {
		from, ferr := parseLayer(before)
		to, terr := parseLayer(after)
		if ferr == nil && terr == nil {
			var changes []configChange
			diffTables(from, to, nil, &changes)
			for _, ch := range changes {
				if !slices.ContainsFunc(keys, func(k []string) bool { return slices.Equal(k, ch.path) }) {
					keys = append(keys, ch.path)
				}
			}
		}
	}
	if cfg.holds == nil {
		cfg.holds = &optionHolds{}
	}
	if cfg.holds.groups == nil {
		cfg.holds.groups = map[string][]heldKey{}
	}
	group := make([]heldKey, 0, len(keys))
	for _, k := range keys {
		hk := heldKey{path: k}
		dotted := strings.Join(k, ".")
		if _, ok := LookupOption(dotted); ok {
			hk.value, hk.known = GetOptionValue(cfg, dotted)
		}
		group = append(group, hk)
		delete(cfg.holds.force, dotted)
	}
	cfg.holds.groups[path] = group
	return nil
}

// ReleaseOption ends the hold on the option at path, ahead of a set that
// saves it. The next save writes each key the hold covered, unless another
// held option still covers it. It does nothing for an option not held.
func (cfg *UserConfig) ReleaseOption(path string) {
	if cfg == nil || cfg.holds == nil {
		return
	}
	group, ok := cfg.holds.groups[path]
	if !ok {
		return
	}
	delete(cfg.holds.groups, path)
	for _, k := range group {
		if cfg.isHeld(k.path) {
			continue
		}
		if cfg.holds.force == nil {
			cfg.holds.force = map[string][]string{}
		}
		cfg.holds.force[strings.Join(k.path, ".")] = k.path
	}
}

// KeepHolds carries the holds of from onto cfg, a config just read from the
// files, and sets each held value on it again. A hold lasts until the client
// exits, and an edit to a config file is not that.
func (cfg *UserConfig) KeepHolds(from *UserConfig) {
	if cfg == nil || from == nil || from.holds == nil || cfg == from {
		return
	}
	cfg.holds = from.holds
	for _, group := range cfg.holds.groups {
		for _, k := range group {
			if k.known {
				_ = SetOptionValue(cfg, strings.Join(k.path, "."), k.value)
			}
		}
	}
}

func (cfg *UserConfig) isHeld(path []string) bool {
	if cfg.holds == nil {
		return false
	}
	for _, group := range cfg.holds.groups {
		for _, k := range group {
			if slices.Equal(k.path, path) {
				return true
			}
		}
	}
	return false
}

func (cfg *UserConfig) heldPaths() [][]string {
	if cfg.holds == nil {
		return nil
	}
	var out [][]string
	for _, group := range cfg.holds.groups {
		for _, k := range group {
			out = append(out, k.path)
		}
	}
	return out
}

func (cfg *UserConfig) takeForced() [][]string {
	if cfg.holds == nil || len(cfg.holds.force) == 0 {
		return nil
	}
	out := slices.Collect(maps.Values(cfg.holds.force))
	cfg.holds.force = nil
	return out
}

func (cfg *UserConfig) restoreForced(keys [][]string) {
	if len(keys) == 0 {
		return
	}
	if cfg.holds == nil {
		cfg.holds = &optionHolds{}
	}
	if cfg.holds.force == nil {
		cfg.holds.force = map[string][]string{}
	}
	for _, k := range keys {
		if !cfg.isHeld(k) {
			cfg.holds.force[strings.Join(k, ".")] = k
		}
	}
}

func skipHeld(changes []configChange, held [][]string) []configChange {
	if len(held) == 0 {
		return changes
	}
	out := changes[:0]
	for _, ch := range changes {
		drop := false
		for _, h := range held {
			switch {
			case hasPrefix(ch.path, h):
				drop = true
			case hasPrefix(h, ch.path) && !ch.deleted:
				t, ok := ch.value.(map[string]any)
				if !ok {
					continue
				}
				t = deepCopy(t).(map[string]any)
				removePath(t, h[len(ch.path):])
				if len(t) == 0 {
					drop = true
				}
				ch.value = t
			}
			if drop {
				break
			}
		}
		if !drop {
			out = append(out, ch)
		}
	}
	return out
}

func addForced(changes []configChange, want map[string]any, forced [][]string) []configChange {
	for _, k := range forced {
		if slices.ContainsFunc(changes, func(ch configChange) bool { return hasPrefix(k, ch.path) }) {
			continue
		}
		if v, ok := lookupPath(want, k); ok {
			changes = append(changes, configChange{path: k, value: v})
		} else {
			changes = append(changes, configChange{path: k, deleted: true})
		}
	}
	return changes
}

func hasPrefix(p, prefix []string) bool {
	return len(p) >= len(prefix) && slices.Equal(p[:len(prefix)], prefix)
}

func removePath(t map[string]any, path []string) {
	if len(path) == 0 {
		return
	}
	if len(path) == 1 {
		delete(t, path[0])
		return
	}
	sub, ok := t[path[0]].(map[string]any)
	if !ok {
		return
	}
	removePath(sub, path[1:])
	if len(sub) == 0 {
		delete(t, path[0])
	}
}
