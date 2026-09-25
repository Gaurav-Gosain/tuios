package federation

import (
	"errors"
	"testing"
)

// TestQuoteRemoteArgKeepsOneWordOneWord is the reason the quoting exists: a
// session name with a space has to reach the far side as one argument.
func TestQuoteRemoteArgKeepsOneWordOneWord(t *testing.T) {
	cases := map[string]string{
		"api":         "api",
		"my session":  "'my session'",
		"a$b":         "'a$b'",
		"":            "''",
		"web-2.0_x@y": "web-2.0_x@y",
	}
	for in, want := range cases {
		got, err := QuoteRemoteArg(in)
		if err != nil {
			t.Errorf("QuoteRemoteArg(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ASSERTION: QuoteRemoteArg(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"it's", `back\slash`, "new\nline"} {
		if _, err := QuoteRemoteArg(bad); !errors.Is(err, ErrUnsafeRemoteArg) {
			t.Errorf("ASSERTION: QuoteRemoteArg(%q) accepted a name no shell quoting can carry safely: %v", bad, err)
		}
	}
}
