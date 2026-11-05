package client

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/guios/git"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// errNotYet answers a method the app has and tuios does not serve yet.
// ErrNeedsTUI is the error of a keymap action only the tuios TUI can run,
// such as one that moves a floating pane, on a session no TUI shows.
var ErrNeedsTUI = errors.New("this action needs the tuios TUI on the session")

func errNotYet(what string) error {
	return errfmt.Errorf("%s is not available with the tuios daemon yet", what)
}

var watchOnce sync.Map // *Client to *sync.Once

// dispatch serves one of the app's methods with the daemon's verbs.
func (c *Client) dispatch(method string, params any) (any, error) {
	switch method {
	case "subscribe":
		setCurrent(c)
		once, _ := watchOnce.LoadOrStore(c, &sync.Once{})
		once.(*sync.Once).Do(func() { go c.st.watch() })
		st, err := c.st.build()
		if err != nil {
			return nil, err
		}
		go c.emit(proto.Message{Event: "state", State: st})
		return true, nil

	case "state":
		return c.st.build()

	case "view":
		p, err := decode[proto.ViewParams](params)
		if err != nil {
			return nil, err
		}
		c.st.seen(p.ID)
		c.setView(p.ID)
		return true, nil

	case "create":
		p, err := decode[proto.CreateParams](params)
		if err != nil {
			return nil, err
		}
		if p.Split != "" {
			return c.split(p)
		}
		return c.create(p)

	case "split":
		p, err := decode[proto.CreateParams](params)
		if err != nil {
			return nil, err
		}
		return c.split(p)

	case "close":
		p, err := decode[proto.CloseParams](params)
		if err != nil {
			return nil, err
		}
		_, err = c.verb("kill-session", map[string]any{"session": c.st.nameOf(p.ID)})
		return true, err

	case "pane.close":
		p, err := decode[proto.IDParams](params)
		if err != nil {
			return nil, err
		}
		sid, name, ok := c.st.lookup(p.ID)
		if !ok {
			return nil, errors.New("no such pane")
		}
		_, err = c.verb("close-window", map[string]any{"session": name, "window": p.ID})
		_ = sid
		return true, err

	case "rename":
		p, err := decode[proto.RenameParams](params)
		if err != nil {
			return nil, err
		}
		_, err = c.verb("rename-session", map[string]any{"session": c.st.nameOf(p.ID), "name": p.Name})
		return true, err

	case "verb":
		p, err := decode[proto.VerbParams](params)
		if err != nil {
			return nil, err
		}
		args := map[string]any{}
		for k, v := range p.Args {
			args[k] = v
		}
		if p.Session != "" {
			args["session"] = c.st.nameOf(p.Session)
		}
		raw, err := c.verb(p.Verb, args)
		if err != nil {
			return nil, err
		}
		return raw, nil

	case "pane.rename":
		p, err := decode[proto.RenameParams](params)
		if err != nil {
			return nil, err
		}
		_, name, ok := c.st.lookup(p.ID)
		if !ok {
			return nil, errors.New("no such pane")
		}
		_, err = c.verb("set-window", map[string]any{"session": name, "window": p.ID, "name": p.Name})
		return true, err

	case "workspace.rename":
		p, err := decode[proto.NameWorkspaceParams](params)
		if err != nil {
			return nil, err
		}
		_, err = c.verb("set-workspace-name", map[string]any{"session": c.st.nameOf(p.Session), "workspace": p.Workspace, "name": p.Name})
		return true, err

	case "note":
		p, err := decode[proto.NoteParams](params)
		if err != nil {
			return nil, err
		}
		args := map[string]any{"session": c.st.nameOf(p.Session), "note": p.Note}
		if p.Pane != "" {
			args["window"] = p.Pane
		}
		_, err = c.verb("set-note", args)
		return true, err

	case "focus":
		p, err := decode[proto.FocusParams](params)
		if err != nil {
			return nil, err
		}
		c.setFocus(p.Session, p.Pane)
		_, err = c.verb("focus-window", map[string]any{"session": c.st.nameOf(p.Session), "window": p.Pane})
		return true, err

	case "zoom":
		p, err := decode[proto.FocusParams](params)
		if err != nil {
			return nil, err
		}
		return true, c.zoom(p.Session, p.Pane)

	case "layout":
		p, err := decode[proto.LayoutParams](params)
		if err != nil {
			return nil, err
		}
		c.st.setLayout(p.Session, p.Layout)
		if !c.writeTree(p.Session, p.Layout) {
			c.kickState()
		}
		return true, nil

	case "grid":
		p, err := decode[proto.GridParams](params)
		if err != nil {
			return nil, err
		}
		if setGrid(p.Cols, p.Rows) {
			c.kickState()
		}
		return true, nil

	case "arrange":
		p, err := decode[proto.ArrangeParams](params)
		if err != nil {
			return nil, err
		}
		return c.arrangeOp(p)

	case "scratch":
		// The TUI's scratch terminal (toggle_scratch): the built-in scratch
		// group, which the daemon puts on a scratch workspace and focuses.
		p, err := decode[proto.ScratchParams](params)
		if err != nil {
			return nil, err
		}
		args := map[string]any{"session": c.st.nameOf(p.Session), "scratch": true, "scratch_name": config.DefaultScratchName}
		switch {
		case p.From != "":
			// The daemon reads the pane's folder now: guios's copy of it
			// is from the last state, which a cd does not send.
			args["cwd_from"] = p.From
		case p.Dir != "":
			args["cwd"] = p.Dir
		}
		_, err = c.verb("popup", args)
		return true, err

	case "workspace":
		p, err := decode[proto.WorkspaceParams](params)
		if err != nil {
			return nil, err
		}
		if p.Pane != "" {
			_, name, ok := c.st.lookup(p.Pane)
			if !ok {
				return nil, errors.New("no such pane")
			}
			_, err = c.verb("move-window", map[string]any{"session": name, "window": p.Pane, "workspace": p.Workspace})
			return true, err
		}
		_, err = c.verb("select-workspace", map[string]any{"session": c.st.nameOf(p.Session), "workspace": p.Workspace})
		return true, err

	case "command":
		p, err := decode[proto.CommandParams](params)
		if err != nil {
			return nil, err
		}
		_, err = c.verb("run-command", map[string]any{"session": c.st.nameOf(p.Session), "command": p.Command})
		var ve *session.VerbCallError
		if errors.As(err, &ve) && ve.Code == session.ErrVerbNeedsClient {
			return nil, ErrNeedsTUI
		}
		return true, err

	case "move", "project.rename", "project.close", "project.move":
		return true, nil

	case "project.create":
		p, err := decode[proto.ProjectParams](params)
		if err != nil {
			return nil, err
		}
		id := p.Name
		if id == "" {
			id = defaultProject
		}
		return proto.Project{ID: c.projectID(id), Name: p.Name, Dir: p.Dir}, nil

	case "restart":
		return nil, errNotYet("restarting a pane")

	case "worktree.check":
		return proto.WorktreeCheck{}, nil

	case "conversations":
		return c.conversations()

	case "conversation.resume":
		p, err := decode[proto.ConversationParams](params)
		if err != nil {
			return nil, err
		}
		return c.resumeConversation(p)

	case "conversation.archive":
		p, err := decode[proto.ConversationParams](params)
		if err != nil {
			return nil, err
		}
		setArchived(p.Agent, p.ID, p.Archived)
		return true, nil

	case "conversation.forget":
		p, err := decode[proto.ConversationParams](params)
		if err != nil {
			return nil, err
		}
		setArchived(p.Agent, p.ID, false)
		_, err = c.verb("forget-conversation", map[string]any{"harness": harnessOf(p.Agent), "id": p.ID})
		return true, err

	case "browser.answer":
		return true, nil

	case "shutdown":
		return nil, errors.New("guios leaves the daemon running. Stop it with: tuios kill-server")

	case "fs.list":
		p, err := decode[proto.FSListParams](params)
		if err != nil {
			return nil, err
		}
		if c.host != "" {
			return c.remoteFSList(p)
		}
		return fsList(p)
	case "fs.stat":
		p, err := decode[proto.FSStatParams](params)
		if err != nil {
			return nil, err
		}
		if c.host != "" {
			return c.remoteFSStat(p)
		}
		return fsStat(p), nil
	case "fs.read":
		p, err := decode[proto.FSReadParams](params)
		if err != nil {
			return nil, err
		}
		if c.host != "" {
			return c.remoteFSRead(p)
		}
		return fsRead(p)
	case "fs.create", "fs.rename", "fs.delete", "fs.write", "paste.file":
		if c.host != "" {
			return nil, c.errRemoteWrite()
		}
		return c.localWrite(method, params)
	case "git.changes", "git.file", "git.revert":
		return c.gitDispatch(method, params)
	}
	return nil, errfmt.Errorf("unknown method %q", method)
}

// localWrite serves the methods that write files on this machine.
func (c *Client) localWrite(method string, params any) (any, error) {
	switch method {
	case "fs.create":
		p, err := decode[proto.FSCreateParams](params)
		if err != nil {
			return nil, err
		}
		return true, fsCreate(p)
	case "fs.rename":
		p, err := decode[proto.FSRenameParams](params)
		if err != nil {
			return nil, err
		}
		return true, fsRename(p)
	case "fs.delete":
		p, err := decode[proto.FSDeleteParams](params)
		if err != nil {
			return nil, err
		}
		return true, fsDelete(p)
	case "fs.write":
		p, err := decode[proto.FSWriteParams](params)
		if err != nil {
			return nil, err
		}
		return fsWrite(p)
	case "paste.file":
		p, err := decode[proto.PasteFileParams](params)
		if err != nil {
			return nil, err
		}
		return pasteFile(p)
	}
	return nil, errfmt.Errorf("unknown method %q", method)
}

func (c *Client) gitDispatch(method string, params any) (any, error) {
	switch method {
	case "git.changes", "git.file", "git.revert":
		if c.host != "" {
			return nil, errfmt.Errorf("changes of a pane on %s are not available yet", c.host)
		}
		return c.gitCall(method, params)
	}
	return nil, errfmt.Errorf("unknown method %q", method)
}

// kickState has the state sent anew, as after an event.
func (c *Client) kickState() {
	select {
	case c.st.kick <- struct{}{}:
	default:
	}
}

// create makes a session with its first pane.
func (c *Client) create(p proto.CreateParams) (proto.Session, error) {
	params := map[string]any{"window": true}
	if p.Name != "" {
		params["name"] = p.Name
	}
	if p.Dir != "" {
		params["cwd"] = p.Dir
	}
	if len(p.Command) > 0 {
		params["command"] = p.Command
	}
	if env := paneEnv(); env != nil && c.host == "" {
		// A linked machine refuses env from a link.
		params["env"] = env
	}
	if p.Cols > 0 && p.Rows > 0 {
		params["width"], params["height"] = p.Cols, p.Rows
	}
	raw, err := c.verb("new-session", params)
	if err != nil {
		return proto.Session{}, err
	}
	var res struct {
		Name string `json:"session"`
		ID   string `json:"session_id"`
	}
	_ = jsonx.Unmarshal(raw, &res)
	if space := c.spaceOf(p.Project); space != "" && space != defaultProject {
		_, _ = c.verb("set-session-space", map[string]any{"session": res.Name, "space": space})
	}
	return c.sessionNamed(res.Name, res.ID)
}

// split makes a pane beside one of a session's.
func (c *Client) split(p proto.CreateParams) (proto.Session, error) {
	sid, name, ok := c.st.lookup(p.Split)
	if !ok {
		return proto.Session{}, errors.New("no such session or pane")
	}
	beside := p.Split
	if beside == sid {
		beside = ""
	}
	params := map[string]any{"session": name}
	dir := p.Dir
	if dir == "" && beside != "" && p.Host == "" {
		// A pane on another machine starts in its home there: this
		// pane's folder is this machine's.
		dir = c.paneDir(sid, beside)
	}
	if p.Host != "" {
		params["host"] = p.Host
	}
	if dir != "" {
		params["cwd"] = dir
	}
	if len(p.Command) > 0 {
		params["command"] = p.Command
	}
	if env := paneEnv(); env != nil && c.host == "" && p.Host == "" {
		params["env"] = env
	}
	// The side the new pane takes, or "" for the side the layout picks:
	// tuios's auto scheme in the split tree.
	dirn := p.Direction
	c.st.mu.Lock()
	ti := c.st.trees[sid]
	if beside == "" {
		for _, w := range c.st.shown[sid] {
			if w.Focused {
				beside = w.ID
			}
		}
	}
	c.st.mu.Unlock()
	// A TUI on the session places a new pane in the workspace's layout
	// itself, so the split goes through it: a tree written here would race
	// the one it writes for the new pane. A pane the daemon makes splits
	// the focused pane there, so the focus goes first.
	if ti.tui && beside != "" && len(p.Command) == 0 && p.Ratio == 0 && p.Host == "" {
		if dirn != "" && ti.useTree {
			if _, err := c.verb("split-window", map[string]any{"session": name, "window": beside, "side": dirn}); err == nil {
				return c.sessionNamed(name, sid)
			}
		}
		_, _ = c.verb("focus-window", map[string]any{"session": name, "window": beside})
	}
	if !ti.tui && beside != "" {
		// layoutOf puts the pane in the tree when it shows up, as the
		// TUI's insertSyncedWindow does with its pending split.
		c.st.mu.Lock()
		c.st.pending[sid] = pendingSplit{target: beside, dir: preselection(dirn), at: time.Now()}
		c.st.mu.Unlock()
	}
	if p.Cols > 0 && p.Rows > 0 && p.Host == "" {
		// The pane starts at the size it will show at, so its shell
		// prints its first prompt at it (QA-011).
		params["cols"], params["rows"] = p.Cols, p.Rows
	}
	_, err := c.verb("new-window", params)
	var ve *session.VerbCallError
	if errors.As(err, &ve) && ve.Code == session.ErrVerbInvalidParams && params["cols"] != nil {
		// A daemon from before cols and rows refuses them: the pane
		// starts at the session's size there, as it always did.
		delete(params, "cols")
		delete(params, "rows")
		_, err = c.verb("new-window", params)
	}
	if err != nil {
		return proto.Session{}, err
	}
	return c.sessionNamed(name, sid)
}

// zoom zooms a pane, or unzooms it when it is the zoomed one. A TUI on the
// session zooms it there, so every client shows the zoom; with none, the
// zoom is guios's alone.
func (c *Client) zoom(sid, pane string) error {
	c.st.mu.Lock()
	ti := c.st.trees[sid]
	cur := ti.zoomByTUI
	if cur == "" {
		cur = c.st.zoom[sid]
	}
	name := c.st.sessionName[sid]
	c.st.mu.Unlock()
	if ti.tui {
		target := pane
		if cur != "" && cur != pane {
			// Unzoom the zoomed one first, then zoom this one.
			if _, err := c.verb("focus-window", map[string]any{"session": name, "window": cur}); err != nil {
				return err
			}
			if _, err := c.verb("run-command", map[string]any{"session": name, "command": "ToggleZoom"}); err != nil {
				return err
			}
		}
		if _, err := c.verb("focus-window", map[string]any{"session": name, "window": target}); err != nil {
			return err
		}
		_, err := c.verb("run-command", map[string]any{"session": name, "command": "ToggleZoom"})
		return err
	}
	c.st.mu.Lock()
	if cur == pane {
		delete(c.st.zoom, sid)
	} else {
		c.st.zoom[sid] = pane
	}
	c.st.mu.Unlock()
	c.kickState()
	return nil
}

// sessionNamed reads the state and returns one session of it.
func (c *Client) sessionNamed(name, id string) (proto.Session, error) {
	st, err := c.st.build()
	if err != nil {
		return proto.Session{}, err
	}
	for _, s := range st.Sessions {
		if s.ID == id || s.Name == name {
			return s, nil
		}
	}
	return proto.Session{}, errfmt.Errorf("the session %q did not appear", name)
}

func (c *Client) paneDir(sid, pane string) string {
	c.st.mu.Lock()
	st := c.st.state
	c.st.mu.Unlock()
	if st == nil {
		return ""
	}
	for _, s := range st.Sessions {
		if s.ID == sid {
			if p := s.PaneByID(pane); p != nil {
				return p.WorkDir()
			}
		}
	}
	return ""
}

// gitCall serves git.changes, git.file and git.revert in this process, on
// the pane's working directory. A turn's base is the agent's checkpoint in
// tuios, which comes later; head and branch work now.
func (c *Client) gitCall(method string, params any) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var pane, base string
	switch method {
	case "git.revert":
		p, err := decode[proto.GitWriteParams](params)
		if err != nil {
			return nil, err
		}
		pane, base = p.Pane, p.Base
		r, rev, _, err := c.repoOf(ctx, pane, base)
		if err != nil {
			return nil, err
		}
		changed, err := r.Changed(ctx, rev, p.Path)
		if err != nil {
			return nil, err
		}
		if !changed {
			return nil, errfmt.Errorf("%s has no changes to revert", p.Path)
		}
		if err := r.Write(p.Path, p.Hash, p.Text); err != nil {
			return nil, err
		}
		return git.Hash([]byte(p.Text)), nil
	}
	p, err := decode[proto.GitParams](params)
	if err != nil {
		return nil, err
	}
	r, rev, name, err := c.repoOf(ctx, p.Pane, p.Base)
	if err != nil {
		return nil, err
	}
	if method == "git.changes" {
		files, err := r.Changes(ctx, rev, "")
		if err != nil {
			return nil, err
		}
		res := proto.GitChanges{Root: r.Root, BaseName: name, Files: make([]proto.GitFile, 0, len(files))}
		for _, f := range files {
			res.Files = append(res.Files, proto.GitFile{Path: f.Path, OldPath: f.OldPath, Status: f.Status, Added: f.Added, Removed: f.Removed, Binary: f.Binary})
		}
		return res, nil
	}
	t, err := r.FileText(ctx, rev, p.Path, p.OldPath)
	if err != nil {
		return nil, err
	}
	if p.Known != "" && p.Known == t.Hash {
		return proto.GitText{Hash: t.Hash, Same: true}, nil
	}
	return proto.GitText{Old: t.Old, New: t.New, Hash: t.Hash, Binary: t.Binary, Large: t.Large, Patch: t.Patch}, nil
}

func (c *Client) repoOf(ctx context.Context, pane, base string) (git.Repo, string, string, error) {
	sid, _, ok := c.st.lookup(pane)
	if !ok {
		return git.Repo{}, "", "", errfmt.Errorf("no session or pane %q", pane)
	}
	dir := c.paneDir(sid, pane)
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	if base == git.BaseTurn {
		return git.Repo{}, "", "", errNotYet("changes since the agent's last turn")
	}
	r, err := git.Open(ctx, dir)
	if err != nil {
		return git.Repo{}, "", "", err
	}
	rev, name, err := r.Base(ctx, base, "")
	return r, rev, name, err
}

// envMu guards env, the variables the app adds to every new pane's
// environment (the Environment settings).
var (
	envMu sync.Mutex
	env   map[string]string
)

// SetPaneEnv sets the variables every new pane gets, as NAME=value.
func SetPaneEnv(kvs []string) {
	m := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
			m[k] = v
		}
	}
	envMu.Lock()
	env = m
	envMu.Unlock()
}

// paneEnv is what SetPaneEnv set, or nil for none.
func paneEnv() map[string]string {
	envMu.Lock()
	defer envMu.Unlock()
	if len(env) == 0 {
		return nil
	}
	return env
}
