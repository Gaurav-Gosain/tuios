package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

func TestLinkPolicyResolvesDefaultThenStarThenPeer(t *testing.T) {
	const src = `
[hosts."*"]
allow = ["list", "mail"]
hold_mail = true

[hosts.laptop]
addr = "laptop"
allow = ["list", "write", "respond"]
hosted_grace = "2m"

[hosts.desk]
allow = []
`
	var cfg UserConfig
	if err := toml.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	none := LinkPolicyFor(nil, "laptop")
	if !slices.Equal(none.Allow, DefaultLinkAllow) || none.HoldMail || none.HostedGrace != DefaultHostedGrace || none.Source != "" {
		t.Errorf("with no table the policy is %+v, want the built-in default", none)
	}
	if none.Allows(LinkAllowRespond) {
		t.Error("the built-in default allows respond")
	}

	anon := LinkPolicyFor(cfg.Hosts, "")
	if !slices.Equal(anon.Allow, []string{"list", "mail"}) || !anon.HoldMail || anon.Source != `hosts."*"` {
		t.Errorf("a link with no name got %+v, want [hosts.\"*\"]", anon)
	}

	laptop := LinkPolicyFor(cfg.Hosts, "LAPTOP")
	if !slices.Equal(laptop.Allow, []string{"list", "write", "respond"}) {
		t.Errorf("laptop may %v", laptop.Allow)
	}
	if !laptop.HoldMail {
		t.Error("laptop did not inherit hold_mail from [hosts.\"*\"]")
	}
	if laptop.HostedGrace != 2*time.Minute || laptop.Source != "hosts.laptop" {
		t.Errorf("laptop grace %v from %s", laptop.HostedGrace, laptop.Source)
	}

	desk := LinkPolicyFor(cfg.Hosts, "desk")
	if desk.Allow == nil || len(desk.Allow) != 0 {
		t.Errorf("allow = [] resolved to %v, want nothing allowed", desk.Allow)
	}
}

func TestHostedGraceParses(t *testing.T) {
	for in, want := range map[string]time.Duration{"0": 0, "10m": 10 * time.Minute, "90s": 90 * time.Second, "1000h": MaxHostedGrace} {
		got, err := ParseHostedGrace(in)
		if err != nil || got != want {
			t.Errorf("%q parsed to %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"soon", "-1m", "10"} {
		if _, err := ParseHostedGrace(in); err == nil {
			t.Errorf("%q parsed", in)
		}
	}
}

func TestValidateWarnsAboutAnUnknownCapability(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Hosts = map[string]HostConfig{"laptop": {Allow: []string{"lsit"}, HostedGrace: "soon"}}
	res := ValidateConfig(cfg)
	var keys []string
	for _, w := range res.Warnings {
		if w.Field == "hosts.laptop" {
			keys = append(keys, w.Key)
		}
	}
	if !slices.Contains(keys, "allow") || !slices.Contains(keys, "hosted_grace") {
		t.Errorf("warnings for hosts.laptop are %v, want allow and hosted_grace", keys)
	}
}

// TestRewritingAHostKeepsItsLinkPolicy: `tuios hosts add` on a known name
// rewrites the table, and must not drop what the machine may do here.
func TestRewritingAHostKeepsItsLinkPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	hold := true
	if err := SetHostInFile(path, "build", HostConfig{Addr: "a", Allow: []string{"list"}, HoldMail: &hold, HostedGrace: "5m"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{`allow = ["list"]`, "hold_mail = true", `hosted_grace = "5m"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the rewritten table lacks %s:\n%s", want, data)
		}
	}
	hosts, err := HostsInFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if h := hosts["build"]; h.HoldMail == nil || !*h.HoldMail || !slices.Equal(h.Allow, []string{"list"}) {
		t.Errorf("read back %+v", h)
	}
}
