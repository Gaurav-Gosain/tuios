package config

import (
	"fmt"
	"slices"
	"strings"
)

// What a process in a pane may do through tuios.
//
//	[agents.permissions]
//	mode = "strict"                    # open (the default) or strict
//	grants = ["read", "write", "fan"]  # what a pane holds under strict
//
// Every pane holds a set of grants. A pane started with grants of its own
// (tuios start-agent --grants, fan --grants, new-window --grants, or
// set-pane-grants afterwards) holds those. A pane started with none holds the
// default, which is the mode's: admin under open, which is everything a pane
// could do before grants existed, and the grants list under strict.
//
// The daemon reads the table at start and again when the file changes. A
// change reaches every pane that holds the default at its next call. The
// grant names and what each allows are in docs/AGENT_STATE.md.

// Pane grants, the values of grants.
const (
	// PaneGrantRead reads the pane's own session and the sessions of its fan
	// group: listings, captures, agent state, waits and the event stream.
	PaneGrantRead = "read"
	// PaneGrantWrite types into the panes of its own session and leaves mail
	// and stashed files there.
	PaneGrantWrite = "write"
	// PaneGrantFan does what write does in the sessions of its fan group and
	// the sessions it launched, and starts agents with fan and start-agent.
	PaneGrantFan = "fan"
	// PaneGrantRespond answers an on-screen prompt of a pane it may write to,
	// with respond, without the person's attach nonce. No mode gives it by
	// default, and admin does not imply it.
	PaneGrantRespond = "respond"
	// PaneGrantAdmin is everything else a pane could do before grants
	// existed: every session, the listings across sessions, windows,
	// layouts, options, attach and the rest. It implies read, write and fan.
	PaneGrantAdmin = "admin"
)

// PaneGrantNames is every grant, in the order they are documented.
var PaneGrantNames = []string{PaneGrantRead, PaneGrantWrite, PaneGrantFan, PaneGrantRespond, PaneGrantAdmin}

// Permission modes, the values of mode.
const (
	// PaneModeOpen gives a pane started with no grants of its own admin.
	PaneModeOpen = "open"
	// PaneModeStrict gives a pane started with no grants of its own the
	// grants list.
	PaneModeStrict = "strict"
)

// PaneModes is every mode.
var PaneModes = []string{PaneModeOpen, PaneModeStrict}

// DefaultStrictGrants is what a pane holds under strict when grants is not
// set: it reads its session and fan group, types into its own session, and
// starts and drives agents in its fan group.
var DefaultStrictGrants = []string{PaneGrantRead, PaneGrantWrite, PaneGrantFan}

// PermissionsConfig is the [agents.permissions] table.
type PermissionsConfig struct {
	// Mode is open or strict. Empty is open. Any other value is read as
	// strict, so a typo never turns the protection off.
	Mode string `toml:"mode,omitempty"`
	// Grants is what a pane holds under strict when it was started with no
	// grants of its own. Nil is DefaultStrictGrants; an empty list is no
	// grants at all. Names outside PaneGrantNames are dropped.
	Grants []string `toml:"grants,omitempty"`
}

// ResolvedPermissions is the table as the daemon uses it.
type ResolvedPermissions struct {
	// Strict is true under strict mode.
	Strict bool
	// Grants is the default for a pane started with no grants of its own
	// under strict, valid names only, in documented order.
	Grants []string
}

// Resolve reads the table: the mode, with anything but open or empty read as
// strict, and the grants with unknown names dropped.
func (c PermissionsConfig) Resolve() ResolvedPermissions {
	mode := strings.ToLower(strings.TrimSpace(c.Mode))
	r := ResolvedPermissions{Strict: mode != "" && mode != PaneModeOpen}
	src := c.Grants
	if src == nil {
		src = DefaultStrictGrants
	}
	r.Grants, _ = CanonicalPaneGrants(src)
	return r
}

// CanonicalPaneGrants returns the valid names in names, lower-cased, without
// repeats and in documented order, and the names it dropped.
func CanonicalPaneGrants(names []string) (valid, unknown []string) {
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if slices.Contains(PaneGrantNames, n) {
			seen[n] = true
		} else if n != "" {
			unknown = append(unknown, n)
		}
	}
	valid = []string{}
	for _, n := range PaneGrantNames {
		if seen[n] {
			valid = append(valid, n)
		}
	}
	return valid, unknown
}

// validatePanePermissions warns about a mode or grant name the daemon does
// not know. An unknown mode is read as strict and an unknown grant is dropped,
// so both fail toward less authority, and the warning says so.
func validatePanePermissions(cfg *UserConfig, result *ValidationResult) {
	p := cfg.Agents.Permissions
	if mode := strings.ToLower(strings.TrimSpace(p.Mode)); mode != "" && !slices.Contains(PaneModes, mode) {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "agents.permissions",
			Key:     "mode",
			Message: fmt.Sprintf("'%s' is not a valid value (allowed: %s); read as strict", p.Mode, strings.Join(PaneModes, ", ")),
		})
	}
	if _, unknown := CanonicalPaneGrants(p.Grants); len(unknown) > 0 {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "agents.permissions",
			Key:     "grants",
			Message: fmt.Sprintf("unknown grant %s (allowed: %s); dropped", strings.Join(unknown, ", "), strings.Join(PaneGrantNames, ", ")),
		})
	}
}
