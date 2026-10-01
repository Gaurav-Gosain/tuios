package courier

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPathsHome(t *testing.T) {
	t.Setenv(HomeEnv, "/tmp/courier-a")
	p := DefaultPaths()
	if p.ConfigFile() != filepath.Join("/tmp/courier-a", "courier.toml") ||
		p.KeyFile() != filepath.Join("/tmp/courier-a", "identity.key") ||
		p.StateDir != filepath.Join("/tmp/courier-a", "state") {
		t.Fatalf("paths under %s: %+v", HomeEnv, p)
	}
	t.Setenv(HomeEnv, "")
	p = DefaultPaths()
	if !strings.HasSuffix(p.ConfigDir, filepath.Join("tuios", "courier")) || !strings.HasSuffix(p.StateDir, filepath.Join("tuios", "courier")) {
		t.Fatalf("XDG paths: %+v", p)
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "courier.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfig(t *testing.T) {
	gg := mustKeys(t).Identity()
	path := writeConfig(t, `
name = "ghaith"
relay = "https://courier.example.internal/"

[peers.gg]
identity = "`+gg.String()+`"
release = "auto"

[peers."zain.k"]
identity = "`+mustKeys(t).Identity().String()+`"
`)
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.Name != "ghaith" || c.Relay != "https://courier.example.internal/" || c.ReplyRelease != ReleaseAuto {
		t.Fatalf("config: %+v", c)
	}
	if p := c.Peers["gg"]; !p.Identity.Equal(gg) || p.Release != ReleaseAuto {
		t.Fatalf("peer gg: %+v", p)
	}
	if c.Peers["zain.k"].Release != ReleaseHold {
		t.Fatal("a peer with no release setting must default to hold")
	}
	if name, ok := c.Lookup(gg); !ok || name != "gg" {
		t.Fatalf("Lookup(gg) = %q %v", name, ok)
	}
	if _, ok := c.Lookup(mustKeys(t).Identity()); ok {
		t.Fatal("Lookup found an identity nobody added")
	}
}

func TestLoadConfigRejects(t *testing.T) {
	a, b := mustKeys(t).Identity().String(), mustKeys(t).Identity().String()
	for name, body := range map[string]string{
		"no relay":        `name = "g"`,
		"http relay":      "name = \"g\"\nrelay = \"http://courier.example/\"",
		"relay no host":   "name = \"g\"\nrelay = \"https:///x\"",
		"ftp relay":       "name = \"g\"\nrelay = \"ftp://x/\"",
		"bad name":        "name = \"g h\"\nrelay = \"https://x/\"",
		"no name":         "relay = \"https://x/\"",
		"unknown key":     "name = \"g\"\nrelay = \"https://x/\"\ncolour = \"red\"",
		"bad release":     "name = \"g\"\nrelay = \"https://x/\"\n[peers.gg]\nidentity = \"" + a + "\"\nrelease = \"sometimes\"",
		"bad reply":       "name = \"g\"\nrelay = \"https://x/\"\nreply_release = \"never\"",
		"bad peer name":   "name = \"g\"\nrelay = \"https://x/\"\n[peers.\"g:g\"]\nidentity = \"" + a + "\"",
		"bad identity":    "name = \"g\"\nrelay = \"https://x/\"\n[peers.gg]\nidentity = \"tc1.nope\"",
		"same key twice":  "name = \"g\"\nrelay = \"https://x/\"\n[peers.gg]\nidentity = \"" + a + "\"\n[peers.gg2]\nidentity = \"" + a + "\"",
		"not toml":        "this is = = not toml",
		"peer missing id": "name = \"g\"\nrelay = \"https://x/\"\n[peers.gg]\nrelease = \"hold\"",
	} {
		if _, err := LoadConfig(writeConfig(t, body)); err == nil {
			t.Errorf("%s: LoadConfig accepted it", name)
		}
	}
	_ = b
	for _, ok := range []string{"http://127.0.0.1:8080/", "http://localhost:9/", "http://[::1]:7/"} {
		if _, err := LoadConfig(writeConfig(t, "name = \"g\"\nrelay = \""+ok+"\"")); err != nil {
			t.Errorf("a loopback http relay %s was refused: %v", ok, err)
		}
	}
}

func TestLoadConfigMissingNamesTheFix(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "courier.toml"))
	if err == nil || !strings.Contains(err.Error(), "tuios-courier init") {
		t.Fatalf("a missing config: %v, want a pointer to tuios-courier init", err)
	}
}

func TestConfigPeersPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "courier.toml")
	c := &Config{Name: "ghaith", Relay: "https://x.example/"}
	gg := mustKeys(t).Identity()
	if err := c.AddPeer("gg", gg, ReleaseHold); err != nil {
		t.Fatal(err)
	}
	if err := c.AddPeer("gg", mustKeys(t).Identity(), ReleaseHold); err == nil {
		t.Fatal("AddPeer replaced an existing name")
	}
	if err := c.AddPeer("other", gg, ReleaseHold); err == nil {
		t.Fatal("AddPeer took the same identity under a second name")
	}
	if err := c.AddPeer("bad name", mustKeys(t).Identity(), ReleaseHold); err == nil {
		t.Fatal("AddPeer took a bad name")
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
			t.Fatalf("config mode %v, want 0600", st.Mode().Perm())
		}
	}
	back, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Peers["gg"].Identity.Equal(gg) {
		t.Fatal("the saved peer did not come back")
	}
	if err := back.RemovePeer("gg"); err != nil {
		t.Fatal(err)
	}
	if err := back.RemovePeer("gg"); err == nil {
		t.Fatal("RemovePeer of a missing name succeeded")
	}
}
