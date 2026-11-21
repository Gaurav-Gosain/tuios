package session

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// Error codes the worktree verbs raise, on top of the shared ones.
const (
	// ErrVerbNotWorktree reports a session that is not in a git worktree, so
	// there is nothing to remove or diff.
	ErrVerbNotWorktree = "not_worktree"
	// ErrVerbWorktreeDirty reports a removal refused because the worktree holds
	// uncommitted changes and the caller did not say what to do with them.
	// Nothing was removed. The remedy is stash or force, and the hint says so.
	ErrVerbWorktreeDirty = "worktree_dirty"
	// ErrVerbWorktreeDetached reports a removal refused because the worktree
	// sits on a detached HEAD holding commits no branch, tag or remote has.
	// Removing the worktree would leave those commits unreachable. Nothing was
	// removed; force removes them anyway.
	ErrVerbWorktreeDetached = "worktree_detached"
	// ErrVerbWorktreeNested reports a removal refused because an ignored or
	// untracked path in the worktree holds a git repository of its own. git
	// worktree remove deletes it with everything around it, and no stash can
	// save a nested repository's history. Nothing was removed; force removes
	// it anyway.
	ErrVerbWorktreeNested = "worktree_nested"
	// ErrVerbGitFailed reports a git command that failed, with git's own
	// message. It is final: the repository is as it was.
	ErrVerbGitFailed = "git_failed"
)

const fanDefaultReadyTimeout = 10 * time.Minute

const fanMaxCount = 16

var fanReadyStates = map[string]bool{
	AgentStateIdle.Name(): true,
	AgentStateDone.Name(): true,
}

func (d *Daemon) worktreeTarget(cs *connState, name string) (*Session, *WorktreeInfo, *verbError) {
	sess, verr := d.resolveVerbSession(cs, name)
	if verr != nil {
		return nil, nil, verr
	}
	info := sess.Worktree()
	if info == nil {
		return nil, nil, hintedVerbError(ErrVerbNotWorktree, "session "+sess.Name()+" is not in a git worktree", &VerbHint{
			Param:     "session",
			Verb:      "list-worktrees",
			Command:   "tuios worktree ls",
			Available: d.worktreeSessionNames(),
		})
	}
	if info.Main {
		return nil, nil, hintedVerbError(ErrVerbNotWorktree, "session "+sess.Name()+" is in the repository's main checkout, which is not a worktree", &VerbHint{
			Param:     "session",
			Verb:      "list-worktrees",
			Command:   "tuios worktree ls",
			Available: d.worktreeSessionNames(),
		})
	}
	return sess, info, nil
}

func (d *Daemon) worktreeSessionNames() []string {
	var out []string
	for _, s := range d.manager.ListSessions() {
		if s.Worktree != nil && !s.Worktree.Main {
			out = append(out, s.Name)
		}
	}
	return out
}

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

// SetWorktreePath sets [worktree] path, the template new worktrees are placed
// by. Empty is the default layout.
func (d *Daemon) SetWorktreePath(template string) {
	d.worktreePath.Store(&template)
}

func (d *Daemon) worktreeTemplate() string {
	if t := d.worktreePath.Load(); t != nil {
		return *t
	}
	return ""
}

func worktreePathFor(template, root, branch string) (string, *verbError) {
	path, err := worktree.PathFor(template, root, branch)
	if err != nil {
		return "", hintedVerbError(ErrVerbInternal, "[worktree] path in config.toml: "+err.Error(), &VerbHint{
			Detail: "The [worktree] path template in config.toml cannot place a worktree. Nothing was created. Fix it, or leave it empty for the default folder.",
		})
	}
	return path, nil
}

func worktreeAgentName(branch string, command []string) string {
	if len(command) == 0 {
		return ""
	}
	return branch[strings.LastIndex(branch, "/")+1:]
}

func (d *Daemon) createWorktreeSession(cs *connState, root, branch, base, sessionName string, command, env []string, grants *Grants, setup setupSpec, record func(*WorktreeInfo), onAgent func(*Session, WindowState)) (map[string]any, *verbError) {
	template := d.worktreeTemplate()
	path, verr := worktreePathFor(template, root, branch)
	if verr != nil {
		return nil, verr
	}
	withSetup := func(info *WorktreeInfo) {
		if record != nil {
			record(info)
		}
		if setup.Command != "" {
			info.Setup = &WorktreeSetup{Command: setup.Command, Async: setup.Async, State: SetupPending, Agent: SetupAgentWaiting, Shell: len(command) == 0}
		}
	}
	sess, info, created, hookErr, verr := d.makeWorktreeSession(cs, root, path, branch, base, sessionName, withSetup)
	if verr != nil {
		return nil, verr
	}
	sessionName = sess.Name()

	sessionID := sess.ID
	onExit := func(ptyID string) { d.notifyPTYClosed(sessionID, ptyID) }
	startAgent := func() (WindowState, error) {
		return sess.AddDaemonWindowWith(NewWindowOptions{
			Cwd:     path,
			Focus:   true,
			Command: command,
			Env:     env,
			Grants:  grants,
			Name:    worktreeAgentName(branch, command),
		}, onExit)
	}
	agentFailed := func(err error) *verbError {
		return hintedVerbError(ErrVerbInternal, "the worktree and session were created but the first window could not start: "+err.Error(), &VerbHint{
			Detail: "Both are kept. Open a window in the session with: tuios new-window -s " + sessionName + " --cwd " + path,
		})
	}
	out := map[string]any{
		"session":        sess.Name(),
		"session_id":     sess.ID,
		"repo":           info.Repo,
		"repo_root":      root,
		"branch":         branch,
		"created_branch": created,
		"path":           path,
	}
	var win WindowState
	var err error
	if setup.Command == "" {
		if win, err = startAgent(); err != nil {
			return nil, agentFailed(err)
		}
	} else {
		if setup.Async {
			if win, err = startAgent(); err != nil {
				return nil, agentFailed(err)
			}
		}
		setupWin, err := d.startWorktreeSetup(setupLaunch{
			sess: sess, info: info, created: created, spec: setup, env: env,
			agent: startAgent, onAgent: onAgent,
		})
		if err != nil {
			agent := SetupAgentNotStarted
			if setup.Async {
				agent = SetupAgentStarted
			} else {
				sess.setPromptIfWaiting(PromptNotSent, "The setup window could not start, so the agent did not start.")
			}
			sess.setWorktreeSetup(&WorktreeSetup{Command: setup.Command, Async: setup.Async, State: SetupFailed, Agent: agent, Shell: len(command) == 0, Note: "The setup window could not start: " + err.Error()})
			out["setup_error"] = err.Error()
		} else {
			out["setup_window_id"] = setupWin.ID
			if setup.Async {
				sess.setWorktreeSetupAgent(SetupAgentStarted)
			}
		}
		out["setup"] = sess.worktreeSetupOrEmpty()
	}
	if win.ID != "" {
		out["window_id"] = win.ID
		out["pty_id"] = win.PTYID
		if onAgent != nil {
			onAgent(sess, win)
		}
	} else {
		out["agent_pending"] = true
	}
	if hookErr != "" {
		out["hook_error"] = hookErr
	}
	if warning := unignoredWorktreeWarning(template, root, path); warning != "" {
		out["warning"] = warning
	}
	return out, nil
}

func (d *Daemon) makeWorktreeSession(cs *connState, root, path, branch, base, sessionName string, record func(*WorktreeInfo)) (sess *Session, info *WorktreeInfo, created bool, hookErr string, verr *verbError) {
	var err error
	if _, err := os.Lstat(path); err == nil {
		return nil, nil, false, "", hintedVerbError(ErrVerbGitFailed, path+" already exists", &VerbHint{
			Param:  "branch",
			Detail: "A worktree for this branch is already there. Attach to its session, or remove it with remove-worktree first.",
		})
	}
	if sessionName == "" {
		sessionName = worktree.SessionName(filepath.Base(root), branch)
	}
	if err := ValidateSessionName(sessionName); err != nil {
		return nil, nil, false, "", invalidParam("name", err.Error())
	}
	if d.manager.GetSession(sessionName) != nil {
		return nil, nil, false, "", hintedVerbError(ErrVerbSessionExists, "session "+sessionName+" already exists", &VerbHint{
			Param:     "name",
			Command:   "tuios ls",
			Available: d.sessionNames(),
			Detail:    "Choose another session name with name, or attach to the session that exists.",
		})
	}

	created, err = worktree.Add(root, path, branch, base)
	if err != nil {
		return nil, nil, false, "", hintedVerbError(ErrVerbGitFailed, err.Error(), &VerbHint{
			Param:  "branch",
			Detail: "git refused to add the worktree. The repository is as it was.",
		})
	}

	sess, err = d.manager.CreateSession(sessionName, callerSessionConfig(cs), defaultVerbSessionWidth, defaultVerbSessionHeight)
	if err != nil {
		return nil, nil, false, "", hintedVerbError(ErrVerbInternal, "the worktree was created at "+path+" but its session could not: "+err.Error(), &VerbHint{
			Detail: "The worktree is kept. Start a session in it with: tuios new-window --cwd " + path,
		})
	}
	info = &WorktreeInfo{
		Info: worktree.Info{
			Repo:     filepath.Base(root),
			RepoRoot: root,
			Branch:   branch,
			Path:     path,
		},
		Base:    base,
		Managed: true,
	}
	if start, err := worktree.HeadCommit(path); err == nil {
		info.StartCommit = start
	}
	if record != nil {
		record(info)
	}
	_ = sess.SetWorktree(info)

	hookErr = d.runWorktreeHook(sess.Name(), info, created)
	return sess, info, created, hookErr, nil
}

func unignoredWorktreeWarning(template, root, path string) string {
	if !worktree.UnignoredInRepo(root, path) {
		return ""
	}
	rel := ""
	if dir, err := worktree.HoldingDir(template, root); err == nil {
		rel, _ = filepath.Rel(root, dir)
	}
	if rel == "" || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		rel, _ = filepath.Rel(root, path)
	}
	rel = filepath.ToSlash(rel)
	return filepath.Join(root, rel) + " is inside the repository and git does not ignore it, so the main checkout lists the worktree as untracked. Add /" + rel + "/ to .gitignore or .git/info/exclude."
}

const worktreeHookTimeout = time.Minute

func (d *Daemon) runWorktreeHook(sessionName string, info *WorktreeInfo, created bool) string {
	if d.hooks == nil || !d.hooks.HasEvent(hooks.AfterNewWorktree) {
		return ""
	}
	createdBranch := "0"
	if created {
		createdBranch = "1"
	}
	err := d.hooks.Run(hooks.AfterNewWorktree, hooks.Context{
		SessionID:             sessionName,
		WorktreePath:          info.Path,
		WorktreeBranch:        info.Branch,
		WorktreeRepoRoot:      info.RepoRoot,
		WorktreeCreatedBranch: createdBranch,
		Dir:                   info.Path,
	}, worktreeHookTimeout)
	if err != nil {
		return err.Error()
	}
	return ""
}

func (d *Daemon) verbNewWorktree(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		repoSource
		Branch    string   `json:"branch"`
		Base      string   `json:"base"`
		Name      string   `json:"name"`
		Command   []string `json:"command"`
		NewBranch bool     `json:"new_branch"`
		Prompt    string   `json:"prompt"`
		setupParam
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	prompt := strings.TrimSpace(p.Prompt)
	if prompt != "" && len(p.Command) == 0 {
		return nil, invalidParam("prompt", "prompt is typed into the agent, and command names none. Pass the agent in command")
	}
	setup, verr := d.resolveSetup(p.setupParam)
	if verr != nil {
		return nil, verr
	}
	branch := strings.TrimSpace(p.Branch)
	if err := worktree.ValidBranch(branch); err != nil {
		return nil, invalidParam("branch", err.Error())
	}
	if len(p.Command) > 0 && p.Command[0] == "" {
		return nil, invalidParam("command", "command[0] is the program to exec and cannot be empty")
	}
	command, verr := d.clampChildMode(cs, "new-worktree", "", p.Command, nil, false)
	if verr != nil {
		return nil, verr
	}
	root, cloned, verr := d.resolveRepoSource(cs, "new-worktree", p.repoSource, nil)
	if verr != nil {
		return nil, verr
	}
	if p.NewBranch {
		path, verr := worktreePathFor(d.worktreeTemplate(), root, branch)
		if verr != nil {
			return nil, verr
		}
		_, statErr := os.Lstat(path)
		if statErr == nil || worktree.BranchExists(root, branch) || d.manager.GetSession(cmp.Or(strings.TrimSpace(p.Name), worktree.SessionName(filepath.Base(root), branch))) != nil {
			return nil, newVerbError(ErrVerbBranchExists, "branch "+echoName(branch)+", its worktree folder or its session exists already")
		}
	}
	startedBy := d.startedByLater(cs)
	var record func(*WorktreeInfo)
	if prompt != "" {
		record = func(info *WorktreeInfo) {
			info.Prompt = prompt
			info.PromptStatus = PromptPending
		}
	}
	var (
		promptMu  sync.Mutex
		returned  bool
		agentSess *Session
		agentWin  string
	)
	out, verr := d.createWorktreeSession(cs, root, branch, strings.TrimSpace(p.Base), strings.TrimSpace(p.Name), command, nil, d.childDefaultGrants(cs), setup, record, func(sess *Session, win WindowState) {
		startedBy(win.ID)
		if prompt == "" {
			return
		}
		promptMu.Lock()
		defer promptMu.Unlock()
		if !returned {
			agentSess, agentWin = sess, win.ID
			return
		}
		go d.deliverFanPrompt(sess, win.ID, "", prompt, fanDefaultReadyTimeout)
	})
	promptMu.Lock()
	returned = true
	sess, win := agentSess, agentWin
	promptMu.Unlock()
	if verr != nil {
		return nil, verr
	}
	out["type"] = "worktree_created"
	if cloned {
		out["cloned"] = true
	}
	if prompt != "" {
		out["prompt_status"] = PromptPending
		if sess != nil {
			d.deliverFanPrompt(sess, win, "", prompt, startAgentDefaultReadyTimeout)
			if wt := sess.Worktree(); wt != nil {
				out["prompt_status"] = wt.PromptStatus
				if wt.PromptNote != "" {
					out["prompt_note"] = wt.PromptNote
				}
			}
		}
	}
	return out, nil
}

func (d *Daemon) verbListWorktrees(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Repo    string `json:"repo"`
		Group   string `json:"group"`
		Session string `json:"session"`
		Changes bool   `json:"changes"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	rows := make([]map[string]any, 0)
	withChanges := make([]worktreeChanges, 0)
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
		if p.Session != "" && s.Name != p.Session {
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
			"session":         s.Name,
			"repo":            wt.Repo,
			"repo_root":       wt.RepoRoot,
			"branch":          wt.Branch,
			"path":            wt.Path,
			"base":            wt.Base,
			"group":           wt.Group,
			"fan_id":          wt.FanID,
			"managed":         wt.Managed,
			"gone":            wt.Gone,
			"main":            wt.Main,
			"state":           state,
			"harness":         harnessID,
			"windows":         s.WindowCount,
			"attached":        s.Attached,
			"prompt_status":   wt.PromptStatus,
			"prompt_note":     wt.PromptNote,
			"prompt_ready_by": wt.PromptReadyBy,
			"agent":           wt.Agent,
		}
		if wt.LaunchedFrom != "" {
			row["launched_from"] = wt.LaunchedFrom
		}
		if wt.PR != nil {
			row["pr"] = wt.PR
		}
		if wt.Setup != nil {
			if sess := d.manager.GetSession(s.Name); sess != nil {
				row["setup"] = d.setupReport(sess, wt.Setup)
			} else {
				row["setup"] = wt.Setup
			}
		}
		if p.Changes && !wt.Gone {
			withChanges = append(withChanges, worktreeChanges{row: row, wt: wt})
		}
		rows = append(rows, row)
	}
	if len(withChanges) > 0 {
		group, ctx := errgroup.WithContext(context.Background())
		group.SetLimit(worktreeListWorkers)
		for _, c := range withChanges {
			c := c
			group.Go(func() error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				c.fill()
				return nil
			})
		}
		_ = group.Wait()
	}
	return map[string]any{
		"type":      "worktree_list",
		"worktrees": rows,
		"total":     len(rows),
	}, nil
}

type worktreeChanges struct {
	row map[string]any
	wt  *WorktreeInfo
}

const worktreeListWorkers = 8

func (c worktreeChanges) fill() {
	row, wt := c.row, c.wt
	if changes, ignored, err := worktree.ChangeCounts(wt.Path); err == nil {
		row["changes"] = changes
		row["ignored"] = ignored
	} else {
		row["changes"] = -1
		row["ignored"] = -1
	}
	if n, err := worktree.OwnCommits(wt.Path); err == nil {
		row["own_commits"] = n
	} else {
		row["own_commits"] = -1
	}
	if wt.Base != "" {
		if n, err := worktree.Ahead(wt.Path, wt.Base); err == nil {
			row["ahead"] = n
		}
	}
	into, _ := worktree.Merged(wt.Path, wt.RepoRoot, wt.Branch, wt.StartCommit, wt.Base)
	row["merged"] = into != ""
	if into != "" {
		row["merged_into"] = into
	}
}

func (d *Daemon) verbRemoveWorktree(cs *connState, params json.RawMessage) (any, *verbError) {
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
	sess, info, verr := d.worktreeTarget(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	d.setups.halt(sess.ID)
	notesRoot := canonRoot(info.Path)
	killed := false

	out := map[string]any{
		"type":        "worktree_removed",
		"session":     sess.Name(),
		"branch":      info.Branch,
		"path":        info.Path,
		"repo":        info.Repo,
		"branch_kept": true,
		"changes":     0,
		"stashed":     false,
		"discarded":   false,
	}

	if _, err := os.Stat(info.Path); err != nil {
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
		jj := gitstate.Find(info.Path).JJ
		if jj {
			changes = 0
			left, err := worktree.JJLeftOut(info.Path)
			if err != nil {
				return nil, hintedVerbError(ErrVerbGitFailed, err.Error(), &VerbHint{
					Detail: "jj could not snapshot the workspace, so nothing was removed.",
				})
			}
			if len(left) > 0 && !p.Force {
				return nil, hintedVerbError(ErrVerbWorktreeDirty,
					fmt.Sprintf("%s holds %d %s jj did not record: %s. Nothing was removed.", info.Path, len(left), plural.Word(len(left), "file", "files"), strings.Join(left, ", ")),
					&VerbHint{
						Param:   "force",
						Command: "tuios worktree rm " + sess.Name() + " --force",
						Detail:  "jj leaves a new file over snapshot.max-new-file-size out of the working-copy commit. Raise that limit and snapshot again, or pass force to delete the files with the workspace.",
					})
			}
		}
		if own, err := worktree.OwnCommits(info.Path); err == nil {
			out["own_commits"] = own
			if own > 0 && !p.Force {
				return nil, hintedVerbError(ErrVerbWorktreeDetached,
					fmt.Sprintf("%s sits on a detached HEAD holding %d %s no branch has. Nothing was removed.", info.Path, own, plural.Word(own, "commit", "commits")),
					&VerbHint{
						Param:  "force",
						Detail: "Commit them onto a branch first, or pass force to remove the worktree and lose the commits.",
					})
			}
		} else {
			out["own_commits"] = -1
		}
		if ignored, err := worktree.Ignored(info.Path); err == nil {
			out["ignored"] = ignored
		} else {
			out["ignored"] = -1
		}
		if nested, err := worktree.NestedRepo(info.Path); err != nil {
			detail := "git could not read the worktree's status for the nested repository check. Nothing was removed."
			if _, walk := errors.AsType[*fs.PathError](err); walk {
				detail = "tuios could not read the untracked and ignored folders for the nested repository check. Nothing was removed."
			}
			return nil, hintedVerbError(ErrVerbGitFailed, err.Error(), &VerbHint{Detail: detail})
		} else if nested != "" && !p.Force {
			return nil, hintedVerbError(ErrVerbWorktreeNested,
				fmt.Sprintf("%s holds a nested git repository at %s. Nothing was removed.", info.Path, nested),
				&VerbHint{
					Param:  "force",
					Detail: "Remove the nested repository first, or pass force to delete it with the worktree, its history included.",
				})
		}
		if changes > 0 && !p.Force && !p.Stash {
			return nil, hintedVerbError(ErrVerbWorktreeDirty,
				fmt.Sprintf("%s holds %d uncommitted %s. Nothing was removed.", info.Path, changes, plural.Word(changes, "change", "changes")),
				&VerbHint{
					Param:   "stash",
					Command: "tuios worktree rm " + sess.Name() + " --stash",
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
		discard := changes > 0 && !p.Stash || jj && p.Force
		if runtime.GOOS == "windows" && !p.KeepSession {
			out["clients_switched"] = d.moveClientsOff(sess)
			if err := d.manager.DeleteSession(sess.Name()); err == nil {
				killed = true
			}
		}
		if err := worktree.Remove(info.RepoRoot, info.Path, discard); err != nil {
			msg := "git refused to remove the worktree: " + err.Error() + " It is still there."
			if stashed, _ := out["stashed"].(bool); stashed {
				msg += " The changes were already stashed: they are in git stash under the message \"tuios: " + info.Branch + "\"."
			}
			if killed {
				msg += " The session was killed to free the directory for the removal."
			}
			return nil, hintedVerbError(ErrVerbGitFailed, msg, nil)
		}
		out["discarded"] = discard
		d.worktreeRemoved(sess.Name(), info.Path)
	}
	d.reviewNotes.dropRoot(notesRoot)
	d.dropCheckpointsOf(info.RepoRoot, notesRoot)

	out["session_killed"] = killed
	if !killed && !p.KeepSession {
		out["clients_switched"] = d.moveClientsOff(sess)
		if err := d.manager.DeleteSession(sess.Name()); err == nil {
			out["session_killed"] = true
		}
	}
	return out, nil
}

func (d *Daemon) moveClientsOff(sess *Session) int {
	var tuis []*connState
	d.clientsMu.RLock()
	for _, c := range d.clients {
		c.mu.Lock()
		on := c.isTUIClient && c.attached && !c.passive && c.sessionID == sess.ID
		c.mu.Unlock()
		if on {
			tuis = append(tuis, c)
		}
	}
	d.clientsMu.RUnlock()
	if len(tuis) == 0 {
		return 0
	}
	args := []string{sess.Name()}
	for _, name := range d.sessionNames() {
		if name != sess.Name() {
			args = append(args, name)
		}
	}
	if len(args) == 1 {
		return 0
	}
	moved := 0
	for _, tui := range tuis {
		res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
			CommandType: "leave_session",
			TapeArgs:    args,
		}, routedVerbTimeout)
		if err != nil || !res.Success {
			continue
		}
		to, _ := res.Data["session"].(string)
		dest, _ := d.manager.ResolveSession(to)
		if dest == nil || dest.ID == sess.ID {
			continue
		}
		if dest.waitChange(routedVerbTimeout, func() bool { return connSessionID(tui) == dest.ID }) {
			moved++
		}
	}
	return moved
}

func (d *Daemon) worktreeRemoved(session, path string) {
	d.events.publish(streamEvent{Type: EventWorktreeRemoved, Session: session, Path: path})
}

var fanStopWords = map[string]bool{
	"a": true, "an": true, "the": true, "to": true, "of": true, "in": true, "on": true,
	"for": true, "with": true, "and": true, "or": true, "is": true, "it": true, "this": true,
	"that": true, "please": true, "into": true, "from": true, "by": true, "at": true,
}

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

func fanBranches(template, root, stem string, count int) []string {
	names := make([]string, 0, count)
	for i := 1; len(names) < count && i < count+100; i++ {
		name := stem
		if i > 1 {
			name = stem + "-" + strconv.Itoa(i)
		}
		if worktree.BranchExists(root, name) {
			continue
		}
		if path, err := worktree.PathFor(template, root, name); err == nil {
			if _, err := os.Lstat(path); err == nil {
				continue
			}
		}
		names = append(names, name)
	}
	return names
}

func (d *Daemon) verbFan(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		repoSource
		Count        int               `json:"count"`
		Agent        string            `json:"agent"`
		Agents       []string          `json:"agents"`
		Prompt       string            `json:"prompt"`
		Prompts      []string          `json:"prompts"`
		Base         string            `json:"base"`
		Name         string            `json:"name"`
		Join         string            `json:"join"`
		ReadyTimeout int               `json:"ready_timeout"`
		Env          map[string]string `json:"env"`
		Grants       []string          `json:"grants"`
		Task         string            `json:"task"`
		TaskSession  string            `json:"task_session"`
		HumanNonce   string            `json:"human_nonce"`
		setupParam
		launchChoice
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	setup, verr := d.resolveSetup(p.setupParam)
	if verr != nil {
		return nil, verr
	}
	grants, verr := d.launchGrants(cs, p.Grants)
	if verr != nil {
		return nil, verr
	}
	var task *taskParam
	if p.TaskSession != "" && p.Task == "" {
		return nil, invalidParam("task_session", "task_session names the session of task, so it takes a task")
	}
	if p.Task != "" {
		if task, verr = d.admitTaskParam(cs, "fan", p.TaskSession, p.Task, p.HumanNonce, true); verr != nil {
			return nil, verr
		}
	}
	if len(p.Prompts) > 0 {
		if p.Prompt != "" {
			return nil, invalidParam("prompts", "prompt is the same for every session and prompts is one per session. Pass one or the other")
		}
		if p.Count == 0 {
			p.Count = len(p.Prompts)
		}
		if p.Count != len(p.Prompts) {
			return nil, invalidParam("prompts", fmt.Sprintf("prompts has %d entries and count is %d: there is one prompt per session", len(p.Prompts), p.Count))
		}
		for i, pr := range p.Prompts {
			if strings.TrimSpace(pr) == "" {
				return nil, invalidParam("prompts", fmt.Sprintf("prompts[%d] is empty", i))
			}
		}
	} else if strings.TrimSpace(p.Prompt) == "" {
		return nil, invalidParam("prompt", "prompt is required: it is what every agent is asked")
	}
	if p.Count < 1 || p.Count > fanMaxCount {
		return nil, invalidParam("count", fmt.Sprintf("count must be between 1 and %d", fanMaxCount))
	}
	promptFor := func(i int) string {
		if len(p.Prompts) > 0 {
			return p.Prompts[i]
		}
		return p.Prompt
	}
	specs := p.Agents
	switch {
	case p.Agent != "" && len(p.Agents) > 0:
		return nil, invalidParam("agents", "agent names one agent for every session and agents several. Pass one or the other")
	case len(p.Agents) == 0:
		specs = []string{p.Agent}
	case len(p.Agents) > fanMaxCount:
		return nil, invalidParam("agents", fmt.Sprintf("agents names at most %d agents", fanMaxCount))
	}
	var env []string
	var launches []agentLaunch
	resolveAgents := func() *verbError {
		var pathList string
		var verr *verbError
		if env, pathList, verr = callerEnv(cs, p.Env); verr != nil {
			return verr
		}
		launches = make([]agentLaunch, len(specs))
		for i, spec := range specs {
			param := "agent"
			if len(p.Agents) > 0 {
				param = "agents"
			}
			if launches[i], verr = d.resolveAgentLaunch(param, spec, pathList); verr != nil {
				return verr
			}
			if launches[i], verr = d.withChoice(launches[i], p.launchChoice); verr != nil {
				return verr
			}
			if launches[i].argv, verr = d.clampChildMode(cs, "fan", launches[i].harness, launches[i].argv, env, false); verr != nil {
				return verr
			}
		}
		return nil
	}
	root, cloned, verr := d.resolveRepoSource(cs, "fan", p.repoSource, resolveAgents)
	if verr != nil {
		return nil, verr
	}
	if launches == nil {
		if verr := resolveAgents(); verr != nil {
			return nil, verr
		}
	}
	stem := strings.TrimSpace(p.Name)
	if verr := fanStemPlain(stem); verr != nil {
		return nil, verr
	}
	if join := strings.TrimSpace(p.Join); join != "" && stem == "" {
		if s := d.manager.GetSession(join); s != nil {
			if wt := s.Worktree(); wt != nil {
				stem = wt.Group
			}
		}
	}
	named := stem != ""
	if !named {
		stem = fanStem(promptFor(0))
	}
	if err := worktree.ValidBranch(stem); err != nil {
		return nil, invalidParam("name", err.Error())
	}
	stem, fanID, release, verr := d.reserveFan(cs, root, stem, named, strings.TrimSpace(p.Join))
	if verr != nil {
		return nil, verr
	}
	defer release()
	template := d.worktreeTemplate()
	if _, verr := worktreePathFor(template, root, stem); verr != nil {
		return nil, verr
	}
	branches := fanBranches(template, root, stem, p.Count)
	if len(branches) < p.Count {
		return nil, invalidParam("name", "could not find "+strconv.Itoa(p.Count)+" free branch names from "+stem)
	}
	readyTimeout := durationOr(p.ReadyTimeout, fanDefaultReadyTimeout)
	launchedFrom, launchedFromID := "", ""
	if s := d.callerSession(cs); s != nil {
		launchedFrom, launchedFromID = s.Name(), s.ID
	}

	sessions := make([]map[string]any, 0, p.Count)
	var warnings []string
	stamp := func(out map[string]any) string {
		if task == nil || len(sessions) == 0 {
			return ""
		}
		if row, ok := d.stampFan(task, stem, fanID); ok {
			out["task"] = row
			return "Task card " + task.id + " is stamped with the fan."
		}
		note := "Task card " + task.id + " was removed while the fan was made, so nothing was stamped."
		out["task_note"] = note
		return note
	}
	startedBy := d.startedByLater(cs)
	for i, branch := range branches {
		launch, prompt := launches[i%len(launches)], promptFor(i)
		onAgent := func(sess *Session, win WindowState) {
			startedBy(win.ID)
			go d.deliverFanPrompt(sess, win.ID, launch.harness, prompt, readyTimeout)
		}
		out, verr := d.createWorktreeSession(cs, root, branch, strings.TrimSpace(p.Base), "", launch.argv, launch.withPath(env), grants, setup, func(info *WorktreeInfo) {
			info.Group = stem
			info.FanID = fanID
			info.LaunchedFrom = launchedFrom
			info.LaunchedFromID = launchedFromID
			info.Prompt = prompt
			info.PromptStatus = PromptPending
			info.Agent = launch.spec
		}, onAgent)
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
				if note := stamp(map[string]any{}); note != "" {
					verr.Hint.Detail = strings.TrimSpace(verr.Hint.Detail + " " + note)
				}
			}
			return nil, verr
		}
		windowID, _ := out["window_id"].(string)
		if w, ok := out["warning"].(string); ok && !slices.Contains(warnings, w) {
			warnings = append(warnings, w)
		}
		row := map[string]any{
			"session":   out["session"],
			"branch":    branch,
			"path":      out["path"],
			"window_id": windowID,
			"agent":     launch.harness,
			"command":   launch.command(),
		}
		if rec, ok := out["setup"]; ok {
			row["setup"] = rec
		}
		if pending, ok := out["agent_pending"]; ok {
			row["agent_pending"] = pending
		}
		if hookErr, ok := out["hook_error"]; ok {
			row["hook_error"] = hookErr
		}
		sessions = append(sessions, row)
	}
	out := map[string]any{
		"type":      "fan_started",
		"group":     stem,
		"fan_id":    fanID,
		"repo":      filepath.Base(root),
		"repo_root": root,
		"agent":     launches[0].harness,
		"command":   launches[0].command(),
		"prompt":    promptFor(0),
		"sessions":  sessions,
		"total":     len(sessions),
	}
	if cloned {
		out["cloned"] = true
	}
	if len(warnings) > 0 {
		out["warning"] = strings.Join(warnings, " ")
	}
	stamp(out)
	return out, nil
}

func (d *Daemon) deliverFanPrompt(sess *Session, windowID, harness, text string, timeout time.Duration) {
	w, outcome := d.waitAgentStart(sess, windowID, harness, timeout, false, false, func(WindowState) {
		sess.setPromptStatus(PromptHeld, "The agent is not at a prompt tuios recognises. Look at the pane: it may be showing a first-run choice. The prompt is typed as soon as the agent is ready.", 0)
	})
	switch outcome {
	case agentStartReady:
	case agentStartTimeout:
		sess.setPromptStatus(PromptNotSent, "The agent was not ready before the wait ended. Send the prompt with send-text.", 0)
		return
	case agentStartWindowClosed:
		sess.setPromptStatus(PromptNotSent, "The agent's window closed before it was ready.", 0)
		return
	default:
		return
	}
	sess.setPromptReadyBy(readyBy(w))
	status, note, at := d.typeFirstPrompt(sess, windowID, text)
	sess.setPromptStatus(status, note, at)
}

type fanReservations struct {
	mu    sync.Mutex
	names map[string]bool
}

func fanKey(root, stem string) string { return canonRoot(root) + "\x00" + stem }

func newFanID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func sameFan(a, b *WorktreeInfo) bool {
	return a != nil && b != nil && a.Group != "" && a.Group == b.Group &&
		a.RepoRoot == b.RepoRoot && a.FanID == b.FanID
}

func (d *Daemon) reachedNames(cs *connState, names []string) []string {
	var out []string
	for _, n := range names {
		if d.callerReachesSession(cs, n) {
			out = append(out, n)
		}
	}
	return out
}

func (d *Daemon) fanMembers(root, stem string) []*Session {
	var out []*Session
	for _, s := range d.manager.AllSessions() {
		if wt := s.Worktree(); wt != nil && wt.Group == stem && canonRoot(wt.RepoRoot) == canonRoot(root) {
			out = append(out, s)
		}
	}
	return out
}

func (d *Daemon) reserveFan(cs *connState, root, stem string, named bool, join string) (string, string, func(), *verbError) {
	r := &d.fanReserved
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.names == nil {
		r.names = map[string]bool{}
	}
	key := func(st string) string { return fanKey(root, st) }
	taken := func(st string) bool { return r.names[key(st)] || len(d.fanMembers(root, st)) > 0 }
	switch {
	case join != "":
		var names []string
		var joined *Session
		for _, m := range d.fanMembers(root, stem) {
			names = append(names, m.Name())
			if m.Name() == join {
				joined = m
			}
		}
		in := joined != nil
		if !in {
			return "", "", nil, hintedVerbError(ErrVerbInvalidParams, "join names "+echoName(join)+", which is not a session of the fan "+echoName(stem)+" in this repository", &VerbHint{
				Param: "join", Available: d.reachedNames(cs, names),
				Detail: "Nothing was made. join takes a session of the fan to add attempts to.",
			})
		}
		if !d.callerReachesSession(cs, join) {
			return "", "", nil, newVerbError(ErrVerbForbidden, "the caller cannot reach "+echoName(join)+", so it cannot add attempts to its fan")
		}
		return stem, joined.Worktree().FanID, func() {}, nil
	case taken(stem) && named:
		var names []string
		for _, m := range d.fanMembers(root, stem) {
			names = append(names, m.Name())
		}
		return "", "", nil, hintedVerbError(ErrVerbFanExists, "a fan named "+echoName(stem)+" already exists in this repository", &VerbHint{
			Param: "name", Available: d.reachedNames(cs, names),
			Detail: "Nothing was made. Choose another name for a new fan. To add attempts to this fan on purpose, pass join with one of its sessions (tuios fan --join SESSION).",
		})
	case taken(stem):
		base := stem
		for i := 2; taken(stem); i++ {
			if i > 99 {
				return "", "", nil, invalidParam("name", "no free fan name from "+base)
			}
			stem = base + "-" + strconv.Itoa(i)
		}
	}
	k := key(stem)
	r.names[k] = true
	return stem, newFanID(), func() {
		r.mu.Lock()
		delete(r.names, k)
		r.mu.Unlock()
	}, nil
}

func fanStemPlain(stem string) *verbError {
	for _, r := range stem {
		if !FanNameChar(r) {
			return invalidParam("name", fmt.Sprintf("the fan name %s has the character U+%04X. A fan name can have only the letters A to Z and a to z, the digits 0 to 9, and / . _ -. Nothing was made.", strconv.QuoteToASCII(stem), r))
		}
	}
	if stem != "" && !FanNameRefOK(stem) {
		return invalidParam("name", "git does not accept "+strconv.QuoteToASCII(stem)+" as a branch name. Nothing was made.")
	}
	return nil
}

// FanNameRefOK reports whether git takes name as a branch, by the rules of
// git check-ref-format --branch, without running git, so the cockpit's
// new-agent form can check a name in its update loop. The daemon checks the
// same, and git checks again when it makes the branch.
func FanNameRefOK(name string) bool {
	if name == "" || name == "@" || name == "HEAD" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") ||
		strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") || strings.Contains(name, "..") ||
		strings.Contains(name, "//") || strings.Contains(name, "@{") {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(" ~^:?*[\\", r) {
			return false
		}
	}
	for part := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

// FanNameChar reports whether a fan name may hold r: A-Z, a-z, 0-9 or
// / . _ -. The cockpit's new-agent form checks a name by it before start.
func FanNameChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return r == '/' || r == '.' || r == '_' || r == '-'
}
