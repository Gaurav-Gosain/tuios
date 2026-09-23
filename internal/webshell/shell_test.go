package webshell

import (
	"bytes"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// freshFS gives a test its own copy of the shared filesystem and repository,
// and puts the original back afterwards, so one test's edits never leak into
// the next.
func freshFS(t *testing.T) {
	t.Helper()
	fsMu.Lock()
	savedFiles := make(map[string]string, len(files))
	for k, v := range files {
		savedFiles[k] = v
	}
	savedDirs := make(map[string]bool, len(fsDirs))
	for k, v := range fsDirs {
		savedDirs[k] = v
	}
	savedHead := make(map[string]string, len(repo.head))
	for k, v := range repo.head {
		savedHead[k] = v
	}
	savedCommits := append([]gitCommit(nil), repo.commits...)
	fsMu.Unlock()
	t.Cleanup(func() {
		fsMu.Lock()
		defer fsMu.Unlock()
		files, fsDirs = savedFiles, savedDirs
		repo.head, repo.commits, repo.staged = savedHead, savedCommits, map[string]bool{}
	})
}

// events records what the guests emit while a test runs.
type events struct {
	mu  sync.Mutex
	all []Event
}

func recordEvents(t *testing.T) *events {
	t.Helper()
	ev := &events{}
	SetEventSink(func(e Event) { ev.mu.Lock(); ev.all = append(ev.all, e); ev.mu.Unlock() })
	t.Cleanup(func() { SetEventSink(nil) })
	return ev
}

func (ev *events) of(typ string) []Event {
	ev.mu.Lock()
	defer ev.mu.Unlock()
	var out []Event
	for _, e := range ev.all {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// guest is a pty running the shell, with its output collected.
type guest struct {
	t   *testing.T
	p   *Pty
	mu  sync.Mutex
	out bytes.Buffer
	emu *vt.Emulator
}

func startGuest(t *testing.T, name string) *guest {
	t.Helper()
	g := &guest{t: t, p: NewPty(80, 24), emu: vt.NewEmulator(80, 24)}
	cmd := exec.Command(name)
	cmd.Env = []string{"TUIOS_WINDOW_ID=w1"}
	if err := g.p.Start(cmd); err != nil {
		t.Fatal(err)
	}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := g.p.Read(buf)
			g.mu.Lock()
			g.out.Write(buf[:n])
			_, _ = g.emu.Write(buf[:n])
			g.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { _ = g.p.Close() })
	return g
}

func (g *guest) send(s string) { _, _ = g.p.Write([]byte(s)) }

// waitFor waits until the output since the last call contains want, and
// returns that output.
func (g *guest) waitFor(want string) string {
	g.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		got := g.out.String()
		if i := strings.Index(got, want); i >= 0 {
			g.out.Reset()
			g.out.WriteString(got[i+len(want):])
			g.mu.Unlock()
			return got[:i+len(want)]
		}
		g.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.t.Fatalf("timed out waiting for %q, got %q", want, g.out.String())
	return ""
}

// screenTail is the last n lines of the emulated screen, as the harness
// classifier reads them.
func (g *guest) screenTail(n int) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	lines := strings.Split(g.emu.String(), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func TestShellRunsCommands(t *testing.T) {
	freshFS(t)
	g := startGuest(t, "/bin/zsh")
	g.waitFor("❯")

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"ls lists home", "ls\r", "projects/"},
		{"cd then pwd", "cd projects/hello && pwd\r", "/home/guest/projects/hello"},
		{"cat a file", "cat main.go\r", "func main()"},
		{"echo writes a file", "echo hi there > note.txt\r", "❯"},
		{"the file reads back", "cat note.txt\r", "hi there"},
		{"unknown command", "nope\r", "command not found"},
		{"tab completes", "ca\t go.\t\r", "module example.com/hello"},
		{"go run reads greet.go", "go run . tuios\r", "hello, tuios!"},
		{"tree draws", "tree\r", "files"},
		{"cowsay", "cowsay moo\r", "(oo)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g.t = t
			g.send(tt.input)
			g.waitFor(tt.want)
		})
	}
	g.t = t
	g.send("exit\r")
	if err := g.p.Wait(); err != nil {
		t.Fatalf("exit status: %v", err)
	}
}

// TestShellReportsCommandsAndExitCodes is the contract a lesson reads: a start
// event when a command begins, and a finish event with its exit code.
func TestShellReportsCommandsAndExitCodes(t *testing.T) {
	freshFS(t)
	ev := recordEvents(t)
	g := startGuest(t, "sh")
	g.waitFor("❯")
	g.send("ls\r")
	g.waitFor("projects/")
	g.send("nope\r")
	g.waitFor("command not found")
	g.send("cd projects\r")
	g.waitFor("❯")
	time.Sleep(20 * time.Millisecond)

	starts, done := ev.of(EventCommandStart), ev.of(EventCommand)
	if len(starts) != 3 || len(done) != 3 {
		t.Fatalf("got %d starts and %d finishes, want 3 each: %+v", len(starts), len(done), ev.all)
	}
	want := []struct {
		command string
		code    int
	}{{"ls", 0}, {"nope", 127}, {"cd", 0}}
	for i, w := range want {
		e := done[i]
		if e.WindowID != "w1" || e.Data["command"] != w.command || e.Data["exitCode"] != w.code {
			t.Errorf("finish %d = %+v, want command %s exit %d in w1", i, e, w.command, w.code)
		}
	}
	if cwd := ev.of(EventCwd); len(cwd) != 1 || cwd[0].Data["cwd"] != Home+"/projects" {
		t.Errorf("cwd events = %+v", cwd)
	}
}

func TestFullscreenProgramsQuit(t *testing.T) {
	for _, tt := range []struct{ run, ready, quit string }{
		{"top\r", "q to quit", "q"},
		{"less README.md\r", "(END)", "q"},
		{"vim README.md\r", "[readonly]", ":q\r"},
		{"vim\r", "VIM - Vi IMproved", ":wq\r"},
	} {
		t.Run(strings.TrimSpace(tt.run), func(t *testing.T) {
			g := startGuest(t, "sh")
			g.waitFor("❯")
			g.send(tt.run)
			g.waitFor(tt.ready)
			g.send(tt.quit)
			g.waitFor("\x1b[?1049l")
			g.waitFor("❯")
		})
	}
}

func TestViewerIsReadOnlyAndSearches(t *testing.T) {
	g := startGuest(t, "sh")
	g.waitFor("❯")
	g.send("vim README.md\r")
	g.waitFor("[readonly]")
	g.send("i")
	g.waitFor("read-only view")
	g.send(":w\r")
	g.waitFor("nothing to save")
	g.send("/tape\r")
	g.waitFor("\x1b[7mtape\x1b[27m")
	g.send(":q\r")
	g.waitFor("\x1b[?1049l")
}

func TestGitShowsTheWorkingChange(t *testing.T) {
	freshFS(t)
	g := startGuest(t, "sh")
	g.waitFor("❯")
	g.send("git status\r")
	g.waitFor("not a git repository")

	g.send("cd projects/hello\r")
	g.waitFor("main")
	g.send("git status\r")
	out := g.waitFor("TODO.md")
	if !strings.Contains(out, "modified:  greet.go") {
		t.Errorf("git status did not list greet.go as modified: %q", out)
	}
	g.send("git diff\r")
	out = g.waitFor(`+	return "hello, " + name + "!"`)
	if !strings.Contains(out, `-	return "hello, " + name`) {
		t.Errorf("git diff did not show the removed line: %q", out)
	}
	g.send("go test\r")
	g.waitFor("FAIL")

	g.send("git log --oneline\r")
	g.waitFor("Initial commit")

	g.send("git add . && git commit -m \"Shout when asked\"\r")
	g.waitFor("2 files changed")
	g.send("git status\r")
	g.waitFor("working tree clean")
	g.send("git log --oneline -1\r")
	g.waitFor("Shout when asked")

	g.send("git restore greet.go\r")
	g.waitFor("❯")
	g.send("echo more >> README.md && git diff\r")
	g.waitFor("+more")
	g.send("git restore README.md && git status -s\r")
	g.waitFor("❯")
}

// TestAgentReportsItsStates runs the fake agent to the approval and answers
// it, and checks what it tells tuios on the way: working, then needs_input as
// an approval, then done.
func TestAgentReportsItsStates(t *testing.T) {
	freshFS(t)
	ev := recordEvents(t)
	g := startGuest(t, "sh")
	g.waitFor("❯")
	g.send("claude\r")
	g.waitFor("Do you want to make this edit to style.css?")
	g.waitFor("\a")

	g.send("\x1b[B") // arrow down moves the choice
	g.waitFor("❯ 2. Yes, and don't ask again")
	g.send("y")
	g.waitFor("dark mode toggle now")
	g.waitFor("❯")

	var states []string
	for _, e := range ev.of(EventAgentReport) {
		states = append(states, e.Data["state"].(string))
		if e.Data["harness"] != AgentHarness || e.WindowID != "w1" {
			t.Errorf("report %+v lacks the harness or the window", e)
		}
		if e.Data["state"] == "needs_input" && e.Data["kind"] != "approval" {
			t.Errorf("needs_input report %+v is not an approval", e)
		}
	}
	if got := strings.Join(states, ","); got != "working,working,needs_input,working,done" {
		t.Errorf("states = %s", got)
	}
	if css, _ := readFile(Home + "/projects/website/style.css"); !strings.Contains(css, "data-theme=dark") {
		t.Errorf("the approved edit was not applied: %q", css)
	}
}

func TestAgentCanBeTurnedDown(t *testing.T) {
	freshFS(t)
	ev := recordEvents(t)
	g := startGuest(t, "claude")
	g.waitFor("Do you want")
	g.send("n")
	g.waitFor("No changes made")
	if err := g.p.Wait(); err == nil {
		t.Error("turning the agent down should exit non-zero")
	}
	reports := ev.of(EventAgentReport)
	if last := reports[len(reports)-1]; last.Data["state"] != "done" {
		t.Errorf("last report = %+v, want done", last)
	}
	if css, _ := readFile(Home + "/projects/website/style.css"); strings.Contains(css, "data-theme") {
		t.Error("a turned down edit was applied")
	}
}

// TestAgentScreenMatchesTheHarness reads the fake agent's screen through
// tuios's own emulator and the claude-code harness manifest, so the demo shows
// what tuios would detect on a real Claude Code pane.
func TestAgentScreenMatchesTheHarness(t *testing.T) {
	freshFS(t)
	reg, errs := harness.Load()
	if len(errs) > 0 {
		t.Fatalf("load manifests: %v", errs)
	}
	lines := reg.ScreenLines(AgentHarness)
	g := startGuest(t, "claude")
	g.waitFor("Thinking…")
	time.Sleep(30 * time.Millisecond)
	if state, _, ok := reg.Classify(AgentHarness, g.screenTail(lines)); !ok || state != "working" {
		t.Errorf("thinking screen classified %q (%v), want working:\n%s", state, ok, strings.Join(g.screenTail(lines), "\n"))
	}
	g.waitFor("\a")
	time.Sleep(30 * time.Millisecond)
	if state, _, ok := reg.Classify(AgentHarness, g.screenTail(lines)); !ok || state != "needs_input" {
		t.Errorf("approval screen classified %q (%v), want needs_input:\n%s", state, ok, strings.Join(g.screenTail(lines), "\n"))
	}
	g.send("\x03")
}

func TestTapePlayHandsTheTapeToTuios(t *testing.T) {
	freshFS(t)
	ev := recordEvents(t)
	g := startGuest(t, "sh")
	g.waitFor("❯")
	g.send("tuios tape list\r")
	g.waitFor("demo.tape")
	g.send("tuios tape play demo.tape\r")
	g.waitFor("Playing")
	g.waitFor("❯")
	plays := ev.of(EventTapePlay)
	if len(plays) != 1 || plays[0].Data["name"] != "demo.tape" {
		t.Fatalf("tape.play events = %+v", plays)
	}
	script := plays[0].Data["script"].(string)
	cmds, perrs := tape.ParseFile(script)
	if len(perrs) > 0 || len(cmds) == 0 {
		t.Fatalf("demo.tape does not parse: %v", perrs)
	}
}

// TestSampleTapesParse checks every tape in the home directory is one tuios
// can play, and that party.tape ends on workspace 2, the trip it promises.
func TestSampleTapesParse(t *testing.T) {
	freshFS(t)
	for _, name := range []string{"demo.tape", "party.tape"} {
		t.Run(name, func(t *testing.T) {
			text, ok := ReadFile(Home + "/" + name)
			if !ok {
				t.Fatalf("no %s", name)
			}
			cmds, perrs := tape.ParseFile(text)
			if len(perrs) > 0 || len(cmds) == 0 {
				t.Fatalf("%s does not parse: %v", name, perrs)
			}
		})
	}
	party, _ := ReadFile(Home + "/party.tape")
	last := strings.LastIndex(party, "SwitchWorkspace ")
	if last < 0 || !strings.HasPrefix(party[last:], "SwitchWorkspace 2") {
		t.Errorf("party.tape does not end on workspace 2:\n%s", party)
	}
}

// TestRunnableCommandsHighlightAsValid checks that the highlighter agrees with
// the executor: every name the shell runs, aliases included, is drawn green,
// and a name it does not run is drawn red.
func TestRunnableCommandsHighlightAsValid(t *testing.T) {
	for _, alias := range []string{"ll", "la", "logout", "less", "more", "bat", "htop", "btop", "cmatrix", "claude", "vi", "sh", "bash", "git", "go", "tuios", "fortune"} {
		if !runnable(alias) {
			t.Errorf("%s is not runnable", alias)
		}
	}
	for _, name := range commandNames() {
		t.Run(name, func(t *testing.T) {
			for _, line := range []string{name, name + " ", name + " arg"} {
				s := &shell{line: []rune(line)}
				if got := s.highlighted(); !strings.HasPrefix(got, green+name+reset) {
					t.Errorf("highlighted(%q) = %q, want the command in green", line, got)
				}
			}
			s := &shell{line: []rune("cd x && " + name)}
			if got := s.highlighted(); !strings.Contains(got, green+name+reset) {
				t.Errorf("highlighted(%q) = %q, want the second command in green", string(s.line), got)
			}
		})
	}
	for _, name := range []string{"nope", "l"} {
		s := &shell{line: []rune(name)}
		if got := s.highlighted(); !strings.HasPrefix(got, red+name+reset) {
			t.Errorf("highlighted(%q) = %q, want the command in red", name, got)
		}
	}
}

// TestRunnableCommandsAreFound checks the other direction: nothing the
// highlighter draws green gets "command not found" when it runs. The
// full-screen programs wait for a key and are covered above.
func TestRunnableCommandsAreFound(t *testing.T) {
	freshFS(t)
	skip := map[string]bool{
		"top": true, "htop": true, "btop": true, "rain": true, "cmatrix": true,
		"agent": true, "claude": true, "exit": true, "logout": true,
		"less": true, "more": true, "view": true, "vim": true, "vi": true, "nvim": true,
	}
	for _, name := range commandNames() {
		if skip[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			g := startGuest(t, "sh")
			g.waitFor("❯")
			// echo collapses the double space, so the marker only matches
			// the command's output and never the echoed input line.
			g.send(name + "\recho end  mark\r")
			if out := g.waitFor("end mark\r\n"); strings.Contains(out, "command not found") {
				t.Errorf("%s: %q", name, out)
			}
		})
	}
}

func TestProgramsRunAsAPaneProcess(t *testing.T) {
	for _, p := range Programs() {
		if !runnable(p.Name) {
			t.Errorf("launcher program %s is not a command", p.Name)
		}
	}
	g := startGuest(t, "fortune")
	if err := g.p.Wait(); err != nil {
		t.Fatalf("fortune as a pane process: %v", err)
	}
}

func TestLineDiff(t *testing.T) {
	a := "one\ntwo\nthree\nfour\n"
	b := "one\n2\nthree\nfour\nfive\n"
	got := unifiedHunks(lineDiff(a, b))
	for _, want := range []string{"-two", "+2", "+five", "@@ -1,4 +1,5 @@"} {
		if !strings.Contains(got, want) {
			t.Errorf("diff lacks %q:\n%s", want, got)
		}
	}
	if unifiedHunks(lineDiff(a, a)) != "" {
		t.Error("a diff of equal texts is not empty")
	}
}

// TestShellMarksCommands checks the OSC 133 marks tuios's scrollback browser
// splits the history on: a prompt, the typed command, its output and its end
// with the exit status.
func TestShellMarksCommands(t *testing.T) {
	freshFS(t)
	g := startGuest(t, "sh")
	g.waitFor(markInput)
	g.send("ls\r")
	out := g.waitFor(markInput)
	for _, want := range []string{markOutput, "projects/", markDone(0), markPrompt} {
		if !strings.Contains(out, want) {
			t.Errorf("output of ls lacks %q: %q", want, out)
		}
	}
	g.send("nope\r")
	if out := g.waitFor(markInput); !strings.Contains(out, markDone(127)) {
		t.Errorf("an unknown command did not end with status 127: %q", out)
	}
	g.mu.Lock()
	n := g.emu.SemanticMarkers().Len()
	g.mu.Unlock()
	if n < 8 {
		t.Errorf("the emulator took %d marks, want at least 8", n)
	}
}

// TestEveryTapeParses keeps the tapes in the demo's filesystem playable.
func TestEveryTapeParses(t *testing.T) {
	for _, p := range tapeFiles() {
		script, _ := readFile(p)
		if cmds, errs := tape.ParseFile(script); len(errs) > 0 || len(cmds) == 0 {
			t.Errorf("%s does not parse: %v", p, errs)
		}
	}
}

// TestTuiosSamples checks the subcommands that need a real machine print a
// sample and succeed, and that every table in a sample lines up.
func TestTuiosSamples(t *testing.T) {
	freshFS(t)
	ev := recordEvents(t)
	g := startGuest(t, "sh")
	g.waitFor(markInput)
	for _, sub := range []string{"ls", "fan", "worktree", "list-agents", "send-agent-message", "list-verbs", "list-hooks", "attach"} {
		g.send("tuios " + sub + "\r")
		out := g.waitFor(markInput)
		if !strings.Contains(out, "On a real machine") || !strings.Contains(out, "tuios.gaurav.zip/docs/") {
			t.Errorf("tuios %s printed no sample: %q", sub, out)
		}
	}
	for _, e := range ev.of(EventCommand) {
		if e.Data["exitCode"] != 0 {
			t.Errorf("%v exited %v", e.Data["line"], e.Data["exitCode"])
		}
	}
	for name, s := range samples {
		width := -1
		for _, line := range strings.Split(s.text, "\n") {
			if !strings.ContainsAny(line, "│╭╰├") {
				continue
			}
			n := len([]rune(line))
			if width >= 0 && n != width {
				t.Errorf("sample %q: a table row is %d wide, want %d: %q", name, n, width, line)
			}
			width = n
		}
	}
}
