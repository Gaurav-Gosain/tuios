package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// capture-pane --scrollback with no --lines keeps the last 3 commands, cut
// by the shell's OSC 133 marks, in at most 2000 lines. A long output keeps
// its start and its end, with one line in the middle that says how many lines
// it left out. A shell with no marks gives the last 2000 lines. --lines 0
// gives the whole history.

// captureRun runs capture-pane on one pane and returns stdout and stderr
// apart, so a note on stderr can never pass for pane text.
func captureRun(t *testing.T, base, sess, window string, args ...string) (string, string) {
	t.Helper()
	cmd := exec.Command(tuiosBin, append([]string{"capture-pane", "-s", sess, "-w", window}, args...)...)
	cmd.Env = cliEnv(base)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("capture-pane %v: %v: %s", args, err, stderr.String())
	}
	return string(stdout), stderr.String()
}

// nonBlankLines counts content's lines up to the last one that is not blank.
func nonBlankLines(content string) int {
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	n := len(lines)
	for n > 0 && strings.TrimSpace(lines[n-1]) == "" {
		n--
	}
	return n
}

// TestCaptureScrollbackFallsBackToLinesWithoutMarks: the e2e panes run
// /bin/sh, which sends no OSC 133 marks.
//
// How this could pass wrongly, written down first:
//   - The pane could hold fewer than 2000 lines, so nothing is cut and a
//     capture with no cap at all looks capped. The pane prints 2500 numbered
//     lines, and the --lines 0 capture must hold every one of them.
//   - The capped capture could keep the wrong end. It must hold CAP2500 and
//     not CAP1.
//   - The note could be printed on stdout. stdout and stderr are read apart.
//   - lines_omitted could be a guess. It must equal the line count of the
//     whole history minus 2000.
//   - The note could be printed always. The --lines 0 and --lines 50
//     captures must print nothing on stderr.
//
// Negative control: on origin/main the capped capture holds CAP1 and prints
// no note. With the CLI default cut (capped never set), the same.
func TestCaptureScrollbackFallsBackToLinesWithoutMarks(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "capcap"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create the session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", "0", "printf 'CAP%d\\n' $(seq 1 2500)\n"); err != nil {
		t.Fatalf("send-text: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "wait-for", "window-output", "-s", sess, "-w", "0", "--pattern", `CAP2500\b`, "--timeout", "20000"); err != nil {
		t.Fatalf("the pane never printed its last line: %v: %s", err, out)
	}
	run := func(args ...string) (string, string) {
		t.Helper()
		return captureRun(t, base, sess, "0", append([]string{"--scrollback"}, args...)...)
	}

	// Positive half first: --lines 0 is the whole history.
	full, fullErr := run("--lines", "0")
	for _, want := range []string{"CAP1", "CAP1250", "CAP2500"} {
		if !hasLine(full, want) {
			t.Fatalf("ASSERTION: --lines 0 left out %s, want the whole history", want)
		}
	}
	if fullErr != "" {
		t.Fatalf("ASSERTION: --lines 0 printed a note on stderr: %q", fullErr)
	}
	total := nonBlankLines(full)

	capped, cappedErr := run()
	if hasLine(capped, "CAP1") || !hasLine(capped, "CAP2500") {
		t.Fatalf("ASSERTION: --scrollback with no --lines did not keep only the newest lines: CAP1 present=%v, CAP2500 present=%v, %d lines",
			hasLine(capped, "CAP1"), hasLine(capped, "CAP2500"), nonBlankLines(capped))
	}
	if n := nonBlankLines(capped); n != 2000 {
		t.Fatalf("ASSERTION: the capped capture holds %d lines, want 2000", n)
	}
	for _, want := range []string{"no command marks", "2,000", "tuios doctor shell", "--lines 0", itoa(total - 2000)} {
		if !strings.Contains(cappedErr, want) {
			t.Fatalf("ASSERTION: the note on stderr does not say %q: %q", want, cappedErr)
		}
	}
	if strings.Contains(capped, "note:") {
		t.Fatalf("ASSERTION: the note reached stdout")
	}

	jsonOut, jsonErr := run("--json")
	var res struct {
		LinesOmitted *int  `json:"lines_omitted"`
		CommandMarks *bool `json:"command_marks"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &res); err != nil {
		t.Fatalf("decode the JSON capture: %v: %.200s", err, jsonOut)
	}
	if res.LinesOmitted == nil || *res.LinesOmitted != total-2000 {
		t.Fatalf("ASSERTION: lines_omitted is %v, want %d", res.LinesOmitted, total-2000)
	}
	if res.CommandMarks == nil || *res.CommandMarks {
		t.Fatalf("ASSERTION: command_marks is %v, want false for a shell with no marks", res.CommandMarks)
	}
	if jsonErr != "" {
		t.Fatalf("ASSERTION: --json printed a note on stderr: %q", jsonErr)
	}

	small, smallErr := run("--lines", "50")
	if n := nonBlankLines(small); n != 50 || smallErr != "" {
		t.Fatalf("ASSERTION: --lines 50 gave %d lines and stderr %q, want 50 lines and no note", n, smallErr)
	}

	t.Logf("whole history %d lines, capped capture %d lines, note: %s", total, nonBlankLines(capped), strings.TrimSpace(cappedErr))
}

// markedRC is a bash setup that sends OSC 133 A, B, C and D around every
// command, as tuios doctor shell prints for bash.
const markedRC = `PS1='$ \[\e]133;B\a\]'
PS0='\e]133;C\a'
PROMPT_COMMAND='printf "\e]133;D;%s\a\e]133;A\a" "$?"'
`

// TestCaptureScrollbackKeepsTheLastCommands runs commands in a bash pane
// that marks them.
//
// How this could pass wrongly, written down first:
//   - The short commands could come back whole only because the history is
//     short. A fourth, older command must be left out, so the capture is cut
//     by commands and not by lines.
//   - "Whole" could mean the output without the command line. Each command
//     line must be in the capture.
//   - The long command could be cut from the front, as a line cap does. Its
//     command line and its first output line BIG1 must stay, with the tail
//     BIG10000, and BIG5000 from the middle must go.
//   - The omitted count could be a guess. The marker line's number, the
//     JSON lines_omitted, and the stderr note must agree, and kept plus
//     omitted must be every line of the command.
//   - The trim could cut the short commands too. The two short commands
//     before the long one must stay whole beside it.
//
// Negative control: on origin/main the default capture holds ZEROZ (no
// command cut) and BIG5000 (no middle trim). With the commands default cut
// from the CLI, the capture falls back to the line cap: it loses BIG1 and
// the command line of the long command, and holds no marker line.
func TestCaptureScrollbackKeepsTheLastCommands(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "capcmd"
	rc := filepath.Join(base, "markedrc")
	if err := os.WriteFile(rc, []byte(markedRC), 0o644); err != nil {
		t.Fatal(err)
	}
	// Room for the long output and the commands before it: the default
	// history of 10,000 lines would drop them.
	writeConfig(t, base, "[appearance]\nscrollback_lines = 30000\n")
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create the session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "sh", "--", "bash", "--noprofile", "--rcfile", rc, "-i"); err != nil {
		t.Fatalf("open the bash pane: %v: %s", err, out)
	}
	run := func(command string) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			out, err := tuiosCLI(t, base, "run", "-s", sess, "-w", "sh", "--lines", "1", "--timeout", "30000", "--", command)
			if err == nil {
				return
			}
			// The first prompt may not be up yet.
			if time.Now().After(deadline) || !strings.Contains(out, "not_at_prompt") && !strings.Contains(out, "no_shell_integration") {
				t.Fatalf("run %q: %v: %s", command, err, out)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	for _, c := range []string{`printf 'ZERO%s\n' Z`, `printf 'ONE%s\n' A`, `printf 'TWO%s\n' B`, `printf 'THREE%s\n' C`} {
		run(c)
	}

	// Three short commands: the last three come back whole, the older one
	// does not.
	short, shortErr := captureRun(t, base, sess, "sh", "--scrollback")
	for _, want := range []string{"$ printf 'ONE%s\\n' A", "ONEA", "$ printf 'TWO%s\\n' B", "TWOB", "$ printf 'THREE%s\\n' C", "THREEC"} {
		if !hasLine(short, want) {
			t.Fatalf("ASSERTION: the capture of the last 3 commands lacks %q:\n%s", want, short)
		}
	}
	if strings.Contains(short, "ZEROZ") {
		t.Fatalf("ASSERTION: the capture holds the fourth command back, want only the last 3:\n%s", short)
	}
	if strings.Contains(short, "omitted") || !strings.Contains(shortErr, "last 3 commands") {
		t.Fatalf("ASSERTION: want the 3 short commands whole and a note that names them, got stderr %q:\n%s", shortErr, short)
	}

	// One long command: its middle goes, its start and its end stay.
	run(`printf 'BIG%d\n' $(seq 1 10000)`)
	long, longErr := captureRun(t, base, sess, "sh", "--scrollback")
	if n := nonBlankLines(long); n > 2000 {
		t.Fatalf("ASSERTION: the capture holds %d lines, want at most 2000", n)
	}
	for _, want := range []string{"$ printf 'TWO%s\\n' B", "TWOB", "THREEC", "$ printf 'BIG%d\\n' $(seq 1 10000)", "BIG1", "BIG10000"} {
		if !hasLine(long, want) {
			t.Fatalf("ASSERTION: the trimmed capture lacks %q", want)
		}
	}
	if hasLine(long, "BIG5000") || hasLine(long, "ONEA") {
		t.Fatalf("ASSERTION: the capture kept the middle of the long output or an older command")
	}
	marker := regexp.MustCompile(`(?m)^… ([0-9,]+) lines omitted …$`).FindStringSubmatch(long)
	if marker == nil {
		t.Fatalf("ASSERTION: the long output has no marker line in its middle")
	}
	count := strings.ReplaceAll(marker[1], ",", "")
	if !strings.Contains(marker[1], ",") {
		t.Fatalf("ASSERTION: the count %q has no thousands separator", marker[1])
	}
	if !strings.Contains(longErr, marker[1]) || !strings.Contains(longErr, "--lines 0") {
		t.Fatalf("ASSERTION: the note %q does not give the count %s and --lines 0", longErr, marker[1])
	}
	jsonOut, _ := captureRun(t, base, sess, "sh", "--scrollback", "--json")
	var res struct {
		LinesOmitted int  `json:"lines_omitted"`
		CommandMarks bool `json:"command_marks"`
		Commands     []struct {
			Lines        int  `json:"lines"`
			LinesOmitted int  `json:"lines_omitted"`
			ExitCode     *int `json:"exit_code"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &res); err != nil {
		t.Fatalf("decode the JSON capture: %v", err)
	}
	if !res.CommandMarks || len(res.Commands) != 3 || itoa(res.LinesOmitted) != count {
		t.Fatalf("ASSERTION: want command_marks, 3 commands and lines_omitted %s, got %v, %d, %d", count, res.CommandMarks, len(res.Commands), res.LinesOmitted)
	}
	big := res.Commands[2]
	// The command line, 10000 output lines and the prompt after it, less the
	// marker line.
	if big.LinesOmitted != res.LinesOmitted || big.Lines-1+big.LinesOmitted != 10002 {
		t.Fatalf("ASSERTION: the long command kept %d lines and left out %d, want them to add up to its 10002 lines with the marker", big.Lines, big.LinesOmitted)
	}

	// The positive half: --lines 0 still gives everything.
	full, _ := captureRun(t, base, sess, "sh", "--scrollback", "--lines", "0")
	for _, want := range []string{"ZEROZ", "BIG1", "BIG5000", "BIG10000"} {
		if !hasLine(full, want) {
			t.Fatalf("ASSERTION: --lines 0 left out %s", want)
		}
	}
	t.Logf("trimmed capture %d lines, marker %q, note: %s", nonBlankLines(long), marker[0], strings.TrimSpace(longErr))
}
