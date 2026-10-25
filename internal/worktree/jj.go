package worktree

import (
	"path/filepath"

	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
)

// jjDetect is DetectEither for a jj workspace, the main one included.
func jjDetect(root, mainRoot string) (Info, bool) {
	// The rail's reading, cached on jj's operation log, so a detection that
	// finds nothing new runs no jj.
	branch := filepath.Base(root)
	if st, ok := gitstate.Read(root); ok && st.Branch != "" {
		branch = st.Branch
	}
	return Info{Repo: filepath.Base(mainRoot), RepoRoot: mainRoot, Branch: branch, Path: root}, true
}
