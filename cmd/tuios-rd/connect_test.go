//go:build linux || darwin

package main

import "testing"

func TestParseProbe(t *testing.T) {
	st := parseProbe("os=Darwin\narch=arm64\nengine=yes\ntoken=abc\nport=23456\nneed_screen=yes\ntail=Screen capture permission is missing \nsetsid=yes\n")
	if st.os != "Darwin" || st.arch != "arm64" || !st.engine || st.token != "abc" || st.port != "23456" || !st.needScreen || st.needInput || st.up || !st.setsid {
		t.Fatalf("parseProbe = %+v", st)
	}
	if st.logTail != "Screen capture permission is missing" {
		t.Fatalf("logTail = %q", st.logTail)
	}
	if got := (&connectCfg{service: "auto"}).serviceKind(st); got != "launchd" {
		t.Fatalf("service on macOS = %s, want launchd", got)
	}
	if got := (&connectCfg{service: "auto"}).serviceKind(hostState{os: "Linux", systemd: true}); got != "systemd" {
		t.Fatalf("service on Linux with systemd = %s", got)
	}
	if got := (&connectCfg{service: "auto"}).serviceKind(hostState{os: "Linux"}); got != "process" {
		t.Fatalf("service on Linux without systemd = %s", got)
	}
}

func TestShq(t *testing.T) {
	if got := shq("a'b c"); got != `'a'\''b c'` {
		t.Fatalf("shq = %s", got)
	}
}
