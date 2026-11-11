package tuie2e

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Issue #1768: [shell] login = true starts each pane's shell as a login
// shell, so it reads the user's login files (.zprofile, .bash_profile,
// .profile) as a terminal on macOS does. Off by default.
//
// Each case writes the user's files into the test's home: the login file
// exports TK_LOGIN, and the rc file puts it in the prompt, so the prompt on
// the screen says which files the shell read, and in what order.
//
// How these could pass wrongly, written down first:
//   - The prompt could come from the developer's own files. HOME is the
//     test's, and ZDOTDIR and BASH_ENV are out of the daemon's environment.
//   - Login could cost the prompt marks. zsh and bash with marks are asked
//     with tuios run, which needs them, and bash must still read tuios's rc
//     file, which reads .bashrc once: the prompt counts the reads.
//   - "Off by default" could pass because the pane had not drawn its prompt.
//     The default case waits for its prompt, which says "none".
func TestLoginShellReadsTheLoginFiles(t *testing.T) {
	const loginOn = "[shell]\nlogin = true\n"

	t.Run("zsh", func(t *testing.T) {
		zsh := siShellPath(t, "zsh")
		const sess = "lszsh"
		base := siSession(t, sess, zsh, loginOn, map[string]string{
			".zshenv":   siZshenv,
			".zprofile": "export TK_LOGIN=zprofile\n",
			".zshrc":    "PS1=\"Z-${TK_LOGIN:-none}> \"\n",
		}, nil)
		win := firstWindow(t, base, sess).ID
		siWaitScreen(t, base, sess, win, "Z-zprofile>")
		if out := siRunOK(t, base, sess, win, `[[ -o login ]] && echo LOGIN-''YES`); !strings.Contains(out, "LOGIN-YES") {
			t.Fatalf("ASSERTION: zsh is not a login shell: %q", out)
		}
		siSave(t, "zsh-login.txt", siWaitScreen(t, base, sess, win, "Z-zprofile>"))
	})

	t.Run("bash", func(t *testing.T) {
		bash := siShellPath(t, "bash")
		const sess = "lsbash"
		base := siSession(t, sess, bash, loginOn, map[string]string{
			".bash_profile": "export TK_LOGIN=bash_profile\n[ -r ~/.bashrc ] && . ~/.bashrc\n",
			".bashrc":       "BRC=$((BRC+1))\nPS1=\"B-${TK_LOGIN:-none}-$BRC> \"\n",
		}, nil)
		win := firstWindow(t, base, sess).ID
		// .bash_profile ran, and it read .bashrc: tuios's rc file did not
		// read it a second time.
		siWaitScreen(t, base, sess, win, "B-bash_profile-1>")
		if bashMarks(t, bash) {
			if out := siRunOK(t, base, sess, win, "echo RUN-''WORKS"); !strings.Contains(out, "RUN-WORKS") {
				t.Fatalf("ASSERTION: the login bash lost its prompt marks: %q", out)
			}
		}
		siSave(t, "bash-login.txt", siWaitScreen(t, base, sess, win, "B-bash_profile-1>"))
	})

	t.Run("sh", func(t *testing.T) {
		sh := siShellPath(t, "sh")
		const sess = "lssh"
		base := siSession(t, sess, sh, loginOn, map[string]string{
			".profile": "TK_LOGIN=profile\nPS1=\"S-$TK_LOGIN> \"\n",
		}, nil)
		win := firstWindow(t, base, sess).ID
		siSave(t, "sh-login.txt", siWaitScreen(t, base, sess, win, "S-profile>"))
	})

	t.Run("fish", func(t *testing.T) {
		fish := siShellPath(t, "fish")
		const sess = "lsfish"
		base := siSession(t, sess, fish, loginOn, map[string]string{
			// fish reads its config from XDG_CONFIG_HOME, the harness's,
			// next to the test's home.
			"../XDG_CONFIG_HOME/fish/config.fish": "if status is-login\n    set -gx TK_LOGIN fish\nend\nfunction fish_greeting; end\nfunction fish_prompt; echo -n \"F-$TK_LOGIN> \"; end\n",
		}, nil)
		win := firstWindow(t, base, sess).ID
		siSave(t, "fish-login.txt", siWaitScreen(t, base, sess, win, "F-fish>"))
	})

	t.Run("off by default", func(t *testing.T) {
		zsh := siShellPath(t, "zsh")
		const sess = "lsoff"
		base := siSession(t, sess, zsh, "", map[string]string{
			".zshenv":   siZshenv,
			".zprofile": "export TK_LOGIN=zprofile\n",
			".zshrc":    "PS1=\"Z-${TK_LOGIN:-none}> \"\n",
		}, nil)
		win := firstWindow(t, base, sess).ID
		siWaitScreen(t, base, sess, win, "Z-none>")
	})
}

// bashMarks reports whether bash is new enough for tuios's prompt marks
// (4.4): macOS ships bash 3.2.
func bashMarks(t *testing.T, bash string) bool {
	t.Helper()
	out, err := exec.Command(bash, "-c", `echo "${BASH_VERSINFO[0]} ${BASH_VERSINFO[1]}"`).Output()
	if err != nil {
		return false
	}
	m := regexp.MustCompile(`^(\d+) (\d+)`).FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > 4 || major == 4 && minor >= 4
}

// TestLoginBashSendsCWhenAHookNeverDoes holds a login bash on a machine whose
// /etc/profile.d carries a prompt-mark script, such as WezTerm's: it installs
// precmd hooks that send A, B and D and a preexec hook whose body names the C
// mark and that never fires in this shell. tuios read the C in that body as
// sent, sent none itself, and run refused the pane as one that marks prompts
// and never commands. The hooks are written into the test's own login files,
// so the case needs nothing from /etc.
//
// How this could pass wrongly, written down first:
//   - run could work because the hook did send C. The preexec hook is only
//     named in preexec_functions and nothing calls it, and the raw log is
//     counted: one C for the command, from tuios.
//   - The C could come twice. The count is one.
//   - The pane could draw its prompt late. The test waits for the prompt.
//
// Negative control: with the script reading preexec hook bodies again, no C
// is sent and run fails with "prompt marks only".
func TestLoginBashSendsCWhenAHookNeverDoes(t *testing.T) {
	bash := siShellPath(t, "bash")
	if !bashMarks(t, bash) {
		t.Skip("bash older than 4.4 sends no C mark")
	}
	const sess = "lsbashhook"
	base := siSession(t, sess, bash, "[shell]\nlogin = true\n", map[string]string{
		".bash_profile": "[ -r ~/.bashrc ] && . ~/.bashrc\n",
		".bashrc": `__w_precmd() { local s=$?; printf '\e]133;D;%s\a\e]133;A\a' "$s"; }
__w_preexec() { printf '\e]133;C\a'; }
PROMPT_COMMAND=__w_precmd
preexec_functions=(__w_preexec)
PS1='HOOK> \[\e]133;B\a\]'
`,
	}, nil)
	win := firstWindow(t, base, sess).ID
	siWaitScreen(t, base, sess, win, "HOOK>")
	if out := siRunOK(t, base, sess, win, "echo RUN-''WORKS"); !strings.Contains(out, "RUN-WORKS") {
		t.Fatalf("ASSERTION: run did not work in the login bash: %q", out)
	}
	screen := siWaitScreen(t, base, sess, win, "RUN-WORKS")
	raw := siRawLog(t, base)
	siSave(t, "bash-login-hook-raw.txt", fmt.Sprintf("%q\n", raw))
	siSave(t, "bash-login-hook-pane.txt", screen)
	if got := siMarkCounts(raw)["C"]; got != 1 {
		t.Fatalf("ASSERTION: the pane sent %d C marks for one command, want 1, from tuios: %v", got, siMarkCounts(raw))
	}
}
