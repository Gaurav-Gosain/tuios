package session

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// The worktree verbs: a git worktree as a session, and one prompt fanned out
// across several of them.
//
// new-worktree makes the worktree and the session in one call, so a caller
// never holds a worktree with no session or a session in a directory that is
// not there. list-worktrees is the listing the CLI and an orchestrating agent
// read. remove-worktree is the one destructive verb, and it refuses to
// discard uncommitted work unless told to in so many words. fan is
// new-worktree n times with an agent started in each and the prompt typed at
// it once the agent is ready to read.

// Error codes the worktree verbs raise, on top of the shared ones.
const (
	// ErrVerbNotWorktree reports a session that is not in a git worktree, so
	// there is nothing to remove or diff.
	ErrVerbNotWorktree = "not_worktree"
	// ErrVerbWorktreeDirty reports a removal refused because the worktree holds
	// uncommitted changes and the caller did not say what to do with them.
	// Nothing was removed. The remedy is stash or force, and the hint says so.
	ErrVerbWorktreeDirty = "worktree_dirty"
	// ErrVerbGitFailed reports a git command that failed, with git's own
	// message. It is final: the repository is as it was.
	ErrVerbGitFailed = "git_failed"
)

// fanDefaultReadyTimeout bounds how long a fan-out waits for an agent to be
// ready before it gives up on typing the prompt. It is long because the wait
// covers a person answering a first-run question in the pane.
const fanDefaultReadyTimeout = 10 * time.Minute

// fanMaxCount bounds a fan-out. Every worktree is a full checkout and every
// agent is a process, and a count typed by mistake should not fill a disk.
const fanMaxCount = 16

// fanReadyStates are the states in which the fan-out types its prompt. idle
// and done are an agent at its prompt. unknown is what the silence timer
// writes to an agent that reports nothing, which is most of them, once it has
// stopped drawing. needs_input is not here: an agent asking to trust the
// folder must be answered by the person, and the prompt is typed after.
var fanReadyStates = map[string]bool{
	AgentStateIdle.Name():    true,
	AgentStateDone.Name():    true,
	AgentStateUnknown.Name(): true,
}

// worktreeTarget is what every worktree verb needs from a session: the
// session itself and its record, or the error saying it has none.
func (d *Daemon) worktreeTarget(name string) (*Session, *WorktreeInfo, *verbError) {
	sess, verr := d.resolveVerbSession(name)
	if verr != nil {
		return nil, nil, verr
	}
	info := sess.Worktree()
	if info == nil {
		return nil, nil, hintedVerbError(ErrVerbNotWorktree, "session "+sess.Name+" is not in a git worktree", &VerbHint{
			Param:     "session",
			Verb:      "list-worktrees",
			Command:   "tuios worktree ls",
			Available: d.worktreeSessionNames(),
		})
	}
	return sess, info, nil
}

// worktreeSessionNames lists the sessions that are in a worktree.
func (d *Daemon) worktreeSessionNames() []string {
	var out []string
	for _, s := range d.manager.ListSessions() {
		if s.Worktree != nil {
			out = append(out, s.Name)
		}
	}
	return out
}

// repoRootParam resolves the repo parameter, a directory inside the
// repository, to the main checkout.
func repoRootParam(dir string) (string, *verbError) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", invalidParam("repo", "repo is required: a directory inside the git repository")
	}
	root, err := worktree.Root(dir)
	if err != nil {
		return "", hintedVerbError(ErrVerbGitFailed, err.Error(), &VerbHint{
			Param:  "repo",
			Detail: "repo must be a directory inside a git repository, in its main checkout or in one of its worktrees.",
		})
	}
	return root, nil
}

// createWorktreeSession is what new-worktree and fan share: the worktree, then
// the session in it, then the record that ties them. The worktree is never
// removed on a later failure. It exists on disk and git lists it, and taking
// it away because a session failed to start would be removing something the
// caller asked for and can still use.
func (d *Daemon) createWorktreeSession(root, branch, base, sessionName string, command []string, record func(*WorktreeInfo)) (map[string]any, *verbError) {
	path := worktree.PathFor(worktree.DefaultDir(), root, branch)
	if _, err := os.Lstat(path); err == nil {
		return nil, hintedVerbError(ErrVerbGitFailed, path+" already exists", &VerbHint{
			Param:  "branch",
			Detail: "A worktree for this branch is already there. Attach to its session, or remove it with remove-worktree first.",
		})
	}
	if sessionName == "" {
		sessionName = worktree.SessionName(filepath.Base(root), branch)
	}
	if err := ValidateSessionName(sessionName); err != nil {
		return nil, invalidParam("name", err.Error())
	}
	if d.manager.GetSession(sessionName) != nil {
		return nil, hintedVerbError(ErrVerbSessionExists, "session "+sessionName+" already exists", &VerbHint{
			Param:     "name",
			Command:   "tuios ls",
			Available: d.sessionNames(),
			Detail:    "Choose another session name with name, or attach to the session that exists.",
		})
	}

	created, err := worktree.Add(root, path, branch, base)
	if err != nil {
		return nil, hintedVerbError(ErrVerbGitFailed, err.Error(), &VerbHint{
			Param:  "branch",
			Detail: "git refused to add the worktree. The repository is as it was.",
		})
	}

	sess, err := d.manager.CreateSession(sessionName, &SessionConfig{}, defaultVerbSessionWidth, defaultVerbSessionHeight)
	if err != nil {
		return nil, hintedVerbError(ErrVerbInternal, "the worktree was created at "+path+" but its session could not: "+err.Error(), &VerbHint{
			Detail: "The worktree is kept. Start a session in it with: tuios new-window --cwd " + path,
		})
	}
	info := &WorktreeInfo{
		Info: worktree.Info{
			Repo:     filepath.Base(root),
			RepoRoot: root,
			Branch:   branch,
			Path:     path,
		},
		Base:    base,
		Managed: true,
	}
	if record != nil {
		record(info)
	}
	// The record goes on before the window, so a client that adopts the
	// session on the window's push already sees it under its repository.
	_ = sess.SetWorktree(info)

	sessionID := sess.ID
	onExit := func(ptyID string) { d.notifyPTYClosed(sessionID, ptyID) }
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{
		Cwd:     path,
		Focus:   true,
		Command: command,
	}, onExit)
	if err != nil {
		return nil, hintedVerbError(ErrVerbInternal, "the worktree and session were created but the first window could not start: "+err.Error(), &VerbHint{
			Detail: "Both are kept. Open a window in the session with: tuios new-window -s " + sessionName + " --cwd " + path,
		})
	}
	return map[string]any{
		"session":        sess.Name,
		"session_id":     sess.ID,
		"repo":           info.Repo,
		"repo_root":      root,
		"branch":         branch,
		"created_branch": created,
		"path":           path,
		"window_id":      win.ID,
		"pty_id":         win.PTYID,
	}, nil
}

func (d *Daemon) verbNewWorktree(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Repo    string   `json:"repo"`
		Branch  string   `json:"branch"`
		Base    string   `json:"base"`
		Name    string   `json:"name"`
		Command []string `json:"command"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	branch := strings.TrimSpace(p.Branch)
	if err := worktree.ValidBranch(branch); err != nil {
		return nil, invalidParam("branch", err.Error())
	}
	if len(p.Command) > 0 && p.Command[0] == "" {
		return nil, invalidParam("command", "command[0] is the program to exec and cannot be empty")
	}
	root, verr := repoRootParam(p.Repo)
	if verr != nil {
		return nil, verr
	}
	out, verr := d.createWorktreeSession(root, branch, strings.TrimSpace(p.Base), strings.TrimSpace(p.Name), p.Command, nil)
	if verr != nil {
		return nil, verr
	}
	out["type"] = "worktree_created"
	return out, nil
}

func (d *Daemon) verbListWorktrees(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Repo    string `json:"repo"`
		Group   string `json:"group"`
		Changes bool   `json:"changes"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	rows := make([]map[string]any, 0)
	for _, s := range d.listSessions() {
		wt := s.Worktree
		if wt == nil {
			continue
		}
		if p.Repo != "" && wt.Repo != p.Repo {
			continue
		}
		if p.Group != "" && wt.Group != p.Group {
			continue
		}
		states := make([]string, 0, len(s.Windows))
		harnessID := ""
		for _, w := range s.Windows {
			states = append(states, w.AgentState)
			if harnessID == "" {
				harnessID = w.AgentHarness
			}
		}
		state := sessiontree.RollUpState(states)
		if state == "" {
			state = AgentStateNone.Name()
		}
		row := map[string]any{
			"session":       s.Name,
			"repo":          wt.Repo,
			"repo_root":     wt.RepoRoot,
			"branch":        wt.Branch,
			"path":          wt.Path,
			"base":          wt.Base,
			"group":         wt.Group,
			"managed":       wt.Managed,
			"gone":          wt.Gone,
			"state":         state,
			"harness":       harnessID,
			"windows":       s.WindowCount,
			"attached":      s.Attached,
			"prompt_status": wt.PromptStatus,
			"prompt_note":   wt.PromptNote,
		}
		if p.Changes && !wt.Gone {
			// A git status per worktree, only when asked for: the rail never
			// asks, and a listing an agent polls should not run git it did not
			// want.
			if n, err := worktree.Changes(wt.Path); err == nil {
				row["changes"] = n
			} else {
				row["changes"] = -1
			}
			if wt.Base != "" {
				if n, err := worktree.Ahead(wt.Path, wt.Base); err == nil {
					row["ahead"] = n
				}
			}
		}
		rows = append(rows, row)
	}
	return map[string]any{
		"type":      "worktree_list",
		"worktrees": rows,
		"total":     len(rows),
	}, nil
}

func (d *Daemon) verbRemoveWorktree(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session     string `json:"session"`
		Force       bool   `json:"force"`
		Stash       bool   `json:"stash"`
		KeepSession bool   `json:"keep_session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Session == "" {
		return nil, hintedVerbError(ErrVerbInvalidParams,
			"session is required (remove-worktree never guesses which worktree to remove)",
			&VerbHint{Param: "session", Command: "tuios worktree ls", Available: d.worktreeSessionNames()})
	}
	sess, info, verr := d.worktreeTarget(p.Session)
	if verr != nil {
		return nil, verr
	}

	out := map[string]any{
		"type":        "worktree_removed",
		"session":     sess.Name,
		"branch":      info.Branch,
		"path":        info.Path,
		"repo":        info.Repo,
		"branch_kept": true,
		"changes":     0,
		"stashed":     false,
		"discarded":   false,
	}

	if _, err := os.Stat(info.Path); err != nil {
		// The directory is already gone. Git still lists the worktree, and
		// forgetting it is "git worktree prune", which this daemon never runs:
		// prune forgets every worktree whose directory is missing, not only
		// this one, and a directory can be missing because it is being moved.
		out["gone"] = true
		out["note"] = "The directory was already gone. Git still lists the worktree. Run 'git worktree prune' in " + info.RepoRoot + " to forget it."
	} else {
		changes, err := worktree.Changes(info.Path)
		if err != nil {
			return nil, hintedVerbError(ErrVerbGitFailed, err.Error(), &VerbHint{
				Detail: "git could not read the worktree's status, so nothing was removed.",
			})
		}
		out["changes"] = changes
		if changes > 0 && !p.Force && !p.Stash {
			return nil, hintedVerbError(ErrVerbWorktreeDirty,
				fmt.Sprintf("%s holds %d uncommitted %s. Nothing was removed.", info.Path, changes, plural(changes, "change", "changes")),
				&VerbHint{
					Param:   "stash",
					Command: "tuios worktree rm " + sess.Name + " --stash",
					Detail:  "Pass stash to keep the changes in git stash, or force to discard them. The branch " + info.Branch + " is kept either way.",
				})
		}
		if changes > 0 && p.Stash {
			if err := worktree.Stash(info.Path, "tuios: "+info.Branch); err != nil {
				return nil, hintedVerbError(ErrVerbGitFailed, err.Error(), &VerbHint{
					Detail: "git could not stash the changes, so nothing was removed.",
				})
			}
			out["stashed"] = true
			out["stash_message"] = "tuios: " + info.Branch
		}
		discard := changes > 0 && !p.Stash
		if err := worktree.Remove(info.RepoRoot, info.Path, discard); err != nil {
			return nil, hintedVerbError(ErrVerbGitFailed, err.Error(), &VerbHint{
				Detail: "git refused to remove the worktree. It is still there.",
			})
		}
		out["discarded"] = discard
	}

	out["session_killed"] = false
	if !p.KeepSession {
		if err := d.manager.DeleteSession(sess.Name); err == nil {
			out["session_killed"] = true
		}
	}
	return out, nil
}

// fanStopWords are the words a branch stem drops. They carry no meaning on
// their own, and a stem has room for four words.
var fanStopWords = map[string]bool{
	"a": true, "an": true, "the": true, "to": true, "of": true, "in": true, "on": true,
	"for": true, "with": true, "and": true, "or": true, "is": true, "it": true, "this": true,
	"that": true, "please": true, "into": true, "from": true, "by": true, "at": true,
}

// fanStem turns a prompt into a branch stem a person can read: "fan/" and the
// first content words of the prompt. "Add a retry to the client" is
// fan/add-retry-client.
func fanStem(prompt string) string {
	var words []string
	for _, w := range regexp.MustCompile(`[A-Za-z0-9]+`).FindAllString(strings.ToLower(prompt), -1) {
		if fanStopWords[w] {
			continue
		}
		words = append(words, w)
		if len(words) == 4 {
			break
		}
	}
	stem := strings.Join(words, "-")
	if len(stem) > 32 {
		stem = stem[:32]
		if i := strings.LastIndex(stem, "-"); i > 8 {
			stem = stem[:i]
		}
	}
	if stem == "" {
		stem = "prompt"
	}
	return "fan/" + stem
}

// fanBranches picks count branch names from stem that are free in the
// repository and on disk: stem, then stem-2, stem-3, and so on, skipping any
// that exist. Numbering from 2 keeps the first name bare, so a fan of one
// reads like a worktree made by hand.
func fanBranches(root, stem string, count int) []string {
	dir := worktree.DefaultDir()
	names := make([]string, 0, count)
	for i := 1; len(names) < count && i < count+100; i++ {
		name := stem
		if i > 1 {
			name = stem + "-" + strconv.Itoa(i)
		}
		if worktree.BranchExists(root, name) {
			continue
		}
		if _, err := os.Lstat(worktree.PathFor(dir, root, name)); err == nil {
			continue
		}
		names = append(names, name)
	}
	return names
}

func (d *Daemon) verbFan(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Count        int    `json:"count"`
		Agent        string `json:"agent"`
		Prompt       string `json:"prompt"`
		Repo         string `json:"repo"`
		Base         string `json:"base"`
		Name         string `json:"name"`
		ReadyTimeout int    `json:"ready_timeout"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Count < 1 || p.Count > fanMaxCount {
		return nil, invalidParam("count", fmt.Sprintf("count must be between 1 and %d", fanMaxCount))
	}
	if strings.TrimSpace(p.Prompt) == "" {
		return nil, invalidParam("prompt", "prompt is required: it is what every agent is asked")
	}
	root, verr := repoRootParam(p.Repo)
	if verr != nil {
		return nil, verr
	}
	reg := d.agentMatcher.registry
	manifest, command, ok := reg.Resolve(p.Agent)
	if !ok {
		return nil, invalidParam("agent", "agent must name a harness tuios recognises", reg.IDs()...)
	}
	if _, err := exec.LookPath(command); err != nil {
		return nil, hintedVerbError(ErrVerbInvalidParams, manifest.DisplayName+" is not installed: "+command+" is not on PATH", &VerbHint{
			Param:  "agent",
			Detail: "Install it, or name a harness that is installed. list-agents shows the ones running now.",
		})
	}
	stem := strings.TrimSpace(p.Name)
	if stem == "" {
		stem = fanStem(p.Prompt)
	}
	if err := worktree.ValidBranch(stem); err != nil {
		return nil, invalidParam("name", err.Error())
	}
	branches := fanBranches(root, stem, p.Count)
	if len(branches) < p.Count {
		return nil, invalidParam("name", "could not find "+strconv.Itoa(p.Count)+" free branch names from "+stem)
	}
	readyTimeout := durationOr(p.ReadyTimeout, fanDefaultReadyTimeout)

	sessions := make([]map[string]any, 0, p.Count)
	for _, branch := range branches {
		out, verr := d.createWorktreeSession(root, branch, strings.TrimSpace(p.Base), "", []string{command}, func(info *WorktreeInfo) {
			info.Group = stem
			info.Prompt = p.Prompt
			info.PromptStatus = PromptPending
		})
		if verr != nil {
			if len(sessions) > 0 {
				names := make([]string, 0, len(sessions))
				for _, s := range sessions {
					names = append(names, s["session"].(string))
				}
				if verr.Hint == nil {
					verr.Hint = &VerbHint{}
				}
				verr.Hint.Available = names
				verr.Message = "fan stopped at " + branch + ": " + verr.Message + " Sessions already started are kept."
			}
			return nil, verr
		}
		sess := d.manager.GetSession(out["session"].(string))
		windowID := out["window_id"].(string)
		go d.deliverFanPrompt(sess, windowID, p.Prompt, readyTimeout)
		sessions = append(sessions, map[string]any{
			"session":   out["session"],
			"branch":    branch,
			"path":      out["path"],
			"window_id": windowID,
		})
	}
	return map[string]any{
		"type":     "fan_started",
		"group":    stem,
		"repo":     filepath.Base(root),
		"agent":    manifest.ID,
		"command":  command,
		"prompt":   p.Prompt,
		"sessions": sessions,
		"total":    len(sessions),
	}, nil
}

// deliverFanPrompt types the prompt into the agent's pane once the agent is
// ready to read it, and records what happened on the session's record.
//
// The wait is the same shape as ask-agent's, with a stricter set of states:
// see fanReadyStates. It runs detached from the verb, so fan returns as soon
// as the sessions exist and the person watches the rail rather than a
// blocked command.
func (d *Daemon) deliverFanPrompt(sess *Session, windowID, text string, timeout time.Duration) {
	sub := d.events.subscribe(eventFilter{
		session: sess.Name,
		types:   map[string]bool{EventAgentState: true, EventWindowClosed: true, EventSessionClosed: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	ready := func() bool {
		st := sess.GetState()
		i, err := findWindowStateIndex(st.Windows, windowID)
		if err != nil {
			return false
		}
		return fanReadyStates[st.Windows[i].AgentState.Name()]
	}
	deadline := time.After(timeout)
	for !ready() {
		select {
		case <-deadline:
			sess.setPromptStatus(PromptNotSent, "The agent was not ready before the wait ended. Send the prompt with send-text.", 0)
			return
		case <-d.ctx.Done():
			return
		case ev := <-sub.ch:
			if ev.Type == EventSessionClosed {
				return
			}
			if ev.Type == EventWindowClosed && ev.Window == windowID {
				sess.setPromptStatus(PromptNotSent, "The agent's window closed before it was ready.", 0)
				return
			}
		}
	}
	pty, err := d.resolvePTYForTarget(sess, windowID)
	if err != nil {
		sess.setPromptStatus(PromptNotSent, "The agent's pane is gone.", 0)
		return
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if _, err := pty.Write([]byte(text)); err != nil {
		sess.setPromptStatus(PromptNotSent, "Could not write to the agent's pane: "+err.Error(), 0)
		return
	}
	sess.setPromptStatus(PromptSent, "", time.Now().UnixNano())
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
