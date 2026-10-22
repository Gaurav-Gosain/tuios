package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// Native plugins: a tuios-plugin.toml asks for pane grants, and its
// processes hold what the person approved at install, in every session.
//
// The probe asks for read. Its startup script, in probe.sh, reads with
// list-sessions and through herdr's CLI, lists the plugins, subscribes to
// window events, tries to type with send-text, tries to make a session over
// the client protocol with tuios new, and leaves a process behind
// whose parent exits at once, whose TUIOS_* variables are cut, and which
// holds none of the script's output open. Then the script ends. A second later the process it left tries to type, and tries
// to disable the plugin, which only the person may do. Each result goes to
// a file in the plugin's state folder. The probe's pane records its grants.
//
// The same script runs from a herdr manifest, installed from a folder, which
// keeps the default grants (admin under mode open), and from a native
// manifest that is linked, not installed, which asks for nothing and runs
// only after tuios plugins approve.
//
// The negative controls are in NEGATIVE_CONTROLS.md, "Native plugins".

const nativeProbeScript = `T='%TUIOS%'
D="$HERDR_PLUGIN_STATE_DIR"
run() { n=$1; shift; "$@" >"$D/$n.out" 2>&1; echo $? >"$D/$n.code"; }
run grants "$T" pane-grants --json
run read "$T" list-sessions
run herdrlist "$HERDR_BIN_PATH" workspace list
run pluginlist "$HERDR_BIN_PATH" plugin list
run write "$T" send-text -s probe "echo from-$HERDR_PLUGIN_ID"
( timeout 30 "$T" subscribe --types window-created --count 1 >"$D/events.out" 2>&1; echo $? >"$D/events.code" ) </dev/null &
run binary "$T" new "made-by-$(echo "$HERDR_PLUGIN_ID" | tr . -)" --detach
( ( sleep 1
    exec env -u TUIOS_SOCKET -u TUIOS_PANE_ID -u TUIOS_WINDOW_ID -u TUIOS_PANE_TOKEN sh -c '
      D=$1; T=$2
      "$T" send-text -s probe "echo orphan-of-$3" >"$D/orphan-write.out" 2>&1; echo $? >"$D/orphan-write.code"
      "$T" plugins disable "$3" >"$D/orphan-trust.out" 2>&1; echo $? >"$D/orphan-trust.code"
    ' orphan "$D" "$T" "$HERDR_PLUGIN_ID" ) </dev/null >/dev/null 2>&1 & )
`

// nativePaneScript is the native probe's pane: it records its grants.
const nativePaneScript = `"%TUIOS%" pane-grants --json >"$HERDR_PLUGIN_STATE_DIR/pane-grants.out" 2>&1
exec sleep 600
`

// nativeProbeDir writes a plugin folder: probe.sh and the manifest named.
func nativeProbeDir(t *testing.T, name, manifest string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "plug")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(nativeProbeScript, "%TUIOS%", tuiosBin)
	if err := os.WriteFile(filepath.Join(dir, "probe.sh"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	pane := strings.ReplaceAll(nativePaneScript, "%TUIOS%", tuiosBin)
	if err := os.WriteFile(filepath.Join(dir, "pane.sh"), []byte(pane), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func nativeManifest(id, grants, extra string) string {
	return `schema = 1
id = "` + id + `"
name = "Native probe"
version = "1.0.0"
platforms = ["linux", "macos"]

[permissions]
grants = [` + grants + `]

[[process]]
id = "main"
run = "startup"
command = ["sh", "probe.sh"]

[[process]]
id = "panel"
run = "pane"
title = "Panel"
placement = "tab"
command = ["sh", "pane.sh"]
` + extra
}

// TestNativePluginGrants installs the native probe from git and checks what
// its processes may do, beside a herdr plugin and a linked native plugin.
func TestNativePluginGrants(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "probe", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}

	// Refused at install, before anything runs: WASM, admin not typed, and
	// two manifests that name two ids.
	herdrTwin := "id = \"e2e.other\"\nname = \"x\"\nversion = \"1.0.0\"\nmin_herdr_version = \"0.9.3\"\n"
	for _, c := range []struct{ why, manifest, want string }{
		{"wasm", nativeManifest("e2e.wasm", `"read"`, "\n[[wasm]]\nid = \"cell\"\n"), "does not run WASM plugins yet"},
		{"admin", nativeManifest("e2e.admin", `"admin"`, ""), "--grant admin"},
		{"respond", nativeManifest("e2e.respond", `"respond"`, ""), "cannot hold respond"},
		{"typo", nativeManifest("e2e.typo", `"read"`, "\n[permision]\n"), "unknown key permision"},
		{"twins", "schema = 1\nid = \"e2e.twin\"\n\n[herdr]\nmanifest = \"herdr-plugin.toml\"\n", "The ids must match"},
	} {
		dir := nativeProbeDir(t, "tuios-plugin.toml", c.manifest)
		if c.why == "twins" {
			if err := os.WriteFile(filepath.Join(dir, "herdr-plugin.toml"), []byte(herdrTwin), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		out, err := tuiosCLI(t, base, "plugins", "install", dir, "--yes")
		if err == nil || !strings.Contains(out, c.want) {
			t.Fatalf("install of the %s manifest: %v, want %q\n%s", c.why, err, c.want, out)
		}
	}

	// The native probe, from a bare repository over file://.
	src := nativeProbeDir(t, "tuios-plugin.toml", nativeManifest("e2e.native", `"read"`, ""))
	work := testutil.GitRepo(t)
	for _, f := range []string{"probe.sh", "pane.sh", "tuios-plugin.toml"} {
		b, _ := os.ReadFile(filepath.Join(src, f))
		if err := os.WriteFile(filepath.Join(work, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	testutil.Git(t, work, "add", "-A")
	testutil.Git(t, work, "commit", "-q", "-m", "native probe")
	bare := filepath.Join(t.TempDir(), "native.git")
	testutil.Git(t, work, "clone", "-q", "--bare", work, bare)
	out, err := tuiosCLI(t, base, "plugins", "install", "file://"+bare, "--yes")
	if err != nil || !strings.Contains(out, "(tuios plugin)") || !strings.Contains(out, "It can call tuios with these grants, in every session:\n  read   Reads every session and pane on this machine.") {
		t.Fatalf("install of the native probe: %v\n%s", err, out)
	}
	art := artifactDir(t)
	_ = os.WriteFile(filepath.Join(art, "install.txt"), []byte(out), 0o644)

	// A herdr plugin from a folder, and a native one that is only linked.
	herdrDir := nativeProbeDir(t, "herdr-plugin.toml", "id = \"e2e.herdrprobe\"\nname = \"Herdr probe\"\nversion = \"1.0.0\"\nmin_herdr_version = \"0.9.3\"\n\n[[startup]]\ncommand = [\"sh\", \"probe.sh\"]\n")
	if out, err := tuiosCLI(t, base, "plugins", "install", herdrDir, "--yes"); err != nil {
		t.Fatalf("install of the herdr probe: %v\n%s", err, out)
	}
	linked := nativeProbeDir(t, "tuios-plugin.toml", nativeManifest("e2e.linked", "", ""))
	if out, err := tuiosCLI(t, base, "plugins", "link", linked, "--enable"); err != nil {
		t.Fatalf("link of the native probe: %v\n%s", err, out)
	}
	// Linked, it waits for the person on this machine.
	if out, err := tuiosCLI(t, base, "plugins", "approve", "e2e.linked", "--yes"); err != nil || !strings.Contains(out, "It runs now.") {
		t.Fatalf("approve of the linked probe: %v\n%s", err, out)
	}

	// "0" is a success, "x" any failure.
	want := map[string]map[string]string{
		// The native probe reads, and neither it nor what it left behind
		// types or changes which plugins run.
		"e2e.native": {"read": "0", "herdrlist": "0", "pluginlist": "0", "write": "x", "binary": "x", "orphan-write": "x", "orphan-trust": "x"},
		// A herdr plugin holds the default grants: admin under open. What it
		// left behind still cannot act as the person.
		"e2e.herdrprobe": {"read": "0", "herdrlist": "0", "pluginlist": "0", "write": "0", "binary": "0", "orphan-write": "0", "orphan-trust": "x"},
		// A linked native plugin that asks for nothing holds nothing.
		"e2e.linked": {"read": "x", "herdrlist": "0", "pluginlist": "x", "write": "x", "binary": "x", "orphan-write": "x", "orphan-trust": "x"},
	}
	for id, codes := range want {
		for name, code := range codes {
			got := strings.TrimSpace(waitPluginFile(t, base, id, name+".code", "\n"))
			if (got == "0") != (code == "0") {
				t.Errorf("%s: %s exited %s, want %s (x is any failure). Output:\n%s", id, name, got, code, readPluginFile(base, id, name+".out"))
			}
		}
		for _, name := range []string{"grants.out", "read.out", "herdrlist.out", "pluginlist.out", "write.out", "binary.out", "orphan-write.out", "orphan-trust.out"} {
			savePluginArtifact(t, base, id, name)
		}
	}
	for _, c := range []struct{ id, file, want string }{
		{"e2e.native", "write.out", "refused for plugin e2e.native: send-text needs the write grant"},
		{"e2e.native", "orphan-write.out", "refused for plugin e2e.native"},
		{"e2e.native", "binary.out", "refused for plugin e2e.native"},
		// The herdr surface agrees with the verbs: read reaches every
		// session, and a plugin without read sees none and is told no.
		{"e2e.native", "herdrlist.out", `"label":"probe"`},
		{"e2e.linked", "herdrlist.out", `"workspaces":[]`},
		{"e2e.linked", "pluginlist.out", "plugin.list needs the read grant"},
		{"e2e.native", "orphan-trust.out", "forbidden"},
		{"e2e.native", "grants.out", `e2e.native`},
		{"e2e.herdrprobe", "orphan-trust.out", "forbidden"},
		{"e2e.linked", "read.out", "refused for plugin e2e.linked: list-sessions needs the read grant"},
	} {
		if got := readPluginFile(base, c.id, c.file); !strings.Contains(got, c.want) {
			t.Errorf("%s %s lacks %q:\n%s", c.id, c.file, c.want, got)
		}
	}
	// admin holds only with an approval on this machine, at the exact
	// trust hash. tuios plugins install --grant admin writes it.
	adminDir := nativeProbeDir(t, "tuios-plugin.toml", nativeManifest("e2e.admin", `"admin"`, ""))
	if out, err := tuiosCLI(t, base, "plugins", "install", adminDir, "--yes", "--no-enable", "--grant", "admin"); err != nil {
		t.Fatalf("install with --grant admin: %v\n%s", err, out)
	}
	approvals := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "plugins", "%approvals.toml")
	if st, err := os.Stat(approvals); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the admin approval file: %v, mode %v, want 0600", err, st)
	}
	if info, _ := tuiosCLI(t, base, "plugins", "info", "e2e.admin"); !strings.Contains(info, "holds admin") || strings.Contains(info, "error") {
		t.Errorf("plugins info of the approved admin plugin:\n%s", info)
	}
	adminState := func(why, want string) {
		t.Helper()
		info, _ := tuiosCLI(t, base, "plugins", "info", "e2e.admin")
		if !strings.Contains(info, want) {
			t.Errorf("%s: plugins info lacks %q:\n%s", why, want, info)
		}
	}
	// The approval covers the files the commands name: a changed probe.sh
	// needs a new one.
	probe := filepath.Join(adminDir, "probe.sh")
	orig, _ := os.ReadFile(probe)
	_ = os.WriteFile(probe, append(slices.Clone(orig), "# changed\n"...), 0o644)
	adminState("after probe.sh changed", "plugin_admin_not_approved")
	_ = os.WriteFile(probe, orig, 0o644)
	adminState("after probe.sh changed back", "holds admin")
	// A folder that others may write is not trusted.
	_ = os.Chmod(filepath.Dir(approvals), 0o770)
	adminState("with the approval folder writable by the group", "plugin_needs_approval")
	_ = os.Chmod(filepath.Dir(approvals), 0o700)
	adminState("with the approval folder private again", "holds admin")
	// Drop the admin plugin's approval and keep the others.
	kept := ""
	text, _ := os.ReadFile(approvals)
	for _, chunk := range strings.SplitAfter(string(text), "[[approval]]") {
		if !strings.Contains(chunk, "e2e.admin") {
			kept += chunk
		} else if strings.HasSuffix(chunk, "[[approval]]") {
			kept += "[[approval]]"
		}
	}
	if err := os.WriteFile(approvals, []byte(kept), 0o600); err != nil {
		t.Fatal(err)
	}
	adminState("with no approval on this machine", "plugin_needs_approval")
	hasAdminApproval := func() bool {
		b, _ := os.ReadFile(approvals)
		return strings.Contains(string(b), "e2e.admin")
	}
	// A pane cannot approve admin, also with its pane variables cut: the
	// daemon places the caller by the kernel.
	if out, err := tuiosCLI(t, base, "new", "approver", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	// The same with --server, which names a daemon that does not know the
	// pane: every daemon that runs is asked.
	t.Cleanup(func() { _, _ = tuiosCLI(t, base, "--server", "zz", "kill-server") })
	for i, extra := range []string{"", "--server zz "} {
		paneOut := filepath.Join(t.TempDir(), "pane-approve")
		line := "env -u TUIOS_PANE_ID -u TUIOS_WINDOW_ID '" + tuiosBin + "' " + extra + "plugins install '" + adminDir + "' --yes --no-enable --grant admin >'" + paneOut + ".out' 2>&1; echo $? >'" + paneOut + ".code'"
		if out, err := tuiosCLI(t, base, "send-text", "-s", "approver", line+"\n"); err != nil {
			t.Fatalf("send-text: %v\n%s", err, out)
		}
		deadline := time.Now().Add(2 * uiTimeout)
		for {
			if b, _ := os.ReadFile(paneOut + ".code"); strings.Contains(string(b), "\n") {
				if strings.TrimSpace(string(b)) == "0" {
					t.Errorf("a pane approved admin with its pane variables cut %s", extra)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the install in the pane never finished (%s)", extra)
			}
			time.Sleep(100 * time.Millisecond)
		}
		paneText, _ := os.ReadFile(paneOut + ".out")
		_ = os.WriteFile(filepath.Join(art, "pane-approve-"+strconv.Itoa(i)+".txt"), paneText, 0o644)
		if !strings.Contains(string(paneText), "only you, from a terminal outside tuios") {
			t.Errorf("the refusal in the pane (%s):\n%s", extra, paneText)
		}
		if hasAdminApproval() {
			t.Errorf("a pane wrote the admin approval (%s)", extra)
		}
	}
	adminState("after the pane tried to approve", "plugin_needs_approval")
	// A hand-written grant in plugins.lock does not widen a local plugin:
	// its trust hash no longer matches.
	localDir := nativeProbeDir(t, "tuios-plugin.toml", nativeManifest("e2e.local", `"fan"`, ""))
	if out, err := tuiosCLI(t, base, "plugins", "install", localDir, "--yes", "--no-enable"); err != nil {
		t.Fatalf("install of the local plugin: %v\n%s", err, out)
	}
	lockPath := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "plugins.lock")
	lockText, _ := os.ReadFile(lockPath)
	forged := regexp.MustCompile(`grants = \[[^\]]*fan[^\]]*\]`).ReplaceAllString(string(lockText), "grants = ['admin']")
	if forged == string(lockText) {
		t.Fatalf("the test found no fan grant to change in plugins.lock:\n%s", lockText)
	}
	if err := os.WriteFile(lockPath, []byte(forged), 0o600); err != nil {
		t.Fatal(err)
	}
	if info, _ := tuiosCLI(t, base, "plugins", "info", "e2e.local"); !strings.Contains(info, "plugin_changed") || strings.Contains(info, "holds admin") {
		t.Errorf("a local plugin whose lock entry was given admin by hand:\n%s", info)
	}
	_ = os.WriteFile(filepath.Join(art, "plugins.lock"), []byte(forged), 0o644)

	// The event stream agrees with the verbs too: a plugin with read gets
	// events from every session, and one without read cannot subscribe.
	for _, id := range []string{"e2e.native", "e2e.herdrprobe"} {
		waitPluginFile(t, base, id, "events.out", "seq")
	}
	waitPluginFile(t, base, "e2e.linked", "events.code", "\n")
	if out, err := tuiosCLI(t, base, "new-window", "-s", "probe", "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	for id, want := range map[string]string{"e2e.native": "window-created", "e2e.herdrprobe": "window-created", "e2e.linked": "needs the read grant"} {
		waitPluginFile(t, base, id, "events.code", "\n")
		if got := readPluginFile(base, id, "events.out"); !strings.Contains(got, want) {
			t.Errorf("%s events.out lacks %q:\n%s", id, want, got)
		}
		savePluginArtifact(t, base, id, "events.out")
	}

	// Only the herdr plugin made a session over the client protocol.
	sessions, _ := tuiosCLI(t, base, "list-sessions")
	if !strings.Contains(sessions, "made-by-e2e-herdrprobe") || strings.Contains(sessions, "made-by-e2e-native") || strings.Contains(sessions, "made-by-e2e-linked") {
		t.Errorf("the sessions the plugins made over the client protocol:\n%s", sessions)
	}
	// Only the herdr plugin typed into the pane.
	screen, _ := tuiosCLI(t, base, "capture-pane", "-s", "probe")
	_ = os.WriteFile(filepath.Join(art, "pane.txt"), []byte(screen), 0o644)
	if !strings.Contains(screen, "from-e2e.herdrprobe") || strings.Contains(screen, "from-e2e.native") ||
		strings.Contains(screen, "orphan-of-e2e.native") || strings.Contains(screen, "e2e.linked") {
		t.Errorf("the pane shows what the plugins typed:\n%s", screen)
	}
	// The plugin's pane holds no more than the plugin, though the person
	// opened it, and the person's panes hold admin.
	if out, err := tuiosCLI(t, base, "plugins", "open", "e2e.native", "panel"); err != nil {
		t.Fatalf("plugins open: %v\n%s", err, out)
	}
	var pg struct {
		Pane   bool     `json:"pane"`
		Grants []string `json:"grants"`
	}
	if err := json.Unmarshal([]byte(waitPluginFile(t, base, "e2e.native", "pane-grants.out", "}")), &pg); err != nil || !pg.Pane || !slices.Equal(pg.Grants, []string{"read"}) {
		t.Errorf("the plugin's pane holds %+v (%v), want read", pg, err)
	}
	savePluginArtifact(t, base, "e2e.native", "pane-grants.out")
	info, _ := tuiosCLI(t, base, "plugins", "info", "e2e.native")
	_ = os.WriteFile(filepath.Join(art, "info.txt"), []byte(info), 0o644)
	if !strings.Contains(info, "grants    asks read, holds read") {
		t.Errorf("plugins info does not show the grants:\n%s", info)
	}
	if info, _ := tuiosCLI(t, base, "plugins", "info", "e2e.linked"); !strings.Contains(info, "grants    asks none, holds none") {
		t.Errorf("plugins info of the linked plugin:\n%s", info)
	}
}
