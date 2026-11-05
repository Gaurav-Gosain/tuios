package tuie2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// tuios plugins install NAME from the assets of the running release, against
// a fake GitHub API that serves the archive scripts/plugin-assets.sh makes
// from this tree. A stamped build stands for a release.
//
// The negative controls are in NEGATIVE_CONTROLS.md, "Plugins from the
// release".

// fakeReleases serves releases: tag to asset name to bytes. checksums.txt
// is made from the assets unless a test set it.
type fakeReleases struct {
	mu   sync.Mutex
	tags map[string]map[string][]byte
}

func (f *fakeReleases) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if tag, ok := strings.CutPrefix(r.URL.Path, "/repos/Gaurav-Gosain/tuios/releases/tags/"); ok {
		assets, ok := f.tags[tag]
		if !ok {
			http.NotFound(w, r)
			return
		}
		type asset struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		}
		rel := struct {
			Tag    string  `json:"tag_name"`
			Assets []asset `json:"assets"`
		}{Tag: tag}
		for name := range assets {
			rel.Assets = append(rel.Assets, asset{name, "http://" + r.Host + "/dl/" + tag + "/" + name})
		}
		_ = json.NewEncoder(w).Encode(rel)
		return
	}
	if rest, ok := strings.CutPrefix(r.URL.Path, "/dl/"); ok {
		tag, name, _ := strings.Cut(rest, "/")
		if data, ok := f.tags[tag][name]; ok {
			_, _ = w.Write(data)
			return
		}
	}
	http.NotFound(w, r)
}

// add publishes a release with the plugin archive of this tree, changed by
// edit, and its checksums.
func (f *fakeReleases) add(t *testing.T, tag string, edit func(dir string), sums func(name string, data []byte) string) {
	t.Helper()
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	src := t.TempDir()
	if out, err := exec.Command("cp", "-r", filepath.Join(root, "plugins"), filepath.Join(root, "scripts"), src).CombinedOutput(); err != nil {
		t.Fatalf("copy: %v\n%s", err, out)
	}
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", src}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if edit != nil {
		edit(filepath.Join(src, "plugins"))
	}
	run("init", "-q")
	run("add", "plugins")
	out := t.TempDir()
	cmd := exec.Command(filepath.Join(src, "scripts", "plugin-assets.sh"), tag, out)
	cmd.Dir = src
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("plugin-assets.sh: %v\n%s", err, b)
	}
	name := "tuios-plugins_" + strings.TrimPrefix(tag, "v") + ".tar.gz"
	data, err := os.ReadFile(filepath.Join(out, name))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	line := hex.EncodeToString(sum[:])
	if sums != nil {
		line = sums(name, data)
	}
	f.mu.Lock()
	if f.tags == nil {
		f.tags = map[string]map[string][]byte{}
	}
	f.tags[tag] = map[string][]byte{name: data, "checksums.txt": []byte(line + "  " + name + "\n")}
	f.mu.Unlock()
}

// stampedTuios builds tuios as release version v.
func stampedTuios(t *testing.T, v string) string {
	t.Helper()
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	bin := filepath.Join(t.TempDir(), "tuios")
	cmd := exec.Command("go", "build", "-ldflags", "-X main.version="+v, "-o", bin, "./cmd/tuios")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build tuios %s: %v\n%s", v, err, out)
	}
	return bin
}

func TestPluginsFromRelease(t *testing.T) {
	var rel fakeReleases
	srv := httptest.NewServer(&rel)
	defer srv.Close()
	t.Setenv("TUIOS_RELEASE_API", srv.URL)
	rel.add(t, "v0.99.0", nil, nil)
	rel.add(t, "v0.99.1", func(dir string) {
		_ = os.WriteFile(filepath.Join(dir, "pr-status", "README.md"), []byte("new\n"), 0o644)
	}, nil)
	rel.add(t, "v0.99.2", nil, func(string, []byte) string { return strings.Repeat("0", 64) })

	old, next, bad := stampedTuios(t, "0.99.0"), stampedTuios(t, "0.99.1"), stampedTuios(t, "0.99.2")
	base := t.TempDir()
	writeConfig(t, base, "")
	killDaemon(t, base)
	cli := func(bin string, args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = workDirIn(t, base)
		cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
		for _, key := range xdgKeys {
			cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
		}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	lockPath := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "plugins.lock")
	lock := func() string { b, _ := os.ReadFile(lockPath); return string(b) }

	// A build that no release made names the folder form instead.
	if out, err := tuiosCLI(t, base, "plugins", "install", "pr-status", "--yes"); err == nil || !strings.Contains(out, "plugins install Gaurav-Gosain/tuios/plugins/NAME") {
		t.Errorf("install by name on a dev build: %v\n%s", err, out)
	}
	if out, err := cli(old, "new", "rel", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	// A wrong checksum installs nothing.
	if out, err := cli(bad, "plugins", "install", "pr-status", "--yes"); err == nil || !strings.Contains(strings.ToLower(out), "checksum") || strings.Contains(lock(), "pr-status") {
		t.Fatalf("install with a wrong checksum: %v\n%s\n%s", err, out, lock())
	}
	out, err := cli(old, "plugins", "install", "pr-status", "--yes")
	if err != nil || !strings.Contains(out, "the tuios release, plugin pr-status @ v0.99.0") {
		t.Fatalf("install by name: %v\n%s", err, out)
	}
	if !strings.Contains(lock(), `source = 'release:pr-status'`) || !strings.Contains(lock(), `ref = 'v0.99.0'`) {
		t.Fatalf("plugins.lock:\n%s", lock())
	}
	if out, _ := cli(old, "plugins", "info", "tuios.pr-status"); !strings.Contains(out, "state     on") {
		t.Errorf("info after the install:\n%s", out)
	}
	if out, err := cli(old, "plugins", "install", "pr-status", "--yes"); err == nil || !strings.Contains(out, "tuios plugins update tuios.pr-status") {
		t.Errorf("a second install: %v\n%s", err, out)
	}

	// The same tuios has nothing newer. A newer one moves the plugin.
	if out, err := cli(old, "plugins", "update", "--yes"); err != nil || !strings.Contains(out, "up to date at v0.99.0") {
		t.Errorf("update with the same tuios: %v\n%s", err, out)
	}
	if out, err := cli(next, "plugins", "update", "--yes"); err != nil || !strings.Contains(out, "Updated tuios.pr-status") || !strings.Contains(lock(), `ref = 'v0.99.1'`) {
		t.Fatalf("update with a newer tuios: %v\n%s\n%s", err, out, lock())
	}

	// Another machine gets the pinned files, or nothing.
	other := t.TempDir()
	writeConfig(t, other, "")
	if err := os.MkdirAll(filepath.Join(xdgDir(other, "XDG_CONFIG_HOME"), "tuios"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdgDir(other, "XDG_CONFIG_HOME"), "tuios", "plugins.lock"), []byte(lock()), 0o600); err != nil {
		t.Fatal(err)
	}
	base = other
	if out, err := cli(next, "plugins", "install", "--yes"); err != nil || !strings.Contains(out, "Installed tuios.pr-status") {
		t.Fatalf("install from plugins.lock: %v\n%s", err, out)
	}
	rel.add(t, "v0.99.1", func(dir string) {
		_ = os.WriteFile(filepath.Join(dir, "pr-status", "README.md"), []byte("swapped\n"), 0o644)
	}, nil)
	third := t.TempDir()
	writeConfig(t, third, "")
	_ = os.MkdirAll(filepath.Join(xdgDir(third, "XDG_CONFIG_HOME"), "tuios"), 0o700)
	_ = os.WriteFile(filepath.Join(xdgDir(third, "XDG_CONFIG_HOME"), "tuios", "plugins.lock"), []byte(lock()), 0o600)
	base = third
	if out, err := cli(next, "plugins", "install", "--yes"); err == nil || !strings.Contains(out, "different from the ones plugins.lock pins") {
		t.Errorf("install from plugins.lock after the asset changed: %v\n%s", err, out)
	}
}

// On a release build, a name of the release installs the release's plugin,
// also in a folder that holds a folder of that name. That folder installs as
// ./NAME, and uninstall by the name there removes it. A dev build has no
// release to take, so there the name keeps meaning the folder.
//
// The negative controls are in NEGATIVE_CONTROLS.md, "The sketch plugin",
// "The plan names the sketch program, and a release name is the release".
func TestPluginsReleaseNameBeatsAFolder(t *testing.T) {
	var rel fakeReleases
	srv := httptest.NewServer(&rel)
	defer srv.Close()
	t.Setenv("TUIOS_RELEASE_API", srv.URL)
	rel.add(t, "v0.99.0", nil, nil)
	stamped := stampedTuios(t, "0.99.0")
	base := t.TempDir()
	writeConfig(t, base, "")
	killDaemon(t, base)

	// A folder pr-status with the release plugin's id, whose build leaves a
	// file outside it.
	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "built")
	if err := os.Mkdir(filepath.Join(dir, "pr-status"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, filepath.Join(dir, "pr-status"), map[string]string{"tuios-plugin.toml": "api = 1\nid = \"tuios.pr-status\"\nname = \"PR status\"\nversion = \"0.0.1\"\n\n[[build]]\ncommand = [\"touch\", " + strconv.Quote(marker) + "]\n"})
	cli := func(args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(stamped, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
		for _, key := range xdgKeys {
			cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
		}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	lockPath := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "plugins.lock")
	lock := func() string { b, _ := os.ReadFile(lockPath); return string(b) }
	fromFolder := func() bool {
		for line := range strings.Lines(lock()) {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "source = 'path:") && strings.HasSuffix(line, "/pr-status'") {
				return true
			}
		}
		return false
	}
	built := func() bool { _, err := os.Stat(marker); return err == nil }

	if out, err := cli("new", "rel", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	out, err := cli("plugins", "install", "pr-status", "--yes")
	if err != nil || !strings.Contains(lock(), `source = 'release:pr-status'`) {
		t.Errorf("install pr-status in a folder that holds pr-status/: %v\n%s\n%s", err, out, lock())
	}
	if built() {
		t.Errorf("install pr-status built the folder pr-status/:\n%s", out)
	}

	// The folder, given as ./pr-status, replaces the release's plugin.
	if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	out, err = cli("plugins", "install", "./pr-status", "--yes", "--replace")
	if err != nil || !fromFolder() || !built() {
		t.Errorf("install ./pr-status: %v, built %v\n%s\n%s", err, built(), out, lock())
	}
	out, err = cli("plugins", "uninstall", "pr-status", "--yes")
	if err != nil || fromFolder() {
		t.Errorf("uninstall pr-status in that folder: %v\n%s\n%s", err, out, lock())
	}

	// A dev build installs the folder, as before.
	out, err = tuiosCLIIn(t, base, dir, "plugins", "install", "pr-status", "--yes")
	if err != nil || !fromFolder() {
		t.Errorf("install pr-status on a dev build: %v\n%s\n%s", err, out, lock())
	}
}
