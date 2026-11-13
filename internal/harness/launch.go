package harness

import (
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"tuios.dev/harness"
)

// The flags a new agent starts with, from the person's choice and the
// manifest's [launch] block (harness.Launch).

// LaunchChoice is what the person chose for a new agent. An empty field is
// the harness's own default.
type LaunchChoice struct {
	Model, Effort, Permission string
}

// Empty reports whether nothing was chosen.
func (c LaunchChoice) Empty() bool { return c.Model == "" && c.Effort == "" && c.Permission == "" }

// LaunchArgs is the argv the choice adds after the harness's program, or why
// the harness cannot take it. m may be nil, for a program no manifest knows.
func LaunchArgs(m *harness.Manifest, c LaunchChoice) ([]string, error) {
	if c.Empty() {
		return nil, nil
	}
	name := "this program"
	var l harness.Launch
	if m != nil {
		name, l = m.DisplayName, m.Launch
	}
	var out []string
	add := func(what, flag, v string, values []string, free bool) error {
		if v == "" {
			return nil
		}
		if flag == "" {
			return errfmt.Errorf("%s takes no %s option that tuios knows", name, what)
		}
		if !harness.LaunchValueOK(v) {
			return errfmt.Errorf("%s %q: one word, not starting with -", what, v)
		}
		if !free && !slices.Contains(values, v) {
			return errfmt.Errorf("%s %q is not one %s takes: %s", what, v, name, strings.Join(values, ", "))
		}
		out = append(out, flag, v)
		return nil
	}
	if err := add("model", l.ModelFlag, c.Model, l.Models, true); err != nil {
		return nil, err
	}
	if err := add("effort", l.EffortFlag, c.Effort, l.Efforts, false); err != nil {
		return nil, err
	}
	if c.Permission != "" && c.Permission == l.DefaultPermission {
		return out, nil
	}
	if err := add("permission mode", l.PermissionFlag, c.Permission, l.Permissions, false); err != nil {
		return nil, err
	}
	return out, nil
}
