package federation

import (
	"errors"
	"strings"
	"testing"
)

// TestTableRefusesReservedAndMalformedNames guards the addressing rule in
// section 3: a name becomes a qualifier in host:target, so a name carrying a
// colon would make an address readable two ways, and `local` already means this
// machine.
func TestTableRefusesReservedAndMalformedNames(t *testing.T) {
	table, problems := NewTable([]Host{
		{Name: "build", Addr: "gaurav@buildbox"},
		{Name: "local", Addr: "somewhere"},
		{Name: "has:colon", Addr: "somewhere"},
		{Name: "noaddr"},
		{Name: "build", Addr: "second"},
	})

	if got := table.Names(); len(got) != 1 || got[0] != "build" {
		t.Fatalf("table holds %v, want just [build]", got)
	}
	if len(problems) != 4 {
		t.Fatalf("got %d problems, want 4: %v", len(problems), problems)
	}
	joined := strings.Join(errStrings(problems), " | ")
	for _, want := range []string{"local", "has:colon", "noaddr", "already used"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems do not mention %q: %s", want, joined)
		}
	}
	// The one good host survived the four bad ones, which is the point: a
	// typo in one entry must not cost the user the rest of the table.
	h, err := table.Lookup("build")
	if err != nil {
		t.Fatalf("lookup build: %v", err)
	}
	if h.Addr != "gaurav@buildbox" {
		t.Errorf("build addr is %q, want gaurav@buildbox", h.Addr)
	}
}

// TestLookupNamesTheConfiguredHosts is section 3's fail-fast rule: an unknown
// name is answered with the real set, never with a guess.
func TestLookupNamesTheConfiguredHosts(t *testing.T) {
	table, _ := NewTable([]Host{
		{Name: "build", Addr: "a"},
		{Name: "work", Addr: "b"},
	})
	_, err := table.Lookup("buil")
	if !errors.Is(err, ErrUnknownHost) {
		t.Fatalf("lookup of a near miss returned %v, want ErrUnknownHost", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "build") || !strings.Contains(msg, "work") {
		t.Errorf("error does not list the configured hosts: %s", msg)
	}
	// A near miss must not be resolved for the caller.
	if strings.Contains(msg, "did you mean") {
		t.Errorf("error suggests a host; exact resolution is the whole defense: %s", msg)
	}
}

func TestSplitTargetTreatsUnqualifiedAsLocal(t *testing.T) {
	cases := []struct{ in, host, target string }{
		{"work", LocalHostName, "work"},
		{"build:api", "build", "api"},
		{"local:api", LocalHostName, "api"},
		{":api", LocalHostName, ":api"},
		{"build:", "build", ""},
	}
	for _, tc := range cases {
		host, target := SplitTarget(tc.in)
		if host != tc.host || target != tc.target {
			t.Errorf("SplitTarget(%q) = (%q, %q), want (%q, %q)", tc.in, host, target, tc.host, tc.target)
		}
	}
}

func errStrings(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Error())
	}
	return out
}
