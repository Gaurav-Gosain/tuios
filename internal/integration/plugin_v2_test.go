package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestOpenCodeV2WireCompatibility covers the changed client/event contract:
// data rather than properties, cumulative cost rather than per-message cost,
// session-scoped permission replies, and cleanup of pending Inbox requests.
// The installed JavaScript runs in Node and starts a real hook subprocess.
func TestOpenCodeV2WireCompatibility(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in tuios is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "calls"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "tuios")
	script := "#!/bin/sh\nf=$(mktemp \"$TUIOS_TEST_CALLS/call.XXXXXX\")\n{ printf '%s\\n' \"$*\"; cat; } >\"$f\"\nprintf '%s\\n' '{\"reply\":\"once\"}'\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	tg := mustTarget(t, OpenCode)
	for _, file := range tg.files(Env{}) {
		path := filepath.Join(dir, file.file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		data := file.format.(ownedFile).render(tg, fake)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	solid, err := os.ReadFile("testdata/solid.mjs")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "node_modules/solid-js/package.json"), `{"type":"module","exports":"./index.js"}`)
	writeFile(t, filepath.Join(dir, "node_modules/solid-js/index.js"), string(solid))
	driver, err := filepath.Abs("testdata/opencode-v2.mjs")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, driver, dir)
	cmd.Env = append(os.Environ(), "TUIOS_ENV=1", "TUIOS_TEST_CALLS="+filepath.Join(dir, "calls"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("V2 wire compatibility: %v\n%s", err, out)
	}
}
