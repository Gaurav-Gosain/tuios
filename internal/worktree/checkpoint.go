package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Checkpoints: the state of a work tree at the end of an agent's turn, kept
// so a turn can be read again and undone.
//
// A checkpoint is a commit object under refs/tuios/checkpoints/<pane>/<n>.
// Its tree is the working state as SnapshotTree writes it: tracked files as
// they are on disk, untracked files that are not ignored, and nothing that is
// ignored. Its parent is HEAD at the time, when there is one. Nothing the
// person works with changes: the index is a temporary one, HEAD and the
// branch do not move, and the stash is not used. The refs live in the
// repository's common directory, so every worktree of a repository sees the
// checkpoints of every pane, and they are not pushed, since no refspec git
// sets up names refs/tuios.
//
// The message carries what the daemon knew when it took the checkpoint, one
// "Key: value" line each after the subject, so a listing is one for-each-ref
// and needs no state of its own.

// CheckpointRefPrefix is where every checkpoint ref is kept.
const CheckpointRefPrefix = "refs/tuios/checkpoints/"

// Kinds of checkpoint.
const (
	// CheckpointTurn is taken when an agent's turn ends.
	CheckpointTurn = "turn"
	// CheckpointSafety is taken before a restore, so the restore can be
	// undone.
	CheckpointSafety = "safety"
)

// Checkpoint is one saved state of a work tree.
type Checkpoint struct {
	// N numbers the pane's checkpoints from 1, oldest first.
	N int `json:"n"`
	// Ref is the full ref name.
	Ref string `json:"ref"`
	// Commit is the checkpoint's commit, Tree its tree.
	Commit string `json:"commit"`
	Tree   string `json:"tree"`
	// Head is HEAD when the checkpoint was taken, empty in a repository with
	// no commit yet.
	Head string `json:"head,omitempty"`
	// Kind is CheckpointTurn or CheckpointSafety.
	Kind string `json:"kind"`
	// Pane is the window id the checkpoint was taken for.
	Pane string `json:"pane"`
	// Session is the session the pane was in.
	Session string `json:"session,omitempty"`
	// Turn is the pane's finished-turn count (completion_seq) when the
	// checkpoint was taken.
	Turn uint64 `json:"turn"`
	// State is the agent state the turn ended in.
	State string `json:"state,omitempty"`
	// Label is a short line saying what the turn was: the prompt, or what
	// the turn ended with.
	Label string `json:"label,omitempty"`
	// Worktree is the root of the work tree the checkpoint was taken from.
	Worktree string `json:"worktree"`
	// At is when the checkpoint was taken, in Unix nanoseconds.
	At int64 `json:"at"`
}

// CheckpointMeta is what SaveCheckpoint records about a checkpoint.
type CheckpointMeta struct {
	Kind    string
	Pane    string
	Session string
	Turn    uint64
	State   string
	Label   string
	At      time.Time
}

// ErrNoCheckpoint reports a checkpoint number the pane has no ref for.
var ErrNoCheckpoint = errors.New("no such checkpoint")

// checkpointLabelMax bounds the label kept in a checkpoint's message.
const checkpointLabelMax = 120

// ValidPaneKey refuses a pane id that cannot be one component of a ref name.
func ValidPaneKey(pane string) error {
	if pane == "" || len(pane) > 128 {
		return fmt.Errorf("pane id %q is empty or too long", pane)
	}
	for _, r := range pane {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return fmt.Errorf("pane id %q holds a character a ref name cannot", pane)
		}
	}
	return nil
}

// checkpointEnv is the environment of the git calls that write a checkpoint:
// a fixed identity, so a repository with no user.name configured still takes
// one, and the commit says who made it.
var checkpointEnv = []string{
	"GIT_AUTHOR_NAME=tuios", "GIT_AUTHOR_EMAIL=",
	"GIT_COMMITTER_NAME=tuios", "GIT_COMMITTER_EMAIL=",
}

// ListCheckpoints returns the checkpoints of pane in the repository dir is in,
// oldest first. An empty pane lists every pane's.
func ListCheckpoints(ctx context.Context, dir, pane string) ([]Checkpoint, error) {
	prefix := CheckpointRefPrefix
	if pane != "" {
		if err := ValidPaneKey(pane); err != nil {
			return nil, err
		}
		prefix += pane + "/"
	}
	// A NUL between the fields and a record separator after each record:
	// the message runs over several lines.
	out, err := runCtx(ctx, dir, nil, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(tree)%00%(parent)%00%(contents)%1e", prefix)
	if err != nil {
		return nil, err
	}
	var list []Checkpoint
	for rec := range strings.SplitSeq(out, "\x1e") {
		rec = strings.TrimPrefix(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, "\x00", 5)
		if len(f) < 5 {
			continue
		}
		cp, ok := parseCheckpoint(f[0], f[1], f[2], f[3], f[4])
		if ok {
			list = append(list, cp)
		}
	}
	slices.SortFunc(list, func(a, b Checkpoint) int {
		if a.Pane != b.Pane {
			return strings.Compare(a.Pane, b.Pane)
		}
		return a.N - b.N
	})
	return list, nil
}

// parseCheckpoint reads one ref of ListCheckpoints. A ref under the prefix
// that is not <pane>/<n> is not a checkpoint and is skipped.
func parseCheckpoint(ref, commit, tree, parents, msg string) (Checkpoint, bool) {
	rest, ok := strings.CutPrefix(ref, CheckpointRefPrefix)
	if !ok {
		return Checkpoint{}, false
	}
	pane, num, ok := strings.Cut(rest, "/")
	if !ok {
		return Checkpoint{}, false
	}
	n, err := strconv.Atoi(num)
	if err != nil || n < 1 {
		return Checkpoint{}, false
	}
	cp := Checkpoint{N: n, Ref: ref, Commit: commit, Tree: tree, Pane: pane, Kind: CheckpointTurn}
	if p, _, _ := strings.Cut(strings.TrimSpace(parents), " "); p != "" {
		cp.Head = p
	}
	for line := range strings.SplitSeq(msg, "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		switch key {
		case "Kind":
			cp.Kind = value
		case "Session":
			cp.Session = value
		case "Turn":
			cp.Turn, _ = strconv.ParseUint(value, 10, 64)
		case "State":
			cp.State = value
		case "Label":
			cp.Label = value
		case "Worktree":
			cp.Worktree = value
		case "Time":
			if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
				cp.At = t.UnixNano()
			}
		}
	}
	return cp, true
}

// oneLine makes s fit one line of a commit message: control characters
// become spaces and it is cut to max bytes on a rune boundary.
func oneLine(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// utf8RuneStart reports whether b starts a UTF-8 sequence.
func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// checkpointMessage is the commit message of a checkpoint.
func checkpointMessage(n int, root string, m CheckpointMeta) string {
	label := oneLine(m.Label, checkpointLabelMax)
	subject := fmt.Sprintf("tuios checkpoint %d", n)
	if label != "" {
		subject += ": " + label
	}
	var b strings.Builder
	b.WriteString(subject + "\n\n")
	fmt.Fprintf(&b, "Kind: %s\n", m.Kind)
	fmt.Fprintf(&b, "Pane: %s\n", m.Pane)
	if m.Session != "" {
		fmt.Fprintf(&b, "Session: %s\n", oneLine(m.Session, 200))
	}
	fmt.Fprintf(&b, "Turn: %d\n", m.Turn)
	if m.State != "" {
		fmt.Fprintf(&b, "State: %s\n", oneLine(m.State, 32))
	}
	if label != "" {
		fmt.Fprintf(&b, "Label: %s\n", label)
	}
	fmt.Fprintf(&b, "Worktree: %s\n", oneLine(root, 4096))
	fmt.Fprintf(&b, "Time: %s\n", m.At.UTC().Format(time.RFC3339Nano))
	return b.String()
}

// SaveCheckpoint writes the working state of the work tree at root as the
// pane's next checkpoint and drops the oldest past keep (zero keeps all). It
// reports saved false, and the newest checkpoint, when nothing changed since
// that one: a turn that changed no file takes no checkpoint. A safety
// checkpoint is always saved, so every restore has its own to undo it.
func SaveCheckpoint(ctx context.Context, root string, m CheckpointMeta, keep int) (Checkpoint, bool, error) {
	if err := ValidPaneKey(m.Pane); err != nil {
		return Checkpoint{}, false, err
	}
	if m.Kind == "" {
		m.Kind = CheckpointTurn
	}
	if m.At.IsZero() {
		m.At = time.Now()
	}
	list, err := ListCheckpoints(ctx, root, m.Pane)
	if err != nil {
		return Checkpoint{}, false, err
	}
	tree, err := SnapshotTree(ctx, root)
	if err != nil {
		return Checkpoint{}, false, err
	}
	n := 1
	if len(list) > 0 {
		last := list[len(list)-1]
		if last.Tree == tree && m.Kind != CheckpointSafety {
			return last, false, nil
		}
		n = last.N + 1
	}
	args := []string{"commit-tree", "--no-gpg-sign", tree}
	head, headErr := HeadCommitCtx(ctx, root)
	if headErr == nil {
		args = append(args, "-p", head)
	}
	msg := checkpointMessage(n, root, m)
	args = append(args, "-m", msg)
	out, err := runCtx(ctx, root, checkpointEnv, args...)
	if err != nil {
		return Checkpoint{}, false, err
	}
	commit := strings.TrimSpace(out)
	ref := CheckpointRefPrefix + m.Pane + "/" + strconv.Itoa(n)
	// The empty old value makes git refuse a ref that exists already, so two
	// writers racing for one number cannot overwrite each other.
	if _, err := runCtx(ctx, root, nil, "update-ref", "--no-deref", "-m", "tuios checkpoint", ref, commit, ""); err != nil {
		return Checkpoint{}, false, err
	}
	cp, _ := parseCheckpoint(ref, commit, tree, head, msg)
	if headErr != nil {
		cp.Head = ""
	}
	if keep > 0 && len(list)+1 > keep {
		drop := list[:len(list)+1-keep]
		if err := DeleteCheckpoints(ctx, root, drop); err != nil {
			return cp, true, fmt.Errorf("checkpoint %d was saved, and the old ones were not pruned: %w", n, err)
		}
	}
	return cp, true, nil
}

// DeleteCheckpoints removes the refs of cps in one transaction.
func DeleteCheckpoints(ctx context.Context, dir string, cps []Checkpoint) error {
	if len(cps) == 0 {
		return nil
	}
	var b strings.Builder
	for _, cp := range cps {
		if !strings.HasPrefix(cp.Ref, CheckpointRefPrefix) {
			return fmt.Errorf("%q is not a checkpoint ref", cp.Ref)
		}
		fmt.Fprintf(&b, "delete %s %s\n", cp.Ref, cp.Commit)
	}
	_, err := runStdin(ctx, dir, nil, b.String(), "update-ref", "--no-deref", "--stdin")
	return err
}

// DeleteCheckpointsOf removes every checkpoint taken from the work tree at
// root, for any pane: what a removed worktree leaves behind. dir is any
// checkout of the repository that is still there.
func DeleteCheckpointsOf(ctx context.Context, dir, root string) (int, error) {
	list, err := ListCheckpoints(ctx, dir, "")
	if err != nil {
		return 0, err
	}
	var drop []Checkpoint
	for _, cp := range list {
		if cp.Worktree == root {
			drop = append(drop, cp)
		}
	}
	return len(drop), DeleteCheckpoints(ctx, dir, drop)
}

// FindCheckpoint returns checkpoint n of the pane.
func FindCheckpoint(list []Checkpoint, n int) (Checkpoint, bool) {
	for _, cp := range list {
		if cp.N == n {
			return cp, true
		}
	}
	return Checkpoint{}, false
}

// CheckpointBase is the tree a checkpoint's own change is read against: the
// pane's checkpoint before it, else HEAD when it was taken, else the empty
// tree. It returns the tree and what it is, as a person reads it.
func CheckpointBase(ctx context.Context, dir string, list []Checkpoint, cp Checkpoint) (tree, name string, err error) {
	var prev *Checkpoint
	for i := range list {
		if list[i].Pane == cp.Pane && list[i].N < cp.N && (prev == nil || list[i].N > prev.N) {
			prev = &list[i]
		}
	}
	if prev != nil {
		return prev.Tree, "checkpoint " + strconv.Itoa(prev.N), nil
	}
	if cp.Head != "" {
		out, err := runCtx(ctx, dir, nil, "rev-parse", "--verify", "--quiet", cp.Head+"^{tree}")
		if err == nil {
			return strings.TrimSpace(out), "HEAD " + short(cp.Head), nil
		}
	}
	out, err := runStdin(ctx, dir, nil, "", "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(out), "an empty tree", nil
}

// short is the first seven characters of a hash.
func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// RestoreResult is what RestoreTree changed.
type RestoreResult struct {
	// Written are the paths written from the checkpoint, Removed the paths
	// the checkpoint does not have, both relative to the root.
	Written []string `json:"written"`
	Removed []string `json:"removed"`
}

// RestoreTree makes the files of the work tree at root match tree, where
// current is the tree the work tree holds now (from SnapshotTree, so the
// caller has kept it). Only the paths that differ between the two are
// touched. Ignored files are in neither tree and are never touched. The
// index, HEAD and the branch are as they were: the files are written through
// a temporary index, so git status afterwards shows the restored files as
// changed against what the index holds.
func RestoreTree(ctx context.Context, root, current, tree string) (RestoreResult, error) {
	var res RestoreResult
	out, err := runCtx(ctx, root, nil, "diff-tree", "-r", "-z", "--no-renames", "--name-status", current, tree)
	if err != nil {
		return res, err
	}
	var write []string
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		status, path := fields[i], fields[i+1]
		if status == "D" {
			res.Removed = append(res.Removed, path)
		} else {
			write = append(write, path)
		}
	}
	// Removals first: a path that was a file and is now a directory, or the
	// other way, needs the old entry gone before the new one is written.
	for _, path := range res.Removed {
		if err := removeInside(root, path); err != nil {
			return res, err
		}
	}
	if len(write) == 0 {
		return res, nil
	}
	tmp, err := os.CreateTemp("", "tuios-restore-index-*")
	if err != nil {
		return res, err
	}
	index := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(index)
	defer func() { _ = os.Remove(index) }()
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err := runCtx(ctx, root, env, "read-tree", tree); err != nil {
		return res, err
	}
	// checkout-index takes paths relative to where it runs, which is the
	// root. --force writes over the file that is there.
	stdin := strings.Join(write, "\x00") + "\x00"
	if _, err := runStdin(ctx, root, env, stdin, "checkout-index", "--force", "-z", "--stdin"); err != nil {
		return res, err
	}
	res.Written = write
	return res, nil
}

// removeInside removes the file at path under root, and then each parent
// directory it leaves empty, up to root. A path whose parent leads out of
// root through a symbolic link is refused.
func removeInside(root, path string) error {
	full := filepath.Join(root, filepath.FromSlash(path))
	parent := filepath.Dir(full)
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	rel, err := filepath.Rel(realRoot, realParent)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%q leaves the work tree", path)
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}
	for dir := parent; dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			break
		}
	}
	return nil
}

// runStdin is runCtx with stdin.
func runStdin(ctx context.Context, dir string, env []string, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true")
	cmd.Env = append(cmd.Env, env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("git %s: %w", args[0], ctx.Err())
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return stdout.String(), nil
}
