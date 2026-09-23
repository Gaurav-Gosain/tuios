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
		{"qoder", []string{"qodercli", "--resume", "3f2a-9c"}},
	} {
		got, err := r.ResumeArgv(tc.harness, "3f2a-9c")
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
	if _, err := r.ResumeArgv("aider", "x"); !errors.Is(err, ErrNoResume) {
		t.Errorf("a harness without [resume] built a command: %v", err)
	}
	if _, err := r.ResumeArgv("no-such-harness", "x"); !errors.Is(err, ErrNoResume) {
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
		if argv, err := r.ResumeArgv("claude-code", id); !errors.Is(err, ErrBadResumeID) {
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

// TestResumeCommandLineIsTheTokens pins the typed form: the checks leave
// nothing to quote, so it is the tokens and single spaces.
func TestResumeCommandLineIsTheTokens(t *testing.T) {
	if got := ResumeCommandLine([]string{"claude", "--resume", "5f1c"}); got != "claude --resume 5f1c" {
		t.Errorf("ResumeCommandLine = %q", got)
	}
}
