package config

import (
	"fmt"
	"strings"
)

// LeaderRow is the leader as one row of the keybind manager's list, so the
// leader can be changed where every other key is. Its section is the
// [keybindings] table and its action the leader_key setting.
func LeaderRow(leader string) Binding {
	return Binding{
		Scope:   ScopeGlobal,
		Section: "keybindings",
		Action:  LeaderAction,
		Desc:    "Leader key: press it before a prefix key",
		Key:     leader,
		Press:   leader,
	}
}

// LeaderProblem says why key cannot be the leader of cfg, or "" when it can.
// The keybind manager asks it before it writes keybindings.leader_key.
//
// The checks are the ones config validation makes on the leader and on
// nest_exit_key, and one more: no action in global, window or terminal mode
// may hold the key. Such an action would lose its key to the leader, and the
// manager refuses a change that takes a key away without saying so.
func LeaderProblem(cfg *UserConfig, key string) string {
	key = strings.TrimSpace(key)
	normalizer := &KeyNormalizer{isMacOS: macOSHost}
	if ok, msg := normalizer.ValidateKey(key); !ok {
		return fmt.Sprintf("%s cannot be the leader: %s", key, msg)
	}
	if KeyTypes(key) {
		return fmt.Sprintf("%s is a key programs type, so it cannot be the leader. Use a key with ctrl or alt", key)
	}
	if cfg == nil {
		return ""
	}
	if exit := cfg.Keybindings.NestExitKey; exit != "" && IsKeyPress(CanonicalKey(key), exit) {
		return fmt.Sprintf("%s ends nest mode, so it cannot be the leader. Use another key", key)
	}
	next := *cfg
	next.Keybindings.LeaderKey = key
	if clashes := NewKeybindRegistry(&next).LeaderClashes(); len(clashes) > 0 {
		c := clashes[0]
		return fmt.Sprintf("%s runs %s in %s. Free the key with ctrl+x first, then set the leader", key, c.Action, strings.ToLower(c.ScopeName))
	}
	return ""
}
