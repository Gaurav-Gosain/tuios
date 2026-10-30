package harness

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestResumeArgvBuildsTheCommand checks the bundled commands the resume offer
// types, and that the id lands where the placeholder was, whole token or part
// of one.
func TestResumeArgvBuildsTheCommand(t *testing.T) {
	r, errs := Load()
	if len(errs) != 0 {
		t.Fatalf("load errors: %v", errs)
	}
	for _, tc := range []struct {
		harness string
		want    []string
	}{
		{"claude-code", []string{"claude", "--resume", "3f2a-9c"}},
		{"codex", []string{"codex", "resume", "3f2a-9c"}},
		{"copilot", []string{"copilot", "--resume=3f2a-9c"}},
		{"opencode", []string{"opencode", "--session", "3f2a-9c"}},
		{"pi", []string{"pi", "--session", "3f2a-9c"}},
		{"qoder", []string{"qodercli", "--resume", "3f2a-9c"}},
	} {
		got, err := r.ResumeArgv(tc.harness, "3f2a-9c", nil)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: ResumeArgv = %q, %v; want %q", tc.harness, got, err, tc.want)
		}
		if !r.CanResume(tc.harness) {
			t.Errorf("%s: CanResume is false with a resume command", tc.harness)
		}
	}
	if r.CanResume("aider") {
		t.Error("aider has no resume command and CanResume says it does")
	}
	if _, err := r.ResumeArgv("aider", "x", nil); !errors.Is(err, ErrNoResume) {
		t.Errorf("a harness without [resume] built a command: %v", err)
	}
	if _, err := r.ResumeArgv("no-such-harness", "x", nil); !errors.Is(err, ErrNoResume) {
		t.Errorf("an unknown harness built a command: %v", err)
	}
}

// TestResumeRefusesAnIdAShellWouldRead is the security half: the id comes from
// a pane's own report, and the command is typed into a shell, so an id that a
// shell would read as more than one plain argument must never reach it.
func TestResumeRefusesAnIdAShellWouldRead(t *testing.T) {
	r, _ := Load()
	for _, id := range []string{
		"",
		"abc; rm -rf ~",
		"abc && true",
		"$(id)",
		"`id`",
		"a b",
		"a\nb",
		"a\rb",
		"'quoted'",
		`"quoted"`,
		"--dangerously-skip-permissions",
		"-x",
		"a|b",
		"a>b",
		"a*b",
		"%PATH%",
		"@args",
		"a,b",
		"a=b",
		"~root",
		"a\x1bb",
		strings.Repeat("a", MaxResumeSessionID+1),
	} {
		if ValidResumeSessionID(id) {
			t.Errorf("ValidResumeSessionID(%q) = true", id)
		}
		if argv, err := r.ResumeArgv("claude-code", id, nil); !errors.Is(err, ErrBadResumeID) {
			t.Errorf("ResumeArgv(claude-code, %q) = %q, %v; want ErrBadResumeID", id, argv, err)
		}
	}
	for _, id := range []string{
		"5f1c2b7e-9a3d-4c1e-8f00-1234567890ab",
		"ses_01HZX9",
		"thread.42",
		"a:b",
		"x/y",
		strings.Repeat("a", MaxResumeSessionID),
	} {
		if !ValidResumeSessionID(id) {
			t.Errorf("ValidResumeSessionID(%q) = false for an ordinary id", id)
		}
	}
}

// TestResumeBlockIsChecked holds a manifest's [resume] to tokens every shell
// reads unquoted, with the placeholder somewhere past the program.
func TestResumeBlockIsChecked(t *testing.T) {
	head := "schema_version = 1\nid = \"x\"\n[detect]\ncomm = [\"xagent\"]\n"
	for _, tc := range []struct {
		name, body, wantErr string
	}{
		{"valid", `[resume]
argv = ["xagent", "--resume", "{session_id}"]`, ""},
		{"part of a token", `[resume]
argv = ["xagent", "--resume={session_id}"]`, ""},
		{"empty block", `[resume]
source = "none"`, ""},
		{"no placeholder", `[resume]
argv = ["xagent", "--continue"]`, "does not hold"},
		{"program is the placeholder", `[resume]
argv = ["{session_id}"]`, "argv[0]"},
		{"shell metacharacter", `[resume]
argv = ["xagent", "--resume", "{session_id}", ";", "rm"]`, "token 3"},
		{"space", `[resume]
argv = ["xagent --resume", "{session_id}"]`, "token 0"},
		{"empty token", `[resume]
argv = ["xagent", "", "{session_id}"]`, "token 1"},
		{"another placeholder", `[resume]
argv = ["xagent", "{cwd}", "{session_id}"]`, "token 1"},
		{"kept flags", `[resume]
argv = ["xagent", "--resume={session_id}"]
keep_flags = ["--yolo"]
keep_options = ["--model", "-m"]`, ""},
		{"kept resume flag", `[resume]
argv = ["xagent", "--resume={session_id}"]
keep_options = ["--resume"]`, "own flag"},
		{"kept flag with a value", `[resume]
argv = ["xagent", "--resume", "{session_id}"]
keep_options = ["--model=opus"]`, "--model=opus"},
		{"kept flag is not a flag", `[resume]
argv = ["xagent", "--resume", "{session_id}"]
keep_flags = ["model"]`, "model"},
		{"kept flag with a metacharacter", `[resume]
argv = ["xagent", "--resume", "{session_id}"]
keep_flags = ["--a;b"]`, "--a;b"},
		{"risky marks", `[resume]
argv = ["xagent", "--resume", "{session_id}"]
keep_flags = ["--yolo"]
keep_options = ["--sandbox"]
keep_risky = ["--yolo", "--sandbox=off"]`, ""},
		{"risky flag not kept", `[resume]
argv = ["xagent", "--resume", "{session_id}"]
keep_flags = ["--yolo"]
keep_risky = ["--bypass"]`, "--bypass"},
		{"risky value of a flag", `[resume]
argv = ["xagent", "--resume", "{session_id}"]
keep_flags = ["--yolo"]
keep_risky = ["--yolo=1"]`, "--yolo=1"},
		{"kept flag twice", `[resume]
argv = ["xagent", "--resume", "{session_id}"]
keep_flags = ["--yolo"]
keep_options = ["--yolo"]`, "twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseManifest(tc.name+".toml", []byte(head+tc.body))
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("parse: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("parse error = %v, want one naming %q", err, tc.wantErr)
			}
		})
	}
}

// TestResumeKeepsOnlyListedFlags is the security half of kept flags: a value
// from the agent's command line is typed into a shell with the resume command,
// so a value a shell would read as anything but one plain argument, or as a
// flag, is never kept, and only the flags the manifest lists are.
func TestResumeKeepsOnlyListedFlags(t *testing.T) {
	r, _ := Load()
	started := []string{
		"/usr/bin/node", "/opt/claude/cli.js",
		"--permission-mode", "plan", "--model=opus", "-p", "--print", "--continue",
		"--resume", "old-id", "--dangerously-skip-permissions", "--effort", "--yolo",
		"--agent", "a;rm", "--fallback-model", "$(id)", "fix the bug",
		"--", "--model", "sonnet",
	}
	want := []string{"--permission-mode", "plan", "--model", "opus", "--dangerously-skip-permissions"}
	if got := r.KeptFlags("claude-code", started); !slices.Equal(got, want) {
		t.Fatalf("KeptFlags = %q, want %q", got, want)
	}
	argv, err := r.ResumeArgv("claude-code", "3f2a-9c", started)
	if err != nil || !slices.Equal(argv, append([]string{"claude", "--resume", "3f2a-9c"}, want...)) {
		t.Fatalf("ResumeArgv = %q, %v", argv, err)
	}
	// The same flags under another harness's list keep only what it names.
	if got := r.KeptFlags("pi", started); !slices.Equal(got, []string{"--model", "opus"}) {
		t.Fatalf("pi KeptFlags = %q", got)
	}
	if got := r.KeptFlags("aider", started); got != nil {
		t.Fatalf("a harness with no list kept %q", got)
	}
	// Only the marked flags and values are named as turning approvals off.
	if got := r.RiskyFlags("claude-code", argv); !slices.Equal(got, []string{"--dangerously-skip-permissions"}) {
		t.Fatalf("RiskyFlags = %q", got)
	}
	for _, tc := range []struct {
		harness string
		started []string
		want    []string
	}{
		{"claude-code", []string{"claude", "--permission-mode", "bypassPermissions"}, []string{"--permission-mode bypassPermissions"}},
		{"claude-code", []string{"claude", "--permission-mode", "plan"}, nil},
		{"codex", []string{"codex", "-s", "danger-full-access", "--ask-for-approval=never", "-m", "o3"}, []string{"-s danger-full-access", "--ask-for-approval never"}},
		{"codex", []string{"codex", "--sandbox", "workspace-write", "-a", "on-request"}, nil},
		{"copilot", []string{"copilot", "--mode", "autopilot", "--allow-all-paths", "--model", "x"}, []string{"--mode autopilot", "--allow-all-paths"}},
	} {
		argv, err := r.ResumeArgv(tc.harness, "id-1", tc.started)
		if got := r.RiskyFlags(tc.harness, argv); err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s %q: RiskyFlags = %q, %v; want %q", tc.harness, tc.started, got, err, tc.want)
		}
	}
}
