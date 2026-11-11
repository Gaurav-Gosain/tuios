package envvars

import (
	"fmt"
	"strings"
)

// pageOrder is the order the generated page lists the kinds in, with the
// heading and a line of intro for each.
var pageOrder = []struct {
	Kind  Kind
	Title string
	Intro string
}{
	{Pane, "Variables every pane gets", "tuios sets these in the environment of the program in each pane. A `tuios` command run there reads them, so it needs no `--session` or `--window`. They are removed from the environment of a program that tuios starts on another machine by ssh, unless the pane is hosted there."},
	{Setting, "Variables you can set", "Set these in the shell that starts tuios, or in the environment of a service."},
	{Timing, "Timings and limits", "Most of these also have a key in `[daemon]`. The variable wins when both are set."},
	{Switch, "Switches", "A `LEGACY` switch turns one behaviour back to the older one and is removed in a later release. The `NO_` switches turn a fast path off so it can be compared with the slow one."},
	{Debug, "Logs and traces", "These write files. Some record what you type, so use them for a bug report and delete the files after."},
	{Hook, "Variables a command gets", "tuios sets these for a hook command, for the command of a custom dock component or rail section, and for each pane of `tuios xpanes`."},
	{Plugin, "Variables a plugin gets", "tuios sets these for the process of a plugin."},
}

// Page is the text of docs/ENVIRONMENT.md. It lists every variable a person can
// meet; the Test and Internal kinds are in the table and not on the page.
func Page() string {
	var b strings.Builder
	b.WriteString("<!-- This file is generated from internal/envvars. Run `go generate ./internal/envvars` after a change to the table. -->\n\n")
	b.WriteString("# Environment variables\n\n")
	b.WriteString("tuios reads and sets these variables. The list is generated from the one table in the code, `internal/envvars`, and a test fails when a variable in the code is not in it.\n\n")
	counts := map[Kind]int{}
	for _, v := range All {
		counts[v.Kind]++
	}
	for _, sec := range pageOrder {
		var rows []Var
		for _, v := range All {
			if v.Kind == sec.Kind {
				rows = append(rows, v)
			}
		}
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n%s\n\n| Variable | What it does |\n|----------|--------------|\n", sec.Title, sec.Intro)
		for _, v := range rows {
			fmt.Fprintf(&b, "| `%s` | %s |\n", v.Name, strings.ReplaceAll(v.Meaning, "|", "\\|"))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "tuios also reads %d variables that only its test suites set, and %d that one tuios process passes to the next. They are in the table and not on this page.\n", counts[Test], counts[Internal])
	return b.String()
}
