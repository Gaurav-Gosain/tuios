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
	// serves. Required unless AllSessions is set.
	Session string
	// AllSessions makes the shim serve every session of the daemon, each a
	// tmux session. It is for a caller outside any pane, such as a tool that
	// drives the person's tmux from outside it. A caller in a pane is held
	// to its own session.
	AllSessions bool
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
	// Environ is the caller's environment, the base of the global
	// environment show-environment -g lists.
	Environ []string
	// Shell is the panes' shell ($SHELL). Its base name is
	// pane_current_command when nothing else runs in a pane.
	Shell string
	// Stdin is what load-buffer - reads, and where control mode reads its
	// commands.
	Stdin io.Reader
	// Stdout and Stderr receive what tmux would print.
	Stdout, Stderr io.Writer
	// Subscribe opens a stream of daemon events (the subscribe verb) on a
	// connection of its own. Control mode needs it. Nil leaves control mode
	// to notice changes by reading the session again every few seconds.
	Subscribe func(params map[string]any) (EventStream, error)
	// Log records the calls (see Logger). Nil records nothing.
	Log *Logger

	// respawn delivers a respawn-pane request. Nil means RequestRespawn.
	respawn func(dir, windowID string, req RespawnRequest) error
	// memBuffers are the paste buffers of a shim with no runtime directory.
	memBuffers []buffer
	// bufMode says where the paste buffers are: in the daemon or the shim's
	// own (buffers_daemon.go). It is found on first use.
	bufMode int8
	// memEnv is the set-environment state of a shim with no runtime
	// directory, by scope (see envScope).
	memEnv map[string][]envVar
	// control is set while the shim answers a control-mode client.
	control bool
	// created is the session new-session made last, for control mode to
	// attach to.
	created string
	// daemonPID is the daemon's pid, read once (pidRead) when a format
	// asks for #{pid}.
	daemonPID int
	pidRead   bool
	// attached finds the session a control client attached to in a view,
	// nil outside control mode.
	attached func(*view) *sessionView
	// depth counts the command lines run inside one another by if-shell
	// and run-shell -C.
	depth int
}

// handler runs one command. It returns the outcome to log, detail for the
// log, and an error to print.
type handler func(s *Shim, name string, args []string) (string, []string, error)

// commands maps every command the shim answers to its handler.
var commands = map[string]handler{
	"split-window":        (*Shim).splitWindow,
	"new-window":          (*Shim).newWindow,
	"send-keys":           (*Shim).sendKeys,
	"capture-pane":        (*Shim).capturePane,
	"display-message":     (*Shim).displayMessage,
	"list-panes":          (*Shim).listPanes,
	"list-windows":        (*Shim).listWindows,
	"list-sessions":       (*Shim).listSessions,
	"has-session":         (*Shim).hasSession,
	"kill-pane":           (*Shim).killPane,
	"kill-window":         (*Shim).killWindow,
	"select-pane":         (*Shim).selectPane,
	"select-window":       (*Shim).selectWindow,
	"rename-window":       (*Shim).renameWindow,
	"respawn-pane":        (*Shim).respawnPane,
	"load-buffer":         (*Shim).loadBuffer,
	"set-buffer":          (*Shim).setBuffer,
	"paste-buffer":        (*Shim).pasteBuffer,
	"delete-buffer":       (*Shim).deleteBuffer,
	"list-clients":        (*Shim).listClients,
	"detach-client":       (*Shim).detachClient,
	"show-options":        (*Shim).showOptions,
	"show-window-options": (*Shim).showOptions,
	"new-session":         (*Shim).newSession,
	"last-pane":           (*Shim).lastPane,
	"next-window":         (*Shim).nextWindow,
	"previous-window":     (*Shim).previousWindow,
	"break-pane":          (*Shim).breakPane,
	"join-pane":           (*Shim).joinPane,
	"move-pane":           (*Shim).joinPane,
	"swap-pane":           (*Shim).swapPane,
	"rename-session":      (*Shim).renameSession,
	"show-buffer":         (*Shim).showBuffer,
	"save-buffer":         (*Shim).saveBuffer,
	"list-buffers":        (*Shim).listBuffers,
	"show-environment":    (*Shim).showEnvironment,
	"set-environment":     (*Shim).setEnvironment,
	"wait-for":            (*Shim).waitFor,
	"display-popup":       (*Shim).displayPopup,
}

// specs are the flags each command accepts. A tmux flag missing here is
// refused as unknown and logged, rather than accepted and not honoured. The
// placement flags of split-window (-b -f -h -v -l -p -Z) are accepted and
// leave placement to tuios's layout.
var specs = map[string]spec{
	"split-window":        {bools: "bdfhvPZ", values: "celpFt"},
	"new-window":          {bools: "dP", values: "ceFnt"},
	"send-keys":           {bools: "HlR", values: "Nt"},
	"capture-pane":        {bools: "eJNpq", values: "ESt"},
	"display-message":     {bools: "Nlpv", values: "cdtF"},
	"list-panes":          {bools: "as", values: "Ft"},
	"list-windows":        {bools: "a", values: "Ft"},
	"list-sessions":       {values: "F"},
	"has-session":         {values: "t"},
	"kill-pane":           {values: "t"},
	"kill-window":         {values: "t"},
	"select-pane":         {bools: "DLRUZ", values: "tTP"},
	"select-window":       {values: "t"},
	"rename-window":       {values: "t"},
	"respawn-pane":        {bools: "k", values: "cet"},
	"load-buffer":         {bools: "w", values: "bt"},
	"set-buffer":          {bools: "aw", values: "btn"},
	"paste-buffer":        {bools: "dpr", values: "bst"},
	"delete-buffer":       {values: "b"},
	"list-clients":        {values: "Ft"},
	"detach-client":       {bools: "a", values: "st"},
	"show-options":        {bools: "AgHpqsvw", values: "t"},
	"show-window-options": {bools: "gv", values: "t"},
	"set-option":          {bools: "aFgopqsuUw", values: "t"},
	"set-window-option":   {bools: "aFgoqu", values: "t"},
	"new-session":         {bools: "AdDEPX", values: "cefFnstxy"},
	"last-pane":           {bools: "Z", values: "t"},
	"next-window":         {values: "t"},
	"previous-window":     {values: "t"},
	"break-pane":          {bools: "dP", values: "Fnst"},
	"join-pane":           {bools: "bdfhv", values: "lst"},
	"move-pane":           {bools: "bdfhv", values: "lst"},
	"swap-pane":           {bools: "dDUZ", values: "st"},
	"rename-session":      {values: "t"},
	"show-buffer":         {values: "b"},
	"save-buffer":         {bools: "a", values: "b"},
	"list-buffers":        {values: "Ff"},
	"show-environment":    {bools: "ghs", values: "t"},
	"set-environment":     {bools: "Fghru", values: "t"},
	"run-shell":           {bools: "bC", values: "cdt"},
	"if-shell":            {bools: "bF", values: "t"},
	"wait-for":            {bools: "LSU"},
	"display-popup":       {bools: "BCE", values: "bcdehsStTwxy"},
}

// textCommands carry text as their positional arguments: keys to type or a
// command line to run. The log records how many there were, not what they
// said, since they can hold secrets.
var textCommands = []string{"send-keys", "split-window", "new-window", "respawn-pane", "set-buffer", "new-session",
	"run-shell", "if-shell", "display-popup", "set-environment"}

// redact returns argv (starting "tmux") as the log records it. Only what the
// shim can name is kept: the global flags, the name of a known tmux command,
// and the flags of a command it parses. The positional arguments of text
// commands, the arguments of every command it does not answer, a word it
// could not place and every VAR=value are replaced by a marker, since any of
// them can hold something typed or run.
func redact(argv []string) []string {
	if len(argv) == 0 {
		return argv
	}
	_, words, err := ParseGlobal(argv[1:])
	if err != nil {
		// The globals did not parse, so which word is a flag value, a
		// command or text is unknown. Keep only the words that are flags.
		out := []string{argv[0]}
		for _, w := range argv[1:] {
			if !strings.HasPrefix(w, "-") || strings.Contains(w, "=") {
				w = "<redacted>"
			}
			out = append(out, w)
		}
		return out
	}
	out := append([]string{}, argv[:len(argv)-len(words)]...)
	for i, cmd := range SplitCommands(words) {
		if i > 0 {
			out = append(out, ";")
		}
		name, lookupErr := lookupCommand(cmd[0])
		_, answered := commands[name]
		switch {
		case lookupErr != nil:
			out = append(out, "<unknown command>")
			if len(cmd) > 1 {
				out = append(out, fmt.Sprintf("<%d redacted>", len(cmd)-1))
			}
			continue
		case !answered:
			// Ignored and refused commands, and tmux commands the shim does
			// not answer: the name says what the call was.
			out = append(out, cmd[0])
			if len(cmd) > 1 {
				out = append(out, fmt.Sprintf("<%d redacted>", len(cmd)-1))
			}
			continue
		case !slices.Contains(textCommands, name):
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

// refusedCommands end or replace tuios sessions, which the shim never does:
// the caller's session is not the shim's to end, and another session is out
// of its reach.
var refusedCommands = []string{"kill-session", "kill-server", "attach-session", "switch-client"}

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
	if g.Name != "" || (g.Socket != "" && s.Dir != "" && !IsShimSocket(g.Socket, s.Dir)) {
		return s.fail(full, OutcomeUnsupported, detail, errors.New("the tuios tmux shim answers only for its own server; -L and -S name another one"))
	}
	if g.Version {
		fmt.Fprintf(s.Stdout, "tmux %s\n", Version)
		s.Log.Record(full, OutcomeOK, detail)
		return 0
	}
	if s.Session == "" && !s.AllSessions {
		return s.fail(full, OutcomeError, detail, errors.New("no tuios session: the tmux shim runs in a tuios pane (TUIOS_SESSION is unset)"))
	}
	if g.Control > 0 {
		return s.runControl(full, g, words, detail)
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
		if se, ok := errors.AsType[statusError](err); ok {
			// The command's own status, already reported on stdout.
			s.Log.Record(full, worse(outcome, OutcomeError), detail)
			return se.code
		}
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
	s.Log.Record(argv, outcome, append(detail, logText(err)))
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

func (s *Shim) runOne(word string, args []string) (string, []string, error) {
	name, err := lookupCommand(word)
	if err != nil {
		if strings.HasPrefix(err.Error(), "unknown command") {
			// The word may be text a split left in command position: stderr
			// names it, the log does not.
			err = logAs{err: err, log: "unknown command"}
		}
		return OutcomeUnsupported, nil, err
	}
	if h, ok := commands[name]; ok {
		o, d, err := h(s, name, args)
		if _, ok := errors.AsType[errUnknownFlag](err); ok {
			return OutcomeUnsupported, d, err
		}
		return o, d, err
	}
	// set-option window-size is the one option tuios honours: it is the
	// session's daemon.window_size. Every other option is ignored.
	if name == "set-option" || name == "set-window-option" {
		if opt, value, ok := optionAssignment(name, args); ok && opt == "window-size" {
			o, err := s.setWindowSize(value)
			return o, nil, err
		}
	}
	if slices.Contains(ignoredCommands, name) {
		return OutcomeIgnored, nil, nil
	}
	if slices.Contains(refusedCommands, name) {
		return OutcomeUnsupported, nil, fmt.Errorf("%s: refused, the tuios tmux shim does not start, attach or end sessions", name)
	}
	// A tmux command the shim does not answer, named in full.
	return OutcomeUnsupported, nil, fmt.Errorf("unknown command: %s", name)
}

// logAs is an error printed as err and logged as log, for an error whose
// message holds words the log must not keep.
type logAs struct {
	err error
	log string
}

func (e logAs) Error() string { return e.err.Error() }
func (e logAs) Unwrap() error { return e.err }

// logText is err as the log records it.
func logText(err error) string {
	if la, ok := errors.AsType[logAs](err); ok {
		return la.log
	}
	return err.Error()
}

func (s *Shim) println(line string) { fmt.Fprintln(s.Stdout, line) }

// callerPane is the pane an empty target means: TMUX_PANE, then the caller's
// tuios window, then the focused pane of the default session.
func (s *Shim) callerPane(v *view) *pane {
	// A control client that attached to a session works in that session,
	// as tmux's commands from an attached client do, whatever pane the
	// client itself runs in.
	if s.attached != nil {
		if sv := s.attached(v); sv != nil {
			if p := sv.byWindowID(sv.focused); p != nil && p.Workspace == sv.current {
				return p
			}
			if p := sv.active(sv.current); p != nil {
				return p
			}
		}
	}
	if strings.HasPrefix(s.TmuxPane, "%") {
		if p, err := v.paneByID(s.TmuxPane[1:]); err == nil {
			return p
		}
	}
	if p := v.byWindowID(s.Window); p != nil {
		return p
	}
	if v.def == nil {
		return nil
	}
	if p := v.def.byWindowID(v.def.focused); p != nil {
		return p
	}
	return v.def.active(v.def.current)
}

// expand expands a format and turns missing variables into log detail.
func (s *Shim) expand(format string, vars map[string]string) (string, []string) {
	out, missing := expandWith(format, vars, s.serverVar)
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
		// One word, so an entry that removes a variable (-NAME) is not
		// read as a flag.
		argv = append(argv, "--env="+e)
	}
	argv = append(argv, "--")
	return append(argv, cmd...)
}

// newPane opens a tuios window for split-window and new-window in workspace
// ws of session sess and returns its id.
func (s *Shim) newPane(sess string, ws int, focus bool, cwd string, cmd, env []string) (string, error) {
	if cwd == "" {
		cwd = s.Cwd
	}
	params := map[string]any{
		"session":   sess,
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

// printNew prints the -P line for a pane just made. cwd is the directory it
// was started in: a new pane's shell has not said where it is yet, so
// pane_current_path is cwd until it does.
func (s *Shim) printNew(id, format, cwd string) []string {
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
	vars := s.paneVars(p)
	if vars["pane_current_path"] == "" {
		vars["pane_current_path"] = cmpOr(cwd, s.Cwd)
	}
	out, detail := s.expand(format, vars)
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
	id, err := s.newPane(target.sess.name, target.Workspace, !p.Has('d'), cwd, p.Args, append(s.paneEnvFor(target.sess), p.Values('e')...))
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
	detail := s.printNew(id, format, cwd)
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
	tv, _ := p.Value('t')
	sessRef, win, _, hasSess, _ := splitTarget(tv)
	var sv *sessionView
	switch {
	case !hasSess && win != "" && v.isSession(win):
		sv, _ = v.sessionOf(win)
		win = ""
	case !hasSess && win != "":
		if wsv, _, ok := v.windowByID(win); ok {
			sv = wsv
		}
	}
	if sv == nil {
		dflt := s.callerPane(v)
		if sv, err = v.targetSession(sessRef, hasSess, dflt); err != nil {
			return OutcomeError, nil, err
		}
	}
	ws := 0
	if win != "" {
		n, err := sv.workspaceOf(win)
		if err != nil {
			return OutcomeError, nil, fmt.Errorf("create window failed: tuios has no workspace %s", win)
		}
		if sv.wsCount[n] > 0 {
			return OutcomeError, nil, fmt.Errorf("create window failed: index %d in use", n)
		}
		ws = n
	} else {
		for _, n := range sv.workspace {
			if sv.wsCount[n] == 0 {
				ws = n
				break
			}
		}
		if ws == 0 {
			return OutcomeError, nil, errors.New("create window failed: every tuios workspace already holds panes")
		}
	}
	cwd, _ := p.Value('c')
	id, err := s.newPane(sv.name, ws, !p.Has('d'), cwd, p.Args, append(s.paneEnvFor(sv), p.Values('e')...))
	if err != nil {
		return OutcomeError, nil, err
	}
	if n, ok := p.Value('n'); ok {
		if _, err := s.Caller.Call("set-workspace-name", map[string]any{"session": sv.name, "workspace": ws, "name": n}); err != nil {
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
	detail := s.printNew(id, format, cwd)
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
	if _, err := s.Caller.Call("send-text", map[string]any{"session": target.sess.name, "window": target.ID, "text": text}); err != nil {
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

func (s *Shim) capture(p *pane, source string, styled bool) ([]string, error) {
	raw, err := s.Caller.Call("capture-pane", map[string]any{"session": p.sess.name, "window": p.ID, "source": source, "styled": styled})
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
// fills a paste buffer, which the shim does not do.
func (s *Shim) capturePane(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	if !p.Has('p') {
		return OutcomeUnsupported, nil, errors.New("capture-pane: only -p (print) is supported")
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
	visible, err := s.capture(target, "visible", styled)
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
			recent, err := s.capture(target, "recent", styled)
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
	var vars map[string]string
	dflt := s.callerPane(v)
	if tv == "" && dflt == nil {
		// No pane to describe: a server with no session. The server's own
		// variables (version, socket_path) still expand, as in tmux.
		vars = s.sessionVars(nil)
	} else {
		target, err := v.resolvePane(tv, dflt)
		if err != nil {
			return OutcomeError, nil, err
		}
		vars = s.paneVars(target)
	}
	out, detail := format, []string(nil)
	if !p.Has('l') {
		out, detail = s.expand(format, vars)
	}
	if !p.Has('p') {
		return OutcomeIgnored, detail, nil
	}
	s.println(out)
	return outcomeFor(detail), detail, nil
}

// listPanes prints the panes of the target window, with -s of the target
// session, or with -a of every session.
func (s *Shim) listPanes(name string, args []string) (string, []string, error) {
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
		format = "#{pane_index}: [#{pane_width}x#{pane_height}] #{pane_id}#{?pane_active, (active),}"
		if p.Has('a') || p.Has('s') {
			format = "#{session_name}:#{window_index}." + format
		}
	}
	var list []*pane
	tv, _ := p.Value('t')
	switch {
	case p.Has('a'):
		for _, sv := range v.sessions {
			for _, ws := range sv.windowsInUse() {
				list = append(list, sv.panesOn(ws)...)
			}
		}
	case p.Has('s'):
		sv, _, err := v.resolveWindow(tv, s.callerPane(v))
		if err != nil {
			return OutcomeError, nil, err
		}
		for _, ws := range sv.windowsInUse() {
			list = append(list, sv.panesOn(ws)...)
		}
	default:
		sv, ws, err := v.resolveWindow(tv, s.callerPane(v))
		if err != nil {
			return OutcomeError, nil, err
		}
		list = sv.panesOn(ws)
	}
	var detail []string
	for _, pn := range list {
		out, d := s.expand(format, s.paneVars(pn))
		detail = mergeDetail(detail, d)
		s.println(out)
	}
	return outcomeFor(detail), detail, nil
}

// listWindows prints the workspaces that hold panes: of the target session,
// or with -a of every session.
func (s *Shim) listWindows(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	sessions := v.sessions
	if !p.Has('a') {
		sv := v.def
		if c := s.callerPane(v); c != nil {
			sv = c.sess
		}
		if tv, ok := p.Value('t'); ok && tv != "" {
			named, found := v.sessionOf(strings.TrimSuffix(tv, ":"))
			if !found {
				return OutcomeError, nil, fmt.Errorf("can't find session: %s", tv)
			}
			sv = named
		}
		if sv == nil {
			return OutcomeError, nil, errors.New("no current session")
		}
		sessions = []*sessionView{sv}
	}
	format, ok := p.Value('F')
	if !ok {
		format = "#{window_index}: #{window_name}#{window_flags} (#{window_panes} panes) [#{window_width}x#{window_height}]"
		if p.Has('a') {
			format = "#{session_name}:" + format
		}
	}
	var detail []string
	for _, sv := range sessions {
		for _, ws := range sv.windowsInUse() {
			vars := s.sessionVars(sv)
			s.windowVars(sv, ws, vars)
			if a := sv.active(ws); a != nil {
				vars = s.paneVars(a)
			}
			out, d := s.expand(format, vars)
			detail = mergeDetail(detail, d)
			s.println(out)
		}
	}
	return outcomeFor(detail), detail, nil
}

// listSessions prints the sessions the shim serves.
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
		format = "#{session_name}: #{session_windows} windows#{?session_attached, (attached),}"
	}
	var detail []string
	for _, sv := range v.sessions {
		out, d := s.expand(format, s.sessionVars(sv))
		detail = mergeDetail(detail, d)
		s.println(out)
	}
	return outcomeFor(detail), detail, nil
}

// hasSession succeeds for a session the shim serves and fails for any other.
func (s *Shim) hasSession(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	tv, _ := p.Value('t')
	sess := tv
	if before, _, ok := strings.Cut(tv, ":"); ok {
		sess = before
	}
	if !s.AllSessions {
		if sess == "" || strings.TrimPrefix(sess, "=") == s.Session || sess == "$0" {
			return OutcomeOK, nil, nil
		}
		return OutcomeOK, nil, fmt.Errorf("can't find session: %s", sess)
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	if sess == "" && v.def != nil || v.isSession(sess) {
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
	if _, err := s.Caller.Call("close-window", map[string]any{"session": target.sess.name, "window": target.ID}); err != nil {
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
	sv, ws, err := v.resolveWindow(tv, s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	for _, pn := range sv.panesOn(ws) {
		if _, err := s.Caller.Call("close-window", map[string]any{"session": sv.name, "window": pn.ID}); err != nil {
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
	sess := target.sess.name
	// -T and -P change the pane and return, as they do in tmux: naming a
	// teammate's pane must not move the person's focus.
	if title, ok := p.Value('T'); ok {
		if _, err := s.Caller.Call("set-window", map[string]any{"session": sess, "window": target.ID, "name": title}); err != nil {
			return OutcomeError, nil, err
		}
		return OutcomeOK, nil, nil
	}
	if _, ok := p.Value('P'); ok {
		return OutcomeIgnored, nil, nil
	}
	for _, c := range []byte("LRUD") {
		if p.Has(c) {
			if err := s.selectDirection(target, c); err != nil {
				return OutcomeError, nil, err
			}
			return OutcomeOK, nil, nil
		}
	}
	if _, err := s.Caller.Call("focus-window", map[string]any{"session": sess, "window": target.ID}); err != nil {
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
	sv, ws, err := v.resolveWindow(tv, s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	if _, err := s.Caller.Call("select-workspace", map[string]any{"session": sv.name, "workspace": ws}); err != nil {
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
	sv, ws, err := v.resolveWindow(tv, s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	if _, err := s.Caller.Call("set-workspace-name", map[string]any{"session": sv.name, "workspace": ws, "name": p.Args[0]}); err != nil {
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
	if err := s.mayRespawn(target.ID); err != nil {
		return OutcomeError, nil, fmt.Errorf("respawn pane failed: %w", err)
	}
	cwd, _ := p.Value('c')
	req := RespawnRequest{Command: p.Args, Cwd: cwd, Env: append(s.paneEnvFor(target.sess), p.Values('e')...)}
	send := s.respawn
	if send == nil {
		send = RequestRespawn
	}
	if err := send(s.Dir, target.ID, req); err != nil {
		return OutcomeError, nil, fmt.Errorf("respawn pane failed: %w", err)
	}
	return OutcomeOK, nil, nil
}

// mayRespawn holds respawn-pane to the caller's pane grants. A respawn does
// not go through the daemon: the request goes to the target's holder over its
// socket. It replaces the target's process, which is closing that pane and
// opening another, and the daemon gives both of those (close-window and
// new-window) only to a pane holding admin. So a caller in a pane without
// admin may respawn only its own pane, and the new command there runs with
// that pane's grants. The daemon answers what the caller holds (pane-grants),
// so the answer is the daemon's, not the caller's environment.
//
// A caller in no pane is the person, and a daemon from before pane grants
// (unknown_verb) holds no pane to anything, so both may respawn any pane, as
// before. Any other failure refuses: the check fails closed.
func (s *Shim) mayRespawn(target string) error {
	return respawnGrantsAllow(s.Caller, map[string]any{}, 0, target)
}

// RespawnAllowed is mayRespawn for the pane holder: it asks the daemon about
// the process with peerPID, the one on the other end of the holder's socket,
// rather than about the caller. A process that dials the socket directly
// skips the shim, so the holder makes the check itself. With no pid (a
// platform where the kernel does not give it) there is nothing to ask about,
// and the shim's own check is the only one.
//
// peerStart is the process's start time, read when it connected. The daemon
// answers a pid whose process has another start time as one that cannot be
// read, so a caller that exits and has its pid reused is not judged as the
// process that took the pid.
func RespawnAllowed(c Caller, peerPID int, peerStart uint64, target string) error {
	if peerPID <= 0 {
		return nil
	}
	params := map[string]any{"peer_pid": peerPID}
	if peerStart != 0 {
		params["peer_start"] = peerStart
	}
	return respawnGrantsAllow(c, params, peerPID, target)
}

// respawnGrantsAllow asks pane-grants with params and decides whether the
// process it answers for may respawn target. See mayRespawn. peerPID is the
// pid asked about, 0 when the caller asks about itself.
func respawnGrantsAllow(c Caller, params map[string]any, peerPID int, target string) error {
	if c == nil {
		return errors.New("could not read the caller's grants: no daemon connection")
	}
	raw, err := c.Call("pane-grants", params)
	if err != nil {
		var coded interface{ ErrorCode() string }
		if errors.As(err, &coded) && coded.ErrorCode() == "unknown_verb" {
			return nil
		}
		return fmt.Errorf("could not read the caller's grants: %w", err)
	}
	var pg struct {
		Pane    bool     `json:"pane"`
		Window  string   `json:"window"`
		Grants  []string `json:"grants"`
		Admin   bool     `json:"admin"`
		PeerPID int      `json:"peer_pid"`
	}
	if err := json.Unmarshal(raw, &pg); err != nil {
		return fmt.Errorf("read pane-grants: %w", err)
	}
	if peerPID > 0 && pg.PeerPID != peerPID {
		// A daemon that does not know peer_pid answers about the holder
		// itself, which says nothing about the caller.
		return errors.New("the daemon did not answer about the caller, so the respawn is refused. Restart the daemon (tuios kill-server) to run this build")
	}
	if !pg.Pane || pg.Admin || slices.Contains(pg.Grants, "admin") || (pg.Window != "" && pg.Window == target) {
		return nil
	}
	if pg.Window == "" {
		return errors.New("the caller runs in another pane without the admin grant, and replacing the process of another pane needs admin (see tuios pane-grants)")
	}
	return fmt.Errorf("pane %s holds %s, and replacing the process of another pane needs the admin grant (see tuios pane-grants)",
		PaneID(pg.Window), strings.Join(pg.Grants, ","))
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
