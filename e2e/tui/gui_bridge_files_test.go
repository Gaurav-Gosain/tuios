package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The explorer's half of the bridge (wave 5): a verb the renderer sends with
// a host runs on that machine's daemon, through this machine's daemon and its
// link, so the GUI lists, previews and copies another machine's files with
// the same verbs it uses here. A verb that answers for the person never goes
// to another machine with this client's nonce.
//
// What would pass a weaker test and fail this one: a bridge that ignores host
// (the listing is this machine's folder, or "does not exist"), and a bridge
// that sends the nonce along (respond on build would get one).

// TestGUIBridgeVerbOnAHost lists a folder that exists only on build, through
// the bridge, then copies a file from build with a transfer this machine's
// daemon runs, sees a pane held for build named as build's, and refuses a
// person verb with a host.
func TestGUIBridgeVerbOnAHost(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	if out, err := tuiosCLI(t, remote, "new", "-d", "far"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	env := []string{"TUIOS_SSH=" + ssh}
	killDaemon(t, base)
	if out, err := tuiosCLIEnv(t, base, env, "start-server"); err != nil {
		t.Fatalf("start-server: %v\n%s", err, out)
	}
	waitForHostListing(t, base, func(s string) bool { return strings.Contains(s, "│ up ") }, "the link to build comes up")

	farDir := filepath.Join(remote, "only-on-build")
	if err := os.MkdirAll(farDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(farDir, "report.txt"), []byte("from build\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := startBridgeWith(t, base, bridgeOpts{args: []string{"--session", "here"}, env: env, cols: 100, rows: 30, keepDaemon: true, name: "here"})

	res := b.verbOn("build", "file-list", map[string]any{"dir": farDir})
	if !res.OK {
		t.Fatalf("ASSERTION: file-list on build through the bridge failed: %s %s", res.Code, res.Error)
	}
	var list struct {
		Home    string `json:"home"`
		Entries []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"entries"`
	}
	_ = json.Unmarshal(res.Result, &list)
	if len(list.Entries) != 1 || list.Entries[0].Name != "report.txt" || list.Entries[0].Size != 11 {
		t.Fatalf("ASSERTION: the listing is not build's folder: %s", res.Result)
	}
	// The two machines share this disk, so the folder alone does not say
	// which daemon listed it. The home folder does: each daemon answers with
	// its own, and the ssh stand-in gives build's daemon build's.
	if !strings.HasPrefix(list.Home, remote) {
		t.Fatalf("ASSERTION: the listing came from this machine's daemon (home %s), not build's (under %s)", list.Home, remote)
	}
	here := b.verb("file-list", map[string]any{"dir": farDir})
	var hereList struct {
		Home string `json:"home"`
	}
	_ = json.Unmarshal(here.Result, &hereList)
	if !strings.HasPrefix(hereList.Home, base) {
		t.Fatalf("a verb with no host did not run here: home %s", hereList.Home)
	}

	// A copy from build, which this machine's daemon runs.
	dst := filepath.Join(base, "report.txt")
	start := b.verb("transfer-start", map[string]any{
		"src": map[string]any{"host": "build", "path": filepath.Join(farDir, "report.txt")},
		"dst": map[string]any{"path": dst},
	})
	if !start.OK {
		t.Fatalf("ASSERTION: transfer-start through the bridge failed: %s %s", start.Code, start.Error)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if got, err := os.ReadFile(dst); err == nil && string(got) == "from build\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the copy from build never arrived")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// A pane this session holds for build says so, so the explorer and a
	// drop on it use build's files.
	if res := b.verb("new-window", map[string]any{"session": "here", "host": "build"}); !res.OK {
		t.Fatalf("make a pane on build: %s %s", res.Code, res.Error)
	}
	b.waitState(func(s *wireState) bool {
		for _, w := range s.Windows {
			if w.Host == "build" {
				return true
			}
		}
		return false
	}, "a window whose host is build")

	// The nonce is for this machine's daemon only.
	refused := b.verbOn("build", "respond", map[string]any{"session": "far", "action": "choose", "value": "1"})
	if refused.OK || refused.Code != "not_human" || !strings.Contains(refused.Error, "on this machine only") {
		t.Fatalf("ASSERTION: a person verb went to build: %+v", refused)
	}
	b.saveLog("verb-on-a-host")
}
