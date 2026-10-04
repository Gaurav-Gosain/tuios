package tmuxcompat

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"time"
)

// display-popup opens a tuios popup (the popup verb): a floating pane that
// runs one command and closes when it exits. Like tmux's, the call returns
// when the popup closes, which is what fzf --tmux waits for: it runs fzf in
// the popup and reads the choice from files once tmux returns.
//
// The popup verb needs an attached client, as tmux needs a client to draw a
// popup on. Where tmux places the popup (-x, -y) and how it draws its border
// (-b, -B, -s, -S) are tuios's to decide, so those flags are accepted and
// logged. Without -E a tmux popup stays open after its command exits; a
// tuios popup always closes, and that is logged too. -C, which closes a
// popup from outside, is refused: the popup verb cannot do that.

// timeoutCaller is a Caller that can wait longer than its default for an
// answer. *session.VerbClient is one.
type timeoutCaller interface {
	CallWithTimeout(verb string, params any, timeout time.Duration) (json.RawMessage, error)
}

// popupWait is how long display-popup waits for its popup to close: the
// person is using it, so there is no bound worth guessing.
const popupWait = 24 * time.Hour

func (s *Shim) displayPopup(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	if p.Has('C') {
		return OutcomeUnsupported, nil, errors.New("display-popup -C: not supported, a tuios popup closes when its command exits")
	}
	var detail []string
	for _, c := range []byte("bcsSxy") {
		if _, ok := p.Value(c); ok {
			detail = append(detail, "display-popup -"+string(c)+" ignored: tuios places and draws the popup")
		}
	}
	if p.Has('B') {
		detail = append(detail, "display-popup -B ignored: tuios draws the popup's border")
	}
	if !p.Has('E') {
		detail = append(detail, "display-popup without -E: the popup closes when its command exits")
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

	var argv []string
	switch len(p.Args) {
	case 0:
		argv = []string{cmpOr(s.Shell, "/bin/sh")}
	case 1:
		argv = []string{"/bin/sh", "-c", p.Args[0]}
	default:
		argv = p.Args
	}
	// The environment goes through env(1), since the popup verb takes an
	// argv and no environment.
	if env := append(s.paneEnvFor(target.sess), p.Values('e')...); len(env) > 0 {
		pre := []string{"env"}
		for _, e := range env {
			if len(e) > 1 && e[0] == '-' {
				pre = append(pre, "-u", e[1:])
			} else {
				pre = append(pre, e)
			}
		}
		argv = append(append(pre, "--"), argv...)
	}

	params := map[string]any{
		"session":   target.sess.name,
		"workspace": target.Workspace,
		"command":   argv,
		"wait":      true,
	}
	if w, ok := p.Value('w'); ok {
		params["width"] = w
	}
	if h, ok := p.Value('h'); ok {
		params["height"] = h
	}
	if t, ok := p.Value('T'); ok {
		params["name"] = t
	}
	cwd := s.Cwd
	if d, ok := p.Value('d'); ok {
		cwd = d
		if !filepath.IsAbs(d) && s.Cwd != "" {
			cwd = filepath.Join(s.Cwd, d)
		}
	}
	if cwd != "" {
		params["cwd"] = cwd
	}
	if tc, ok := s.Caller.(timeoutCaller); ok {
		_, err = tc.CallWithTimeout("popup", params, popupWait)
	} else {
		_, err = s.Caller.Call("popup", params)
	}
	if err != nil {
		return OutcomeError, detail, err
	}
	return outcomeFor(detail), detail, nil
}
