package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
)

// Stamp is a work tree's state as a snapshot left it: the tree, and the index
// the tree was written from, kept in a folder of its own. The index holds the
// size and times of every file as the snapshot read them, so Changed can ask
// whether any file is different now from those alone: no file is read and no
// object is written unless git finds a file whose times it cannot trust.
type Stamp struct {
	Tree    string
	tmp     *tempDir
	skipped map[string]bool
	// dirs are the times of every folder that holds a path of the stamp,
	// and of every folder above one, up to the work tree's root. A file
	// made, removed or renamed anywhere changes the time of a folder here:
	// the first folder above it that was already there.
	dirs map[string]time.Time
}

// ErrNoStamp is SnapshotStamp's answer for a jj workspace, which has no git
// index to keep.
var ErrNoStamp = errors.New("a jj workspace has no index to stamp")

// SnapshotStamp writes the work tree at path as a tree, as SnapshotTree
// does, and keeps what it wrote it from as a Stamp, which the caller closes.
func SnapshotStamp(ctx context.Context, path string, maxUntracked int64) (*Stamp, []string, error) {
	if gitstate.Find(path).JJ {
		return nil, nil, ErrNoStamp
	}
	skipped, err := largeUntracked(ctx, path, maxUntracked)
	if err != nil {
		return nil, nil, err
	}
	// Of the index kind, so SweepTemp removes one a crashed process left.
	tmp, tctx, err := newTemp(ctx, "index")
	if err != nil {
		return nil, nil, err
	}
	tree, late, err := snapshotInto(tctx, path, skipped, maxUntracked, filepath.Join(tmp.path, "index"))
	if err != nil {
		tmp.remove()
		return nil, nil, err
	}
	skipped = mergeSkipped(skipped, late)
	s := &Stamp{Tree: tree, tmp: tmp, skipped: map[string]bool{}, dirs: map[string]time.Time{}}
	for _, p := range skipped {
		s.skipped[p] = true
	}
	out, err := runCtx(tctx, path, []string{"GIT_INDEX_FILE=" + filepath.Join(tmp.path, "index")}, "ls-files", "-z")
	if err != nil {
		tmp.remove()
		return nil, nil, err
	}
	rels := append(strings.Split(out, "\x00"), skipped...)
	for _, rel := range rels {
		for d := filepath.Dir(filepath.FromSlash(rel)); rel != ""; d = filepath.Dir(d) {
			if _, ok := s.dirs[d]; ok {
				break
			}
			s.dirs[d] = time.Time{}
			if d == "." || d == string(filepath.Separator) {
				break
			}
		}
	}
	s.dirs["."] = time.Time{}
	for d := range s.dirs {
		info, err := os.Stat(filepath.Join(path, d))
		if err != nil {
			delete(s.dirs, d)
			continue
		}
		s.dirs[d] = info.ModTime()
	}
	return s, skipped, nil
}

// Changed reports whether the work tree at path may differ from the stamp:
// a file of the stamp changed or removed (git update-index --refresh against
// the stamp's own index, which compares sizes and times and reads only the
// files whose times git cannot trust), or a folder of the stamp whose time
// moved, which a file made, removed or renamed in it does. It never misses a
// change. A file git ignores, made in a folder of the stamp, reads as a
// change, and costs the caller one snapshot that matches the last.
func (s *Stamp) Changed(ctx context.Context, path string) (bool, error) {
	for d, at := range s.dirs {
		info, err := os.Stat(filepath.Join(path, d))
		if err != nil || !info.ModTime().Equal(at) {
			return true, nil
		}
	}
	// It writes only the stamp's own index, never the repository's. A file
	// that changed makes it exit 1.
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(s.tmp.path, "index")}
	cmd := childproc.Git(ctx, path, "update-index", "--refresh")
	cmd.Env = append(cmd.Env, gitstate.Find(path).GitEnv()...)
	cmd.Env = append(cmd.Env, env...)
	if err := cmd.Run(); err != nil {
		if exit, ok := errors.AsType[interface {
			error
			ExitCode() int
		}](err); ok && exit.ExitCode() == 1 {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

// Close removes the stamp's index. It is safe to call more than once, and on
// a nil stamp.
func (s *Stamp) Close() {
	if s != nil && s.tmp != nil {
		s.tmp.remove()
	}
}
