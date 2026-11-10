package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// maxText is the size of the largest file shown whole: larger ones show
// as their patch.
const maxText = 1 << 20

// Snapshot writes a tree of the working tree as it is, its tracked files
// and its untracked ones but those ignored, and returns its id. It goes
// through an index of its own, seeded with a copy of the worktree's so
// that git hashes only what changed, and leaves the worktree's alone. The
// tree and its blobs are loose objects nothing refers to, which git's gc
// prunes once they are old.
func (r Repo) Snapshot(ctx context.Context) (string, error) {
	dir, err := os.MkdirTemp("", "kero-index-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	index := filepath.Join(dir, "index")
	if b, err := os.ReadFile(filepath.Join(r.GitDir, "index")); err == nil {
		if err := os.WriteFile(index, b, 0o600); err != nil {
			return "", err
		}
	}
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err := run(ctx, r.Root, env, "add", "--all", "--", "."); err != nil {
		return "", err
	}
	out, err := run(ctx, r.Root, env, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Bases a diff compares the working tree with.
const (
	BaseTurn   = "turn"   // a snapshot: what an agent's turn started from
	BaseHead   = "head"   // HEAD: what isn't committed
	BaseBranch = "branch" // where HEAD left the default branch: a PR's diff
)

// Base returns the tree-ish of a base, and what it is to show: "HEAD", or
// the default branch's name; turn is the snapshot's tree, for BaseTurn.
func (r Repo) Base(ctx context.Context, base, turn string) (rev, name string, err error) {
	switch base {
	case BaseTurn:
		if turn == "" {
			return "", "", errors.New("no turn to compare with")
		}
		return turn, "", nil
	case BaseBranch:
		def := r.DefaultBranch()
		for _, ref := range []string{"refs/remotes/origin/" + def, "refs/heads/" + def} {
			if def == "" {
				break
			}
			if out, err := run(ctx, r.Root, nil, "merge-base", "HEAD", ref); err == nil {
				return strings.TrimSpace(string(out)), def, nil
			}
		}
	}
	if _, err := run(ctx, r.Root, nil, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		// No commit yet: everything is new.
		out, err := r.emptyTree(ctx)
		return out, "HEAD", err
	}
	return "HEAD", "HEAD", nil
}

// emptyTree returns the id of the empty tree, of the repository's hash.
func (r Repo) emptyTree(ctx context.Context) (string, error) {
	cmd := []string{"hash-object", "-t", "tree", "--stdin"}
	git := command()
	if git == "" {
		return "", ErrNoGit
	}
	c := exec.CommandContext(ctx, git, cmd...)
	c.Dir = r.Root
	c.Stdin = strings.NewReader("")
	hide(c)
	out, err := c.Output()
	return strings.TrimSpace(string(out)), err
}

// File is a file changed since a base.
type File struct {
	Path    string // slash-separated, from the worktree's root
	OldPath string // what it was renamed from, else ""
	Status  string // "added", "modified", "deleted", "renamed" or "type"
	Added   int
	Removed int
	Binary  bool
}

// Changes returns the files of the working tree changed since rev: a
// tree-ish, as Base returns. now is a snapshot of the working tree, which
// it takes when "".
func (r Repo) Changes(ctx context.Context, rev, now string) ([]File, error) {
	if now == "" {
		var err error
		if now, err = r.Snapshot(ctx); err != nil {
			return nil, err
		}
	}
	out, err := run(ctx, r.Root, nil, "diff", "--no-ext-diff", "--no-textconv", "-M", "-z", "--name-status", rev, now)
	if err != nil {
		return nil, err
	}
	var files []File
	byPath := map[string]int{}
	fields := strings.Split(string(out), "\x00")
	for i := 0; i+1 < len(fields); i++ {
		code := fields[i]
		if code == "" {
			continue
		}
		f := File{Path: fields[i+1]}
		i++
		switch code[0] {
		case 'A':
			f.Status = "added"
		case 'D':
			f.Status = "deleted"
		case 'R', 'C':
			if i+1 < len(fields) {
				f.OldPath, f.Path = f.Path, fields[i+1]
				i++
			}
			f.Status = "renamed"
			if code[0] == 'C' {
				f.Status, f.OldPath = "added", ""
			}
		case 'T':
			f.Status = "type"
		default:
			f.Status = "modified"
		}
		byPath[f.Path] = len(files)
		files = append(files, f)
	}
	out, err = run(ctx, r.Root, nil, "diff", "--no-ext-diff", "--no-textconv", "-M", "-z", "--numstat", rev, now)
	if err != nil {
		return nil, err
	}
	for _, n := range parseNumstat(out) {
		if i, ok := byPath[n.path]; ok {
			files[i].Added, files[i].Removed, files[i].Binary = n.added, n.removed, n.binary
		}
	}
	return files, nil
}

// Patch returns the working tree's changes since rev as git diff prints
// them, of the files under paths, or all; now is as Changes takes it.
func (r Repo) Patch(ctx context.Context, rev, now string, paths ...string) (string, error) {
	if now == "" {
		var err error
		if now, err = r.Snapshot(ctx); err != nil {
			return "", err
		}
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "-M", rev, now}
	if len(paths) > 0 {
		args = append(append(args, "--"), paths...)
	}
	out, err := run(ctx, r.Root, nil, args...)
	return string(out), err
}

// Text is a changed file's text before and now.
type Text struct {
	Old, New string
	// Hash is the file's on disk, to write it back only as it was
	// (Write).
	Hash string
	// Binary: either side is no text. Large: too large to show whole;
	// Patch is then the change, as git diff prints it.
	Binary bool
	Large  bool
	Patch  string
}

// FileText returns a file's text in rev, from oldPath ("" for path), and
// in the working tree now.
func (r Repo) FileText(ctx context.Context, rev, path, oldPath string) (Text, error) {
	full, err := r.local(path)
	if err != nil {
		return Text{}, err
	}
	if oldPath == "" {
		oldPath = path
	}
	if _, err := r.local(oldPath); err != nil {
		return Text{}, err
	}
	var t Text
	nb, err := readWorking(full)
	if err != nil && !os.IsNotExist(err) {
		return Text{}, err
	}
	t.Hash = Hash(nb)
	ob, err := run(ctx, r.Root, nil, "cat-file", "blob", rev+":"+oldPath)
	if err != nil {
		ob = nil // added since
	}
	switch {
	case binary(ob) || binary(nb):
		t.Binary = true
	case len(ob) > maxText || len(nb) > maxText:
		t.Large = true
		t.Patch, _ = r.Patch(ctx, rev, "", path)
	default:
		t.Old, t.New = string(ob), string(nb)
	}
	return t, nil
}

// readWorking reads a file of the working tree as git sees it: a symbolic
// link is its target's path, not what it points to.
func readWorking(full string) ([]byte, error) {
	fi, err := os.Lstat(full)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(full)
		return []byte(filepath.ToSlash(target)), err
	}
	if !fi.Mode().IsRegular() {
		return nil, nil
	}
	return os.ReadFile(full)
}

// Hash returns the hash Text and Write take of a file's content.
func Hash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Changed reports whether a file of the working tree is other than in
// rev, as git keeps it: one that a diff with rev shows.
func (r Repo) Changed(ctx context.Context, rev, path string) (bool, error) {
	full, err := r.local(path)
	if err != nil {
		return false, err
	}
	nb, err := readWorking(full)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	ob, err := run(ctx, r.Root, nil, "cat-file", "blob", rev+":"+path)
	if err != nil {
		return nb != nil, nil // not in rev: changed when it is now
	}
	return !bytes.Equal(ob, nb), nil
}

// ErrChanged: the file isn't as it was when read.
var ErrChanged = errors.New("the file changed since it was shown")

// Write writes text to a regular file of the working tree, keeping its
// mode, when its content still hashes to hash: one written meanwhile is
// left as it is (ErrChanged).
func (r Repo) Write(path, hash, text string) error {
	full, err := r.local(path)
	if err != nil {
		return err
	}
	return WriteFile(full, hash, text)
}

// WriteFile writes text to a regular file, keeping its mode, when its
// content still hashes to hash: one written meanwhile is left as it is
// (ErrChanged).
func WriteFile(full, hash, text string) error {
	fi, err := os.Lstat(full)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return err
	}
	if Hash(b) != hash {
		return ErrChanged
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".kero-*")
	if err != nil {
		return err
	}
	_, err = tmp.WriteString(text)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), fi.Mode().Perm())
	}
	if err == nil {
		err = os.Rename(tmp.Name(), full)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// local returns the file of the worktree a slash-separated path names,
// refusing one outside it, through .. or a symbolic link, or in its git
// directory.
func (r Repo) local(p string) (string, error) {
	slashed := p
	if filepath.Separator == '\\' {
		slashed = strings.ReplaceAll(p, `\`, "/")
	}
	clean := path.Clean(slashed)
	if p == "" || path.IsAbs(clean) || filepath.IsAbs(filepath.FromSlash(clean)) || clean == ".." || strings.HasPrefix(clean, "../") ||
		strings.EqualFold(clean, ".git") || strings.HasPrefix(strings.ToLower(clean), ".git/") {
		return "", errors.New("not a path in the worktree: " + p)
	}
	full := filepath.Join(r.Root, filepath.FromSlash(clean))
	// The directory holding it, with its links resolved, must be the
	// worktree's.
	root, err := filepath.EvalSymlinks(r.Root)
	if err != nil {
		return "", err
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(full))
	if err != nil {
		if os.IsNotExist(err) {
			return full, nil // gone: nothing to read or write through it
		}
		return "", err
	}
	if rel, err := filepath.Rel(root, dir); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("not a path in the worktree: " + p)
	}
	return full, nil
}

// binary reports whether b looks like no text: a NUL in its first 8 KB,
// as git judges.
func binary(b []byte) bool {
	return bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0
}
