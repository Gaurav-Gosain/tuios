package tmuxcompat

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

const maxShellDepth = 10

func init() {
	commands["run-shell"] = (*Shim).runShell
	commands["if-shell"] = (*Shim).ifShell
}

type statusError struct {
	code int
	msg  string
}

func (e statusError) Error() string { return e.msg }

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

func (s *Shim) shellCommand(line, cwd string) *exec.Cmd {
	c := exec.Command("/bin/sh", "-c", line)
	c.Dir = cmpOr(cwd, s.Cwd)
	return c
}

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
			return OutcomeError, nil, errfmt.Errorf("invalid delay time: %s", d)
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
			return OutcomeError, detail, errfmt.Errorf("run-shell: %w", err)
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
		return OutcomeError, detail, errfmt.Errorf("run-shell: %w", err)
	}
	return outcomeFor(detail), detail, nil
}

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
