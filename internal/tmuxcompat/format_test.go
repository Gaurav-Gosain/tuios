package tmuxcompat

import (
	"testing"
	"time"
)

// TestExpandMatchesTmux is a wire-compatibility table: every want is what
// tmux 3.4 printed for `display -p FORMAT` on a server holding the same
// values as user options (set -g @a abcdefghij, and so on). A tool reads the
// shim's output exactly as it reads tmux's, so the quirks are kept too: an
// s/^/ substitution drops the first character, and an empty match right
// after another is skipped.
func TestExpandMatchesTmux(t *testing.T) {
	saved := time.Local
	time.Local = time.UTC
	defer func() { time.Local = saved }()
	vars := map[string]string{
		"@a":              "abcdefghij",
		"@b":              "/usr/local/bin/bash",
		"@z":              "0",
		"@o":              "1",
		"@x":              "#{@a}",
		"@q":              "a b;c'd*%=",
		"pane_id":         "%0",
		"session_created": "1791099635",
	}
	cases := []struct{ in, want string }{
		{"#{=5:@a}", "abcde"},
		{"#{=-3:@a}", "hij"},
		{"#{=/4/...:@a}", "abcd..."},
		{"#{=/-4/...:@a}", "...ghij"},
		{"#{=/-3/..:@a}", "..hij"},
		{"#{=/3/:@a}", "abc"},
		{"#{=0:@a}", "abcdefghij"},
		{"#{=:@a}", "abcdefghij"},
		{"#{=-20:@a}", "abcdefghij"},
		{"#{=2:#{@a}}", "ab"},
		{"#{=5:nope}|", "|"},
		{"#{l:#{@a}}", "#{@a}"},
		{"#{l:a#,b#}c}", "a,b}c"},
		{"#{l:##}", "#"},
		{"#{l:#{?a,b,c}}", "#{?a,b,c}"},
		{"#{s/b/X/:@a}", "aXcdefghij"},
		{"#{s/[aeiou]/<&>/:@a}", "<&>bcd<&>fgh<&>j"},
		{`#{s/(b)(c)/\2\1/:@a}`, "acbdefghij"},
		{"#{s/B/X/i:@a}", "aXcdefghij"},
		{`#{s/^(.)/[\1]/:@a}`, "[a]bcdefghij"},
		{"#{s/^/>/:@a}", "bcdefghij"},
		{"#{s/x*/-/:@a}", "a-b-c-d-e-f-g-h-i-j-"},
		{"#{s/x/y/:nope}|", "|"},
		{"#{s/a:@a}", "abcdefghij"},
		{"#{=3;s/a/Z/:@a}", "Zbc"},
		{"#{s/a/Z/;=3:@a}", "Zbc"},
		{"#{b:@b}", "bash"},
		{"#{d:@b}", "/usr/local/bin"},
		{"#{b:nope}|", "|"},
		{"#{||:@z,@o}", "1"},
		{"#{&&:@z,@o}", "1"},
		{"#{&&:1,1}", "1"},
		{"#{||:0,}", "0"},
		{"#{||:#{nope},}", "0"},
		{"#{==:#{@z},0}", "1"},
		{"#{==:@z,0}", "0"},
		{"#{==:a,a,b}", "0"},
		{"#{!=:a,b}", "1"},
		{"#{!=:#{@z},0}", "0"},
		{"#{<:a,b}", "1"},
		{"#{>:a,b}", "0"},
		{"#{<=:a,a}", "1"},
		{"#{>=:a,b}", "0"},
		{"#{<:10,9}", "1"},
		{"#{m:*def*,#{@a}}", "1"},
		{"#{m:*DEF*,#{@a}}", "0"},
		{"#{m/i:*DEF*,#{@a}}", "1"},
		{"#{m/r:^abc,#{@a}}", "1"},
		{"#{m/ri:^ABC,#{@a}}", "1"},
		{"#{m:a*,abc}", "1"},
		{"#{m:,}", "1"},
		{"#{m:[a-c]*,#{@a}}", "1"},
		{"#{m:[!a]*,#{@a}}", "0"},
		{"#{?@o,yes,no}", "yes"},
		{"#{?@z,yes,no}", "no"},
		{"#{?@nope,yes,no}", "no"},
		{"#{?@z,a,@o,b,c}", "@o,b,c"},
		{"#{?@z,a,@z,b}", "@z,b"},
		{"#{?@o,#{@a},no}", "abcdefghij"},
		{"#{?,yes,no}", "no"},
		{"#{?@o,yes}", ""},
		{"#{?@o,a#,b,c}", "a,b"},
		{"#{?@o,#{?@z,x,y},n}", "y"},
		{"#{?#{@o},a,b}", "a"},
		{"#{?#{nope},a,b}", "b"},
		{"#{?pane_id,a,b}", "a"},
		{"#{?#{==:#{@z},0},z,nz}", "z"},
		{"#{?#{m:*c*,#{@a}},has,not}", "has"},
		{"#{p6:@z}|", "0     |"},
		{"#{p-6:@z}|", "     0|"},
		{"#{p-3:@a}|", "abcdefghij|"},
		{"#{p-12:@a}|", "  abcdefghij|"},
		{"#{p3;=2:@a}|", "ab |"},
		{"#{n:@a}", "10"},
		{"#{w:@a}", "10"},
		{"#{n:nope}", "0"},
		{"#{q:@q}", `a\ b\;c\'d\*\%\=`},
		{"#{q:@b}", "/usr/local/bin/bash"},
		{"#{E:@x}", "abcdefghij"},
		{"#{T:@x}", "abcdefghij"},
		{"#{E:#{@x}}", "abcdefghij"},
		{"#{@x}", "#{@a}"},
		{"#{a:65}", "A"},
		{"#{a:31}", ""},
		{"#{t:session_created}", "Sun Oct  4 07:40:35 2026"},
		{"#{t:@o}", "Thu Jan  1 00:00:01 1970"},
		{"#{t:@a}", ""},
		{"#{nope}", ""},
		{"#{b:}", ""},
		{"#{d:}", ""},
		{"##x #,y", "#x ,y"},
		{"a#:b", "a#:b"},
	}
	for _, c := range cases {
		if got, _ := Expand(c.in, vars); got != c.want {
			t.Errorf("Expand(%q) = %q, tmux 3.4 printed %q", c.in, got, c.want)
		}
	}
}
