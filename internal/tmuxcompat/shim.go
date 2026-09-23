package tmuxcompat

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// Caller makes one tuios verb call. *session.VerbClient satisfies it.
type Caller interface {
	Call(verb string, params any) (json.RawMessage, error)
}

// Shim answers tmux commands for one caller.
type Shim struct {
	// Caller reaches the daemon.
	Caller Caller
	// Session is the caller's tuios session, the one session the shim
	// serves. Required.
	Session string
	// Window is the caller's tuios window id (TUIOS_PANE_ID), "" outside a
	// pane.
	Window string
	// TmuxPane is TMUX_PANE, the caller's pane as tmux names it.
	TmuxPane string
	// Cwd is the directory a new pane starts in when -c names none.
	Cwd string
	// Exe is the tuios binary, run as the pane holder for panes the shim
	// opens. Empty runs a pane's command directly, and such a pane cannot be
	// respawned.
	Exe string
	// Dir is the shim's runtime directory (see RuntimeDir in the caller).
	Dir string
	// ServerPID is the pid TMUX names, reported as #{pid}.
	ServerPID int
	// HolderEnv is extra KEY=VALUE for the processes of new panes, beyond
	// what the holder sets itself. The launcher passes the log settings here.
	HolderEnv []string
	// Stdout and Stderr receive what tmux would print.
	Stdout, Stderr io.Writer
	// Log records the calls (see Logger). Nil records nothing.
	Log *Logger

	// respawn delivers a respawn-pane request. Nil means RequestRespawn.
	respawn func(dir, windowID string, req RespawnRequest) error
}

// handler runs one command. It returns the outcome to log, detail for the
// log, and an error to print.
type handler func(s *Shim, name string, args []string) (string, []string, error)

// commands maps every command the shim answers to its handler.
var commands = map[string]handler{
	"split-window":    (*Shim).splitWindow,
	"new-window":      (*Shim).newWindow,
	"send-keys":       (*Shim).sendKeys,
	"capture-pane":    (*Shim).capturePane,
	"display-message": (*Shim).displayMessage,
	"list-panes":      (*Shim).listPanes,
	"list-windows":    (*Shim).listWindows,
	"list-sessions":   (*Shim).listSessions,
	"has-session":     (*Shim).hasSession,
	"kill-pane":       (*Shim).killPane,
	"kill-window":     (*Shim).killWindow,
	"select-pane":     (*Shim).selectPane,
	"select-window":   (*Shim).selectWindow,
	"rename-window":   (*Shim).renameWindow,
	"respawn-pane":    (*Shim).respawnPane,
}

// specs are the flags each command accepts. A tmux flag missing here is
// refused as unknown and logged, rather than accepted and not honoured. The
// placement flags of split-window (-b -f -h -v -l -p -Z) are accepted and
// leave placement to tuios's layout.
var specs = map[string]spec{
	"split-window":    {bools: "bdfhvPZ", values: "celpFt"},
	"new-window":      {bools: "dP", values: "ceFnt"},
	"send-keys":       {bools: "HlR", values: "Nt"},
	"capture-pane":    {bools: "eJNpq", values: "ESt"},
	"display-message": {bools: "Nlpv", values: "cdtF"},
	"list-panes":      {bools: "as", values: "Ft"},
	"list-windows":    {bools: "a", values: "Ft"},
	"list-sessions":   {values: "F"},
	"has-session":     {values: "t"},
	"kill-pane":       {values: "t"},
	"kill-window":     {values: "t"},
	"select-pane":     {bools: "DLRUZ", values: "tTP"},
	"select-window":   {values: "t"},
	"rename-window":   {values: "t"},
	"respawn-pane":    {bools: "k", values: "cet"},
}

// textCommands carry text as their positional arguments: keys to type or a
// command line to run. The log records how many there were, not what they
// said, since they can hold secrets.
var textCommands = []string{"send-keys", "split-window", "new-window", "respawn-pane"}

// redact returns argv (starting "tmux") as the log records it: the positional
// arguments of text commands, and every VAR=value, replaced by a marker.
func redact(argv []string) []string {
	if len(argv) == 0 {
		return argv
	}
	_, words, err := ParseGlobal(argv[1:])
	if err != nil {
		return argv
	}
	out := append([]string{}, argv[:len(argv)-len(words)]...)
	for i, cmd := range SplitCommands(words) {
		if i > 0 {
			out = append(out, ";")
		}
		name := cmd[0]
		if full, ok := aliases[name]; ok {
			name = full
		}
		if !slices.Contains(textCommands, name) {
			out = append(out, cmd...)
			continue
		}
		p, err := parseFlags(name, specs[name], cmd[1:])
		flags := cmd[1:]
		if err == nil {
			flags = cmd[1 : len(cmd)-len(p.Args)]
		}
		out = append(out, cmd[0])
		for _, f := range flags {
			if strings.Contains(f, "=") || (err != nil && !strings.HasPrefix(f, "-")) {
				f = "<redacted>"
			}
			out = append(out, f)
		}
		if err == nil && len(p.Args) > 0 {
			out = append(out, fmt.Sprintf("<%d redacted>", len(p.Args)))
		}
	}
	return out
}

// ignoredCommands are known and do nothing here, by design: tuios owns the
// layout, the styling and the options, so a tool setting them loses nothing
// it needs. They succeed with any arguments.
var ignoredCommands = []string{
	"set-option",
	"set-window-option",
	"set-hook",
	"refresh-client",
	"select-layout",
	"resize-pane",
	"start-server",
}

// aliases are tmux's short command names.
var aliases = map[string]string{
	"splitw":    "split-window",
	"neww":      "new-window",
	"send":      "send-keys",
	"capturep":  "capture-pane",
	"display":   "display-message",
	"lsp":       "list-panes",
	"lsw":       "list-windows",
	"ls":        "list-sessions",
	"has":       "has-session",
	"killp":     "kill-pane",
	"killw":     "kill-window",
	"selectp":   "select-pane",
	"selectw":   "select-window",
	"renamew":   "rename-window",
	"respawnp":  "respawn-pane",
	"set":       "set-option",
	"setw":      "set-window-option",
	"refresh":   "refresh-client",
	"selectl":   "select-layout",
	"resizep":   "resize-pane",
	"start":     "start-server",
	"killses":   "kill-session",
	"kill-ses":  "kill-session",
	"new":       "new-session",
	"attach":    "attach-session",
	"a":         "attach-session",
	"at":        "attach-session",
	"showw":     "show-window-options",
	"show":      "show-options",
	"lsc":       "list-clients",
	"breakp":    "break-pane",
	"joinp":     "join-pane",
	"swapp":     "swap-pane",
	"lastp":     "last-pane",
	"pasteb":    "paste-buffer",
	"setb":      "set-buffer",
	"showb":     "show-buffer",
	"run":       "run-shell",
	"if":        "if-shell",
	"source":    "source-file",
	"bind":      "bind-key",
	"unbind":    "unbind-key",
	"wait":      "wait-for",
	"respawnw":  "respawn-window",
	"linkw":     "link-window",
	"movew":     "move-window",
	"swapw":     "swap-window",
	"lastw":     "last-window",
	"next":      "next-window",
	"prev":      "previous-window",
	"rotatew":   "rotate-window",
	"pipep":     "pipe-pane",
	"clearhist": "clear-history",
}

// refusedCommands end or replace tuios sessions, which the shim never does:
// the caller's session is not the shim's to end, and another session is out
// of its reach.
var refusedCommands = []string{"kill-session", "kill-server", "new-session", "attach-session", "switch-client", "detach-client"}

// Run answers one tmux invocation. args is argv without the program name. It
// returns the exit status tmux would.
func (s *Shim) Run(args []string) int {
	// full is the call as the log records it.
	full := redact(append([]string{"tmux"}, args...))
	g, words, err := ParseGlobal(args)
	if err != nil {
		return s.fail(full, OutcomeUnsupported, nil, err)
	}
	var detail []string
	if len(g.Ignored) > 0 {
		detail = append(detail, "global flags ignored: "+strings.Join(g.Ignored, " "))
	}
	if g.Name != "" || (g.Socket != "" && s.Dir != "" && g.Socket != SocketPath(s.Dir)) {
		return s.fail(full, OutcomeUnsupported, detail, errors.New("the tuios tmux shim answers only for its own server; -L and -S name another one"))
	}
	if g.Version {
		fmt.Fprintf(s.Stdout, "tmux %s\n", Version)
		s.Log.Record(full, OutcomeOK, detail)
		return 0
	}
	if s.Session == "" {
		return s.fail(full, OutcomeError, detail, errors.New("no tuios session: the tmux shim runs in a tuios pane (TUIOS_SESSION is unset)"))
	}
	cmds := SplitCommands(words)
	if len(cmds) == 0 {
		return s.fail(full, OutcomeUnsupported, detail, errors.New("the tuios tmux shim does not start or attach tmux sessions; give it a command"))
	}
	outcome := OutcomeOK
	for _, c := range cmds {
		o, d, err := s.runOne(c[0], c[1:])
		detail = append(detail, d...)
		outcome = worse(outcome, o)
		if err != nil {
			return s.fail(full, o, detail, err)
		}
	}
	s.Log.Record(full, outcome, detail)
	return 0
}

// fail prints err the way tmux does, records the call and returns status 1.
func (s *Shim) fail(argv []string, outcome string, detail []string, err error) int {
	fmt.Fprintln(s.Stderr, err)
	s.Log.Record(argv, outcome, append(detail, err.Error()))
	return 1
}

// outcomeRank orders outcomes from best to worst.
var outcomeRank = map[string]int{OutcomeOK: 0, OutcomeIgnored: 1, OutcomePartial: 2, OutcomeError: 3, OutcomeUnsupported: 4}

func worse(a, b string) string {
	if outcomeRank[b] > outcomeRank[a] {
		return b
	}
	return a
}

func (s *Shim) runOne(name string, args []string) (string, []string, error) {
	if full, ok := aliases[name]; ok {
		name = full
	}
	if h, ok := commands[name]; ok {
		o, d, err := h(s, name, args)
		var uf errUnknownFlag
		if errors.As(err, &uf) {
			return OutcomeUnsupported, d, err
		}
		return o, d, err
	}
	if slices.Contains(ignoredCommands, name) {
		return OutcomeIgnored, nil, nil
	}
	if slices.Contains(refusedCommands, name) {
		return OutcomeUnsupported, nil, fmt.Errorf("%s: refused, the tuios tmux shim does not start, attach or end sessions", name)
	}
	return OutcomeUnsupported, nil, fmt.Errorf("unknown command: %s", name)
}

func (s *Shim) println(line string) { fmt.Fprintln(s.Stdout, line) }

// callerPane is the pane an empty target means: TMUX_PANE, then the caller's
// tuios window, then the focused pane.
func (s *Shim) callerPane(v *view) *pane {
	if strings.HasPrefix(s.TmuxPane, "%") {
		if p, err := v.paneByID(s.TmuxPane[1:]); err == nil {
			return p
		}
	}
	if p := v.byWindowID(s.Window); p != nil {
		return p
	}
	if p := v.byWindowID(v.focused); p != nil {
		return p
	}
	return v.active(v.current)
}

// expand expands a format and turns missing variables into log detail.
func expand(format string, vars map[string]string) (string, []string) {
	out, missing := Expand(format, vars)
	var detail []string
	for _, m := range missing {
		detail = append(detail, "format: no value for "+m)
	}
	return out, detail
}

func outcomeFor(detail []string) string {
	if len(detail) > 0 {
		return OutcomePartial
	}
	return OutcomeOK
}

// paneCommand is the argv a new pane runs. With a holder, the tmux command
// rules are applied by the holder: no command is the login shell, one word is
// a shell command line, several are an argv.
func (s *Shim) paneCommand(cmd, env []string) []string {
	if s.Exe == "" || !holderSupported {
		switch {
		case len(cmd) == 0:
			return nil
		case len(cmd) == 1 && runtime.GOOS == "windows":
			return []string{"cmd", "/C", cmd[0]}
		case len(cmd) == 1:
			return []string{"/bin/sh", "-c", cmd[0]}
		}
		return cmd
	}
	argv := []string{s.Exe, "tmux-pane", "--dir", s.Dir}
	for _, e := range append(slices.Clone(s.HolderEnv), env...) {
		argv = append(argv, "--env", e)
	}
	argv = append(argv, "--")
	return append(argv, cmd...)
}

// newPane opens a tuios window for split-window and new-window and returns
// its id.
func (s *Shim) newPane(ws int, focus bool, cwd string, cmd, env []string) (string, error) {
	if cwd == "" {
		cwd = s.Cwd
	}
	params := map[string]any{
		"session":   s.Session,
		"workspace": ws,
		"focus":     focus,
	}
	if cwd != "" {
		params["cwd"] = cwd
	}
	if argv := s.paneCommand(cmd, env); len(argv) > 0 {
		params["command"] = argv
	}
	raw, err := s.Caller.Call("new-window", params)
	if err != nil {
		return "", fmt.Errorf("create pane failed: %w", err)
	}
	var res struct {
		ID string `json:"window_id"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.ID == "" {
		return "", errors.New("create pane failed: new-window returned no window id")
	}
	return res.ID, nil
}

// printNew prints the -P line for a pane just made.
func (s *Shim) printNew(id, format string) []string {
	v, err := s.loadView()
	if err != nil {
		s.println(PaneID(id))
		return []string{"-P: " + err.Error()}
	}
	p := v.byWindowID(id)
	if p == nil {
		s.println(PaneID(id))
		return []string{"-P: the new pane was not in list-windows yet"}
	}
	out, detail := expand(format, s.paneVars(v, p))
	s.println(out)
	return detail
}

// splitWindow opens a pane in the target pane's workspace. Where it lands is
// tuios's layout's answer: -h, -v, -b, -f, -l and -p are accepted and do not
// change it.
func (s *Shim) splitWindow(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	tv, _ := p.Value('t')
	target, err := v.resolvePane(tv, s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	cwd, _ := p.Value('c')
	id, err := s.newPane(target.Workspace, !p.Has('d'), cwd, p.Args, p.Values('e'))
	if err != nil {
		return OutcomeError, nil, err
	}
	if !p.Has('P') {
		return OutcomeOK, nil, nil
	}
	format, ok := p.Value('F')
	if !ok {
		format = "#{session_name}:#{window_index}.#{pane_index}"
	}
	detail := s.printNew(id, format)
	return outcomeFor(detail), detail, nil
}

// newWindow opens a pane on an empty workspace: a new tmux window.
func (s *Shim) newWindow(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	ws := 0
	tv, _ := p.Value('t')
	sess, win, _, hasSess, _ := splitTarget(tv)
	if hasSess && sess != "" && !v.isSession(sess) {
		return OutcomeError, nil, fmt.Errorf("can't find session: %s", sess)
	}
	if !hasSess && v.isSession(win) {
		win = ""
	}
	if win != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(win, "@"))
		if err != nil || !slices.Contains(v.workspace, n) {
			return OutcomeError, nil, fmt.Errorf("create window failed: tuios has no workspace %s", win)
		}
		if v.wsCount[n] > 0 {
			return OutcomeError, nil, fmt.Errorf("create window failed: index %d in use", n)
		}
		ws = n
	} else {
		for _, n := range v.workspace {
			if v.wsCount[n] == 0 {
				ws = n
				break
			}
		}
		if ws == 0 {
			return OutcomeError, nil, errors.New("create window failed: every tuios workspace already holds panes")
		}
	}
	cwd, _ := p.Value('c')
	id, err := s.newPane(ws, !p.Has('d'), cwd, p.Args, p.Values('e'))
	if err != nil {
		return OutcomeError, nil, err
	}
	if n, ok := p.Value('n'); ok {
		if _, err := s.Caller.Call("set-workspace-name", map[string]any{"session": s.Session, "workspace": ws, "name": n}); err != nil {
			return OutcomeError, nil, fmt.Errorf("name window failed: %w", err)
		}
	}
	if !p.Has('P') {
		return OutcomeOK, nil, nil
	}
	format, ok := p.Value('F')
	if !ok {
		format = "#{session_name}:#{window_index}"
	}
	detail := s.printNew(id, format)
	return outcomeFor(detail), detail, nil
}

// sendKeys writes keys to a pane as text.
func (s *Shim) sendKeys(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	var detail []string
	if p.Has('R') {
		detail = append(detail, "send-keys -R (reset the terminal) ignored")
	}
	text, err := sendKeysText(p.Args, p.Has('l'), p.Has('H'))
	if err != nil {
		return OutcomeError, detail, err
	}
	if n, ok := p.Value('N'); ok {
		count, err := strconv.Atoi(n)
		if err != nil || count < 1 || count > 10000 {
			return OutcomeError, detail, fmt.Errorf("send-keys: repeat count %q is not 1 to 10000", n)
		}
		text = strings.Repeat(text, count)
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, detail, err
	}
	tv, _ := p.Value('t')
	target, err := v.resolvePane(tv, s.callerPane(v))
	if err != nil {
		return OutcomeError, detail, err
	}
	if text == "" {
		return outcomeFor(detail), detail, nil
	}
	if _, err := s.Caller.Call("send-text", map[string]any{"session": s.Session, "window": target.ID, "text": text}); err != nil {
		return OutcomeError, detail, err
	}
	return outcomeFor(detail), detail, nil
}

// splitLines splits captured content into lines, without a trailing empty one.
func splitLines(content string) []string {
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

func (s *Shim) capture(id, source string, styled bool) ([]string, error) {
	raw, err := s.Caller.Call("capture-pane", map[string]any{"session": s.Session, "window": id, "source": source, "styled": styled})
	if err != nil {
		return nil, err
	}
	var res struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("read capture-pane: %w", err)
	}
	return splitLines(res.Content), nil
}

// captureLine reads a -S or -E value: "-" or a line number, 0 the top of the
// visible screen and negative lines in the history.
func captureLine(val string, dash int) (int, error) {
	if val == "-" {
		return dash, nil
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("capture-pane: %q is not a line number", val)
	}
	return n, nil
}

// capturePane prints a pane's content. Only -p is supported: without it tmux
// fills a paste buffer, and the shim keeps none.
func (s *Shim) capturePane(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	if !p.Has('p') {
		return OutcomeUnsupported, nil, errors.New("capture-pane: only -p (print) is supported; the tuios tmux shim keeps no paste buffers")
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	tv, _ := p.Value('t')
	target, err := v.resolvePane(tv, s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	styled := p.Has('e')
	visible, err := s.capture(target.ID, "visible", styled)
	if err != nil {
		return OutcomeError, nil, err
	}
	sv, hasS := p.Value('S')
	ev, hasE := p.Value('E')
	lines := visible
	if hasS || hasE {
		var history []string
		start := 0
		if hasS {
			if start, err = captureLine(sv, -1<<30); err != nil {
				return OutcomeError, nil, err
			}
		}
		if start < 0 {
			recent, err := s.capture(target.ID, "recent", styled)
			if err != nil {
				return OutcomeError, nil, err
			}
			if n := len(recent) - len(visible); n > 0 {
				history = recent[:n]
			}
		}
		end := len(visible) - 1
		if hasE {
			if end, err = captureLine(ev, len(visible)-1); err != nil {
				return OutcomeError, nil, err
			}
		}
		all := append(slices.Clone(history), visible...)
		base := len(history)
		from := min(max(base+start, 0), len(all))
		to := min(max(base+end+1, from), len(all))
		lines = all[from:to]
	}
	for _, l := range lines {
		s.println(l)
	}
	return OutcomeOK, nil, nil
}

// displayMessage prints a format with -p. Without -p tmux shows the message
// in its status line, which tuios does not have, so it is ignored.
func (s *Shim) displayMessage(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	format := strings.Join(p.Args, " ")
	if f, ok := p.Value('F'); ok {
		if format != "" {
			return OutcomeError, nil, errors.New("only one of -F or argument must be given")
		}
		format = f
	}
	if format == "" {
		format = "[#{session_name}] #{window_index}:#{window_name}, current pane #{pane_index}"
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	tv, _ := p.Value('t')
	target, err := v.resolvePane(tv, s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	out, detail := format, []string(nil)
	if !p.Has('l') {
		out, detail = expand(format, s.paneVars(v, target))
	}
	if !p.Has('p') {
		return OutcomeIgnored, detail, nil
	}
	s.println(out)
	return outcomeFor(detail), detail, nil
}

// listPanes prints the panes of the target window, or with -s or -a of the
// whole session.
func (s *Shim) listPanes(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	all := p.Has('a') || p.Has('s')
	format, ok := p.Value('F')
	if !ok {
		format = "#{pane_index}: [#{pane_width}x#{pane_height}] #{pane_id}#{?pane_active, (active),}"
		if all {
			format = "#{session_name}:#{window_index}." + format
		}
	}
	var list []*pane
	if all {
		for _, ws := range v.windowsInUse() {
			list = append(list, v.panesOn(ws)...)
		}
	} else {
		tv, _ := p.Value('t')
		ws, err := v.resolveWindow(tv, s.callerPane(v))
		if err != nil {
			return OutcomeError, nil, err
		}
		list = v.panesOn(ws)
	}
	var detail []string
	for _, pn := range list {
		out, d := expand(format, s.paneVars(v, pn))
		detail = mergeDetail(detail, d)
		s.println(out)
	}
	return outcomeFor(detail), detail, nil
}

// listWindows prints the workspaces that hold panes.
func (s *Shim) listWindows(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	if tv, ok := p.Value('t'); ok && tv != "" && !v.isSession(strings.TrimSuffix(tv, ":")) {
		return OutcomeError, nil, fmt.Errorf("can't find session: %s", tv)
	}
	format, ok := p.Value('F')
	if !ok {
		format = "#{window_index}: #{window_name}#{window_flags} (#{window_panes} panes) [#{window_width}x#{window_height}]"
		if p.Has('a') {
			format = "#{session_name}:" + format
		}
	}
	var detail []string
	for _, ws := range v.windowsInUse() {
		vars := s.sessionVars(v)
		s.windowVars(v, ws, vars)
		if a := v.active(ws); a != nil {
			vars = s.paneVars(v, a)
		}
		out, d := expand(format, vars)
		detail = mergeDetail(detail, d)
		s.println(out)
	}
	return outcomeFor(detail), detail, nil
}

// listSessions prints the one session the shim serves.
func (s *Shim) listSessions(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	format, ok := p.Value('F')
	if !ok {
		format = "#{session_name}: #{session_windows} windows (attached)"
	}
	out, detail := expand(format, s.sessionVars(v))
	s.println(out)
	return outcomeFor(detail), detail, nil
}

// hasSession succeeds for the caller's session and fails for any other.
func (s *Shim) hasSession(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	tv, _ := p.Value('t')
	sess := tv
	if i := strings.IndexByte(tv, ':'); i >= 0 {
		sess = tv[:i]
	}
	if sess == "" || strings.TrimPrefix(sess, "=") == s.Session || sess == "$0" {
		return OutcomeOK, nil, nil
	}
	return OutcomeOK, nil, fmt.Errorf("can't find session: %s", sess)
}

// killPane closes the target pane.
func (s *Shim) killPane(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	tv, _ := p.Value('t')
	target, err := v.resolvePane(tv, s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	if _, err := s.Caller.Call("close-window", map[string]any{"session": s.Session, "window": target.ID}); err != nil {
		return OutcomeError, nil, err
	}
	return OutcomeOK, nil, nil
}

// killWindow closes every pane of the target workspace.
func (s *Shim) killWindow(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	tv, _ := p.Value('t')
	ws, err := v.resolveWindow(tv, s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	for _, pn := range v.panesOn(ws) {
		if _, err := s.Caller.Call("close-window", map[string]any{"session": s.Session, "window": pn.ID}); err != nil {
			return OutcomeError, nil, err
		}
	}
	return OutcomeOK, nil, nil
}

// selectPane focuses a pane, or with -T names it. -P (a style) is ignored.
func (s *Shim) selectPane(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	tv, _ := p.Value('t')
	target, err := v.resolvePane(tv, s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	// -T and -P change the pane and return, as they do in tmux: naming a
	// teammate's pane must not move the person's focus.
	if title, ok := p.Value('T'); ok {
		if _, err := s.Caller.Call("set-window", map[string]any{"session": s.Session, "window": target.ID, "name": title}); err != nil {
			return OutcomeError, nil, err
		}
		return OutcomeOK, nil, nil
	}
	if _, ok := p.Value('P'); ok {
		return OutcomeIgnored, nil, nil
	}
	dirs := map[byte]string{'L': "left", 'R': "right", 'U': "up", 'D': "down"}
	for _, c := range []byte("LRUD") {
		if p.Has(c) {
			if _, err := s.Caller.Call("focus-window", map[string]any{"session": s.Session, "direction": dirs[c]}); err != nil {
				return OutcomeError, nil, err
			}
			return OutcomeOK, nil, nil
		}
	}
	if _, err := s.Caller.Call("focus-window", map[string]any{"session": s.Session, "window": target.ID}); err != nil {
		return OutcomeError, nil, err
	}
	return OutcomeOK, nil, nil
}

// selectWindow shows the target workspace.
func (s *Shim) selectWindow(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	tv, _ := p.Value('t')
	ws, err := v.resolveWindow(tv, s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	if _, err := s.Caller.Call("select-workspace", map[string]any{"session": s.Session, "workspace": ws}); err != nil {
		return OutcomeError, nil, err
	}
	return OutcomeOK, nil, nil
}

// renameWindow names the target workspace.
func (s *Shim) renameWindow(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	if len(p.Args) != 1 {
		return OutcomeError, nil, errors.New("rename-window: give exactly one new name")
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	tv, _ := p.Value('t')
	ws, err := v.resolveWindow(tv, s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	if _, err := s.Caller.Call("set-workspace-name", map[string]any{"session": s.Session, "workspace": ws, "name": p.Args[0]}); err != nil {
		return OutcomeError, nil, err
	}
	return OutcomeOK, nil, nil
}

// respawnPane replaces the process of a pane the shim opened, keeping the
// pane and its id. It needs -k, since every pane tuios shows is still
// running: a pane closes when its process ends.
func (s *Shim) respawnPane(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	tv, _ := p.Value('t')
	target, err := v.resolvePane(tv, s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	if !p.Has('k') {
		return OutcomeError, nil, fmt.Errorf("respawn pane failed: pane %s still active", PaneID(target.ID))
	}
	cwd, _ := p.Value('c')
	req := RespawnRequest{Command: p.Args, Cwd: cwd, Env: p.Values('e')}
	send := s.respawn
	if send == nil {
		send = RequestRespawn
	}
	if err := send(s.Dir, target.ID, req); err != nil {
		return OutcomeError, nil, fmt.Errorf("respawn pane failed: %w", err)
	}
	return OutcomeOK, nil, nil
}

// mergeDetail appends the entries of add that dst does not hold yet.
func mergeDetail(dst, add []string) []string {
	for _, d := range add {
		if !slices.Contains(dst, d) {
			dst = append(dst, d)
		}
	}
	return dst
}
