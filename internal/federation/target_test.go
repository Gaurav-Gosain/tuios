package federation

import "testing"

func TestSessionTargetGrammar(t *testing.T) {
	cases := []struct {
		in   string
		want Target
	}{
		{"", Target{Host: LocalHostName}},
		{"api", Target{Host: LocalHostName, Session: "api"}},
		{"build:api", Target{Host: "build", Qualified: true, Session: "api"}},
		{"build:", Target{Host: "build", Qualified: true, Session: ""}},
		{"build:api:0", Target{Host: "build", Qualified: true, Session: "api:0"}},
		{"local:build:api", Target{Host: LocalHostName, Qualified: true, Session: "build:api"}},
		{"local:api", Target{Host: LocalHostName, Qualified: true, Session: "api"}},
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
		{"build:0", Target{Host: LocalHostName, Window: "build:0"}},
		{"build:api:0", Target{Host: "build", Qualified: true, Session: "api", Window: "0"}},
		{"build:api:a:b", Target{Host: "build", Qualified: true, Session: "api", Window: "a:b"}},
		{"https://example.com", Target{Host: LocalHostName, Window: "https://example.com"}},
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
