package tuie2e

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Shell integration by default. tuios starts the bash and zsh in its panes
// so that they send OSC 133 prompt marks, without a line in the user's own
// files. Each test runs a real daemon against a real shell, with the user's
// startup files written into the test's own home, and reads what the shell
// did back through tuios run, capture-pane and the raw pane log.
//
// How these could pass wrongly, written down first:
//   - run could work because the test's rc file sends marks itself. Only the
//     test of a setup that marks itself writes a mark; the others write a
//     prompt and a PROMPT_COMMAND or precmd with none.
//   - The marks could come at the cost of the user's setup: the rc file not
//     read, PS1 replaced, PROMPT_COMMAND dropped or fed the wrong status, a
//     ZDOTDIR lost. Each of those is read back from the shell: the custom
//     prompt on the screen, the status the user's hook saw, the value of
//     ZDOTDIR and a variable from .zshenv.
//   - The shell could pick up the developer's own files. HOME is the
//     test's, and ZDOTDIR and BASH_ENV are taken out of the daemon's
//     environment unless the test sets them.
//   - "No marks with integration off" could pass because the shell had not
//     drawn its prompt yet. The off case waits for the prompt on the screen
//     before it asks run.
//   - "Not marked twice" could pass because the count is of the wrong pane
//     or the wrong bytes. The count is of the raw log of the one pane in the
//     session, and the marks the rc file sends are counted with ours.
//   - The narrowed pane could show its prompt once only because the prompt
//     scrolled away. The screen must still hold the prompt line, with the
//     typed text after it.

// siZshenv keeps Debian's and Ubuntu's /etc/zsh/zshrc from running compinit,
// which stops at a question about the test's folders before the prompt.
const siZshenv = "skip_global_compinit=1\n"

// siBaseConfig is the config every test here starts from.
const siBaseConfig = "[appearance]\npreferred_shell = %q\n"

// siEnv is the environment a tuios command of these tests runs with: the
// suite's, without the developer's ZDOTDIR and BASH_ENV, plus extra.
func siEnv(base string, extra ...string) []string {
	var env []string
	for _, kv := range cliEnv(base) {
		k, _, _ := strings.Cut(kv, "=")
		if k == "ZDOTDIR" || k == "BASH_ENV" || k == "TUIOS_ZSH_ZDOTDIR" {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

// siShellPath finds a shell, and skips the test when it is not installed.
func siShellPath(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s is not installed", name)
	}
	return path
}

// siSession starts a daemon with one detached session whose pane runs
// shell. config goes after preferred_shell. files are written under the
// test's home, by path relative to it. The daemon logs each pane's raw output
// under base/ptylog. extraEnv, given the home, adds to the daemon's
// environment.
func siSession(t *testing.T, sess, shell, config string, files map[string]string, extraEnv func(home string) []string) string {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, fmt.Sprintf(siBaseConfig, shell)+config)
	home := xdgDir(base, "HOME")
	for rel, body := range files {
		path := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pinPreV080Looks(t, base)
	cmd := exec.Command(tuiosBin, "new", sess, "--detach")
	cmd.Dir = workDirIn(t, base)
	env := []string{"TUIOS_PTY_LOG=" + filepath.Join(base, "ptylog")}
	if extraEnv != nil {
		env = append(env, extraEnv(home)...)
	}
	cmd.Env = siEnv(base, env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create the session: %v: %s", err, out)
	}
	return base
}

// siRun is tuios run in a window. It returns the output, and the exit
// status of the run command, which is the command's own.
func siRun(t *testing.T, base, sess, window, command string) (string, int) {
	t.Helper()
	cmd := exec.Command(tuiosBin, "run", "-s", sess, "-w", window, "--timeout", "20000", "--", command)
	cmd.Env = siEnv(base)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %q: %v", command, err)
		}
		code = exit.ExitCode()
	}
	return string(out), code
}

// siRunOK runs a command that must succeed. The first prompt may still be
// on its way, so a refusal for that is tried again.
func siRunOK(t *testing.T, base, sess, window, command string) string {
	t.Helper()
	deadline := time.Now().Add(shellTimeout)
	for {
		out, code := siRun(t, base, sess, window, command)
		if code == 0 {
			return out
		}
		if time.Now().After(deadline) || !strings.Contains(out, "not_at_prompt") && !strings.Contains(out, "no_shell_integration") && !strings.Contains(out, "has not sent OSC 133 marks") {
			t.Fatalf("run %q: exit %d: %s", command, code, out)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// siWaitScreen waits for want on the pane's screen and returns the screen.
func siWaitScreen(t *testing.T, base, sess, window, want string) string {
	t.Helper()
	deadline := time.Now().Add(shellTimeout)
	for {
		rows, err := daemonPane(base, sess, window)
		screen := strings.Join(rows, "\n")
		if err == nil && strings.Contains(screen, want) {
			return screen
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never showed %q:\n%s", want, screen)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// siSave writes a capture into the test's artifact folder.
func siSave(t *testing.T, name, body string) {
	t.Helper()
	path := filepath.Join(artifactDir(t), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Errorf("save %s: %v", name, err)
	}
	t.Logf("artifact: %s", path)
}

// siRawLog is the raw output of the session's one pane.
func siRawLog(t *testing.T, base string) string {
	t.Helper()
	logs, _ := filepath.Glob(filepath.Join(base, "ptylog", "*.raw"))
	if len(logs) != 1 {
		t.Fatalf("want the raw log of one pane, found %d: %v", len(logs), logs)
	}
	raw, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// siMarkCounts counts the OSC 133 marks of each kind in raw output.
func siMarkCounts(raw string) map[string]int {
	counts := map[string]int{}
	for _, m := range []string{"A", "B", "C", "D"} {
		counts[m] = strings.Count(raw, "\x1b]133;"+m)
	}
	return counts
}

// siDoctor is tuios doctor shell --json.
type siDoctor struct {
	Integration  string `json:"integration"`
	SetUpByTuios bool   `json:"set_up_by_tuios"`
	Setup        string `json:"setup"`
	Panes        []struct {
		Window string `json:"window_id"`
		Marks  bool   `json:"marks_commands"`
	} `json:"panes"`
}

func siDoctorReport(t *testing.T, base string) siDoctor {
	t.Helper()
	cmd := exec.Command(tuiosBin, "doctor", "shell", "--json")
	cmd.Env = siEnv(base)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("doctor shell: %v: %s", err, out)
	}
	var r siDoctor
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("decode doctor shell: %v: %s", err, out)
	}
	return r
}

// siUserBashrc is a ~/.bashrc with a prompt and a PROMPT_COMMAND of its own,
// and no mark.
const siUserBashrc = `PS1='CUSTOM-PS1> '
PROMPT_COMMAND='LAST_STATUS=$?; PC_RAN=$((PC_RAN+1))'
`

// TestShellIntegrationBashByDefault: a fresh bash pane, with the default
// config and a ~/.bashrc that sets its own prompt and PROMPT_COMMAND, marks
// its commands. tuios run works in it, the user's prompt shows, and the
// user's PROMPT_COMMAND still runs and sees the status of the command.
// --shell-integration off on new-window opens a pane with no marks.
//
// Negative control: on origin/main, run is refused with
// no_shell_integration.
func TestShellIntegrationBashByDefault(t *testing.T) {
	bash := siShellPath(t, "bash")
	const sess = "sibash"
	base := siSession(t, sess, bash, "", map[string]string{".bashrc": siUserBashrc}, nil)
	win := firstWindow(t, base, sess).ID
	siWaitScreen(t, base, sess, win, "CUSTOM-PS1>")

	if out := siRunOK(t, base, sess, win, "echo RUN-''WORKS"); !strings.Contains(out, "RUN-WORKS") {
		t.Fatalf("ASSERTION: run printed %q, want RUN-WORKS", out)
	}
	if out, code := siRun(t, base, sess, win, "false"); code != 1 {
		t.Fatalf("ASSERTION: run false exited %d, want 1: %s", code, out)
	}
	out := siRunOK(t, base, sess, win, `echo "status=$LAST_STATUS ran=$PC_RAN"`)
	if !strings.Contains(out, "status=1 ") {
		t.Fatalf("ASSERTION: the user's PROMPT_COMMAND saw %q, want status=1 from false", out)
	}
	screen := siWaitScreen(t, base, sess, win, "CUSTOM-PS1> echo RUN-")
	if strings.Contains(screen, "133;") {
		t.Fatalf("ASSERTION: a mark reached the screen as text:\n%s", screen)
	}
	siSave(t, "bash-pane.txt", screen)

	doc := siDoctorReport(t, base)
	if !doc.SetUpByTuios || doc.Integration != "auto" {
		t.Fatalf("ASSERTION: doctor shell says integration %q, set up by tuios %v, want auto and true", doc.Integration, doc.SetUpByTuios)
	}

	// The same session, one pane opened with the integration off.
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "plain", "--shell-integration", "off", "--no-focus"); err != nil {
		t.Fatalf("new-window --shell-integration off: %v: %s", err, out)
	}
	siWaitScreen(t, base, sess, "plain", "CUSTOM-PS1>")
	if out, code := siRun(t, base, sess, "plain", "echo hi"); code == 0 || !strings.Contains(out, "OSC 133") {
		t.Fatalf("ASSERTION: run in the pane opened with --shell-integration off exited %d, want a refusal for no marks: %s", code, out)
	}
}

// TestShellIntegrationZshByDefault: a fresh zsh pane marks its commands. The
// user's files are in a ZDOTDIR of their own: .zshenv exports a variable,
// .zshrc sets the prompt and a precmd. All of them must take effect, and the
// shell must see the user's ZDOTDIR, not the folder tuios started it with.
//
// Negative control: on origin/main, run is refused with
// no_shell_integration.
func TestShellIntegrationZshByDefault(t *testing.T) {
	zsh := siShellPath(t, "zsh")
	const sess = "sizsh"
	const zdotRel = ".config/zsh"
	base := siSession(t, sess, zsh, "", map[string]string{
		zdotRel + "/.zshenv": siZshenv + "export FROM_ZSHENV=yes\n",
		zdotRel + "/.zshrc":  "PS1='ZCUSTOM> '\nprecmd() { LAST_STATUS=$? }\n",
	}, func(home string) []string { return []string{"ZDOTDIR=" + filepath.Join(home, zdotRel)} })
	zdot := filepath.Join(xdgDir(base, "HOME"), zdotRel)
	win := firstWindow(t, base, sess).ID
	siWaitScreen(t, base, sess, win, "ZCUSTOM>")

	siRunOK(t, base, sess, win, "true")
	if out, code := siRun(t, base, sess, win, "false"); code != 1 {
		t.Fatalf("ASSERTION: run false exited %d, want 1: %s", code, out)
	}
	out := siRunOK(t, base, sess, win, `echo "zdot=${ZDOTDIR-unset} env=${FROM_ZSHENV-none} status=$LAST_STATUS shim=${TUIOS_ZSH_ZDOTDIR-none}"`)
	want := "zdot=" + zdot + " env=yes status=1 shim=none"
	// A line wider than the pane comes back wrapped.
	if got := strings.ReplaceAll(out, "\n", ""); !strings.Contains(got, want) {
		t.Fatalf("ASSERTION: the shell said %q, want %q", got, want)
	}
	screen := siWaitScreen(t, base, sess, win, "ZCUSTOM> echo")
	siSave(t, "zsh-pane.txt", screen)
}

// TestShellIntegrationOff: with [shell] integration = "off" a bash pane
// sends no marks, and run refuses it once its prompt is up. A pane opened
// with --shell-integration auto gets the marks all the same.
//
// Negative control: with the config option not read (the daemon always on),
// the first run is not refused.
func TestShellIntegrationOff(t *testing.T) {
	bash := siShellPath(t, "bash")
	const sess = "sioff"
	base := siSession(t, sess, bash, "[shell]\nintegration = \"off\"\n", map[string]string{".bashrc": siUserBashrc}, nil)
	win := firstWindow(t, base, sess).ID
	siWaitScreen(t, base, sess, win, "CUSTOM-PS1>")
	// Time for any mark that would come with the prompt.
	time.Sleep(500 * time.Millisecond)
	if out, code := siRun(t, base, sess, win, "echo hi"); code == 0 || !strings.Contains(out, "OSC 133") {
		t.Fatalf("ASSERTION: run in a pane with integration off exited %d, want a refusal for no marks: %s", code, out)
	}
	if n := siMarkCounts(siRawLog(t, base)); n["A"]+n["B"]+n["C"]+n["D"] != 0 {
		t.Fatalf("ASSERTION: the pane sent marks with integration off: %v", n)
	}
	if doc := siDoctorReport(t, base); doc.Integration != "off" || doc.SetUpByTuios || doc.Setup == "" {
		t.Fatalf("ASSERTION: doctor shell says integration %q, set up by tuios %v, setup %q; want off, false and the bash lines", doc.Integration, doc.SetUpByTuios, doc.Setup)
	}

	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "marked", "--shell-integration", "auto", "--no-focus"); err != nil {
		t.Fatalf("new-window --shell-integration auto: %v: %s", err, out)
	}
	siWaitScreen(t, base, sess, "marked", "CUSTOM-PS1>")
	if out := siRunOK(t, base, sess, "marked", "echo AUTO-''PANE"); !strings.Contains(out, "AUTO-PANE") {
		t.Fatalf("ASSERTION: run in the --shell-integration auto pane printed %q", out)
	}
}

// TestShellIntegrationLeavesAMarkingSetupAlone: a user whose own setup
// already sends every mark, as a prompt theme with OSC 133 does, gets no
// second set from tuios. After two commands the pane's raw output holds one
// C per command and one A, B and D per prompt.
//
// Negative control: with the script's look at the user's setup taken out
// (every mark sent), the counts double.
func TestShellIntegrationLeavesAMarkingSetupAlone(t *testing.T) {
	cases := []struct {
		shell, file, rc, prompt string
		more                    map[string]string
	}{
		{"bash", ".bashrc", `__theme_prompt() { local s=$?; printf '\e]133;D;%s\a\e]133;A\a' "$s"; }
PROMPT_COMMAND=__theme_prompt
PS1='THEMED> \[\e]133;B\a\]'
PS0='\e]133;C\a'
`, "THEMED>", nil},
		{"zsh", ".zshrc", `autoload -Uz add-zsh-hook
_theme_precmd()  { local s=$?; print -n "\e]133;D;$s\a\e]133;A\a"; }
_theme_preexec() { print -n "\e]133;C\a"; }
add-zsh-hook precmd _theme_precmd
add-zsh-hook preexec _theme_preexec
PS1=$'THEMED> %{\e]133;B\a%}'
`, "THEMED>", map[string]string{".zshenv": siZshenv}},
	}
	for _, tc := range cases {
		t.Run(tc.shell, func(t *testing.T) {
			path := siShellPath(t, tc.shell)
			sess := "simark" + tc.shell
			files := map[string]string{tc.file: tc.rc}
			maps.Copy(files, tc.more)
			base := siSession(t, sess, path, "", files, nil)
			win := firstWindow(t, base, sess).ID
			siWaitScreen(t, base, sess, win, tc.prompt)
			siRunOK(t, base, sess, win, "echo ONE")
			siRunOK(t, base, sess, win, "echo TWO")
			screen := siWaitScreen(t, base, sess, win, "TWO")
			time.Sleep(300 * time.Millisecond)
			raw := siRawLog(t, base)
			siSave(t, tc.shell+"-raw.txt", fmt.Sprintf("%q\n", raw))
			siSave(t, tc.shell+"-pane.txt", screen)
			got := siMarkCounts(raw)
			want := map[string]int{"A": 3, "B": 3, "C": 2, "D": 3}
			for m, n := range want {
				if got[m] != n {
					t.Fatalf("ASSERTION: the pane sent %d %s marks, want %d, one set: %v", got[m], m, n, got)
				}
			}
		})
	}
}

// TestShellIntegrationNarrowPaneKeepsOnePrompt is a prompt that repeated in
// a narrowed pane: a bash pane with a long prompt, narrowed below the
// prompt's width while the prompt is open. With the marks, the open prompt is held out of the reflow
// and the shell redraws it once. Without them the reflow wraps the prompt
// and the shell draws it again below the wrapped copy, so it shows twice.
//
// Negative control: on origin/main the narrowed pane shows the prompt
// twice.
func TestShellIntegrationNarrowPaneKeepsOnePrompt(t *testing.T) {
	bash := siShellPath(t, "bash")
	const sess = "sinarrow"
	const prompt = "LONG-PROMPT-abcdefghijklmnopqrstuvwxyz-0123456789-END> "
	base := siSession(t, sess, bash, "", map[string]string{".bashrc": "PS1='" + prompt + "'\n"}, nil)
	term := attachIn(t, base, sess, startOpts{cols: 140, rows: 30})
	enableTiling(t, term)
	win := firstWindow(t, base, sess).ID
	// Typed, not run: run needs the marks, and the build without them must
	// reach the narrowing too.
	siWaitScreen(t, base, sess, win, "END>")
	if err := paneSend(base, sess, win, "clear\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := paneSend(base, sess, win, "typed-text"); err != nil {
		t.Fatal(err)
	}
	siWaitScreen(t, base, sess, win, "END> typed-text")

	if err := term.Resize(40, 30); err != nil {
		t.Fatal(err)
	}
	waitPaneWidth(t, base, sess, func(w, _ int) bool { return w < 45 }, "narrow")
	time.Sleep(time.Second)
	rows, err := daemonPane(base, sess, win)
	if err != nil {
		t.Fatal(err)
	}
	screen := strings.Join(rows, "\n")
	siSave(t, "narrow-pane.txt", screen)
	siSave(t, "narrow-client.txt", term.Snapshot())
	if n := strings.Count(screen, "LONG-PROMPT"); n != 1 {
		t.Fatalf("ASSERTION: the narrowed pane shows the prompt %d times, want 1:\n%s", n, screen)
	}
	if !strings.Contains(strings.ReplaceAll(screen, "\n", ""), "typed-text") {
		t.Fatalf("ASSERTION: the narrowed pane lost the typed text:\n%s", screen)
	}
}
