package session

import (
	"encoding/json"
	"os"
	"strings"
)

// new-session is the verb that was missing from the control surface.
//
// Every other verb addresses a session that already exists, so an external
// program could drive a workspace but never set one up: it had to shell out to
// `tuios new --detach` or give up. It creates the session the way the detached
// CLI path does, in the daemon, with no client anywhere, and it creates the
// first window in the same call because a session with no windows is a session
// nothing else can be done to.

// defaultVerbSessionWidth and defaultVerbSessionHeight are the nominal size a
// session gets when the caller names none. They match what `tuios new --detach`
// sends. Nothing is drawn at this size: an attached client replaces it with its
// own viewport, and a detached session only needs a size its PTYs can start at.
const (
	defaultVerbSessionWidth  = 80
	defaultVerbSessionHeight = 24
)

func (d *Daemon) verbNewSession(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Name       string   `json:"name"`
		Width      int      `json:"width"`
		Height     int      `json:"height"`
		Window     *bool    `json:"window"`
		WindowName string   `json:"window_name"`
		Cwd        string   `json:"cwd"`
		Command    []string `json:"command"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}

	name := strings.TrimSpace(p.Name)
	if err := ValidateSessionName(name); err != nil {
		return nil, invalidParam("name", err.Error())
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

	// Creating the first window is the default, because a session with none is
	// one no other verb can address. Passing false is how a caller that means to
	// place every window itself gets an empty session to place them in.
	makeWindow := p.Window == nil || *p.Window

	// The directory and the argv are checked before the session exists, so a
	// caller that mistyped a path does not get a session it then has to clean
	// up. new-window refuses the same two mistakes for the same reason.
	if makeWindow {
		if verr := checkWindowCwd(p.Cwd); verr != nil {
			return nil, verr
		}
		if len(p.Command) > 0 && p.Command[0] == "" {
			return nil, invalidParam("command", "command[0] is the program to exec and cannot be empty")
		}
	}

	// A name already taken is its own answer, not a bad parameter: the caller
	// asked for something reasonable and the daemon already holds it. Retrying
	// with a different name is the whole remedy, so the code says so.
	if name != "" && d.manager.GetSession(name) != nil {
		return nil, hintedVerbError(ErrVerbSessionExists, "session "+name+" already exists", &VerbHint{
			Param:     "name",
			Verb:      "list-sessions",
			Command:   "tuios ls",
			Available: d.sessionNames(),
			Detail:    "the daemon already holds a session by this name. Choose another name, omit name to have one generated, or address the session that exists.",
		})
	}

	sess, err := d.manager.CreateSession(name, &SessionConfig{}, width, height)
	if err != nil {
		// The name check above is racy by nature, so the manager's own refusal
		// is mapped to the same code rather than reported as an internal fault.
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

	out := map[string]any{
		"type":       "session_created",
		"session":    sess.Name,
		"session_id": sess.ID,
		"width":      width,
		"height":     height,
		"windows":    0,
	}
	if !makeWindow {
		return out, nil
	}

	sessionID := sess.ID
	onExit := func(ptyID string) { d.notifyPTYClosed(sessionID, ptyID) }
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{
		Title:     p.WindowName,
		Name:      p.WindowName,
		Cwd:       p.Cwd,
		Focus:     true,
		Command:   p.Command,
		Workspace: 0,
	}, onExit)
	if err != nil {
		// The session is left standing rather than rolled back. It exists, it is
		// listed, and a caller that wants it gone kills it; deleting it here
		// would destroy a session the daemon already announced as created.
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

// checkWindowCwd refuses a directory a shell cannot start in. A PTY falls back
// to the daemon's own directory, which means a mistyped path gives a working
// shell in the wrong place and no way to tell from the response.
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
