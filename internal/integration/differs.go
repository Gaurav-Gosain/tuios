package integration

import (
	"slices"
	"strings"
)

// hookDiffer is a format that can say, in words, how the hooks it holds
// differ from the ones this tuios writes. Status asks it only when the
// install carries this build's version and is still not current, so the
// version alone does not explain it.
type hookDiffer interface {
	differs(t *Target, have []byte, run Runner) ([]string, error)
}

func (nestedHooks) differs(t *Target, have []byte, run Runner) ([]string, error) {
	entries, err := findManaged(have)
	if err != nil {
		return nil, err
	}
	return hookDifferences(entries, t.Events, run, t.ID, t.Version), nil
}

func (flatHooks) differs(t *Target, have []byte, run Runner) ([]string, error) {
	root, err := parseObject(have)
	if err != nil {
		return nil, err
	}
	entries, err := flatManaged(root)
	if err != nil {
		return nil, err
	}
	return hookDifferences(entries, t.Events, run, t.ID, t.Version), nil
}

// isForwarderCommand reports whether a hook command is the forwarder line,
// which sends the event to the daemon over its socket.
func isForwarderCommand(cmd string) bool {
	return strings.HasPrefix(cmd, "sh -c ") && strings.Contains(cmd, "agent-hook-event")
}

// hookDifferences lists how the managed entries differ from one hook per
// event running the command run writes: events with no hook, hooks for
// events this tuios does not install, events with more than one, and how the
// command itself differs.
func hookDifferences(entries []managedEntry, events []HookEvent, run Runner, harnessID string, version int) []string {
	want := run.command(harnessID, version)
	count := map[string]int{}
	for _, e := range entries {
		count[e.Event]++
	}
	var missing, extra, twice []string
	for _, ev := range events {
		switch count[ev.Name] {
		case 0:
			missing = append(missing, ev.Name)
		case 1:
		default:
			twice = append(twice, ev.Name)
		}
	}
	wanted := func(name string) bool {
		return slices.ContainsFunc(events, func(ev HookEvent) bool { return ev.Name == name })
	}
	var command string
	for _, e := range entries {
		if !wanted(e.Event) {
			if !slices.Contains(extra, e.Event) {
				extra = append(extra, e.Event)
			}
			continue
		}
		if command == "" && e.Command != want {
			command = commandDifference(e.Command, want, run, harnessID)
		}
	}
	var out []string
	if len(missing) > 0 {
		out = append(out, "no hook for "+strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		out = append(out, "a hook for "+strings.Join(extra, ", ")+" that this tuios does not install")
	}
	if len(twice) > 0 {
		out = append(out, "more than one hook for "+strings.Join(twice, ", "))
	}
	if command != "" {
		out = append(out, command)
	}
	return out
}

// commandDifference says how one installed hook command differs from the
// one this tuios writes.
func commandDifference(have, want string, run Runner, harnessID string) string {
	haveFwd, wantFwd := isForwarderCommand(have), isForwarderCommand(want)
	switch {
	case haveFwd && !wantFwd:
		return "the hooks send events to the daemon, but this tuios finds no nc or socat it can use, so it writes hooks that start tuios for each event"
	case !haveFwd && wantFwd:
		return "the hooks start tuios for each event; this tuios writes hooks that send events to the daemon through " + run.Socket
	}
	if prog := unshellWord(hookProgram(have, harnessID)); prog != "" && prog != run.Program {
		return "the hooks run " + prog + ", not " + run.Program
	}
	return "the hook command is not the one this tuios writes"
}
