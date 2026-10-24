package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Two rules for a pane that types into another pane, on a real daemon in the
// default open mode:
//
//   - send-text and send-keys from a pane need -w. Without it they used to
//     type into the pane the person has in focus.
//   - From a pane, they do not type into a pane on needs_input without
//     --allow-blocked, even when the caller holds respond. ask-agent already
//     had this rule.
//
// The person, outside every pane, is held to neither rule.
//
// How this could pass wrongly, written down first:
//   - The probe's calls could fail for a reason that is not the rule, such as
//     a missing grant or a CLI that cannot reach the daemon. So the probe
//     holds admin and respond, and the same calls with -w (into a plain
//     pane) and with --allow-blocked (into the blocked pane) must work and
//     land.
//   - "Nothing landed" could be read before the bytes arrive. The blocked
//     pane's log is read after the allowed text and the person's text have
//     landed, and those were typed after the refused calls.
//   - The blocked pane could fail to report needs_input, and the calls would
//     go through for that reason. The test waits for needs_input first, and
//     the refusals must name agent_blocked's remedy, --allow-blocked.
//   - The focused pane could not be the plain pane, and text without -w would
//     land somewhere the test does not read. The plain pane is created with
//     the focus, and the test checks that it still has it.

// blockedPrompt reports needs_input for its own pane, shows a prompt, and
// appends every byte typed into it to the log named by $1.
const blockedPrompt = `#!/usr/bin/env bash
log=$1 bin=$2
: > "$log"
"$bin" set-agent-state needs_input --kind approval --message 'approve Bash: rm -rf ./build'
printf 'Bash command\n  rm -rf ./build\nDo you want to proceed?\n> 1. Yes\n  2. No\n'
stty -icanon -echo min 1
exec cat >> "$log"
`

// typingProbe waits for its go file, then runs each call and records its
// output and exit status in the out folder.
const typingProbe = `#!/usr/bin/env bash
out=$1 bin=$2 s=$3
until [ -e "$out/go" ]; do sleep 0.05; done
run() { name=$1; shift; "$bin" "$@" > "$out/$name" 2>&1; echo "exit=$?" >> "$out/$name"; }
run nowin-text send-text -s "$s" 'NOWIN_TEXT'
run nowin-keys send-keys -s "$s" 'N O W I N'
run plain-text send-text -s "$s" -w plain 'echo WITH''_WINDOW
'
run blocked-text send-text -s "$s" -w blocked 'REFUSED_TEXT'
run blocked-keys send-keys -s "$s" -w blocked 'Q Z'
run blocked-paste paste-buffer -s "$s" -w blocked -b probe
run allowed-text send-text -s "$s" -w blocked --allow-blocked 'ALLOWED_TEXT'
: > "$out/done"
exec sleep 600
`

// TestAPaneTypesOnlyWhereItAimsAndNotIntoAPrompt: session t holds a plain
// shell with the focus, a pane on needs_input, and the probe, which holds
// admin and respond.
//
// Negative control: on origin/main the calls without -w exit 0 and
// NOWIN_TEXT lands in the focused plain pane, and the calls into the blocked
// pane exit 0 and land in its log.
func TestAPaneTypesOnlyWhereItAimsAndNotIntoAPrompt(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "t"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	out := t.TempDir()
	log := filepath.Join(out, "blocked.log")
	blocked := newPaneRunning(t, base, sess, "blocked", "bash", writeAgentScript(t, base, "blocked.sh", blockedPrompt), log, tuiosBin)
	probe := newPaneRunning(t, base, sess, "probe", "bash", writeAgentScript(t, base, "probe.sh", typingProbe), out, tuiosBin, sess)
	o, err := tuiosCLI(t, base, "new-window", "-s", sess, "--print-id", "plain", "--", "sh")
	if err != nil {
		t.Fatalf("new-window plain: %v\n%s", err, o)
	}
	plain := strings.TrimSpace(o[strings.LastIndexByte(strings.TrimSpace(o), '\n')+1:])
	if o, err := tuiosCLI(t, base, "set-pane-grants", "-s", sess, "-w", probe, "--grants", "admin,respond"); err != nil {
		t.Fatalf("give the probe respond: %v\n%s", err, o)
	}
	if o, err := tuiosCLI(t, base, "set-buffer", "-b", "probe", "BUFFER_TEXT"); err != nil {
		t.Fatalf("set-buffer: %v\n%s", err, o)
	}
	if o, err := tuiosCLI(t, base, "wait-for", "agent-state", "-s", sess, "-w", blocked, "--until", "needs_input", "--timeout", "10000"); err != nil {
		t.Fatalf("the blocked pane never reported needs_input: %v\n%s", err, o)
	}
	if focused, _ := tuiosCLI(t, base, "list-windows", "-s", sess, "--json"); !strings.Contains(focused, `"focused_window_id": "`+plain+`"`) {
		t.Fatalf("the plain pane %s does not have the focus:\n%s", plain, focused)
	}

	if err := os.WriteFile(filepath.Join(out, "go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(shellTimeout)
	for {
		if _, err := os.Stat(filepath.Join(out, "done")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the probe never finished")
		}
		time.Sleep(50 * time.Millisecond)
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(out, name))
		return string(b)
	}
	plainText := func() string {
		o, _ := tuiosCLI(t, base, "capture-pane", "-s", sess, "-w", "plain")
		return o
	}

	// The person, outside every pane, types into the blocked pane.
	if o, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", blocked, "PERSON_TEXT"); err != nil {
		t.Errorf("ASSERTION: the person's send-text into the blocked pane was refused: %v\n%s", err, o)
	}
	// The person's text is typed last, so once it is in the log every call
	// before it has landed or never will.
	got := waitFile(t, log, func(s string) bool {
		return strings.Contains(s, "PERSON_TEXT")
	}, "ASSERTION: the person's text never reached the blocked pane")
	if !strings.Contains(got, "ALLOWED_TEXT") {
		t.Errorf("ASSERTION: send-text --allow-blocked never reached the blocked pane: %q", got)
	}

	for _, name := range []string{"nowin-text", "nowin-keys"} {
		if r := read(name); !strings.Contains(r, "exit=1") || !strings.Contains(r, "-w") {
			t.Errorf("ASSERTION: %s from a pane without -w was not refused with a hint to pass -w:\n%s", name, r)
		}
	}
	if r := read("plain-text"); !strings.Contains(r, "exit=0") {
		t.Errorf("ASSERTION: send-text -w plain from the probe was refused:\n%s", r)
	}
	deadline = time.Now().Add(shellTimeout)
	for !strings.Contains(plainText(), "WITH_WINDOW") {
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: send-text -w plain never reached the plain pane:\n%s", plainText())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if p := plainText(); strings.Contains(p, "NOWIN") {
		t.Errorf("ASSERTION: a call without -w typed into the focused pane:\n%s", p)
	}

	for _, name := range []string{"blocked-text", "blocked-keys", "blocked-paste"} {
		if r := read(name); !strings.Contains(r, "exit=1") || !strings.Contains(r, "allow-blocked") {
			t.Errorf("ASSERTION: %s into the pane on needs_input was not refused with the --allow-blocked remedy:\n%s", name, r)
		}
	}
	if r := read("allowed-text"); !strings.Contains(r, "exit=0") {
		t.Errorf("ASSERTION: send-text --allow-blocked from a pane with respond was refused:\n%s", r)
	}
	for _, refused := range []string{"REFUSED_TEXT", "QZ", "BUFFER_TEXT"} {
		if strings.Contains(got, refused) {
			t.Errorf("ASSERTION: %q reached the pane on needs_input without --allow-blocked: %q", refused, got)
		}
	}
}
