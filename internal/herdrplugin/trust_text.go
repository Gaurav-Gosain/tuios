package herdrplugin

import (
	"fmt"
	"strings"
	"unicode"
)

// The words the install question, tuios plugins approve and the approval
// dialog use for what a plugin may do. One place, so the three say the same
// thing.

// GrantMeaning says in plain words what a grant lets a plugin do. A plugin
// is in no session, so read and write reach every session.
func GrantMeaning(grant string) string {
	switch grant {
	case "read":
		return "Reads every session and pane on this machine."
	case "write":
		return "Types into your panes and shells, so it can run commands as you. It stops at an agent's approval prompt."
	case "fan":
		return "Starts agents in new sessions."
	case "files":
		return "Copies files."
	case "admin":
		return "Does all that you can do in tuios."
	}
	return ""
}

// MaxNameRunes is the longest plugin name tuios shows.
const MaxNameRunes = 64

// ShownName is a plugin name as tuios shows it: with control and bidi
// characters dropped, spaces collapsed, and cut to MaxNameRunes.
func ShownName(name string) string {
	name = strings.Map(func(r rune) rune {
		if hasControl(string(r)) {
			return -1
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if r := []rune(name); len(r) > MaxNameRunes {
		name = string(r[:MaxNameRunes-1]) + "…"
	}
	return name
}

// RunsLines lists what p runs on its own once it is allowed: its startup
// commands, then its hooks, actions, panes and link handlers by name.
// Nothing is an empty list.
func RunsLines(p *Plugin) []string {
	var out []string
	for _, s := range p.Startup {
		out = append(out, "startup  "+ShownArgv(s.Command...))
	}
	var hooks, actions, panes, links []string
	for _, ev := range p.Events {
		hooks = append(hooks, ev.On)
	}
	for _, a := range p.Actions {
		actions = append(actions, a.ID)
	}
	for _, x := range p.Panes {
		panes = append(panes, x.ID+" ("+x.Placement+")")
	}
	for _, h := range p.LinkHandlers {
		links = append(links, h.ID)
	}
	for i, list := range [][]string{hooks, actions, panes, links} {
		if len(list) > 0 {
			out = append(out, fmt.Sprintf("%-8s %s (%d)", [...]string{"hooks", "actions", "panes", "links"}[i], ShownArgv(strings.Join(list, ", ")), len(list)))
		}
	}
	return out
}

// ShownArgv joins argv for a person to read. An argument with a character
// that does not print is quoted, so a control character in a manifest
// cannot change what the terminal shows.
func ShownArgv(argv ...string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = a
		if strings.ContainsFunc(a, func(r rune) bool { return !unicode.IsPrint(r) }) {
			out[i] = fmt.Sprintf("%q", a)
		}
	}
	return strings.Join(out, " ")
}
