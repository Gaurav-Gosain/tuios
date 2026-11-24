package gitstate

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// WatchDirs names the directories whose entries change when what Read
// answers for dir can change, so a caller can watch them and read again only
// then. found is false when dir is in no repository, and the one directory
// named then is dir itself: a ".git" or ".jj" made in it is the change that
// matters.
//
// Every file Read looks at is written by git or jj through a lock file that is
// renamed into place, so a change to it is a change to its directory's
// entries: HEAD, the index and packed-refs in the git directory, the config in
// the shared one, a loose ref in its own folder, the table list of a reftable,
// and a new operation head of jj. A ref folder that is not there yet (a remote
// never fetched) is watched through the nearest folder above it that is, so
// the first fetch, which makes it, is seen too.
//
// It only reads files, the same ones Read reads.
func WatchDirs(dir string) (dirs []string, found bool) {
	if dir == "" {
		return nil, false
	}
	gitdir, commondir, root, ok := Locate(dir)
	if jroot, repoDir, jok := locateJJ(dir); jok && jjWins(jroot, root, ok) {
		return uniqueDirs(filepath.Join(repoDir, "op_heads", "heads")), true
	}
	if !ok {
		return []string{filepath.Clean(dir)}, false
	}
	dirs = []string{gitdir, commondir}
	if usesReftable(commondir) {
		return uniqueDirs(append(dirs, filepath.Join(gitdir, "reftable"), filepath.Join(commondir, "reftable"))...), true
	}
	line, err := readFirstLine(filepath.Join(gitdir, "HEAD"))
	if err != nil {
		return uniqueDirs(dirs...), true
	}
	ref, symbolic := strings.CutPrefix(line, "ref:")
	if !symbolic {
		return uniqueDirs(dirs...), true
	}
	ref = strings.TrimSpace(ref)
	dirs = append(dirs, refFolder(commondir, ref))
	if gitdir != commondir {
		dirs = append(dirs, refFolder(gitdir, ref))
	}
	if up := upstreamOf(commondir, strings.TrimPrefix(ref, "refs/heads/")); up != "" {
		dirs = append(dirs, refFolder(commondir, up))
	}
	return uniqueDirs(dirs...), true
}

// refFolder is the folder a loose ref's file is in under base, or the
// nearest folder above it that exists, and never one above base.
func refFolder(base, ref string) string {
	dir := filepath.Dir(filepath.Join(base, filepath.FromSlash(ref)))
	for dir != base && len(dir) > len(base) {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	return base
}

// uniqueDirs is dirs cleaned, in order, with each named once.
func uniqueDirs(dirs ...string) []string {
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d = filepath.Clean(d); !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

// ForgetMiss drops the memory that dir is in no repository, so the next Read
// looks again. A caller that saw a ".git" or ".jj" made in dir calls it.
func ForgetMiss(dir string) {
	mu.Lock()
	delete(misses, dir)
	mu.Unlock()
}
