package worktree

import (
	"errors"
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

func TestNormalizeRemoteJoinsTheSpellingsOfOneRepository(t *testing.T) {
	same := []string{
		"https://github.com/Gaurav-Gosain/tuios",
		"https://github.com/Gaurav-Gosain/tuios.git",
		"https://GitHub.com/Gaurav-Gosain/tuios/",
		"git@github.com:Gaurav-Gosain/tuios.git",
		"github.com:Gaurav-Gosain/tuios",
		"ssh://git@github.com/Gaurav-Gosain/tuios.git",
		"ssh://git@github.com:22/Gaurav-Gosain/tuios",
		"git://github.com/Gaurav-Gosain/tuios",
	}
	want := "github.com/Gaurav-Gosain/tuios"
	for _, s := range same {
		if got := NormalizeRemote(s); got != want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q", s, got, want)
		}
	}
	different := []string{
		"https://github.com/Gaurav-Gosain/tuios-web",
		"https://gitlab.com/Gaurav-Gosain/tuios",
		"https://github.com/someone-else/tuios",
		"/src/tuios",
	}
	for _, s := range different {
		if got := NormalizeRemote(s); got == want {
			t.Errorf("NormalizeRemote(%q) = %q, which matches a different repository", s, got)
		}
	}
	if a, b := NormalizeRemote("/tmp/x/origin.git"), NormalizeRemote("file:///tmp/x/origin.git"); a != b {
		t.Errorf("a path and its file URL differ: %q and %q", a, b)
	}
	if NormalizeRemote("") != "" {
		t.Error("an empty remote is not empty")
	}
}

func TestValidCloneURLRefusesAnythingButANetworkRemote(t *testing.T) {
	ok := []string{
		"https://github.com/o/r.git",
		"ssh://git@host/o/r",
		"git://host/o/r",
		"git@github.com:o/r.git",
	}
	for _, s := range ok {
		if err := ValidCloneURL(s); err != nil {
			t.Errorf("ValidCloneURL(%q) = %v, want nil", s, err)
		}
	}
	bad := []string{
		"",
		"/etc",
		"../secrets",
		"file:///home/me/.ssh",
		"ext::sh -c touch% /tmp/pwned",
		"fd::17",
		"-uhelp",
		"--upload-pack=touch /tmp/x",
		"http://host/o/r",
		"https://host/o/r\n",
		" https://host/o/r",
		"C:\\src\\repo",
		"-oProxyCommand=x:o/r",
		"ssh://-oProxyCommand=x/o/r",
	}
	for _, s := range bad {
		if err := ValidCloneURL(s); !errors.Is(err, ErrCloneURL) {
			t.Errorf("ValidCloneURL(%q) = %v, want ErrCloneURL", s, err)
		}
	}
}

func TestCloneRefusesALocalPathBeforeRunningGit(t *testing.T) {
	repo := testutil.GitRepo(t)
	parent := t.TempDir()
	if _, err := Clone(repo, parent); !errors.Is(err, ErrCloneURL) {
		t.Fatalf("Clone of a local path = %v, want ErrCloneURL", err)
	}
	if entries, _ := os.ReadDir(parent); len(entries) != 0 {
		t.Errorf("a refused clone left %d entries behind", len(entries))
	}
}
