package harness

import (
	"slices"
	"strings"
)

// PermissionLevel ranks a permission mode. A higher level asks the person
// less.
type PermissionLevel int8

// The levels, narrowest first.
const (
	// PermissionReadOnly plans or reads and changes nothing: Claude Code's
	// plan mode, Codex's read-only sandbox.
	PermissionReadOnly PermissionLevel = iota
	// PermissionAsk asks the person before an edit or a command: the default
	// of Claude Code, Gemini CLI and Copilot.
	PermissionAsk
	// PermissionEdits edits files without asking and asks before other
	// commands: Claude Code's acceptEdits, Codex's default workspace-write
	// sandbox.
	PermissionEdits
	// PermissionAuto runs most tools without asking: Claude Code's auto,
	// Codex with approvals off inside its sandbox.
	PermissionAuto
	// PermissionFull runs everything without a check or a sandbox.
	PermissionFull
)

// String names a level in plain words.
func (l PermissionLevel) String() string {
	switch l {
	case PermissionReadOnly:
		return "read only"
	case PermissionAsk:
		return "asks first"
	case PermissionEdits:
		return "edits without asking"
	case PermissionAuto:
		return "runs tools without asking"
	default:
		return "no checks"
	}
}

// Permission is what a command line says about a harness's permission mode.
type Permission struct {
	// Level is the mode's rank. For a mode the table does not know it is
	// the harness default, and Known is false.
	Level PermissionLevel
	// Named is true when the command line or the environment names a mode,
	// false when the harness starts at its default.
	Named bool
	// Known is false when a mode option has a value the table does not
	// know, such as a mode added in a later release.
	Known bool
	// Mode is the mode as written, such as "--permission-mode plan", or ""
	// for the default.
	Mode string
}

type permissionRow struct {
	token string
	axis  string
	level PermissionLevel
}

type permissionHarness struct {
	def     PermissionLevel
	rows    []permissionRow
	env     []permissionRow
	combine func(axes map[string]PermissionLevel) PermissionLevel
	steps   []permissionStep
}

type permissionStep struct {
	level PermissionLevel
	args  []string
}

var permissionTable = map[string]permissionHarness{
	"claude-code": {
		def: PermissionAsk,
		rows: []permissionRow{
			{"--permission-mode=plan", "mode", PermissionReadOnly},
			{"--permission-mode=default", "mode", PermissionAsk},
			{"--permission-mode=manual", "mode", PermissionAsk},
			{"--permission-mode=dontAsk", "mode", PermissionAsk},
			{"--permission-mode=acceptEdits", "mode", PermissionEdits},
			{"--permission-mode=auto", "mode", PermissionAuto},
			{"--permission-mode=bypassPermissions", "mode", PermissionFull},
			{"--dangerously-skip-permissions", "skip", PermissionFull},
			{"--allow-dangerously-skip-permissions", "skip", PermissionFull},
		},
		steps: []permissionStep{{PermissionReadOnly, []string{"--permission-mode", "plan"}}},
	},
	"codex": {
		def: PermissionEdits,
		rows: slices.Concat(
			codexOption("sandbox", []string{"--sandbox", "-s"}, "", map[string]PermissionLevel{
				"read-only": PermissionReadOnly, "workspace-write": PermissionEdits, "danger-full-access": PermissionFull,
			}),
			codexOption("approval", []string{"--ask-for-approval", "-a"}, "", map[string]PermissionLevel{
				"untrusted": PermissionAsk, "on-request": PermissionEdits, "on-failure": PermissionEdits, "never": PermissionAuto,
			}),
			codexOption("sandbox", []string{"--config", "-c"}, "sandbox_mode=", map[string]PermissionLevel{
				"read-only": PermissionReadOnly, "workspace-write": PermissionEdits, "danger-full-access": PermissionFull,
			}),
			codexOption("approval", []string{"--config", "-c"}, "approval_policy=", map[string]PermissionLevel{
				"untrusted": PermissionAsk, "on-request": PermissionEdits, "on-failure": PermissionEdits, "never": PermissionAuto,
			}),
			[]permissionRow{
				{"--full-auto", "approval", PermissionEdits},
				{"--dangerously-bypass-approvals-and-sandbox", "bypass", PermissionFull},
				{"--yolo", "bypass", PermissionFull},
			},
		),
		combine: codexLevel,
		steps: []permissionStep{
			{PermissionReadOnly, []string{"--sandbox", "read-only"}},
			{PermissionAsk, []string{"--ask-for-approval", "untrusted"}},
		},
	},
	"copilot": {
		def: PermissionAsk,
		rows: []permissionRow{
			{"--plan", "mode", PermissionReadOnly},
			{"--mode=plan", "mode", PermissionReadOnly},
			{"--mode=interactive", "mode", PermissionAsk},
			{"--mode=autopilot", "mode", PermissionAuto},
			{"--autopilot", "mode", PermissionAuto},
			{"--allow-all-tools", "tools", PermissionAuto},
			{"--allow-all-paths", "paths", PermissionAuto},
			{"--allow-all-urls", "urls", PermissionAuto},
			{"--allow-all", "all", PermissionFull},
			{"--yolo", "all", PermissionFull},
		},
		env:   []permissionRow{{"COPILOT_ALLOW_ALL", "all", PermissionFull}},
		steps: []permissionStep{{PermissionReadOnly, []string{"--mode", "plan"}}},
	},
	"gemini-cli": {
		def: PermissionAsk,
		rows: []permissionRow{
			{"--approval-mode=default", "mode", PermissionAsk},
			{"--approval-mode=auto_edit", "mode", PermissionEdits},
			{"--approval-mode=yolo", "mode", PermissionFull},
			{"--yolo", "mode", PermissionFull},
			{"-y", "mode", PermissionFull},
		},
	},
}

func codexOption(axis string, names []string, prefix string, values map[string]PermissionLevel) []permissionRow {
	var rows []permissionRow
	for _, name := range names {
		for value, level := range values {
			rows = append(rows, permissionRow{name + "=" + prefix + value, axis, level})
		}
	}
	return rows
}

func codexLevel(axes map[string]PermissionLevel) PermissionLevel {
	if _, ok := axes["bypass"]; ok {
		return PermissionFull
	}
	sandbox, hasSandbox := axes["sandbox"]
	switch {
	case hasSandbox && sandbox == PermissionFull:
		return PermissionFull
	case hasSandbox && sandbox == PermissionReadOnly:
		return PermissionReadOnly
	}
	if approval, ok := axes["approval"]; ok {
		return approval
	}
	return PermissionEdits
}

// PermissionRanked reports whether the modes of harness id are ranked.
func PermissionRanked(id string) bool {
	_, ok := permissionTable[id]
	return ok
}

func (h permissionHarness) options() []string {
	var out []string
	for _, r := range h.rows {
		if name, _, ok := strings.Cut(r.token, "="); ok && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// PermissionOf reads the permission mode a process of harness id starts in,
// from its argv and its environment (env may be nil). argv[0] is the program,
// or an interpreter with the script after it. Reading stops at "--". ok is
// false for a harness PermissionRanked does not know.
func PermissionOf(id string, argv []string, env func(string) (string, bool)) (p Permission, ok bool) {
	h, ok := permissionTable[id]
	if !ok {
		return Permission{}, false
	}
	p = Permission{Level: h.def, Known: true}
	axes := map[string]PermissionLevel{}
	var shown []string
	note := func(row permissionRow, as string) {
		axes[row.axis] = row.level
		p.Named = true
		shown = append(shown, as)
	}
	options := h.options()
	for i := 1; i < len(argv); i++ {
		tok := argv[i]
		if tok == "--" {
			break
		}
		if len(tok) < 2 || tok[0] != '-' {
			continue
		}
		name, value, inline := strings.Cut(tok, "=")
		if slices.Contains(options, name) {
			if !inline {
				if i+1 >= len(argv) {
					break
				}
				i++
				value = argv[i]
			}
			value = strings.Trim(value, `"'`)
			if k := strings.IndexByte(value, '='); k > 0 {
				value = value[:k+1] + strings.Trim(value[k+1:], `"'`)
			}
			row, found := findPermissionRow(h.rows, name+"="+value)
			switch {
			case found:
				note(row, name+" "+value)
			case name == "--config" || name == "-c":
			default:
				p.Known, p.Named = false, true
				shown = append(shown, name+" "+value)
			}
			continue
		}
		if row, found := findPermissionRow(h.rows, tok); found {
			note(row, tok)
		}
	}
	if env != nil {
		for _, row := range h.env {
			if v, set := env(row.token); set && envTrue(v) {
				note(row, row.token+"="+v)
			}
		}
	}
	if len(axes) > 0 {
		if h.combine != nil {
			p.Level = h.combine(axes)
		} else {
			p.Level = PermissionReadOnly
			for _, l := range axes {
				p.Level = max(p.Level, l)
			}
		}
	}
	p.Mode = strings.Join(shown, " ")
	return p, true
}

func findPermissionRow(rows []permissionRow, token string) (permissionRow, bool) {
	for _, r := range rows {
		if r.token == token {
			return r, true
		}
	}
	return permissionRow{}, false
}

func envTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// PermissionArgs returns the arguments that start harness id at the widest
// level it has that is not wider than limit, with that level. ok is false
// when the harness has no such level below its default. A harness that
// already starts at or below limit needs none: the caller checks that first.
func PermissionArgs(id string, limit PermissionLevel) (args []string, level PermissionLevel, ok bool) {
	h, known := permissionTable[id]
	if !known {
		return nil, 0, false
	}
	for _, s := range h.steps {
		if s.level <= limit && (!ok || s.level > level) {
			args, level, ok = s.args, s.level, true
		}
	}
	return slices.Clone(args), level, ok
}
