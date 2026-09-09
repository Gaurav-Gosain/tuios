package federation

import "testing"

// The grammar is fixed, not a function of the hosts table, and every case
// here is one a caller could type. The ones that matter most are the escapes:
// a session or window whose own name carries a colon still has one spelling
// that reaches it, and adding a host never moves an address.

func TestSessionTargetGrammar(t *testing.T) {
	cases := []struct {
		in   string
		want Target
	}{
		{"", Target{Host: LocalHostName}},
		{"api", Target{Host: LocalHostName, Session: "api"}},
		{"build:api", Target{Host: "build", Qualified: true, Session: "api"}},
		// An empty session on a host is that host's default session.
		{"build:", Target{Host: "build", Qualified: true, Session: ""}},
		// The rest is verbatim, colons included.
		{"build:api:0", Target{Host: "build", Qualified: true, Session: "api:0"}},
		// local: is the spelling for a session here whose name has a colon.
		{"local:build:api", Target{Host: LocalHostName, Qualified: true, Session: "build:api"}},
		{"local:api", Target{Host: LocalHostName, Qualified: true, Session: "api"}},
		// A prefix that could not be a host name is not a qualifier.
		{"my notes:v2", Target{Host: LocalHostName, Session: "my notes:v2"}},
		{":api", Target{Host: LocalHostName, Session: ":api"}},
		{"a/b:c", Target{Host: LocalHostName, Session: "a/b:c"}},
	}
	for _, c := range cases {
		if got := ParseSessionTarget(c.in); got != c.want {
			t.Errorf("ASSERTION: ParseSessionTarget(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestWindowTargetGrammar(t *testing.T) {
	cases := []struct {
		in   string
		want Target
	}{
		{"", Target{Host: LocalHostName}},
		{"0", Target{Host: LocalHostName, Window: "0"}},
		{"editor", Target{Host: LocalHostName, Window: "editor"}},
		// One colon is never a qualifier for a window.
		{"build:0", Target{Host: LocalHostName, Window: "build:0"}},
		{"build:api:0", Target{Host: "build", Qualified: true, Session: "api", Window: "0"}},
		// The window keeps any further colons.
		{"build:api:a:b", Target{Host: "build", Qualified: true, Session: "api", Window: "a:b"}},
		// A middle part that is empty is not a qualifier, so a title shaped
		// like a URL stays a window on this machine.
		{"https://example.com", Target{Host: LocalHostName, Window: "https://example.com"}},
		// Unless the host is local, which is the escape for such a name.
		{"local::https://example.com", Target{Host: LocalHostName, Qualified: true, Session: "", Window: "https://example.com"}},
		{"local:work:a:b:c", Target{Host: LocalHostName, Qualified: true, Session: "work", Window: "a:b:c"}},
		{"my host:api:0", Target{Host: LocalHostName, Window: "my host:api:0"}},
	}
	for _, c := range cases {
		if got := ParseWindowTarget(c.in); got != c.want {
			t.Errorf("ASSERTION: ParseWindowTarget(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestAnAddressDoesNotMoveWhenAHostIsAdded(t *testing.T) {
	// The parse never consults a table, so the same string parses the same
	// way whether or not "build" is configured. What changes when a host is
	// missing is the daemon's answer: unknown_host, by name.
	before := ParseSessionTarget("build:api")
	_, _ = NewTable([]Host{{Name: "build", Addr: "x"}})
	after := ParseSessionTarget("build:api")
	if before != after || !after.Remote() {
		t.Fatalf("ASSERTION: build:api parsed as %+v then %+v", before, after)
	}
	if ParseSessionTarget("local:x").Remote() {
		t.Fatal("ASSERTION: local: is treated as another machine")
	}
}
