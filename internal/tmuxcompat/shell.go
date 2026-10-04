package tmuxcompat

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"time"
)

// run-shell and if-shell.
//
// tmux runs these commands in its server. The shim has no server: it runs
// them itself, as the caller, from the caller's own process, in the caller's
// directory and environment. So they reach nothing the caller could not
// reach by running the command directly, and they do not go through the
// daemon at all. A read-only control client may not run either. Formats in
// the command are expanded first, as in tmux, so a value a program in a pane
// set (a title, say) lands in a shell command unquoted unless the format
// quotes it with #{q:...}, also as in tmux.
//
// Where tmux shows run-shell's output in the target pane's view mode, the
// shim prints it to its standard output, which is where tmux prints it when
// there is no pane to show it in.

// maxShellDepth caps if-shell and run-shell -C nested in each other.
const maxShellDepth = 10

// The two run other commands through runOne, which reads commands, so they
// join the table here rather than in its literal.
func init() {
	commands["run-shell"] = (*Shim).runShell
	commands["if-shell"] = (*Shim).ifShell
}

// statusError ends a call with a given exit status and prints nothing more:
// run-shell's command failed and tmux exits with its status.
type statusError struct {
	code int
	msg  string
}

func (e statusError) Error() string { return e.msg }

// shellVars is the format context of -t, or of the caller's pane.
func (s *Shim) shellVars(p Parsed) (map[string]string, error) {
	tv, _ := p.Value('t')
	v, err := s.loadView()
	if err != nil {
		return nil, err
	}
	dflt := s.callerPane(v)
	if tv == "" && dflt == nil {
		return s.sessionVars(nil), nil
	}
	target, err := v.resolvePane(tv, dflt)
	if err != nil {
		return nil, err
	}
	return s.paneVars(target), nil
}

// shellCommand is a shell command line run with /bin/sh, as tmux's jobs are.
func (s *Shim) shellCommand(line, cwd string) *exec.Cmd {
	c := exec.Command("/bin/sh", "-c", line)
	c.Dir = cmpOr(cwd, s.Cwd)
	return c
}

// runCommands parses a tmux command line and runs each command in it.
func (s *Shim) runCommands(line string) (string, []string, error) {
	if s.depth >= maxShellDepth {
		return OutcomeError, nil, errors.New("too many nested commands")
	}
	s.depth++
	defer func() { s.depth-- }()
	cmds, err := parseCommandLine(line)
	if err != nil {
		return OutcomeError, nil, err
	}
	outcome := OutcomeOK
	var detail []string
	for _, c := range cmds {
		o, d, err := s.runOne(c[0], c[1:])
		detail = mergeDetail(detail, d)
		outcome = worse(outcome, o)
		if err != nil {
			return outcome, detail, err
		}
	}
	return outcome, detail, nil
}

// runShell runs a shell command, or with -C a tmux command line. -d waits
// that many seconds first. -b starts the command and returns without waiting
// for it. Without -b, a command that fails makes the call fail with its exit
// status, as tmux's client does.
func (s *Shim) runShell(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	if len(p.Args) > 1 {
		return OutcomeError, nil, errors.New("too many arguments")
	}
	if d, ok := p.Value('d'); ok {
		secs, err := strconv.ParseFloat(d, 64)
		if err != nil || secs < 0 {
			return OutcomeError, nil, fmt.Errorf("invalid delay time: %s", d)
		}
		time.Sleep(time.Duration(secs * float64(time.Second)))
	}
	if len(p.Args) == 0 {
		return OutcomeOK, nil, nil
	}
	vars, err := s.shellVars(p)
	if err != nil {
		return OutcomeError, nil, err
	}
	line, detail := s.expand(p.Args[0], vars)
	if p.Has('C') {
		o, d, err := s.runCommands(line)
		return worse(o, outcomeFor(detail)), mergeDetail(detail, d), err
	}
	cwd, _ := p.Value('c')
	c := s.shellCommand(line, cwd)
	if p.Has('b') {
		if err := c.Start(); err != nil {
			return OutcomeError, detail, fmt.Errorf("run-shell: %w", err)
		}
		_ = c.Process.Release()
		return outcomeFor(detail), detail, nil
	}
	c.Stdout, c.Stderr = s.Stdout, s.Stderr
	err = c.Run()
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		msg := fmt.Sprintf("'%s' returned %d", line, ee.ExitCode())
		if ee.ExitCode() < 0 {
			msg = fmt.Sprintf("'%s' terminated by a signal", line)
		}
		s.println(msg)
		return OutcomeOK, detail, statusError{code: max(ee.ExitCode(), 1), msg: msg}
	}
	if err != nil {
		return OutcomeError, detail, fmt.Errorf("run-shell: %w", err)
	}
	return outcomeFor(detail), detail, nil
}

// ifShell runs the first command when the shell command succeeds (or with -F
// when the format is true), and the second, if given, when it does not. -b
// is accepted and the shim waits for the shell command anyway, since it has
// no server to finish the job in after it exits.
func (s *Shim) ifShell(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	if len(p.Args) < 2 {
		return OutcomeError, nil, errors.New("too few arguments")
	}
	if len(p.Args) > 3 {
		return OutcomeError, nil, errors.New("too many arguments")
	}
	var detail []string
	if p.Has('b') {
		detail = append(detail, "if-shell -b waited for the shell command")
	}
	vars, err := s.shellVars(p)
	if err != nil {
		return OutcomeError, detail, err
	}
	cond, d := s.expand(p.Args[0], vars)
	detail = mergeDetail(detail, d)
	var ok bool
	if p.Has('F') {
		ok = formatTrue(cond)
	} else {
		c := s.shellCommand(cond, "")
		c.Stdout, c.Stderr = io.Discard, io.Discard
		ok = c.Run() == nil
	}
	line := ""
	switch {
	case ok:
		line = p.Args[1]
	case len(p.Args) == 3:
		line = p.Args[2]
	default:
		return outcomeFor(detail), detail, nil
	}
	o, d, err := s.runCommands(line)
	return worse(o, outcomeFor(detail)), mergeDetail(detail, d), err
}
