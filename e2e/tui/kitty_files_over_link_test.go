package tuie2e

import (
	"bytes"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A kitty image named by path, in a pane whose process runs on another
// machine.
//
// kitten icat in file mode, mpv, yazi and the rest name a file (t=f), a
// temporary file (t=t) or a shared memory object (t=s) instead of sending the
// bytes. In a window on a host the path names a file over there, and the
// daemon that draws the window is here, so it refused every such command and
// told a guest that asked to stream instead. A guest that did not ask drew
// nothing.
//
// Now the far machine reads the file, where the path means something, and
// the bytes reach the clients as an ordinary direct upload. Here the far
// machine is a second daemon on this machine, linked through the ssh
// stand-in, with its own home and runtime folders.
//
// kittylive walks every medium from inside the far pane and prints PASS or
// FAIL with the reply it was given; the host terminal's stream says what the
// client really sent on.
//
// How this could pass wrongly, written down first:
//   - The replies could be OK while nothing reaches the terminal. The host
//     stream must hold each file's exact bytes as a direct upload.
//   - The path could be handed to the host terminal, which shares this
//     machine and could read it itself. No file medium may reach the host.
//   - The far pane could be a pane of this daemon. The window is opened with
//     --host, and its files live under the far machine's home.
//   - A refusal could be silence. Each refused command must get an error
//     reply, which kittylive counts as its PASS.
//
// Negative control: on main the media steps for t=f, t=t and t=s FAIL (the
// first two with ENOTSUPPORTED:the path would be read on another machine),
// and in the refused run the host terminal is handed the paths, the key's
// among them. See NEGATIVE_CONTROLS.md.
func TestKittyFilesFromAPaneOnAHost(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh, "TUIOS_SIXEL_GRAPHICS=0"}
	live := buildKittyLive(t)

	// The far machine's own folders: a picture folder outside every
	// temporary folder, and a key the link's read rules keep.
	farHome := xdgDir(remote, "HOME")
	pics := filepath.Join(farHome, "pictures")
	if err := os.MkdirAll(pics, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(farHome, ".ssh", "id_ed25519")
	if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("not a real key\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The far daemon runs before the link reaches it, as it does on a
	// machine where tuios is in use.
	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("start the far daemon: %v\n%s", err, out)
	}
	host := newKittyHost()
	term := startIn(t, base, startOpts{cols: 160, rows: 60, args: []string{"new", "home"}, env: env, out: host})
	host.answerProbe(t, term)
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool {
		return containsAll(s, "build", "up")
	}, "the daemon never reported build up")
	if out, err := tuiosCLIEnv(t, base, env, "new-window", "faraway", "-s", "home", "--host", "build"); err != nil {
		t.Fatalf("create a window on build: %v\n%s", err, out)
	}
	waitWindowCount(t, term, 1, "opening the window on build")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo FAR-$((20+22))", "FAR-42", shellTimeout)
	// kittylive's and kitten icat's t=t files go in a temporary folder of
	// the far machine. kitty accepts $TMPDIR only when it is not a parent of
	// the home, and the far home here is under the test's own $TMPDIR, so
	// /tmp it is. On Linux icat's probe picks memory first and never needs
	// the folder. On macOS it has no memory mode, and with the refused
	// folder it falls back to stream.
	tmp, err := os.MkdirTemp("/tmp", "kittylive-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	t.Run("media", func(t *testing.T) {
		host.mark("media")
		clearPane(t, term)
		typeLine(t, term, "TMPDIR="+tmp+" "+live+" -dir "+pics)
		waitLive(t, term, "ALL 8 PASS")
		saveArtifact(t, term, artifactDir(t), "media")

		stream := host.bytes()
		if i := bytes.Index(stream, []byte(phaseMark+"media")); i >= 0 {
			stream = stream[i:]
		}
		paths, uploads := linkTransmits(t, stream)
		if len(paths) > 0 {
			t.Errorf("the host was handed paths %q; a path from another machine means nothing here", paths)
		}
		if bytes.Contains(stream, []byte("tuios-kitty-medium")) {
			t.Errorf("the marker reached the host terminal")
		}
		green, err := os.ReadFile(filepath.Join(pics, "green.png"))
		if err != nil {
			t.Fatalf("the t=f file is gone: %v", err)
		}
		junk, err := os.ReadFile(filepath.Join(pics, "junk.bin"))
		if err != nil {
			t.Fatal(err)
		}
		png := junk[4000 : len(junk)-4000]
		teal := bytes.Repeat([]byte{42, 161, 152, 255}, 64*32)
		wantUploads := map[string][]byte{"t=f": green, "t=f with O= and S=": png}
		// tuios reads a shared memory object only from /dev/shm, and the far
		// side answers ENOTSUPPORTED elsewhere. macOS has no /dev/shm, and Go
		// has no shm_open there without cgo, so kittylive sends no t=s
		// command off Linux.
		if runtime.GOOS == "linux" {
			wantUploads["t=s"] = teal
		}
		for name, want := range wantUploads {
			if !containsUpload(uploads, want) {
				t.Errorf("%s: the host never received the file's %d bytes as a direct upload (got uploads of %v bytes)", name, len(want), uploadSizes(uploads))
			}
		}
		pngs := 0
		for _, u := range uploads {
			if bytes.HasPrefix(u, []byte("\x89PNG")) {
				pngs++
			}
		}
		// direct-png, file, file-offset and tempfile.
		if pngs < 4 {
			t.Errorf("the host received %d PNG uploads, want at least 4 (t=d, t=f, t=f with O=, t=t)", pngs)
		}
	})

	// The real kitten icat, when it is installed: its own probe picks file
	// or memory mode, and each mode is also forced.
	t.Run("icat", func(t *testing.T) {
		kitten, err := exec.LookPath("kitten")
		if err != nil {
			t.Skip("kitten is not installed")
		}
		path, img := writeIcatPNG(t, pics)
		host.mark("icat")
		clearPane(t, term)
		runTagged(t, term, "detect", "TMPDIR="+tmp+" "+kitten+" icat --detect-support 2>&1 | tr a-z A-Z | sed 's/^/MODE-/'")
		if snap := term.Snapshot(); !strings.Contains(snap, "MODE-MEMORY") && !strings.Contains(snap, "MODE-FILE") {
			t.Errorf("icat did not pick file or memory transfer for a pane on another machine:\n%s", snap)
		}
		// Memory mode is a POSIX shared memory object, which the far side
		// reads only on Linux (see the media run).
		modes := []string{"file", "memory"}
		if runtime.GOOS != "linux" {
			modes = modes[:1]
		}
		for _, mode := range modes {
			runTagged(t, term, mode, "TMPDIR="+tmp+" "+kitten+" icat --stdin=no --transfer-mode="+mode+" "+path)
		}
		saveArtifact(t, term, artifactDir(t), "icat")
		stream := host.bytes()
		if i := bytes.Index(stream, []byte(phaseMark+"icat")); i >= 0 {
			stream = stream[i:]
		}
		paths, uploads := linkTransmits(t, stream)
		if len(paths) > 0 {
			t.Errorf("the host was handed paths %q", paths)
		}
		pixels := icatImageW * icatImageH * 4
		got := 0
		for _, u := range uploads {
			if bytes.Equal(u, img) || len(u) == pixels || len(u) == pixels*3/4 {
				got++
			}
		}
		if got < len(modes) {
			t.Errorf("the host received %d of the %d images icat sent in modes %v (uploads of %v bytes; the PNG is %d)", got, len(modes), modes, uploadSizes(uploads), len(img))
		}
	})

	// A marker prefix that the far pane prints in two parts, slower than
	// the far side waits for the rest, and then a real image. The parts
	// used to reach this side as they were, and the scan here, inside what
	// it took for a marker, lost the ESC of the real one: the image never
	// came and the pane waited 30 s.
	t.Run("split-marker", func(t *testing.T) {
		green, err := os.ReadFile(filepath.Join(pics, "green.png"))
		if err != nil {
			t.Fatalf("the media run left no green.png: %v", err)
		}
		clearPane(t, term)
		host.mark("split")
		// The real command comes right after the split prefix, with no
		// other escape between them, which is the case the scan lost.
		cmd := `printf '\033_tuios-kitty-'; sleep 0.3; printf 'medium;x\033_Ga=t,i=3,f=100,t=f,q=2;%s\033\\' "$(printf %s ` +
			filepath.Join(pics, "green.png") + ` | base64 | tr -d '\n')"`
		started := time.Now()
		typeLine(t, term, cmd+`; echo "DONE""-split"`)
		if err := term.WaitForText("DONE-split", 20*time.Second); err != nil {
			t.Fatalf("the pane stalled after a split marker prefix: %v\n%s", err, term.Snapshot())
		}
		if took := time.Since(started); took > 10*time.Second {
			t.Errorf("the pane went on %v after the image, want at once (the far side waits 30 s for a fetch that never comes)", took)
		}
		stream := host.bytes()
		if i := bytes.Index(stream, []byte(phaseMark+"split")); i >= 0 {
			stream = stream[i:]
		}
		paths, uploads := linkTransmits(t, stream)
		if len(paths) > 0 {
			t.Errorf("the host was handed paths %q", paths)
		}
		if !containsUpload(uploads, green) {
			t.Errorf("the image after a split marker prefix never reached the host (uploads of %v bytes)", uploadSizes(uploads))
		}
	})

	// A program prints a lone ESC, pauses past the far side's hold, and then
	// a file command after it. The ESC went to this side alone and the
	// command was dropped, so the ESC joined the next byte the far side sent:
	// ESC D, which moves the cursor down and eats the D.
	t.Run("lone-esc", func(t *testing.T) {
		clearPane(t, term)
		host.mark("lone")
		green := filepath.Join(pics, "green.png")
		runTagged(t, term, "lone", `printf '\033'; sleep 0.3; printf '_Ga=T,i=3,f=100,t=f,q=2;%s\033\\%s\n' "$(printf %s `+green+` | base64 | tr -d '\n')" "D""ONE-LONE-ESC"`)
		if snap := term.Snapshot(); !strings.Contains(snap, "DONE-LONE-ESC") {
			t.Errorf("the text after a dropped command lost its first letter:\n%s", snap)
		}
		stream := host.bytes()
		if i := bytes.Index(stream, []byte(phaseMark+"lone")); i >= 0 {
			stream = stream[i:]
		}
		if paths, _ := linkTransmits(t, stream); len(paths) > 0 {
			t.Errorf("the host was handed paths %q", paths)
		}
	})

	t.Run("refused", func(t *testing.T) {
		host.mark("refused")
		clearPane(t, term)
		typeLine(t, term, live+" -refuse -dir "+pics+" -deny "+key)
		// Seven refusals, and on Linux three shared memory objects that
		// must survive.
		want := "ALL 7 PASS"
		if runtime.GOOS == "linux" {
			want = "ALL 10 PASS"
		}
		// The host checks below run even when a reply is wrong, so a path
		// that reached the host is reported whatever the guest was told.
		expectLive(t, term, want)
		saveArtifact(t, term, artifactDir(t), "refused")
		stream := host.bytes()
		if i := bytes.Index(stream, []byte(phaseMark+"refused")); i >= 0 {
			stream = stream[i:]
		}
		paths, uploads := linkTransmits(t, stream)
		if containsUpload(uploads, []byte("not a real key\n")) {
			t.Errorf("the key crossed the link")
		}
		// A refusal must be the far machine's, before a byte moves: the
		// owner's emulator also answers an error (EBADPNG) for bytes that
		// are not an image, which would pass the reply check alone.
		if passwd, err := os.ReadFile("/etc/passwd"); err == nil && containsUpload(uploads, passwd) {
			t.Errorf("a file of another user (/etc/passwd) crossed the link")
		}
		// A refused path is not handed on either: the host terminal shares
		// this machine here and would read it.
		if len(paths) > 0 {
			t.Errorf("the host was handed paths %q", paths)
		}
	})
	alive(t, term, "after images from a pane on a host")
}

// waitLive waits for kittylive's last line and fails with the screen when it
// says FAIL.
func waitLive(t *testing.T, term *tuitest.Terminal, want string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, want) || strings.Contains(text, " FAIL")
	}, 60*time.Second); err != nil {
		t.Fatalf("kittylive never finished: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Snapshot(), want) {
		t.Fatalf("kittylive did not pass:\n%s", term.Snapshot())
	}
}

// runTagged types cmd and waits for it to exit 0, by a mark that names tag,
// so a run after another one does not match the first one's mark.
func runTagged(t *testing.T, term *tuitest.Terminal, tag, cmd string) {
	t.Helper()
	typeLine(t, term, cmd+`; echo "DONE""-`+tag+`-RC=$?"`)
	if err := term.WaitForText("DONE-"+tag+"-RC=0", shellTimeout); err != nil {
		t.Fatalf("%s did not finish cleanly: %v\n%s", cmd, err, term.Snapshot())
	}
}

// clearPane clears the pane and waits until the last check's lines are gone.
func clearPane(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	typeLine(t, term, "clear")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return !strings.Contains(text, "PASS") && !strings.Contains(text, "FAIL")
	}, uiTimeout); err != nil {
		t.Fatalf("the pane did not clear: %v\n%s", err, term.Snapshot())
	}
}

// expectLive is waitLive that reports a FAIL and goes on.
func expectLive(t *testing.T, term *tuitest.Terminal, want string) {
	t.Helper()
	_ = term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, want) || strings.Contains(text, " FAIL")
	}, 60*time.Second)
	if snap := term.Snapshot(); !strings.Contains(snap, want) {
		t.Errorf("kittylive did not pass:\n%s", snap)
	}
}

// buildKittyLive compiles the check into the test's temporary directory.
func buildKittyLive(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "kittylive")
	build := exec.Command("go", "build", "-o", bin, "./kittylive")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kittylive: %v\n%s", err, out)
	}
	return bin
}

// linkTransmits returns the paths of the file medium commands in stream and
// the decoded bytes of every direct upload, chunks joined.
func linkTransmits(t *testing.T, stream []byte) (paths []string, uploads [][]byte) {
	t.Helper()
	var run []byte
	inRun := false
	for _, m := range graphicsRE.FindAllSubmatch(stream, -1) {
		params, payload := string(m[1]), m[2]
		keys := map[string]string{}
		for kv := range strings.SplitSeq(params, ",") {
			k, v, _ := strings.Cut(kv, "=")
			keys[k] = v
		}
		if t := keys["t"]; t == "f" || t == "t" || t == "s" {
			dec, _ := base64.StdEncoding.DecodeString(string(payload))
			paths = append(paths, string(dec))
			continue
		}
		a := keys["a"]
		switch {
		case a == "t" || a == "T" || (a == "" && !inRun && len(payload) > 0):
			inRun, run = true, nil
		case !inRun || a != "":
			continue
		}
		run = append(run, payload...)
		if keys["m"] != "1" {
			dec, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(string(run), "="))
			if err == nil {
				uploads = append(uploads, dec)
			}
			inRun = false
		}
	}
	return paths, uploads
}

func containsUpload(uploads [][]byte, want []byte) bool {
	for _, u := range uploads {
		if bytes.Equal(u, want) {
			return true
		}
	}
	return false
}

func uploadSizes(uploads [][]byte) []int {
	out := make([]int, len(uploads))
	for i, u := range uploads {
		out[i] = len(u)
	}
	return out
}

// TestKittyFilesStalledOwnerHoldsOnlyItsPane is an owner that stops reading
// two large images, while a third pane on the same far machine sends a small
// one.
//
// The far machine reads at most two files at once. It held each slot from
// the open until the owner had read the last byte, with no time limit on the
// writes. Two images the owner stopped reading took both slots, and every
// other pane's image on that machine waited until the owner read again.
//
// Now a slot covers one read of the file and each write has a budget. The
// owner's stall is played by TUIOS_E2E_KITTY_LINK_STALL, which holds the
// owner's fetch of any image over 1 MB while a file exists.
//
// How this could pass wrongly, written down first:
//   - The large images could not stall at all. Each must end in an error
//     reply, which only a stalled write gives.
//   - The small image could beat the large ones to a slot. It starts two
//     seconds after them, when both are stalled.
//   - The stall could end before the check. It lasts 45 seconds.
//
// Negative control: with the far side as it was before this fix, the small
// image waits for the whole stall, and the large ones never answer in it.
func TestKittyFilesStalledOwnerHoldsOnlyItsPane(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the stand-ins are unix programs")
	}
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	stall := filepath.Join(base, "stall")
	if err := os.WriteFile(stall, []byte("1000000"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"TUIOS_SSH=" + ssh, "TUIOS_SIXEL_GRAPHICS=0", "TUIOS_E2E=1", "TUIOS_E2E_KITTY_LINK_STALL=" + stall}
	load := filepath.Join(t.TempDir(), "linkload")
	if out, err := exec.Command("go", "build", "-o", load, "./linkload").CombinedOutput(); err != nil {
		t.Fatalf("build linkload: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("start the far daemon: %v\n%s", err, out)
	}
	host := newKittyHost()
	term := startIn(t, base, startOpts{cols: 160, rows: 50, args: []string{"new", "home"}, env: env, out: host})
	host.answerProbe(t, term)
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool { return containsAll(s, "build", "up") }, "the daemon never reported build up")

	pics := filepath.Join(xdgDir(remote, "HOME"), "pictures")
	if err := os.MkdirAll(pics, 0o700); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(pics, "big.png")
	writeNoisePNG(t, big, 1200, 1000)
	small, _ := writeIcatPNG(t, pics)

	start := time.Now()
	send := func(name, img string) string {
		out := filepath.Join(pics, name+".json")
		if o, err := tuiosCLIEnv(t, base, env, "new-window", name, "-s", "home", "--host", "build", "--no-focus", "--", load, "send", img, out); err != nil {
			t.Fatalf("open %s: %v\n%s", name, err, o)
		}
		return out
	}
	bigs := []string{send("big1", big), send("big2", big)}
	time.Sleep(2 * time.Second)
	smallAt := time.Now()
	smallOut := send("small", small)

	res, ok := waitResult(smallOut, 30*time.Second)
	if !ok {
		t.Fatalf("the small image never answered in 30 s while two large ones were stalled: their slots held it")
	}
	took := time.Since(smallAt)
	t.Logf("small image: %v in %v", res, took)
	if res["reply"] != "i=901;OK" {
		t.Errorf("the small image was answered %v, want OK", res["reply"])
	}
	if took > 8*time.Second {
		t.Errorf("the small image took %v while the large ones were stalled, want a few seconds", took)
	}
	for _, out := range bigs {
		res, ok := waitResult(out, 40*time.Second-time.Since(start))
		if !ok {
			t.Fatalf("a stalled large image never answered while the stall lasted: the transfer has no budget")
		}
		t.Logf("large image: %v", res)
		if r, _ := res["reply"].(string); strings.HasSuffix(r, ";OK") {
			t.Errorf("a large image the owner never read was answered OK")
		}
		// The reply goes into the pane's input: no socket, no path.
		if r, _ := res["reply"].(string); strings.Contains(r, "/") {
			t.Errorf("the reply %q names a path", r)
		}
	}
	_ = os.Remove(stall)
	alive(t, term, "after a stalled owner")
}
