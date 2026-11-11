package shquote

import (
	"os"
	"os/exec"
	"runtime"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }

// Each word reads back as itself in sh, whatever it holds. This is the
// boundary: a word that sh reads differently runs a different command.
func TestWordRoundTripsThroughSh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	for _, item := range []string{"plain", "user@host:22", "two words", "it's", `"$HOME" $(id) ; rm -rf / #`, "tab\there", "new\nline", "*", "", "~", "café", "a^b", "FOO=bar", "--x=1"} {
		for name, quoted := range map[string]string{"Word": Word(item), "Quote": Quote(item)} {
			out, err := exec.Command("sh", "-c", "printf %s "+quoted).Output()
			if err != nil || string(out) != item {
				t.Errorf("%s: item %q came back as %q (%v) through %s", name, item, out, err, quoted)
			}
		}
	}
}

func TestWordLeavesSafeWordsBare(t *testing.T) {
	for _, w := range []string{"host-1.example.com", "/usr/bin/x_y", "--x=1", "a@b:c,d+e%f"} {
		if got := Word(w); got != w {
			t.Errorf("a safe word was quoted: %s", got)
		}
	}
	for _, w := range []string{"FOO=bar", "_x=1", ""} {
		if got := Word(w); got == w {
			t.Errorf("%q was left bare", w)
		}
	}
}
