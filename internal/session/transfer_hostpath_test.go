package session

import (
	"runtime"
	"testing"
)

// TestIsHostPath: a path on a host is checked before this machine knows the
// host's system, so a Windows drive path passes as well as a Unix one. The
// host holds it to its own rules after that (#1275).
func TestIsHostPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/Users/me/notes.md", true},
		{"~", true},
		{"~/Downloads/tuios", true},
		{`C:\Users\me\Desktop\test.py`, true},
		{"C:/Users/me/Desktop/test.py", true},
		{`d:\data`, true},
		{"", false},
		{"notes.md", false},
		{"Downloads/tuios", false},
		{"C:", false},
		{"C:notes.md", false},
		{`1:\data`, false},
		{`::\data`, false},
	}
	for _, tc := range cases {
		if got := IsHostPath(tc.path); got != tc.want {
			t.Errorf("IsHostPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestTransferNameSplitsAWindowsPath: a copy from a Windows host is named
// after its file on any machine, not after the whole path, while a \ in a Unix
// name stays part of the name.
func TestTransferNameSplitsAWindowsPath(t *testing.T) {
	cases := []struct {
		path, want string
	}{
		{`C:\Users\me\Desktop\test.py`, "test.py"},
		{"C:/Users/me/Desktop/test.py", "test.py"},
		{`D:\projects\my folder\`, "my folder"},
		{"/Users/me/notes.md", "notes.md"},
		{"~/Downloads/tuios", "tuios"},
	}
	for _, tc := range cases {
		if got := transferName(tc.path); got != tc.want {
			t.Errorf("transferName(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
	if runtime.GOOS != "windows" {
		if got := transferName(`/tmp/a\b`); got != `a\b` {
			t.Errorf(`transferName("/tmp/a\b") = %q, want "a\b"`, got)
		}
	}
}

// TestCheckEndpointTakesAWindowsPathOnAHost: a source on a Windows host
// passes the path check, so the copy fails or goes on by the host's answer,
// not by this machine's system.
func TestCheckEndpointTakesAWindowsPathOnAHost(t *testing.T) {
	d, _ := startTestDaemon(t)
	for _, p := range []string{`C:\Users\me\Desktop\test.py`, "C:/Users/me/Desktop/test.py"} {
		e := &Endpoint{Host: "winbox", Path: p}
		verr := d.checkEndpoint("src", e)
		if verr != nil && verr.Hint != nil && verr.Hint.Param == "src.path" {
			t.Errorf("checkEndpoint refused %q on a host: %s", p, verr.Message)
		}
	}
	e := &Endpoint{Host: "winbox", Path: "Desktop/test.py"}
	if verr := d.checkEndpoint("src", e); verr == nil {
		t.Errorf("checkEndpoint took a relative path on a host")
	}
}
