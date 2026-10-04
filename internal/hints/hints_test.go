package hints

import (
	"slices"
	"strings"
	"testing"
)

// found runs the matcher with every built-in on and returns the matched texts
// with their kinds, as "kind:text".
func found(t *testing.T, line string, custom ...string) []string {
	t.Helper()
	m, errs := New(Names(), custom)
	if len(errs) > 0 {
		t.Fatalf("New: %v", errs)
	}
	var out []string
	for _, match := range m.Find(line) {
		out = append(out, match.Kind+":"+line[match.Start:match.End])
	}
	return out
}

// The ways a matcher goes wrong, written down first: it misses a thing it
// names, it reads prose as a thing, it takes a neighbour's punctuation, two
// patterns claim one run of text, and a narrower pattern steals from a wider
// one (the path inside a URL, the hash inside a UUID).
func TestFindBuiltins(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []string
	}{
		{"url", "see https://example.com/a?b=1 now", []string{"url:https://example.com/a?b=1"}},
		{"url gives back a full stop", "go to https://example.com/x.", []string{"url:https://example.com/x"}},
		{"url in brackets", "(https://example.com/b)", []string{"url:https://example.com/b"}},
		{"url keeps balanced brackets", "https://en.wikipedia.org/wiki/Go_(language)", []string{"url:https://en.wikipedia.org/wiki/Go_(language)"}},
		{"scheme alone is a word", "the word https:// alone", nil},
		{"git remote", "origin git@github.com:Gaurav-Gosain/tuios.git (fetch)", []string{"url:git@github.com:Gaurav-Gosain/tuios.git"}},
		{"absolute path", "open /etc/hosts please", []string{"path:/etc/hosts"}},
		{"path with line and column", "internal/app/os.go:42:7: undefined", []string{"path:internal/app/os.go:42:7"}},
		{"home path", "cd ~/dev/tuios.", []string{"path:~/dev/tuios"}},
		{"relative path", "./scripts/deploy-dev.sh", []string{"path:./scripts/deploy-dev.sh"}},
		{"sha", "commit 149c8a8f fix", []string{"sha:149c8a8f"}},
		{"full sha", "a94a8fe5ccb19ba61c4c0873d391e987982fbbd3", []string{"sha:a94a8fe5ccb19ba61c4c0873d391e987982fbbd3"}},
		{"hex word is not a sha", "the defaced wall", nil},
		{"ipv4", "listen 192.168.1.20:8080 ok", []string{"ip:192.168.1.20:8080"}},
		{"ipv4 cidr", "route 10.0.0.0/8 via", []string{"ip:10.0.0.0/8"}},
		{"bad octet", "999.1.1.1", nil},
		{"version is not an ip", "v1.2.3.4.5", nil},
		{"ipv6", "addr fe80::1ff:fe23:4567:890a dev", []string{"ip:fe80::1ff:fe23:4567:890a"}},
		{"ipv6 loopback", "bind [::1]:22", []string{"ip:::1"}},
		{"time is not ipv6", "at 12:34:56 today", nil},
		{"scope operator is not ipv6", "std::vector", nil},
		{"uuid wins over its parts", "id 550e8400-e29b-41d4-a716-446655440000.", []string{"uuid:550e8400-e29b-41d4-a716-446655440000"}},
		{"colour", "color: #1e1e2e;", []string{"color:#1e1e2e"}},
		{"short colour needs a letter", "fixes #123 and #abc", []string{"color:#abc"}},
		{"hex", "addr 0xdeadBEEF", []string{"hex:0xdeadBEEF"}},
		{"number", "pid 12345 and 123", []string{"number:12345"}},
		{"email", "mail ops+tuios@example.co.uk now", []string{"email:ops+tuios@example.co.uk"}},
		{"kube resource", "deployment.apps/web-frontend configured", []string{"id:deployment.apps/web-frontend"}},
		{"pod name", "nginx-deployment-66b6c48dd5-4jw2p   1/1", []string{"id:nginx-deployment-66b6c48dd5-4jw2p", "path:1/1"}},
		{"hyphenated words are not a pod", "a-long-winded-sentence-truly", nil},
		{"digest", "sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", []string{"id:sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"}},
		{"diff header copies the path", "diff --git a/internal/app/os.go b/internal/app/os.go", []string{"diff:internal/app/os.go", "diff:internal/app/os.go"}},
		{"minus header", "--- a/go.mod", []string{"diff:go.mod"}},
		{"git status", "\tmodified:   internal/hints/hints.go   ", []string{"diff:internal/hints/hints.go"}},
		{"git status without a slash", "\tnew file:   README.md", []string{"diff:README.md"}},
		{"a/ in prose is a path", "and a/b testing", []string{"path:a/b"}},
		{"url wins over the path in it", "https://example.com/some/path", []string{"url:https://example.com/some/path"}},
		{"a path with non-Latin names", "open ~/文档/café.md now", []string{"path:~/文档/café.md"}},
		{"a path with a combining mark", "cat /tmp/café/menu.txt", []string{"path:/tmp/café/menu.txt"}},
		{"an address with an accented name", "to josé.garcía@example.es.", []string{"email:josé.garcía@example.es"}},
		{"markdown badge is two urls", "[![ko-fi](https://ko-fi.com/img/githubbutton_sm.svg)](https://ko-fi.com/B0B81N8V1R)",
			[]string{"url:https://ko-fi.com/img/githubbutton_sm.svg", "url:https://ko-fi.com/B0B81N8V1R"}},
		{"html closing tag is not a path", "</p>", nil},
		{"closing tags around text", "<p align=\"center\">text</p></div>", nil},
		{"self-closing tag", "<br/>", nil},
		{"path inside a tag's text", "<p>see ./docs/a.md</p>", []string{"path:./docs/a.md"}},
		{"rooted path", "run /usr/bin/x now", []string{"path:/usr/bin/x"}},
		{"file with line and column", "src/main.go:12:3", []string{"path:src/main.go:12:3"}},
		{"home file", "~/x", []string{"path:~/x"}},
		{"relative file", "./foo", []string{"path:./foo"}},
		{"several on one row", "https://x.org /tmp/a.go:3 deadbee1 10.0.0.1",
			[]string{"url:https://x.org", "path:/tmp/a.go:3", "sha:deadbee1", "ip:10.0.0.1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := found(t, c.line)
			if !slices.Equal(got, c.want) {
				t.Errorf("Find(%q)\n got %q\nwant %q", c.line, got, c.want)
			}
		})
	}
}

func TestFindCustomPatterns(t *testing.T) {
	t.Run("a custom pattern wins over a built-in", func(t *testing.T) {
		got := found(t, "ticket JIRA-1234 done", `JIRA-\d+`)
		want := []string{"custom:JIRA-1234"}
		if !slices.Equal(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("the match group narrows the copy", func(t *testing.T) {
		got := found(t, "branch: feat/hints-mode", `branch: (?P<match>\S+)`)
		want := []string{"custom:feat/hints-mode"}
		if !slices.Equal(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("a bad pattern is reported and the rest still work", func(t *testing.T) {
		m, errs := New(Names(), []string{`(unclosed`, `x*`, `TODO`})
		if len(errs) != 2 {
			t.Fatalf("want 2 errors (does not compile, matches empty), got %v", errs)
		}
		if got := m.Find("a TODO here"); len(got) != 1 || got[0].Kind != Custom {
			t.Errorf("the good pattern did not match: %+v", got)
		}
	})
}

func TestParseBuiltins(t *testing.T) {
	if got, _ := ParseBuiltins(""); !slices.Equal(got, Names()) {
		t.Errorf("empty is all: got %q", got)
	}
	if got, _ := ParseBuiltins("none"); len(got) != 0 {
		t.Errorf("none: got %q", got)
	}
	got, unknown := ParseBuiltins("url, sha,bogus,url")
	if !slices.Equal(got, []string{"url", "sha"}) || !slices.Equal(unknown, []string{"bogus"}) {
		t.Errorf("list: got %q unknown %q", got, unknown)
	}
	m, _ := New(got, nil)
	if res := m.Find("/etc/hosts 1234567 abc1234"); len(res) != 1 || res[0].Kind != SHA {
		t.Errorf("only url and sha may match, got %+v", res)
	}
}

func TestLabels(t *testing.T) {
	cases := []struct {
		n    int
		want []string
	}{
		{1, []string{"a"}},
		{3, []string{"a", "s", "d"}},
		{9, strings.Split(DefaultAlphabet, "")},
		// One more than the alphabet: the last letter is split, and the
		// first eight stay one key long.
		{10, []string{"a", "s", "d", "f", "g", "h", "j", "k", "la", "ls"}},
	}
	for _, c := range cases {
		if got := Labels(c.n, DefaultAlphabet); !slices.Equal(got, c.want) {
			t.Errorf("Labels(%d) = %q, want %q", c.n, got, c.want)
		}
	}

	// At any count: exactly n, unique, prefix-free, shortest first.
	for _, n := range []int{2, 9, 17, 80, 81, 82, 200, 800} {
		got := Labels(n, DefaultAlphabet)
		if len(got) != n {
			t.Fatalf("Labels(%d) returned %d labels", n, len(got))
		}
		for i, a := range got {
			if i > 0 && len(a) < len(got[i-1]) {
				t.Errorf("Labels(%d): %q comes after the longer %q", n, a, got[i-1])
			}
			for j, b := range got {
				if i != j && strings.HasPrefix(b, a) {
					t.Fatalf("Labels(%d): %q is the start of %q, so %q could never be typed", n, a, b, b)
				}
			}
		}
		// As short as possible: n labels need ceil(log_9 n) keys at most.
		limit := 1
		for c := 9; c < n; c *= 9 {
			limit++
		}
		if l := len(got[n-1]); l > limit {
			t.Errorf("Labels(%d): longest label %q has %d keys, want at most %d", n, got[n-1], l, limit)
		}
	}
}

func TestNormalizeAlphabet(t *testing.T) {
	for in, want := range map[string]string{
		"":             DefaultAlphabet,
		"a":            DefaultAlphabet,
		"ASDF":         "asdf",
		"a1s;d a":      "asd",
		"jkl;":         "jkl",
		"qwertyuiopéz": "qwertyuiopz",
	} {
		if got := NormalizeAlphabet(in); got != want {
			t.Errorf("NormalizeAlphabet(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAssign(t *testing.T) {
	targets := []Target{
		{Text: "far", Distance: 30},
		{Text: "near", Distance: 1},
		{Text: "mid", Distance: 5},
		// The same text again, nearer than its first appearance.
		{Text: "far", Distance: 0},
	}
	got := Assign(targets, DefaultAlphabet)
	want := []string{"a", "s", "d", "a"}
	if !slices.Equal(got, want) {
		t.Errorf("Assign = %q, want %q (same text same label, nearest first)", got, want)
	}

	// Ties keep the order the texts were seen in.
	got = Assign([]Target{{Text: "x", Distance: 2}, {Text: "y", Distance: 2}, {Text: "z", Distance: 1}}, "ab")
	if !slices.Equal(got, []string{"a", "ba", "b"}) && !slices.Equal(got, []string{"ba", "bb", "a"}) {
		t.Errorf("Assign with ties = %q", got)
	}
	if got[2] != "a" {
		t.Errorf("the nearest text must get the first label, got %q", got)
	}
}
