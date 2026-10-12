package session

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestApprovalRootIsFixed is a security boundary: the root the risk rules
// hold a pane to must come only from folders fixed when they were recorded,
// and a folder that holds the whole account is no root. Ways it could fail:
// the home folder, a folder above it or a file system root used as a root; a
// link to the home folder used as a root; a recorded folder that has become
// a link followed to wherever it points now; the agent's folder used ahead
// of a narrower pane folder; an agent folder that is too wide used at all.
func TestApprovalRootIsFixed(t *testing.T) {
	home := realPath(t.TempDir())
	t.Setenv("HOME", home)
	proj := filepath.Join(home, "src", "proj")
	other := filepath.Join(home, "src", "other")
	for _, d := range []string{proj, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	links := realPath(t.TempDir())
	homeLink := filepath.Join(links, "home-link")
	projLink := filepath.Join(links, "proj-link")
	for link, target := range map[string]string{homeLink: home, projLink: proj} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	wt := filepath.Join(home, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                      string
		worktree, start, agentDir string
		want                      string
	}{
		{"start folder", "", proj, "", proj},
		{"start folder with a slash", "", proj + "/", "", proj},
		{"a recorded folder that is now a link is no root", "", projLink, "", ""},
		{"a link falls to the agent's folder", "", projLink, other, other},
		{"worktree wins", wt, proj, "", wt},
		{"worktree at home falls to the start folder", home, proj, "", proj},
		{"no folder at all", "", "", "", ""},
		{"file system root", "", "/", "", ""},
		{"home", "", home, "", ""},
		{"above home", "", filepath.Dir(home), "", ""},
		{"a link to home", "", homeLink, "", ""},
		{"start at home falls to the agent's folder", "", home, proj, proj},
		{"start above home falls to the agent's folder", "", "/", proj, proj},
		{"a narrow start folder beats the agent's", "", other, proj, other},
		{"agent folder at home is no root", "", home, home, ""},
		{"agent folder through a link to home is no root", "", home, homeLink, ""},
		{"agent folder that is not there is no root", "", home, filepath.Join(home, "gone"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := approvalRoot(tc.worktree, tc.start, tc.agentDir); got != tc.want {
				t.Errorf("approvalRoot(%q, %q, %q) = %q, want %q", tc.worktree, tc.start, tc.agentDir, got, tc.want)
			}
		})
	}
}

// TestAgentStartDirIsFirstWins: a later report of the same agent session
// never moves its folder, and a window keeps a bounded number of sessions.
func TestAgentStartDirIsFirstWins(t *testing.T) {
	s := &Session{}
	s.noteAgentStartDir("w", "a", "/p/one")
	s.noteAgentStartDir("w", "a", "/p/two")
	if got := s.agentStartDir("w", "a"); got != "/p/one" {
		t.Errorf("the folder moved to %q, want /p/one", got)
	}
	for i := range agentStartDirsPerWindow * 2 {
		s.noteAgentStartDir("w", "s"+strconv.Itoa(i), "/p/x")
	}
	s.agentDirsMu.Lock()
	n := len(s.agentDirs["w"])
	s.agentDirsMu.Unlock()
	if n > agentStartDirsPerWindow {
		t.Errorf("a window keeps %d agent folders, want at most %d", n, agentStartDirsPerWindow)
	}
}
