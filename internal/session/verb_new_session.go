package session

import (
	"encoding/json"
	"os"
	"strings"
)

const (
	defaultVerbSessionWidth  = 80
	defaultVerbSessionHeight = 24
)

func (d *Daemon) verbNewSession(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Name       string            `json:"name"`
		Width      int               `json:"width"`
		Height     int               `json:"height"`
		Window     *bool             `json:"window"`
		WindowName string            `json:"window_name"`
		Cwd        string            `json:"cwd"`
		Command    []string          `json:"command"`
		Space      string            `json:"space"`
		Env        map[string]string `json:"env"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}

	name := strings.TrimSpace(p.Name)
	if err := ValidateSessionName(name); err != nil {
		return nil, invalidParam("name", err.Error())
	}
	space, verr := spaceName(p.Space)
	if verr != nil {
		return nil, verr
	}
	if p.Width < 0 || p.Height < 0 {
		return nil, invalidParam("width", "width and height are cell counts and cannot be negative")
	}
	width, height := p.Width, p.Height
	if width == 0 {
		width = defaultVerbSessionWidth
	}
	if height == 0 {
		height = defaultVerbSessionHeight
	}

	makeWindow := p.Window == nil || *p.Window

	if verr := checkWindowCwd(p.Cwd); verr != nil {
		return nil, verr
	}
	env, _, verr := callerEnv(cs, p.Env)
	if verr != nil {
		return nil, verr
	}
	if len(env) > 0 && !makeWindow {
		return nil, invalidParam("env", "env is set in the first window's process, and window is false. Leave env out, or create the window")
	}
	if makeWindow {
		if len(p.Command) > 0 && p.Command[0] == "" {
			return nil, invalidParam("command", "command[0] is the program to exec and cannot be empty")
		}
	}

	if name != "" && d.manager.GetSession(name) != nil {
		return nil, hintedVerbError(ErrVerbSessionExists, "session "+name+" already exists", &VerbHint{
			Param:     "name",
			Verb:      "list-sessions",
			Command:   "tuios ls",
			Available: d.sessionNames(),
			Detail:    "the daemon already holds a session by this name. Choose another name, omit name to have one generated, or address the session that exists.",
		})
	}

	if _, _, verr := d.childWindowBound(cs, "new-session", p.Cwd); verr != nil {
		return nil, verr
	}
	cfg := callerSessionConfig(cs)
	cfg.Space = space
	sess, err := d.manager.CreateSession(name, cfg, width, height)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return nil, hintedVerbError(ErrVerbSessionExists, err.Error(), &VerbHint{
				Param:     "name",
				Verb:      "list-sessions",
				Command:   "tuios ls",
				Available: d.sessionNames(),
			})
		}
		return nil, newVerbError(ErrVerbInternal, "could not create the session: "+err.Error())
	}

	if p.Cwd != "" {
		sess.SetStartDir(absStartDir(p.Cwd))
	}

	out := map[string]any{
		"type":       "session_created",
		"session":    sess.Name(),
		"session_id": sess.ID,
		"width":      width,
		"height":     height,
		"windows":    0,
	}
	if space != "" {
		out["space"] = space
	}
	if !makeWindow {
		return out, nil
	}

	sessionID := sess.ID
	onExit := func(ptyID string) { d.notifyPTYClosed(sessionID, ptyID) }
	win, bverr, err := d.addWindowFor(cs, "new-session", sess, NewWindowOptions{
		Title:     p.WindowName,
		Name:      p.WindowName,
		Cwd:       p.Cwd,
		Focus:     true,
		Command:   p.Command,
		Env:       env,
		Workspace: 0,
	}, onExit)
	if bverr != nil {
		return nil, bverr
	}
	if err != nil {
		return nil, newVerbError(ErrVerbInternal,
			"the session was created but its first window could not start: "+err.Error())
	}

	displayName := win.Title
	if p.WindowName != "" {
		displayName = p.WindowName
	}
	out["windows"] = 1
	out["window_id"] = win.ID
	out["window_name"] = displayName
	out["pty_id"] = win.PTYID
	return out, nil
}

func checkWindowCwd(cwd string) *verbError {
	if cwd == "" {
		return nil
	}
	info, err := os.Stat(cwd)
	switch {
	case err != nil:
		return invalidParam("cwd", "cannot start a shell in "+echoName(cwd)+": "+err.Error())
	case !info.IsDir():
		return invalidParam("cwd", echoName(cwd)+" is not a directory")
	}
	return nil
}

func callerSessionConfig(cs *connState) *SessionConfig {
	cfg := &SessionConfig{}
	if cs != nil {
		cs.mu.Lock()
		if cs.hello != nil {
			cfg.Term = cs.hello.Term
			cfg.ColorTerm = cs.hello.ColorTerm
		}
		cs.mu.Unlock()
	}
	return cfg
}
