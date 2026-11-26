package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// IndexTree writes the tree of the index of the work tree at path, and
// returns its id. It writes from a copy of the index. git write-tree stores
// the trees it makes back into the index it reads, so on the index itself
// every read would rewrite the repository's index file, and a watch on the git
// folder would see a change that is not one. The copy keeps the original's
// times, so git trusts its stat data as it trusts the original's.
func IndexTree(ctx context.Context, path string) (string, error) {
	tmp, ctx, err := newTemp(ctx, "index")
	if err != nil {
		return "", err
	}
	defer tmp.remove()
	index := filepath.Join(tmp.path, "index")
	if !copyIndex(ctx, path, index) {
		// No index yet, or an empty one: git refuses an empty file as an
		// index, and with no file its tree is the empty tree.
		_ = os.Remove(index)
	}
	out, err := runCtx(ctx, path, []string{"GIT_INDEX_FILE=" + index}, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
