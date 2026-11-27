package tuie2e

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The Program Resume Protocol (OSC 7577) end to end, on the real binary.
//
// rep sends one report the way the specification's resume-wrap does, for
// the pid it is given, with its first argument bending one rule. reswrap is
// resume-wrap itself: it reports itself and its arguments for its own pid,
// with the folder it runs in, takes the token out of its environment and
// becomes the program. fakeprog prints how often it ran, its arguments, its
// folder and whether it holds a token, and then waits.
const programResumeRep = `#!/bin/sh
mode=$1 pid=$2
shift 2
b64() { printf '%s' "$1" | base64 | tr -d '\n'; }
args="" sep=""
for a in "$@"; do args="$args$sep$(b64 "$a")"; sep=","; done
now=$(date +%s)
[ "$mode" = old ] && now=1000000000
[ "$mode" = replay ] && now=$((now-30))
body="pid=$pid:time=$now"
if [ "$mode" = clear ]; then body="$body:clear=1"; else body="$body:argv=$args"; fi
[ -n "$RW_CWD" ] && body="$body:cwd=$(b64 "$RW_CWD")"
mac=$(printf '%s:%s' "$body" "$OSC7577_TOKEN" | { sha256sum 2>/dev/null || shasum -a 256; } | cut -c1-64)
[ "$mode" = badmac ] && mac=$(printf '%064d' 0)
printf '\033]7577;%s:mac=%s\033\\' "$body" "$mac" > /dev/tty
`

const programResumeWrap = `#!/bin/sh
if [ -n "$OSC7577_TOKEN" ]; then RW_CWD=$(pwd) rep good $$ reswrap "$@"; fi
unset OSC7577_TOKEN
exec "$@"
`

// programResumeFake is fakeprog, with the file that counts its runs.
func programResumeFake(count string) string {
	return "#!/bin/sh\nn=$(cat " + count + " 2>/dev/null || echo 0); n=$((n+1)); echo $n > " + count + "\n" +
		"printf 'RUN%s ARGS:' $n; for a in \"$@\"; do printf '[%s]' \"$a\"; done; printf '\\n'\n" +
		"printf 'RUN%s PWD:%s\\n' $n \"$(pwd -P)\"\n" +
		"if [ -n \"$OSC7577_TOKEN\" ]; then echo RUN$n TOKEN:present; else echo RUN$n TOKEN:absent; fi\n" +
		"exec sleep 600\n"
}

// installProgramResumeTools writes rep, reswrap and fakeprog to a folder at
// the front of PATH, which every pane and the restored shells inherit.
func installProgramResumeTools(t *testing.T, base string) {
	t.Helper()
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"rep": programResumeRep, "reswrap": programResumeWrap,
		"fakeprog": programResumeFake(filepath.Join(base, "runs")),
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+agentFreePath("/usr/bin", "/bin"))
}

// savedPrograms is the program_resume record of each window in a session's
// state file.
func savedPrograms(base, session string) ([]savedProgram, string) {
	data, err := os.ReadFile(filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "sessions", session+".json"))
	if err != nil {
		return nil, err.Error()
	}
	var st struct {
		Windows []struct {
			ProgramResume *savedProgram `json:"program_resume"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err.Error()
	}
	var out []savedProgram
	for _, w := range st.Windows {
		if w.ProgramResume != nil {
			out = append(out, *w.ProgramResume)
		}
	}
	return out, string(data)
}

type savedProgram struct {
	Argv []string `json:"argv"`
	Cwd  string   `json:"cwd"`
}

// waitSaved waits until the session's state file holds exactly the records
// want, none for an empty want.
func waitSaved(t *testing.T, base, session, what string, timeout time.Duration, want ...[]string) {
	t.Helper()
	waitUntil(t, what, timeout, func() (bool, string) {
		got, raw := savedPrograms(base, session)
		if len(got) != len(want) {
			return false, raw
		}
		for i := range got {
			if !slices.Equal(got[i].Argv, want[i]) {
				return false, raw
			}
		}
		return true, raw
	})
}

// TestProgramResumeBringsTheProgramBack runs a program under reswrap with
// arguments a shell would run or split, $(reboot) and a; b, and an empty
// one, in a folder whose name has a space. The daemon restarts, and the
// Resume row of the restored pane shows the command with those arguments
// quoted. resume-agent brings the program back with the exact arguments, in
// that folder, without the token, which reswrap takes out again.
//
// Then the rules, on a pane whose shell owns the records: a wrong mac and an
// old time are ignored, a relative program is rejected and removes the
// record, a clear removes it, a record whose owner exits goes, and closing
// the pane drops it. The token is in no saved state file. A daemon started
// from a pane that holds a token gives its own panes a token of their own.
//
// Negative controls, each caught here (see NEGATIVE_CONTROLS.md): the mac
// check, the time window, the last-report rule, the relative program rule,
// the drop on a rejection, the drop on a clear, the owner exit check, the
// save of the record, the strip of an inherited token, a token of its own
// per pane, the token in the saved state, the unset in reswrap, the trailing
// empty argument and the change to the folder.
func TestProgramResumeBringsTheProgramBack(t *testing.T) {
	requireReal(t, "restarts the daemon and reads the processes of a pane")
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the owner check reads processes through procfs or sysctl")
	}
	base := t.TempDir()
	killDaemon(t, base)
	installProgramResumeTools(t, base)
	// The shortest save cadence, so an owner that exits is noticed soon.
	writeConfig(t, base, "[daemon]\nsave_seconds = 10\n")

	const session = "e2e-program-resume"
	work := filepath.Join(base, "proj", "sub dir")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	realWork, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	mustCLI(t, base, "new", session, "--detach")

	// The pane's own token, written to a file by the shell so it never
	// reaches the pane's screen or its saved history.
	tokFile := filepath.Join(base, "tok1")
	mustCLI(t, base, "send-text", "-s", session, "printf %s \"$OSC7577_TOKEN\" > "+tokFile+"\n")
	var token string
	waitUntil(t, "the pane has a token", uiTimeout, func() (bool, string) {
		b, _ := os.ReadFile(tokFile)
		token = string(b)
		return len(token) >= 22, token
	})

	// A subshell runs the program in the folder, so the shell stays where it
	// is and the restore must change to the folder itself.
	mustCLI(t, base, "send-text", "-s", session, "(cd '"+work+"' && reswrap fakeprog '$(reboot)' 'a; b' '')\n")
	waitUnwrapped(t, base, session, "RUN1 ARGS:[$(reboot)][a; b][]")
	waitUnwrapped(t, base, session, "RUN1 TOKEN:absent")
	want := []string{"reswrap", "fakeprog", "$(reboot)", "a; b", ""}
	waitSaved(t, base, session, "the report is saved with the window", uiTimeout, want)
	if got, _ := savedPrograms(base, session); got[0].Cwd != work {
		t.Fatalf("ASSERTION: the saved record has the folder %q, want %q", got[0].Cwd, work)
	}
	assertTokenNotSaved(t, base, token)

	term := startIn(t, base, startOpts{args: []string{"attach", session}})
	if err := term.WaitForText("RUN1 ARGS:", bootTimeout); err != nil {
		t.Fatalf("the client never showed the program: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "program-resume-before")
	term.Close()

	mustCLI(t, base, "restart-server", "--yes")
	waitForSessionInfo(t, base, session)

	// The row shows the command, with every argument a shell reads quoted.
	const shown = `reswrap fakeprog "$(reboot)" "a; b" ""`
	waitUntil(t, "the restored pane offers the program", uiTimeout, func() (bool, string) {
		out, _ := tuiosCLI(t, base, "list-attention")
		return strings.Contains(out, "Resume") && strings.Contains(out, shown), out
	})
	if out, err := tuiosCLI(t, base, "resume-agent", "-s", session, "--dry-run"); err != nil || strings.TrimSpace(out) != shown {
		t.Fatalf("ASSERTION: resume-agent --dry-run does not show the program: %v\n%s", err, out)
	}
	// The restore took the record off the pane: nothing is saved until the
	// program reports again.
	// It says "program", with single spaces: a program record has no
	// harness and no conversation id to name.
	if out := mustCLI(t, base, "resume-agent", "-s", session); !strings.HasPrefix(out, "Resumed the program in ") || strings.Contains(out, "conversation") || strings.Contains(out, "  ") {
		t.Fatalf("ASSERTION: resume-agent words a program as a conversation: %q", out)
	}
	waitUnwrapped(t, base, session, "RUN2 ARGS:[$(reboot)][a; b][]")
	waitUnwrapped(t, base, session, "RUN2 PWD:"+realWork)
	waitUnwrapped(t, base, session, "RUN2 TOKEN:absent")
	// reswrap reported again, with the restored pane's new token.
	waitSaved(t, base, session, "the resumed program reports again", uiTimeout, want)
	if out, _ := tuiosCLI(t, base, "list-attention"); strings.Contains(out, shown) {
		t.Fatalf("ASSERTION: the Resume row stayed after the resume:\n%s", out)
	}
	term = startIn(t, base, startOpts{args: []string{"attach", session}})
	if err := term.WaitForText("RUN2 ARGS:", bootTimeout); err != nil {
		t.Fatalf("the client never showed the resumed program: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "program-resume-after")
	term.Close()

	checkProgramResumeRules(t, base)
	checkNestedToken(t, base, token)
}

// checkProgramResumeRules holds the record of one pane to the rules. rep
// reports for the pane's shell ($$), which runs as long as the pane does.
func checkProgramResumeRules(t *testing.T, base string) {
	t.Helper()
	const s = "e2e-program-rules"
	mustCLI(t, base, "new", s, "--detach")
	rep := func(mode string, args ...string) {
		mustCLI(t, base, "send-text", "-s", s, "rep "+mode+" $$ "+strings.Join(args, " ")+"\n")
	}
	sync := func(mark string) {
		// The typed line holds SYNC-%s, never SYNC-mark, so only the output
		// matches, wherever typeahead put it.
		mustCLI(t, base, "send-text", "-s", s, "printf 'SYNC-%s\\n' "+mark+"\n")
		waitUnwrapped(t, base, s, "SYNC-"+mark)
	}

	rep("good", "cat", "good")
	waitSaved(t, base, s, "a good report makes a record", uiTimeout, []string{"cat", "good"})

	// Ignored: no effect at all, so the record stays past the next save.
	rep("badmac", "cat", "badmac")
	rep("old", "cat", "old")
	// Inside the time window, but older than the last report of the shell.
	rep("replay", "cat", "replay")
	sync("ignored")
	stayTrue(t, "a report with a wrong mac, an old time or a replayed time changes nothing", 4*time.Second, func() (bool, string) {
		got, raw := savedPrograms(base, s)
		return len(got) == 1 && slices.Equal(got[0].Argv, []string{"cat", "good"}), raw
	})

	// Rejected: the owner's record goes.
	rep("good", "./run.sh")
	waitSaved(t, base, s, "a relative program is rejected and removes the record", uiTimeout)

	rep("good", "cat", "again")
	waitSaved(t, base, s, "a new report makes a record again", uiTimeout, []string{"cat", "again"})
	rep("clear")
	waitSaved(t, base, s, "a clear removes the record", uiTimeout)

	// An old time from a process with no report before it and no record
	// above it: only the time window stops it.
	mustCLI(t, base, "send-text", "-s", s, "sh -c 'rep old $$ cat old; sleep 4'\n")
	sync("old-child")
	stayTrue(t, "a report with an old time from a new process changes nothing", 3*time.Second, func() (bool, string) {
		got, raw := savedPrograms(base, s)
		return len(got) == 0, raw
	})

	// A record whose owner exits goes, a little after.
	mustCLI(t, base, "send-text", "-s", s, "sh -c 'rep good $$ cat child; sleep 5'\n")
	waitSaved(t, base, s, "a child of the shell may own the record", uiTimeout, []string{"cat", "child"})
	waitSaved(t, base, s, "the record goes when its owner exits", 40*time.Second)

	// Closing the pane drops its record with the window.
	rep("good", "cat", "closed")
	waitSaved(t, base, s, "the record is saved before the close", uiTimeout, []string{"cat", "closed"})
	ids := paneIDs(t, base, s)
	mustCLI(t, base, "new-window", "-s", s)
	mustCLI(t, base, "close-window", "--session", s, ids[0])
	waitSaved(t, base, s, "closing the pane drops its record", uiTimeout)
}

// checkNestedToken starts a second daemon the way a pane would, with the
// outer pane's token in its environment. Its pane gets a token of its own.
func checkNestedToken(t *testing.T, base, outer string) {
	t.Helper()
	inner := t.TempDir()
	killDaemon(t, inner)
	if out, err := tuiosCLIEnv(t, inner, []string{"OSC7577_TOKEN=" + outer}, "new", "inner", "--detach"); err != nil {
		t.Fatalf("start the inner daemon: %v\n%s", err, out)
	}
	tokFile := filepath.Join(base, "tok-inner")
	if out, err := tuiosCLIEnv(t, inner, nil, "send-text", "-s", "inner", "printf %s \"$OSC7577_TOKEN\" > "+tokFile+"\n"); err != nil {
		t.Fatalf("send-text to the inner pane: %v\n%s", err, out)
	}
	waitUntil(t, "the inner pane has a token of its own", uiTimeout, func() (bool, string) {
		b, _ := os.ReadFile(tokFile)
		got := string(b)
		return len(got) >= 22 && got != outer, "inner token " + strconv.Quote(got)
	})

	// With program resume off, the panes get no token, and none from the
	// daemon's own environment either.
	off := t.TempDir()
	killDaemon(t, off)
	writeConfig(t, off, "[daemon]\nresume_programs = false\n")
	if out, err := tuiosCLIEnv(t, off, []string{"OSC7577_TOKEN=" + outer}, "new", "off", "--detach"); err != nil {
		t.Fatalf("start the daemon with program resume off: %v\n%s", err, out)
	}
	offFile := filepath.Join(base, "tok-off")
	if out, err := tuiosCLIEnv(t, off, nil, "send-text", "-s", "off", "printf '[%s]' \"$OSC7577_TOKEN\" > "+offFile+"\n"); err != nil {
		t.Fatalf("send-text to the pane: %v\n%s", err, out)
	}
	waitUntil(t, "a pane gets no token when program resume is off", uiTimeout, func() (bool, string) {
		b, _ := os.ReadFile(offFile)
		return string(b) == "[]", "token " + strconv.Quote(string(b))
	})
}

// assertTokenNotSaved fails when any file the daemon saved holds the token.
func assertTokenNotSaved(t *testing.T, base, token string) {
	t.Helper()
	root := xdgDir(base, "XDG_STATE_HOME")
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), token) {
			t.Fatalf("ASSERTION: the saved file %s holds the pane's token", path)
		}
		return nil
	})
}

// clientReports are the OSC 7577 reports in a client's output that the
// token signs: each body, decoded.
func clientReports(t *testing.T, wire, token string) []clientReport {
	t.Helper()
	var out []clientReport
	for _, seq := range strings.Split(wire, "\x1b]7577;")[1:] {
		end := strings.Index(seq, "\x1b\\")
		if end < 0 {
			continue
		}
		body := seq[:end]
		i := strings.LastIndex(body, ":mac=")
		if i < 0 {
			continue
		}
		sum := sha256.Sum256([]byte(body[:i] + ":" + token))
		if body[i+5:] != hex.EncodeToString(sum[:]) {
			t.Fatalf("ASSERTION: the client sent a report its token does not sign: %q", body)
		}
		r := clientReport{}
		for p := range strings.SplitSeq(body[:i], ":") {
			k, v, _ := strings.Cut(p, "=")
			switch k {
			case "pid":
				r.pid, _ = strconv.Atoi(v)
			case "clear":
				r.clear = v == "1"
			case "argv":
				for e := range strings.SplitSeq(v, ",") {
					b, err := base64.StdEncoding.DecodeString(e)
					if err != nil {
						t.Fatalf("ASSERTION: the client sent an argv element that is not base64: %q", e)
					}
					r.argv = append(r.argv, string(b))
				}
			}
		}
		out = append(out, r)
	}
	return out
}

type clientReport struct {
	pid   int
	argv  []string
	clear bool
}

// TestClientReportsItsAttachCommand runs a client in a terminal that gives it
// an OSC 7577 token. The client reports tuios attach and the session on
// attach, the new session after a switch, and a clear when the person
// detaches. A client that gets SIGHUP or SIGTERM, which is what it gets when its
// terminal ends, sends no clear.
//
// Negative controls, each caught here (see NEGATIVE_CONTROLS.md): no report
// at all, the session left out of the compare in hostResumeAfter, no clear
// on a detach, and a clear on any exit.
func TestClientReportsItsAttachCommand(t *testing.T) {
	requireReal(t, "reads what the client writes to its own terminal")
	base := t.TempDir()
	killDaemon(t, base)
	const token = "e2e-outer-terminal-token-0123456789"
	mustCLI(t, base, "new", "e2e-client-resume", "--detach")
	mustCLI(t, base, "new", "e2e-client-other", "--detach")
	// The client names itself by the path the kernel gives its binary.
	bin, err := filepath.Abs(tuiosBin)
	if err == nil {
		bin, err = filepath.EvalSymlinks(bin)
	}
	if err != nil {
		t.Fatal(err)
	}
	attachOf := func(session string) []string { return []string{bin, "attach", "--", session} }

	var wire lockedBuffer
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-client-resume"}, env: []string{"OSC7577_TOKEN=" + token}, out: &wire})
	var pid int
	waitUntil(t, "the client reports its attach command", bootTimeout, func() (bool, string) {
		rs := clientReports(t, wire.String(), token)
		if len(rs) == 0 || !slices.Equal(rs[0].argv, attachOf("e2e-client-resume")) {
			return false, strconv.Itoa(len(rs)) + " reports"
		}
		pid = rs[0].pid
		return pid > 0, "pid " + strconv.Itoa(pid)
	})
	mustCLI(t, base, "switch-session", "e2e-client-other")
	waitUntil(t, "the client reports the session it switched to", uiTimeout, func() (bool, string) {
		rs := clientReports(t, wire.String(), token)
		last := rs[len(rs)-1]
		return slices.Equal(last.argv, attachOf("e2e-client-other")) && last.pid == pid, strconv.Itoa(len(rs)) + " reports"
	})
	sendKeys(t, term, tuitest.Ctrl('b'), "d")
	waitUntil(t, "a detach sends a clear", uiTimeout, func() (bool, string) {
		rs := clientReports(t, wire.String(), token)
		last := rs[len(rs)-1]
		return last.clear && last.pid == pid, strconv.Itoa(len(rs)) + " reports"
	})

	// A hangup and a SIGTERM are what a client gets when its terminal ends,
	// which is when the record is needed.
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM} {
		var out lockedBuffer
		startIn(t, base, startOpts{args: []string{"attach", "e2e-client-resume"}, env: []string{"OSC7577_TOKEN=" + token}, out: &out})
		var cpid int
		waitUntil(t, "the client reports before "+sig.String(), bootTimeout, func() (bool, string) {
			rs := clientReports(t, out.String(), token)
			if len(rs) > 0 {
				cpid = rs[0].pid
			}
			return cpid > 0, strconv.Itoa(len(rs)) + " reports"
		})
		if err := syscall.Kill(cpid, sig); err != nil {
			t.Fatalf("send %v to the client: %v", sig, err)
		}
		waitUntil(t, "the client ends on "+sig.String(), uiTimeout, func() (bool, string) {
			return syscall.Kill(cpid, 0) != nil, "still running"
		})
		for _, r := range clientReports(t, out.String(), token) {
			if r.clear {
				t.Fatalf("ASSERTION: a client that got %v sent a clear", sig)
			}
		}
	}
}
