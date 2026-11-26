package progresume

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// The examples of the specification, revision 0.5, with its token and its
// real macs. A terminal that reads them differently is not interoperable,
// which is why this test is kept beside the end-to-end ones.
const specToken = "dummy-token-replace-me"

var specNow = time.Unix(1795543200, 0)

func TestSpecExamples(t *testing.T) {
	valid := []struct {
		name, payload string
		want          Report
	}{
		{"neovim", "7577;pid=74877:time=1795543200:argv=bnZpbQ==,LVM=,L2hvbWUvdXNlci8ubG9jYWwvc3RhdGUvbnZpbS9zZXNzaW9ucy84ZjJjMWQudmlt:mac=9dbb99bfb856749a87563839d7ce9062818d78cf25909db93f309e0cd3b1909b",
			Report{PID: 74877, Time: 1795543200, Argv: []string{"nvim", "-S", "/home/user/.local/state/nvim/sessions/8f2c1d.vim"}}},
		{"neovim clear", "7577;pid=74877:time=1795543200:clear=1:mac=c30603369f98fef581a4098be889175b0c74f0641e49f8b25f4326cb38f97c93",
			Report{PID: 74877, Time: 1795543200, Clear: true}},
		{"lazygit", "7577;pid=74877:time=1795543200:argv=bGF6eWdpdA==:cwd=L2hvbWUvdXNlci9zcmMvcHJvamVjdA==:mac=35cb909805c380336efd3689a125acc774a9dec8422a1d21492ebec06ca758e1",
			Report{PID: 74877, Time: 1795543200, Argv: []string{"lazygit"}, Cwd: "/home/user/src/project"}},
		{"resume-wrap", "7577;pid=90412:time=1795543200:argv=cmVzdW1lLXdyYXA=,c3No,ZGV2Ym94:mac=e76149d0ba54a83570ffab0583867dacdfa23928ba9d18604f0badea88b62b65",
			Report{PID: 90412, Time: 1795543200, Argv: []string{"resume-wrap", "ssh", "devbox"}}},
	}
	for _, c := range valid {
		res := Parse([]byte(c.payload), specToken, specNow)
		if res.Outcome != Accepted {
			t.Errorf("%s: outcome %d (%s), want accepted", c.name, res.Outcome, res.Reason)
			continue
		}
		if !sameReport(res.Report, c.want) {
			t.Errorf("%s: report %+v, want %+v", c.name, res.Report, c.want)
		}
	}

	// The table of invalid reports. M is the right mac of the keys before it.
	signed := func(body string) string { return "7577;" + body + ":mac=" + Mac(body, specToken) }
	T := strconv.FormatInt(specNow.Unix(), 10)
	invalid := []struct {
		name, payload string
		want          Outcome
	}{
		{"wrong mac", "7577;pid=74877:time=" + T + ":argv=bnZpbQ==:mac=" + strings.Repeat("0", 64), Ignored},
		{"no mac", "7577;pid=74877:time=" + T + ":argv=bnZpbQ==", Ignored},
		{"mac first", "7577;mac=" + Mac("pid=74877:time="+T+":argv=bnZpbQ==", specToken) + ":pid=74877:time=" + T + ":argv=bnZpbQ==", Ignored},
		{"old time", signed("pid=74877:time=1000000000:argv=bnZpbQ=="), Ignored},
		{"no time", signed("pid=74877:argv=bnZpbQ=="), Ignored},
		{"relative program", signed("pid=74877:time=" + T + ":argv=Li9ydW4uc2g="), Rejected},
		{"argv and clear", signed("pid=74877:time=" + T + ":argv=bnZpbQ==:clear=1"), Rejected},
		{"relative cwd", signed("pid=74877:time=" + T + ":argv=bnZpbQ==:cwd=cHJvamVjdA=="), Rejected},
		{"need later", signed("pid=74877:time=" + T + ":argv=bnZpbQ==:need=later"), Rejected},
		// And the rules the table does not spell out.
		{"empty argv", signed("pid=74877:time=" + T + ":argv="), Rejected},
		{"wrong padding", signed("pid=74877:time=" + T + ":argv=bnZpbQ="), Rejected},
		{"repeated key", signed("pid=74877:time=" + T + ":argv=bnZpbQ==:argv=bnZpbQ=="), Rejected},
		{"control character", signed("pid=74877:time=" + T + ":argv=bnZpbQ==,YQpi"), Rejected},
		{"program starts with -", signed("pid=74877:time=" + T + ":argv=LXg="), Rejected},
		{"no pid", signed("time=" + T + ":argv=bnZpbQ=="), Rejected},
		{"huge pid", signed("pid=99999999999999999999:time=" + T + ":argv=bnZpbQ=="), Rejected},
		{"unknown key", signed("pid=74877:time=" + T + ":argv=bnZpbQ==:later=x"), Accepted},
		{"need known", signed("pid=74877:time=" + T + ":argv=bnZpbQ==:need=cwd,argv"), Accepted},
		{"reserved", "7577;?", Ignored},
		{"upper-case mac", "7577;pid=74877:time=" + T + ":argv=bnZpbQ==:mac=" + strings.ToUpper(Mac("pid=74877:time="+T+":argv=bnZpbQ==", specToken)), Ignored},
	}
	for _, c := range invalid {
		res := Parse([]byte(c.payload), specToken, specNow)
		if res.Outcome != c.want {
			t.Errorf("%s: outcome %d (%s), want %d", c.name, res.Outcome, res.Reason, c.want)
		}
		if c.want == Rejected && c.name != "no pid" && c.name != "huge pid" && res.Report.PID != 74877 {
			t.Errorf("%s: a rejected report names owner %d, want 74877", c.name, res.Report.PID)
		}
	}

	// The limits: a report at the limit is read, one argv element more or a
	// byte more is not.
	argv := slices.Repeat([]string{"x"}, MaxArgv)
	payload := []byte(trimSeq(Sequence(Report{PID: 1, Time: specNow.Unix(), Argv: argv}, specToken)))
	if res := Parse(payload, specToken, specNow); res.Outcome != Accepted {
		t.Errorf("%d argv elements: outcome %d (%s), want accepted", MaxArgv, res.Outcome, res.Reason)
	}
	payload = []byte(trimSeq(Sequence(Report{PID: 1, Time: specNow.Unix(), Argv: append(argv, "x")}, specToken)))
	if res := Parse(payload, specToken, specNow); res.Outcome != Rejected {
		t.Errorf("%d argv elements: outcome %d, want rejected", MaxArgv+1, res.Outcome)
	}
	for _, total := range []int{MaxSequence, MaxSequence + 1} {
		want := Accepted
		if total > MaxSequence {
			want = Rejected
		}
		if res := Parse(padTo(total), specToken, specNow); res.Outcome != want {
			t.Errorf("a %d byte sequence: outcome %d (%s), want %d", total, res.Outcome, res.Reason, want)
		}
	}
}

// padTo is a valid report payload whose sequence, the opener and a BEL
// included, is total bytes long. An unknown key holds the padding.
func padTo(total int) []byte {
	base := "pid=1:time=" + strconv.FormatInt(specNow.Unix(), 10) + ":argv=eA==:z="
	pad := total - (len("\x1b]") + len("7577;") + len(base) + len(":mac=") + 64 + len("\a"))
	body := base + strings.Repeat("a", pad)
	return []byte("7577;" + body + ":mac=" + Mac(body, specToken))
}

// trimSeq is the payload of a sequence Sequence made: no opener, no ST.
func trimSeq(seq string) string {
	return strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b]"), "\x1b\\")
}

func sameReport(a, b Report) bool {
	return a.PID == b.PID && a.Time == b.Time && slices.Equal(a.Argv, b.Argv) && a.Cwd == b.Cwd && a.Clear == b.Clear
}

// FuzzParse feeds payloads to Parse. The ways it could fail, written down
// first:
//
//   - a panic on a short, empty or odd payload;
//   - an accepted report whose mac is not the one the token makes, so text
//     without the token could make a record;
//   - an accepted report that breaks the rules a resume depends on: no
//     program, a relative program or cwd, a control character, more
//     elements than the limit, both argv and clear;
//   - a report Sequence wrote that Parse does not read back as written.
func FuzzParse(f *testing.F) {
	T := strconv.FormatInt(specNow.Unix(), 10)
	seeds := []string{
		"7577;pid=74877:time=1795543200:argv=bnZpbQ==,LVM=,L2hvbWUvdXNlci8ubG9jYWwvc3RhdGUvbnZpbS9zZXNzaW9ucy84ZjJjMWQudmlt:mac=9dbb99bfb856749a87563839d7ce9062818d78cf25909db93f309e0cd3b1909b",
		"7577;pid=74877:time=1795543200:clear=1:mac=c30603369f98fef581a4098be889175b0c74f0641e49f8b25f4326cb38f97c93",
		"7577;pid=1:time=" + T + ":argv=:mac=",
		"7577;?",
		"7577;",
		"7577;:mac=",
		"7577;mac=:mac=" + strings.Repeat("a", 64),
	}
	for _, s := range seeds {
		f.Add([]byte(s), "x", "", false)
	}
	f.Add([]byte(nil), "cat", "/tmp", false)
	f.Add([]byte(nil), "$(reboot)", "", true)
	f.Fuzz(func(t *testing.T, payload []byte, arg, cwd string, clear bool) {
		res := Parse(payload, specToken, specNow)
		if res.Outcome == Accepted {
			checkAccepted(t, payload, res.Report)
		}
		// A report this package writes reads back as written, when its
		// values are ones a report can carry.
		r := Report{PID: 4242, Time: specNow.Unix(), Argv: []string{"prog", arg, ""}, Cwd: cwd, Clear: clear}
		if clear {
			r.Argv, r.Cwd = nil, ""
		}
		if !carriable(arg) || (cwd != "" && (!carriable(cwd) || !strings.HasPrefix(cwd, "/"))) {
			return
		}
		seq := Sequence(r, specToken)
		if len(seq) > MaxSequence {
			return
		}
		back := Parse([]byte(trimSeq(seq)), specToken, specNow)
		if back.Outcome != Accepted || !sameReport(back.Report, r) {
			t.Fatalf("Sequence(%+v) reads back as %d %+v (%s)", r, back.Outcome, back.Report, back.Reason)
		}
	})
}

// checkAccepted holds an accepted report to the rules a resume relies on.
func checkAccepted(t *testing.T, payload []byte, r Report) {
	t.Helper()
	body := strings.TrimPrefix(string(payload), "7577;")
	i := strings.LastIndex(body, ":mac=")
	if i < 0 || body[i+5:] != Mac(body[:i], specToken) {
		t.Fatalf("accepted a report whose mac the token does not make: %q", payload)
	}
	if r.PID <= 0 || r.Time <= 0 {
		t.Fatalf("accepted a report with pid %d and time %d", r.PID, r.Time)
	}
	if r.Clear == (len(r.Argv) > 0) {
		t.Fatalf("accepted a report with clear %v and %d argv elements", r.Clear, len(r.Argv))
	}
	if len(r.Argv) > MaxArgv || 2+len(payload)+1 > MaxSequence {
		t.Fatalf("accepted a report over the limits: %d elements, %d bytes", len(r.Argv), len(payload))
	}
	if len(r.Argv) > 0 {
		p := r.Argv[0]
		if p == "" || strings.HasPrefix(p, "-") || (strings.Contains(p, "/") && !strings.HasPrefix(p, "/")) {
			t.Fatalf("accepted the program %q", p)
		}
	}
	if r.Cwd != "" && !strings.HasPrefix(r.Cwd, "/") {
		t.Fatalf("accepted the cwd %q", r.Cwd)
	}
	for _, s := range append(slices.Clone(r.Argv), r.Cwd) {
		if !carriable(s) {
			t.Fatalf("accepted the text %q", s)
		}
	}
}

// carriable reports whether s is text a report can carry: UTF-8 with no
// control characters.
func carriable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return false
		}
	}
	return true
}
