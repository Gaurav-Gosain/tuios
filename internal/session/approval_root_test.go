package session

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPaneRootIsFixed is a security boundary: the root the risk rules and the
// files grant hold a pane to must come only from what the daemon fixed, and a
// root that holds the whole account is no root. Ways it could fail: a
// detected worktree (it follows the pane's cd) used as the root; a file system
// root, or the home folder or a folder above it, used as a root; a trailing
// slash or "." that hides one of those.
func TestPaneRootIsFixed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	start := filepath.Join(home, "src", "proj")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	managed := &WorktreeInfo{Managed: true}
	managed.Path = filepath.Join(home, "wt")
	atHome := &WorktreeInfo{Managed: true}
	atHome.Path = home
	detected := &WorktreeInfo{}
	detected.Path = "/elsewhere/repo"
	for _, tc := range []struct {
		name  string
		wt    *WorktreeInfo
		start string
		want  string
	}{
		{"start folder", nil, start, start},
		{"start folder with a slash", nil, start + "/", start},
		{"managed worktree wins", managed, start, managed.Path},
		{"detected worktree ignored", detected, start, start},
		{"detected worktree and no start", detected, "", ""},
		{"no start", nil, "", ""},
		{"file system root", nil, "/", ""},
		{"file system root with a dot", nil, "/.", ""},
		{"home", nil, home, ""},
		{"home with a slash", nil, home + "/", ""},
		{"above home", nil, filepath.Dir(home), ""},
		{"a link to home", nil, link, ""},
		{"managed worktree at home", atHome, start, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := paneRoot(tc.wt, tc.start); got != tc.want {
				t.Errorf("paneRoot(%v, %q) = %q, want %q", tc.wt, tc.start, got, tc.want)
			}
		})
	}
}
