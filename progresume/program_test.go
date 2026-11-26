package progresume

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// specReporter reports as the specification's examples do: the dummy token,
// time 1795543200, and the pid given.
func specReporter(pid int, out *strings.Builder) *Reporter {
	return &Reporter{Token: "dummy-token-replace-me", PID: pid, Out: out,
		Now: func() time.Time { return time.Unix(1795543200, 0) }}
}

// TestReporterWritesTheSpecExamples holds Report and Clear to the
// specification's example reports, byte for byte.
func TestReporterWritesTheSpecExamples(t *testing.T) {
	var out strings.Builder
	if err := specReporter(90412, &out).Report([]string{"resume-wrap", "ssh", "devbox"}, ""); err != nil {
		t.Fatal(err)
	}
	const wrap = "\x1b]7577;pid=90412:time=1795543200:argv=cmVzdW1lLXdyYXA=,c3No,ZGV2Ym94:mac=e76149d0ba54a83570ffab0583867dacdfa23928ba9d18604f0badea88b62b65\x1b\\"
	if out.String() != wrap {
		t.Errorf("resume-wrap ssh devbox:\n got %q\nwant %q", out.String(), wrap)
	}

	out.Reset()
	if err := specReporter(74877, &out).Clear(); err != nil {
		t.Fatal(err)
	}
	const clear = "\x1b]7577;pid=74877:time=1795543200:clear=1:mac=c30603369f98fef581a4098be889175b0c74f0641e49f8b25f4326cb38f97c93\x1b\\"
	if out.String() != clear {
		t.Errorf("clear:\n got %q\nwant %q", out.String(), clear)
	}

	out.Reset()
	if err := specReporter(74877, &out).Report([]string{"lazygit"}, "/home/user/src/project"); err != nil {
		t.Fatal(err)
	}
	const lazygit = "\x1b]7577;pid=74877:time=1795543200:argv=bGF6eWdpdA==:cwd=L2hvbWUvdXNlci9zcmMvcHJvamVjdA==:mac=35cb909805c380336efd3689a125acc774a9dec8422a1d21492ebec06ca758e1\x1b\\"
	if out.String() != lazygit {
		t.Errorf("lazygit:\n got %q\nwant %q", out.String(), lazygit)
	}
}

// TestReporterRefusesABadReport sends nothing for a report a terminal would
// reject, and nothing without a token.
func TestReporterRefusesABadReport(t *testing.T) {
	for name, c := range map[string]struct {
		argv []string
		cwd  string
	}{
		"relative program": {[]string{"./run.sh"}, ""},
		"no program":       {nil, ""},
		"empty program":    {[]string{""}, ""},
		"relative cwd":     {[]string{"nvim"}, "project"},
		"control char":     {[]string{"nvim", "a\x1bb"}, ""},
		"too many args":    {append([]string{"nvim"}, make([]string, MaxArgv)...), ""},
	} {
		var out strings.Builder
		err := specReporter(1, &out).Report(c.argv, c.cwd)
		if err == nil || out.Len() != 0 {
			t.Errorf("%s: err %v, wrote %q, want an error and nothing written", name, err, out.String())
		}
	}
	var out strings.Builder
	r := &Reporter{PID: 1, Out: &out}
	if err := r.Report([]string{"nvim"}, ""); !errors.Is(err, ErrNoToken) || out.Len() != 0 {
		t.Errorf("no token: err %v, wrote %q, want ErrNoToken and nothing written", err, out.String())
	}
}

// TestTakeTokenEmptiesTheEnvironment takes the token out of the
// environment on the first use, also when nothing is sent.
func TestTakeTokenEmptiesTheEnvironment(t *testing.T) {
	t.Setenv(TokenEnv, "dummy-token-replace-me")
	TakeToken()
	if v, ok := os.LookupEnv(TokenEnv); ok {
		t.Fatalf("%s is still set after TakeToken: %q", TokenEnv, v)
	}
}
