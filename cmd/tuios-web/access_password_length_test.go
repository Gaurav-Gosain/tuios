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

// TestNoPasswordWarningWithoutAnOperatorPassword: the generated password, no
// password and --no-auth print no length warning and keep their own lines, and
// a refused start prints none. A short password file on the same path does
// print it, so the cases with no warning reached the code that would print it.
func TestNoPasswordWarningWithoutAnOperatorPassword(t *testing.T) {
	writePasswordFile := func(t *testing.T, pw string) string {
		t.Helper()
		file := filepath.Join(t.TempDir(), "web-password")
		if err := os.WriteFile(file, []byte(pw+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return file
	}

	t.Run("short password file is warned about", func(t *testing.T) {
		t.Setenv(webPasswordEnv, "")
		f := webAccessFlags{host: "127.0.0.1", port: "7681", passwordFile: writePasswordFile(t, "QQZZQ")}
		var out bytes.Buffer
		if _, err := planWebAccess(&out, f); err != nil {
			t.Fatalf("plan: %v", err)
		}
		if !strings.Contains(out.String(), "Warning: the password is 5 characters long.") {
			t.Fatalf("no length warning for a 5 character password; output is %q", out.String())
		}
	})

	quiet := []struct {
		name string
		f    webAccessFlags
		keep string
	}{
		{name: "random password", f: webAccessFlags{host: "127.0.0.1", port: "7681", randomPassword: true}},
		{name: "loopback with no password", f: webAccessFlags{host: "127.0.0.1", port: "7681"}, keep: "other users on this machine can connect"},
		{name: "no-auth on loopback", f: webAccessFlags{host: "127.0.0.1", port: "7681", noAuth: true}, keep: "does not ask for a password"},
	}
	for _, tc := range quiet {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(webPasswordEnv, "")
			var out bytes.Buffer
			if _, err := planWebAccess(&out, tc.f); err != nil {
				t.Fatalf("plan: %v", err)
			}
			got := out.String()
			if strings.Contains(got, "characters long") {
				t.Fatalf("a length warning with no operator password; output is %q", got)
			}
			if tc.keep != "" && !strings.Contains(got, tc.keep) {
				t.Fatalf("the line %q is gone; output is %q", tc.keep, got)
			}
		})
	}

	t.Run("refused start prints no warning", func(t *testing.T) {
		t.Setenv(webPasswordEnv, "")
		f := webAccessFlags{host: "127.0.0.1", port: "7681", passwordFile: writePasswordFile(t, "QQZZQ"), allowHosts: []string{"*"}}
		var out bytes.Buffer
		if _, err := planWebAccess(&out, f); err == nil {
			t.Fatal("--allow-host * was accepted, want an error")
		}
		if strings.Contains(out.String(), "characters long") {
			t.Fatalf("a refused start printed the length warning; output is %q", out.String())
		}
	})
}
