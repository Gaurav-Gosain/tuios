package tmuxcompat

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Commands that move the focus or a pane: the directions of select-pane,
// last-pane, next-window and previous-window, break-pane, join-pane and
// move-pane, and rename-session. Each maps onto a verb the CLI already has
// (focus-window, select-workspace, move-window, rename-session), so the
// daemon holds it to the caller's pane grants as it holds the CLI.
// current one, read from the workspace's focus history.
func (s *Shim) lastPane(name string, args []string) (string, []string, error) {
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
	active := sv.active(ws)
	var last *pane
	for _, id := range sv.wsHistory[ws] {
		if q := sv.byWindowID(id); q != nil && q.Workspace == ws && (active == nil || q.ID != active.ID) {
			last = q
			break
		}
	}
	if last == nil {
		return OutcomeError, nil, errors.New("no last pane")
	}
	if _, err := s.Caller.Call("focus-window", map[string]any{"session": sv.name, "window": last.ID}); err != nil {
		return OutcomeError, nil, err
	}
	return OutcomeOK, nil, nil
}

// stepWindow shows the next (step 1) or previous (step -1) workspace that
// holds panes, wrapping around as tmux does.
func (s *Shim) stepWindow(name string, args []string, step int) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	tv, _ := p.Value('t')
	sessRef := strings.TrimSuffix(tv, ":")
	sv, err := v.targetSession(sessRef, sessRef != "", s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	used := sv.windowsInUse()
	i := slices.Index(used, sv.current)
	word := "next"
	if step < 0 {
		word = "previous"
	}
	if len(used) == 0 || len(used) == 1 && i >= 0 {
		return OutcomeError, nil, fmt.Errorf("no %s window", word)
	}
	var ws int
	switch {
	case i < 0 && step > 0:
		ws = used[0]
		for _, n := range used {
			if n > sv.current {
				ws = n
				break
			}
		}
	case i < 0:
		ws = used[len(used)-1]
		for _, n := range slices.Backward(used) {
			if n < sv.current {
				ws = n
				break
			}
		}
	default:
		ws = used[(i+step+len(used))%len(used)]
	}
	if _, err := s.Caller.Call("select-workspace", map[string]any{"session": sv.name, "workspace": ws}); err != nil {
		return OutcomeError, nil, err
	}
	return OutcomeOK, nil, nil
}

func (s *Shim) nextWindow(name string, args []string) (string, []string, error) {
	return s.stepWindow(name, args, 1)
}

func (s *Shim) previousWindow(name string, args []string) (string, []string, error) {
	return s.stepWindow(name, args, -1)
}

// breakPane moves a pane to a window of its own: the lowest empty workspace,
// or the empty one -t names.
func (s *Shim) breakPane(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	sv0, _ := p.Value('s')
	src, err := v.resolvePane(sv0, s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	sv := src.sess
	if len(sv.panesOn(src.Workspace)) == 1 {
		// tmux refuses to break the only pane of a window.
		return OutcomeError, nil, errors.New("can't break with only one pane")
	}
	ws := 0
	if tv, ok := p.Value('t'); ok && tv != "" {
		_, win, _, _, _ := splitTarget(tv)
		n, err := sv.workspaceOf(win)
		if err != nil {
			return OutcomeError, nil, err
		}
		if sv.wsCount[n] > 0 {
			return OutcomeError, nil, fmt.Errorf("index in use: %d", n)
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
			return OutcomeError, nil, errors.New("break pane failed: every tuios workspace already holds panes")
		}
	}
	if _, err := s.Caller.Call("move-window", map[string]any{"session": sv.name, "window": src.ID, "workspace": ws, "follow": !p.Has('d')}); err != nil {
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
	format := cmpOr(valueOr(p, 'F'), "#{session_name}:#{window_index}.#{pane_index}")
	detail := s.printNew(src.ID, format, "")
	return outcomeFor(detail), detail, nil
}

// joinPane moves the -s pane into the window of the -t pane. Where it lands
// in that window is the tuios layout's answer, as for split-window: -b, -f,
// -h, -v and -l are accepted and do not change it. move-pane is the same
// command.
func (s *Shim) joinPane(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	caller := s.callerPane(v)
	srcRef, _ := p.Value('s')
	dstRef, _ := p.Value('t')
	var src, dst *pane
	// tmux's defaults: the source is the marked pane, which tuios does
	// not have, and the target is the current pane.
	if srcRef == "" {
		return OutcomeError, nil, errors.New("no source pane: give one with -s")
	}
	if src, err = v.resolvePane(srcRef, caller); err != nil {
		return OutcomeError, nil, err
	}
	if dst, err = v.resolvePane(dstRef, caller); err != nil {
		return OutcomeError, nil, err
	}
	if src.ID == dst.ID {
		return OutcomeError, nil, errors.New("source and target panes must be different")
	}
	if src.sess != dst.sess {
		return OutcomeError, nil, errors.New("join pane failed: tuios moves a pane only within its session")
	}
	if src.Workspace == dst.Workspace {
		// Already in that window: tmux would move it beside the target,
		// which is the layout's choice in tuios.
		return OutcomeOK, nil, nil
	}
	if _, err := s.Caller.Call("move-window", map[string]any{"session": src.sess.name, "window": src.ID, "workspace": dst.Workspace, "follow": false}); err != nil {
		return OutcomeError, nil, err
	}
	if !p.Has('d') {
		if _, err := s.Caller.Call("focus-window", map[string]any{"session": src.sess.name, "window": src.ID}); err != nil {
			return OutcomeError, nil, err
		}
	}
	return OutcomeOK, nil, nil
}

// swapPane is refused: tuios has no verb that exchanges two panes' places,
// and one that swapped their workspaces only would put the panes where tmux
// would not.
func (s *Shim) swapPane(name string, args []string) (string, []string, error) {
	if _, err := parseFlags(name, specs[name], args); err != nil {
		return OutcomeUnsupported, nil, err
	}
	return OutcomeUnsupported, nil, errors.New("swap-pane: not supported, tuios places panes itself")
}

// renameSession renames the target session (rename-session verb).
func (s *Shim) renameSession(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	if len(p.Args) != 1 {
		return OutcomeError, nil, errors.New("rename-session: give exactly one new name")
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	tv, _ := p.Value('t')
	sessRef := strings.TrimSuffix(tv, ":")
	sv, err := v.targetSession(sessRef, sessRef != "", s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	newName := p.Args[0]
	if v.isSession(newName) && newName != sv.name {
		return OutcomeError, nil, fmt.Errorf("duplicate session: %s", newName)
	}
	if _, err := s.Caller.Call("rename-session", map[string]any{"session": sv.name, "name": newName}); err != nil {
		return OutcomeError, nil, err
	}
	if !s.AllSessions && sv.name == s.Session {
		// The rest of this call names the session by its new name. The
		// old name still reaches it, but a later list would show the new.
		s.Session = newName
	}
	return OutcomeOK, nil, nil
}
