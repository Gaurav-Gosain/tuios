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

func TestProbeFindsAGrantedEngine(t *testing.T) {
	st := parseProbe("os=Darwin\n" +
		"agent=dev.tuios.remote-desktop|/Users/a/.local/share/tuios-rd/engine/relay-engine|20001|/Users/a/.local/state/tuios-rd/token|/Users/a/.local/state/tuios-rd/engine.log\n" +
		"agent=dev.srterm.relay-engine-tiles|/Users/a/engine/relay-engine|8081|/Users/a/token|/Users/a/engine.log\n" +
		"agent=broken|/x/relay-engine||/t|/l\n" +
		"sign_id=0D687A50DF95BCB6BD4C175420A2E29471068E73\n")
	if len(st.agents) != 2 || st.signID != "0D687A50DF95BCB6BD4C175420A2E29471068E73" {
		t.Fatalf("parseProbe = %+v", st)
	}
	a := pickAgent(st.agents)
	if a == nil || a.label != "dev.srterm.relay-engine-tiles" || a.port != "8081" || a.tokenFile != "/Users/a/token" {
		t.Fatalf("pickAgent = %+v, want the agent that is not ours", a)
	}
	c := &connectCfg{adopt: a}
	if got := c.enginePathOnHost(); got != "/Users/a/engine/relay-engine" {
		t.Fatalf("enginePathOnHost = %s", got)
	}
	if v := c.hostVars(); v != "BIN='/Users/a/engine/relay-engine' LOG='/Users/a/engine.log' TOK='/Users/a/token' PORTV='8081'\n" {
		t.Fatalf("hostVars = %q", v)
	}
	if pickAgent([]agentInfo{{label: launchdLabel}}) != nil {
		t.Fatal("pickAgent took our own agent")
	}
}

func TestShq(t *testing.T) {
	if got := shq("a'b c"); got != `'a'\''b c'` {
		t.Fatalf("shq = %s", got)
	}
}
