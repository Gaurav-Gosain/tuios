package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAShortOperatorPasswordIsWarnedAbout: a password the operator chose, from
// a file or from the environment, gets one start line when it is shorter than
// 12 characters, counted in runes. The start goes on, and nothing printed
// carries the password. The fixtures use only Q, Z and the rune ж, which the
// warning text never contains, so a leak cannot hide inside the warning.
func TestAShortOperatorPasswordIsWarnedAbout(t *testing.T) {
	writePasswordFile := func(t *testing.T, pw string) string {
		t.Helper()
		file := filepath.Join(t.TempDir(), "web-password")
		if err := os.WriteFile(file, []byte(pw+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return file
	}
	linesWithPrefix := func(out, prefix string) int {
		n := 0
		for line := range strings.SplitSeq(out, "\n") {
			if strings.HasPrefix(line, prefix) {
				n++
			}
		}
		return n
	}

	cases := []struct {
		name    string
		text    string
		fromEnv bool
		runes   int
		warn    bool
	}{
		{name: "file, 1 character", text: "Q", runes: 1, warn: true},
		{name: "file, 11 characters", text: "QQZZQQZZQQZ", runes: 11, warn: true},
		{name: "file, 12 characters", text: "QZQZQZQZQZQZ", runes: 12, warn: false},
		{name: "file, 11 runes in 22 bytes", text: strings.Repeat("ж", 11), runes: 11, warn: true},
		{name: "env, short", text: "ZZQQZ", fromEnv: true, runes: 5, warn: true},
		{name: "env, 12 or more", text: "ZQZQZQZQZQZQZ", fromEnv: true, runes: 13, warn: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := webAccessFlags{host: "127.0.0.1", port: "7681"}
			if tc.fromEnv {
				t.Setenv(webPasswordEnv, tc.text)
			} else {
				t.Setenv(webPasswordEnv, "")
				f.passwordFile = writePasswordFile(t, tc.text)
			}
			var out bytes.Buffer
			a, err := planWebAccess(&out, f)
			if err != nil {
				t.Fatalf("plan: %v, want a plan and no error", err)
			}
			if a == nil {
				t.Fatal("plan is nil, want a plan")
			}
			got := out.String()
			if tc.warn {
				prefix := fmt.Sprintf("Warning: the password is %d characters long.", tc.runes)
				if n := linesWithPrefix(got, prefix); n != 1 {
					t.Fatalf("%d lines begin %q, want exactly one; output is %q", n, prefix, got)
				}
			} else if strings.Contains(got, "characters long") {
				t.Fatalf("a password of %d characters was warned about; output is %q", tc.runes, got)
			}
			if strings.Contains(got, tc.text) {
				t.Fatalf("the output carries the password; output is %q", got)
			}
		})
	}
}
