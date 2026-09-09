package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What `tuios hosts add` and `tuios hosts remove` do to the file.
//
// The rule under test throughout is that the command edits one table and
// nothing else. A user who hand-wrote this file gets it back with their
// comments, their spacing and their other sections exactly as they left them.

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write the config: %v", err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatalf("read the config: %v", err)
	}
	return string(data)
}

func TestAddingAHostKeepsEverythingElseInTheFile(t *testing.T) {
	body := `# My own notes about this file.
[appearance]
# the border I like
border_style = "double"

[hosts.build]
addr = "gaurav@buildbox"

[startup]
tiled = true
`
	path := writeTemp(t, body)
	if err := SetHostInFile(path, "lab", HostConfig{Addr: "lab-01"}); err != nil {
		t.Fatalf("add a host: %v", err)
	}
	got := readFile(t, path)

	for _, want := range []string{
		"# My own notes about this file.",
		"# the border I like",
		`border_style = "double"`,
		"[hosts.build]",
		`addr = "gaurav@buildbox"`,
		"[startup]",
		"tiled = true",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ASSERTION: adding a host lost %q from the file:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "[hosts.lab]\naddr = \"lab-01\"") {
		t.Errorf("ASSERTION: the new host is not in the file:\n%s", got)
	}
}

func TestAddingAHostThatExistsReplacesOnlyThatTable(t *testing.T) {
	body := `[hosts.build]
addr = "old-address"
command = "/opt/tuios"

[hosts.lab]
addr = "lab-01"
`
	path := writeTemp(t, body)
	if err := SetHostInFile(path, "build", HostConfig{Addr: "new-address"}); err != nil {
		t.Fatalf("replace a host: %v", err)
	}
	got := readFile(t, path)

	if strings.Contains(got, "old-address") {
		t.Errorf("ASSERTION: the old address survived the replacement:\n%s", got)
	}
	if strings.Contains(got, "/opt/tuios") {
		t.Errorf("ASSERTION: a value the replacement did not set was kept:\n%s", got)
	}
	if !strings.Contains(got, `addr = "new-address"`) {
		t.Errorf("ASSERTION: the new address is not in the file:\n%s", got)
	}
	if !strings.Contains(got, "[hosts.lab]\naddr = \"lab-01\"") {
		t.Errorf("ASSERTION: replacing one host changed another:\n%s", got)
	}
}

func TestRemovingAHostLeavesTheRestOfTheFile(t *testing.T) {
	body := `[appearance]
theme = "dracula"

[hosts.build]
addr = "gaurav@buildbox"
connect_timeout = 5

[hosts.lab]
addr = "lab-01"

[startup]
tiled = true
`
	path := writeTemp(t, body)
	removed, err := RemoveHostFromFile(path, "build")
	if err != nil {
		t.Fatalf("remove a host: %v", err)
	}
	if !removed {
		t.Fatal("ASSERTION: removing a host that is there reported that it was not")
	}
	got := readFile(t, path)
	if strings.Contains(got, "buildbox") || strings.Contains(got, "[hosts.build]") {
		t.Errorf("ASSERTION: the removed host is still in the file:\n%s", got)
	}
	if strings.Contains(got, "connect_timeout") {
		t.Errorf("ASSERTION: a value under the removed table was left behind:\n%s", got)
	}
	for _, want := range []string{`theme = "dracula"`, "[hosts.lab]", "tiled = true"} {
		if !strings.Contains(got, want) {
			t.Errorf("ASSERTION: removing a host lost %q:\n%s", want, got)
		}
	}
}

func TestRemovingAHostThatIsNotThereSaysSo(t *testing.T) {
	path := writeTemp(t, "[hosts.build]\naddr = \"buildbox\"\n")
	removed, err := RemoveHostFromFile(path, "lab")
	if err != nil {
		t.Fatalf("remove a host: %v", err)
	}
	if removed {
		t.Error("ASSERTION: removing a host that is not there reported a removal")
	}
	if !strings.Contains(readFile(t, path), "[hosts.build]") {
		t.Error("ASSERTION: a removal that found nothing changed the file")
	}
}

func TestAddingTheFirstHostToAFileThatDoesNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	if err := SetHostInFile(path, "build", HostConfig{Addr: "buildbox"}); err != nil {
		t.Fatalf("add the first host: %v", err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "[hosts.build]") {
		t.Errorf("ASSERTION: the first host was not written:\n%s", got)
	}
	// The file has to be a config the loader accepts, or the daemon that reads
	// it next would refuse everything in it.
	cfg, err := ParseUserConfig([]byte(got))
	if err != nil {
		t.Fatalf("ASSERTION: the file this wrote does not parse: %v\n%s", err, got)
	}
	if cfg.Hosts["build"].Addr != "buildbox" {
		t.Errorf("ASSERTION: the parsed config does not hold the host, got %+v", cfg.Hosts)
	}
}

func TestAddedHostRoundTripsThroughTheParser(t *testing.T) {
	path := writeTemp(t, "")
	entry := HostConfig{
		Addr:           "gaurav@buildbox",
		Command:        "/home/gaurav/.local/bin/tuios",
		ConnectTimeout: 7,
		SSHOptions:     []string{"-J", "bastion", "-o", "StrictHostKeyChecking=yes"},
	}
	if err := SetHostInFile(path, "build", entry); err != nil {
		t.Fatalf("add a host: %v", err)
	}
	cfg, err := ParseUserConfig([]byte(readFile(t, path)))
	if err != nil {
		t.Fatalf("ASSERTION: the file does not parse: %v", err)
	}
	got := cfg.Hosts["build"]
	if got.Addr != entry.Addr || got.Command != entry.Command || got.ConnectTimeout != entry.ConnectTimeout {
		t.Errorf("ASSERTION: the host did not survive the round trip, got %+v want %+v", got, entry)
	}
	if strings.Join(got.SSHOptions, " ") != strings.Join(entry.SSHOptions, " ") {
		t.Errorf("ASSERTION: the ssh options did not survive the round trip, got %v", got.SSHOptions)
	}
}

// A host name may hold a dot, and an unquoted dot in a table header would make
// two tables out of one name. This is the case that silently writes the wrong
// thing if the key is not quoted.
func TestAHostNameWithADotIsQuoted(t *testing.T) {
	path := writeTemp(t, "")
	if err := SetHostInFile(path, "lab.local", HostConfig{Addr: "lab-01"}); err != nil {
		t.Fatalf("add a host: %v", err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, `[hosts."lab.local"]`) {
		t.Errorf("ASSERTION: a dotted name was written unquoted:\n%s", got)
	}
	cfg, err := ParseUserConfig([]byte(got))
	if err != nil {
		t.Fatalf("ASSERTION: the file does not parse: %v", err)
	}
	if cfg.Hosts["lab.local"].Addr != "lab-01" {
		t.Errorf("ASSERTION: the dotted name did not parse back as one host, got %+v", cfg.Hosts)
	}

	removed, err := RemoveHostFromFile(path, "lab.local")
	if err != nil || !removed {
		t.Fatalf("ASSERTION: a dotted name could not be removed, removed=%v err=%v", removed, err)
	}
	if strings.Contains(readFile(t, path), "lab-01") {
		t.Error("ASSERTION: removing a dotted name left the table behind")
	}
}

func TestAHostNeedsANameAndAnAddress(t *testing.T) {
	path := writeTemp(t, "")
	if err := SetHostInFile(path, "build", HostConfig{Addr: "  "}); err == nil {
		t.Error("ASSERTION: a host with no address was written")
	}
	if err := SetHostInFile(path, "local", HostConfig{Addr: "somewhere"}); err == nil {
		t.Error("ASSERTION: the reserved name 'local' was written as a host")
	}
	if err := SetHostInFile(path, "two words", HostConfig{Addr: "somewhere"}); err == nil {
		t.Error("ASSERTION: a name with a space was written as a host")
	}
	if body := readFile(t, path); strings.TrimSpace(body) != "" {
		t.Errorf("ASSERTION: a refused host still changed the file:\n%s", body)
	}
}

func TestHostsInFileReadsTheTable(t *testing.T) {
	path := writeTemp(t, "[hosts.build]\naddr = \"buildbox\"\n\n[hosts.lab]\naddr = \"lab-01\"\n")
	hosts, err := HostsInFile(path)
	if err != nil {
		t.Fatalf("read the hosts: %v", err)
	}
	if len(hosts) != 2 || hosts["build"].Addr != "buildbox" || hosts["lab"].Addr != "lab-01" {
		t.Errorf("ASSERTION: the table was not read, got %+v", hosts)
	}
}

func TestHostsInFileOnAMissingFileIsEmpty(t *testing.T) {
	hosts, err := HostsInFile(filepath.Join(t.TempDir(), "nothing.toml"))
	if err != nil {
		t.Fatalf("read a missing config: %v", err)
	}
	if len(hosts) != 0 {
		t.Errorf("ASSERTION: a missing file reported hosts, got %+v", hosts)
	}
}

// The block scan has to stop at the next table header of any kind, including an
// array of tables, or a removal would swallow whatever follows.
func TestRemovingAHostStopsAtAnArrayOfTables(t *testing.T) {
	body := `[hosts.build]
addr = "buildbox"

[[keybindings.custom]]
key = "x"
`
	path := writeTemp(t, body)
	if _, err := RemoveHostFromFile(path, "build"); err != nil {
		t.Fatalf("remove a host: %v", err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "[[keybindings.custom]]") || !strings.Contains(got, `key = "x"`) {
		t.Errorf("ASSERTION: the removal swallowed the array of tables that followed:\n%s", got)
	}
}
