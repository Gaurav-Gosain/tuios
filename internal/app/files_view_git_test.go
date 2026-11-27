package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFilesGitFolderInNoRepositoryReadsNothing: a side on a folder in no
// repository makes no read and no watch: the marks cost nothing there.
// A folder in a repository starts one read.
func TestFilesGitFolderInNoRepositoryReadsNothing(t *testing.T) {
	m := shotOS(t)
	m.IsDaemonSession = true
	m.explorer.open = true
	plain := t.TempDir()
	m.filesGit.gen = 7
	if cmd := m.applyFilesGitRoots(filesGitRootsMsg{gen: 7, roots: map[string]string{plain: ""}}); cmd != nil {
		if msg := cmd(); msg != nil {
			t.Errorf("a folder in no repository ran %T", msg)
		}
	}
	if len(m.filesGit.repos) != 0 {
		t.Errorf("a folder in no repository has %d repositories", len(m.filesGit.repos))
	}

	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	m.applyFilesGitRoots(filesGitRootsMsg{gen: 7, roots: map[string]string{repo: repo}})
	if r := m.filesGit.repos[repo]; r == nil || !r.reading {
		t.Errorf("a folder in a repository started no read: %+v", r)
	}
	// An answer for a view since closed is dropped.
	if cmd := m.applyFilesGitRoots(filesGitRootsMsg{gen: 6, roots: map[string]string{repo: repo}}); cmd != nil {
		t.Error("an answer of an old view did something")
	}
}
