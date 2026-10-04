package session

import (
	"strings"
	"testing"
)

// TestPushDestinationShowsNoCredential is a security boundary: the push
// address the Inbox question and a ship result show must never carry a user
// name, password or token, in any form git accepts for a remote. The ways it
// could: userinfo in a URL, an scp-like user, a token in the query or the
// fragment, and an address url.Parse cannot read. The host and path must
// still be there, or the person cannot see where the push goes.
func TestPushDestinationShowsNoCredential(t *testing.T) {
	const secret = "s3cret-tok"
	for _, tc := range []struct{ raw, want string }{
		{"https://user:" + secret + "@github.com/o/r.git", "github.com/o/r.git"},
		{"https://" + secret + "@github.com/o/r.git", "github.com/o/r.git"},
		{"https://github.com/o/r.git?access_token=" + secret, "github.com/o/r.git"},
		{"https://github.com/o/r.git#" + secret, "github.com/o/r.git"},
		{"ssh://git:" + secret + "@host.example:2222/srv/r.git", "host.example:2222/srv/r.git"},
		{"git@" + "github.com:o/r.git", "github.com:o/r.git"},
		{"user:" + secret + "@host.example:o/r.git", "host.example:o/r.git"},
		{"https://u:" + secret + "@bad host/o/r.git?x=" + secret, "bad host/o/r.git"},
		{"/srv/git/r.git", "/srv/git/r.git"},
		{"file:///srv/git/r.git", "/srv/git/r.git"},
	} {
		got := pushDestination(tc.raw)
		if strings.Contains(got, secret) || strings.Contains(got, "user") {
			t.Errorf("pushDestination(%q) = %q, which shows a credential", tc.raw, got)
		}
		if got != tc.want {
			t.Errorf("pushDestination(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
