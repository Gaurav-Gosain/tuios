package serverscope_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"

	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
)

// TestApplyReadsTheCurrentStateHome guards against a cached base going stale.
// internal/session/verb_files_confine.go builds its cross-machine-link deny
// list from BaseStateHome, so a stale base would deny the wrong directory
// tree rather than the one a named server's files actually live under.
func TestApplyReadsTheCurrentStateHome(t *testing.T) {
	saved := xdg.StateHome
	t.Cleanup(func() {
		_ = serverscope.Apply("")
		xdg.StateHome = saved
	})

	xdg.StateHome = "/A"
	if err := serverscope.Apply("foo"); err != nil {
		t.Fatalf("Apply(foo): %v", err)
	}
	if want := filepath.Join("/A", serverscope.StateDirName, "foo"); xdg.StateHome != want {
		t.Fatalf("xdg.StateHome = %q, want %q", xdg.StateHome, want)
	}

	if err := serverscope.Apply(""); err != nil {
		t.Fatalf("Apply(\"\"): %v", err)
	}
	if xdg.StateHome != "/A" {
		t.Fatalf("Apply(\"\") left xdg.StateHome = %q, want the restored /A", xdg.StateHome)
	}

	// Something outside this process's own scoping changed the state home
	// while this process was on the default server, the way a test's XDG
	// isolation helper does between cases.
	xdg.StateHome = "/B"

	if err := serverscope.Apply("bar"); err != nil {
		t.Fatalf("Apply(bar): %v", err)
	}
	if want := filepath.Join("/B", serverscope.StateDirName, "bar"); xdg.StateHome != want {
		t.Fatalf("xdg.StateHome = %q, want %q (base should be /B, not the stale /A)", xdg.StateHome, want)
	}
	if got, want := serverscope.BaseStateHome(), "/B"; got != want {
		t.Fatalf("BaseStateHome() = %q, want %q", got, want)
	}

	// A second named server while one is already applied keeps the same base.
	if err := serverscope.Apply("baz"); err != nil {
		t.Fatalf("Apply(baz): %v", err)
	}
	if got, want := serverscope.BaseStateHome(), "/B"; got != want {
		t.Fatalf("BaseStateHome() after switching named servers = %q, want %q", got, want)
	}
	if want := filepath.Join("/B", serverscope.StateDirName, "baz"); xdg.StateHome != want {
		t.Fatalf("xdg.StateHome = %q, want %q", xdg.StateHome, want)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"", false},
		{"foo", true},
		{"foo-bar_1", true},
		{"foo bar", false},
		{"foo/bar", false},
		{"../escape", false},
		{strings.Repeat("a", serverscope.MaxNameLen), true},
		{strings.Repeat("a", serverscope.MaxNameLen+1), false},
	}
	for _, c := range cases {
		err := serverscope.Validate(c.name)
		if (err == nil) != c.ok {
			t.Errorf("Validate(%q) error = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}
