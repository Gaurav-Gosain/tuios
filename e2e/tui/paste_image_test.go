package tuie2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"

	"tuios.dev/shot"
)

// Pasting an image from the clipboard puts it in a file on the machine where
// the pane's process runs, and pastes that file's path.
//
// The clipboard here is a fake. A wl-paste stand-in on PATH answers the type
// probe with image/png and the read with a fixed PNG, and WAYLAND_DISPLAY
// names no real compositor, so the developer's clipboard is never read. The
// proof is the pane's own shell running wc -c on the pasted path: the shell
// can only print the image's size if the file is on its machine.

// fakeImage is a PNG header and a body long enough that its size is not a
// number that shows up on screen by chance.
func fakeImage() []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("tuios"), 821)...)
}

// fakeClipboardEnv writes the wl-paste stand-in and returns the environment
// that puts it in front of the client.
func fakeClipboardEnv(t *testing.T, img []byte) []string {
	t.Helper()
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "clip.png")
	if err := os.WriteFile(imgPath, img, 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncase \"$*\" in\n  *--list-types*) printf 'image/png\\n' ;;\n  *) cat '" + imgPath + "' ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "wl-paste"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return []string{
		"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"WAYLAND_DISPLAY=tuios-test-no-compositor",
	}
}

// unwrapped is the screen with its lines joined, and the panes' side borders
// and the padding taken out, so a path the pane wrapped reads whole. Where a
// path breaks depends on the length of the runtime folder, which the suite
// shortens when the isolation root is too long to hold a socket.
func unwrapped(screen string) string {
	return strings.NewReplacer("\n", "", "│", "", " ", "").Replace(screen)
}

// showsPastedPath reports whether a pasted image's path is on the screen.
func showsPastedPath(screen string) bool {
	return contains(unwrapped(screen), "tuios-paste-")
}

// showsPastedFile reports whether the path of the one image pasted on base's
// machine is on the screen. A shell that redraws its line where it wraps can
// lose cells in the middle of the path, as sh does when the line was typed
// before its prompt; the end of the path, the file's own name, still shows
// that this file's path arrived.
func showsPastedFile(t *testing.T, screen, base string) bool {
	t.Helper()
	if showsPastedPath(screen) {
		return true
	}
	files := pastedFiles(t, base)
	if len(files) != 1 {
		return false
	}
	return contains(unwrapped(screen), strings.TrimPrefix(filepath.Base(files[0]), "tuios-paste-"))
}

// pastedFiles lists the pasted images under a machine's runtime directory.
func pastedFiles(t *testing.T, base string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "paste", "tuios-paste-*.png"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// checkPasted waits for the pane's shell to print the image's size for the
// pasted path, then checks the file is on want's machine alone, with the
// image's bytes and the owner's permissions.
func checkPasted(t *testing.T, term *tuitest.Terminal, img []byte, want, other string) {
	t.Helper()
	size := strconv.Itoa(len(img)) + " "
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return showsPastedFile(t, s.Text(), want)
	}, uiTimeout); err != nil {
		t.Fatalf("no path was pasted into the pane: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return contains(s.Text(), size)
	}, uiTimeout); err != nil {
		t.Fatalf("the pane's shell never printed the image's size %q for the pasted path: %v\n%s", size, err, term.Snapshot())
	}

	files := pastedFiles(t, want)
	if len(files) != 1 {
		t.Fatalf("the pane's machine holds %d pasted images, want 1: %v", len(files), files)
	}
	got, err := os.ReadFile(files[0])
	if err != nil || !bytes.Equal(got, img) {
		t.Fatalf("the pasted file holds %d bytes (%v), want the clipboard's %d", len(got), err, len(img))
	}
	if info, err := os.Stat(files[0]); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the pasted file's mode is %v (%v), want 0600", info.Mode().Perm(), err)
	}
	if other != "" {
		if files := pastedFiles(t, other); len(files) != 0 {
			t.Errorf("the other machine holds pasted images too: %v", files)
		}
	}
}

func TestAnImagePastedIntoALocalPaneIsAFileHere(t *testing.T) {
	base := t.TempDir()
	img := fakeImage()
	env := fakeClipboardEnv(t, img)

	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	if out, err := tuiosCLIEnv(t, base, env, "new-window", "near", "-s", "home"); err != nil {
		t.Fatalf("create a window: %v\n%s", err, out)
	}
	if err := term.WaitForText("near", uiTimeout); err != nil {
		t.Fatalf("the window never appeared: %v\n%s", err, term.Snapshot())
	}
	enterTerminalMode(t, term)
	if err := term.SendKeys("wc -c "); err != nil {
		t.Fatal(err)
	}
	// The paste_image action, on its default key.
	if err := term.SendKeys(tuitest.Ctrl('b'), "V"); err != nil {
		t.Fatal(err)
	}
	checkPasted(t, term, img, base, "")
}

func TestAnImagePastedIntoAFarPaneIsAFileOnThatMachine(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	img := fakeImage()
	env := append(fakeClipboardEnv(t, img), "TUIOS_SSH="+ssh)

	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool {
		return containsAll(s, "build", "up")
	}, "the daemon never reported build up")

	if out, err := tuiosCLIEnv(t, base, env, "new-window", "faraway", "-s", "home", "--host", "build"); err != nil {
		t.Fatalf("create a window on build: %v\n%s", err, out)
	}
	if err := term.WaitForText("build:faraway", uiTimeout); err != nil {
		t.Fatalf("the far window never appeared: %v\n%s", err, term.Snapshot())
	}
	enterTerminalMode(t, term)
	if err := term.SendKeys("wc -c "); err != nil {
		t.Fatal(err)
	}
	// An empty bracketed paste, which is what a terminal sends when the
	// clipboard holds an image and no text.
	if err := term.SendKeys("\x1b[200~\x1b[201~"); err != nil {
		t.Fatal(err)
	}
	checkPasted(t, term, img, remote, base)
	alive(t, term, "after pasting an image into a far pane")
}

// The other way a pane lives on another machine: a session there, attached in
// this client with attach --host. Every verb then goes to the far daemon
// through this machine's link, paste-image too, and the far daemon writes the
// file itself.
//
// Negative control: none. It passes on main, and it is here so the route
// stays covered.
func TestAnImagePastedIntoASessionOnAHostIsAFileOnThatMachine(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	img := fakeImage()
	term, _ := startFarPane(t, base, remote, img, farSessionRoute)
	if err := term.SendKeys("wc -c "); err != nil {
		t.Fatal(err)
	}
	// The paste_image action, on its default key.
	if err := term.SendKeys(tuitest.Ctrl('b'), "V"); err != nil {
		t.Fatal(err)
	}
	checkPasted(t, term, img, remote, base)
	alive(t, term, "after pasting an image into a session on a host")
}

// farRoute is a way a pane lives on another machine.
type farRoute int

const (
	// farSessionRoute is a session on the host, attached with attach --host.
	farSessionRoute farRoute = iota
	// farWindowRoute is a window on the host in a session of this machine.
	farWindowRoute
)

// startFarPane starts a client whose focused pane runs on remote, reached as
// host build, with img on the fake clipboard. Keys typed next reach the
// pane's shell.
func startFarPane(t *testing.T, base, remote string, img []byte, route farRoute) (*tuitest.Terminal, []string) {
	t.Helper()
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := append(fakeClipboardEnv(t, img), "TUIOS_SSH="+ssh)
	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	switch route {
	case farSessionRoute:
		term := startIn(t, base, startOpts{args: []string{"attach", "--host", "build", "far-shell"}, env: env})
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return contains(s.Text(), "╰──")
		}, bootTimeout); err != nil {
			t.Fatalf("the client never drew the far session: %v\n%s", err, term.Snapshot())
		}
		// A client attached on a host starts in terminal mode.
		return term, env
	default:
		term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
		waitBoot(t, term)
		waitForHostListing(t, base, func(s string) bool {
			return containsAll(s, "build", "up")
		}, "the daemon never reported build up")
		if out, err := tuiosCLIEnv(t, base, env, "new-window", "faraway", "-s", "home", "--host", "build"); err != nil {
			t.Fatalf("create a window on build: %v\n%s", err, out)
		}
		if err := term.WaitForText("build:faraway", uiTimeout); err != nil {
			t.Fatalf("the far window never appeared: %v\n%s", err, term.Snapshot())
		}
		enterTerminalMode(t, term)
		return term, env
	}
}

// TestAnImagePasteNeedsFilesOnTheFarMachine holds an image paste to the far
// machine's link policy. A pasted image is a file from this machine written
// on that one, so a machine that does not allow files from this one takes no
// image. The client says so, names what to add and where, and types nothing.
//
// Both routes to a far pane are driven, and each with files allowed as well,
// so the refusal is shown to come from files and not from the fixture.
//
// Negative control: with paste-image and paste-pane-image back on write and
// open alone in verbCapabilities, the denied cases paste the image.
func TestAnImagePasteNeedsFilesOnTheFarMachine(t *testing.T) {
	for _, route := range []struct {
		name  string
		route farRoute
	}{
		{"session on a host", farSessionRoute},
		{"window on a host", farWindowRoute},
	} {
		for _, files := range []bool{true, false} {
			name := route.name + "/files allowed"
			if !files {
				name = route.name + "/files denied"
			}
			t.Run(name, func(t *testing.T) {
				base := t.TempDir()
				remote := remoteMachine(t)
				allow := `"list", "mail", "open", "write"`
				if files {
					allow += `, "files"`
				}
				writeRemoteConfig(t, remote, "[hosts.\"*\"]\nallow = ["+allow+"]\n")
				img := fakeImage()
				term, _ := startFarPane(t, base, remote, img, route.route)
				if files {
					if err := term.SendKeys("wc -c "); err != nil {
						t.Fatal(err)
					}
					if err := term.SendKeys(tuitest.Ctrl('b'), "V"); err != nil {
						t.Fatal(err)
					}
					checkPasted(t, term, img, remote, base)
					return
				}
				refusedPaste(t, term, base, remote)
			})
		}
	}
}

// refusedPaste pastes into a far pane whose machine refuses files, and checks
// the toast, that nothing was typed, and that no file was written anywhere.
func refusedPaste(t *testing.T, term *tuitest.Terminal, base, remote string) {
	t.Helper()
	if err := term.SendKeys("echo pasted:"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "V"); err != nil {
		t.Fatal(err)
	}
	toast := "Cannot paste the image. Host build does not allow"
	if err := term.WaitForText(toast, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: no toast said the far machine refused the image: %v\n%s", err, term.Snapshot())
	}

	// The shell prints pasted:2 only when nothing was typed after "pasted:".
	// A path typed there would print pasted:/...png2.
	if err := term.SendKeys("$((1+1))", tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return contains(s.Text(), "pasted:2")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: something was typed into the pane: %v\n%s", err, term.Snapshot())
	}
	if showsPastedPath(term.Snapshot()) {
		t.Fatalf("ASSERTION: a pasted path reached the pane:\n%s", term.Snapshot())
	}

	// The dock cuts the message. A click on it shows all of it.
	row, col := findText(t, term, toast)
	mouseClick(t, term, col+2, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "does not allow files from this machine.", `Add "files" to the allow list in [hosts.`, "on build.")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the full message does not say to add files and where: %v\n%s", err, term.Snapshot())
	}
	dir := artifactDir(t)
	saveArtifact(t, term, dir, "refused")
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "refused")
	for _, machine := range []string{remote, base} {
		if files := pastedFiles(t, machine); len(files) != 0 {
			t.Fatalf("ASSERTION: a refused paste wrote %v", files)
		}
	}
	alive(t, term, "after a refused image paste")
}

// A pane of this machine whose shell is on another machine: the person ran
// ssh in it. The program that reads the pasted path is over there, so the
// image goes there, by the drop folder and a transfer, when exactly one
// [hosts] entry is that machine. In every other case the paste goes to the
// right place or is refused, and a path is never typed into the wrong one.
//
// The pane runs a script named ssh, which is what the daemon reads from the
// process. The daemon resolves its line and each [hosts] entry with ssh -G
// through TUIOS_SSH, here a wrapper that runs the real ssh -G and sends
// everything else to the remote stand-in. The script can print an OSC 7
// report, then runs each line typed at it. It runs on this machine, which
// shares a file system with the remote stand-in, so a line run there can read
// a file the remote daemon wrote, and only that file proves where it went.
//
// Negative controls are in NEGATIVE_CONTROLS.md, one per rule.
func TestAnImagePastedIntoAnSSHShellGoesToThatMachine(t *testing.T) {
	build := hostEntry{"build", "someone@buildbox", nil}
	deep := hostEntry{"deep", "someone@deepbox", nil}
	noLink := []string{"The pane runs on", "Add the machine with tuios hosts add."}
	cases := []struct {
		name  string
		hosts []hostEntry
		// line is what the pane runs. SSH is the stand-in's path.
		line string
		// allow is the far machine's allow for this one, "" for the default.
		allow string
		// want is "far" for the far drop folder, "here" for this machine's
		// paste folder, or the phrases of the refusal.
		want []string
	}{
		{"login to the host", []hostEntry{build}, "SSH someone@buildbox", "", []string{"far"}},
		{"login to the host without files", []hostEntry{build}, "SSH someone@buildbox", `"list", "mail", "open", "write"`,
			[]string{"Host build does not allow files", `Add "files" to the allow list in [hosts.`}},
		{"tmux over ssh with a tty", []hostEntry{build}, "SSH -t someone@buildbox tmux", "", []string{"far"}},
		{"a remote command after the host", []hostEntry{build}, "SSH someone@buildbox -t 'tmux a'", "", []string{"far"}},
		{"an address whose shell names itself", []hostEntry{{"build", "someone@127.0.0.1", nil}}, "FAKE_OSC7=ip-10-0-0-5 SSH someone@127.0.0.1", "", []string{"far"}},
		{"a jump host the entry has too", []hostEntry{{"build", "someone@buildbox", []string{"-J", "bastion"}}}, "SSH -J bastion someone@buildbox", "", []string{"far"}},
		{"another user on the host", []hostEntry{build}, "SSH other@buildbox", "", append([]string{"buildbox"}, noLink...)},
		{"another port on the host", []hostEntry{build}, "SSH -p 2223 someone@buildbox", "", append([]string{"buildbox"}, noLink...)},
		{"a host that shares a first label", []hostEntry{{"build", "someone@buildbox.lan", nil}}, "SSH someone@buildbox", "", append([]string{"buildbox"}, noLink...)},
		{"a jump host the entry does not have", []hostEntry{build}, "SSH -J bastion someone@buildbox", "", append([]string{"buildbox"}, noLink...)},
		{"no host", []hostEntry{build}, "SSH someone@farbox", "", append([]string{"farbox"}, noLink...)},
		{"an ssh line tuios cannot read", []hostEntry{build}, "SSH -F /dev/null someone@buildbox", "", []string{"ssh command that tuios cannot read"}},
		{"ssh inside ssh", []hostEntry{build, deep}, "FAKE_OSC7=deepbox SSH someone@buildbox", "", []string{"ssh inside ssh", "to deepbox"}},
		{"a tunnel", []hostEntry{build}, "SSH -N -L 18080:localhost:80 someone@buildbox", "", []string{"here"}},
		{"a report with no ssh", []hostEntry{build}, `sh -c 'printf "\033]7;file://buildbox%s\033\\\\" "$PWD"; echo far""-ready; exec sh'`, "", []string{"here"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			remote := remoteMachine(t)
			if tc.allow != "" {
				writeRemoteConfig(t, remote, "[hosts.\"*\"]\nallow = ["+tc.allow+"]\n")
			}
			ssh := resolvingSSH(t, writeFakeSSHTo(t, base, remote))
			writeHosts(t, base, tuiosBin, tc.hosts)
			img := fakeImage()
			env := append(fakeClipboardEnv(t, img), "TUIOS_SSH="+ssh)
			// The far machine's daemon, which writes the dropped file.
			if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
				t.Fatalf("start the far daemon: %v\n%s", err, out)
			}

			term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
			waitBoot(t, term)
			waitForHostListing(t, base, func(s string) bool {
				return containsAll(s, "build", "up")
			}, "the daemon never reported build up")
			newWindow(t, term)
			waitWindowCount(t, term, 1, "opening a shell")
			enterTerminalMode(t, term)
			line := strings.ReplaceAll(tc.line, "SSH", fakeSSHLogin(t))
			runInShell(t, term, line, "far-ready", uiTimeout)

			switch tc.want[0] {
			case "far", "here":
				if err := term.SendKeys("wc -c "); err != nil {
					t.Fatal(err)
				}
				if err := term.SendKeys(tuitest.Ctrl('b'), "V"); err != nil {
					t.Fatal(err)
				}
				if tc.want[0] == "far" {
					checkDropped(t, term, img, remote, base)
					return
				}
				checkPasted(t, term, img, base, remote)
				if files := droppedFiles(t, remote); len(files) != 0 {
					t.Fatalf("ASSERTION: the far machine got the image: %v", files)
				}
			default:
				refusedPasteSays(t, term, base, remote, tc.want)
			}
		})
	}
}

// hostEntry is one [hosts] table of the client's config.
type hostEntry struct {
	name, addr string
	opts       []string
}

// writeHosts writes the client's [hosts] tables. Every one reaches the remote
// stand-in, whatever its address.
func writeHosts(t *testing.T, base, tuiosPath string, hosts []hostEntry) {
	t.Helper()
	dir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	var b strings.Builder
	for _, h := range hosts {
		b.WriteString("[hosts." + h.name + "]\naddr = \"" + h.addr + "\"\ncommand = \"" + tuiosPath + "\"\nconnect_timeout = 5\n")
		if len(h.opts) > 0 {
			b.WriteString("ssh_options = [\"" + strings.Join(h.opts, `", "`) + "\"]\n")
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// resolvingSSH wraps the remote stand-in: ssh -G goes to the real ssh, which
// reads config and reaches no network, and a -J an entry carries is dropped
// before the stand-in, which knows no -J.
func resolvingSSH(t *testing.T, fake string) string {
	t.Helper()
	real, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("no ssh client to resolve with")
	}
	path := filepath.Join(t.TempDir(), "ssh-resolving")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = -G ]; then exec '" + real + "' \"$@\"; fi\n" +
		"skip=0\n" +
		"for a do\n" +
		"  shift\n" +
		"  if [ $skip = 1 ]; then skip=0; continue; fi\n" +
		"  if [ \"$a\" = -J ]; then skip=1; continue; fi\n" +
		"  set -- \"$@\" \"$a\"\n" +
		"done\n" +
		"exec '" + fake + "' \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeSSHLogin writes a script named ssh that stands in for an ssh login. It
// prints an OSC 7 report for $FAKE_OSC7 when that is set, then runs each line
// typed at it, in its own process, so the pane's foreground stays the script.
func fakeSSHLogin(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ssh")
	script := "#!/bin/sh\n" +
		"[ -n \"$FAKE_OSC7\" ] && printf '\\033]7;file://%s%s\\033\\\\' \"$FAKE_OSC7\" \"$PWD\"\n" +
		"echo far\"\"-ready\n" +
		"while printf 'far$ ' && IFS= read -r line; do eval \"$line\"; done\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// droppedFiles lists the pasted images in a machine's drop folders.
func droppedFiles(t *testing.T, base string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "drop", "*", "tuios-paste-*.png"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// checkDropped waits for the shell to print the image's size for the pasted
// path, then checks the file is in the far machine's drop folder alone, with
// the image's bytes and the owner's permissions, and that this machine kept
// no copy.
func checkDropped(t *testing.T, term *tuitest.Terminal, img []byte, remote, base string) {
	t.Helper()
	size := strconv.Itoa(len(img)) + " "
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return showsPastedPath(s.Text())
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: no path was pasted into the pane: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return contains(s.Text(), size)
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the shell never printed the image's size %q for the pasted path: %v\n%s", size, err, term.Snapshot())
	}
	files := droppedFiles(t, remote)
	if len(files) != 1 {
		t.Fatalf("ASSERTION: the far machine holds %d dropped images, want 1: %v\n%s", len(files), files, term.Snapshot())
	}
	// The pane is narrow, so the path wraps. Read it with the frame and the
	// line breaks taken out.
	flat := strings.NewReplacer("│", "", " ", "", "\n", "").Replace(term.Screen().Text())
	if !strings.Contains(flat, filepath.Base(filepath.Dir(files[0]))+"/"+filepath.Base(files[0])) {
		t.Fatalf("ASSERTION: the pasted path is not the far file %s:\n%s", files[0], term.Snapshot())
	}
	got, err := os.ReadFile(files[0])
	if err != nil || !bytes.Equal(got, img) {
		t.Fatalf("the far file holds %d bytes (%v), want the clipboard's %d", len(got), err, len(img))
	}
	if info, err := os.Stat(files[0]); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the far file's mode is %v (%v), want 0600", info.Mode().Perm(), err)
	}
	if left := append(pastedFiles(t, base), droppedFiles(t, base)...); len(left) != 0 {
		t.Errorf("ASSERTION: this machine kept a copy: %v", left)
	}
	saveArtifact(t, term, artifactDir(t), "dropped")
	alive(t, term, "after pasting an image into an ssh shell")
}

// refusedPasteSays pastes into a pane that tuios cannot place, and checks
// that the message holds every phrase, that nothing was typed, and that no
// file was written.
func refusedPasteSays(t *testing.T, term *tuitest.Terminal, base, remote string, phrases []string) {
	t.Helper()
	if err := term.SendKeys("echo pasted:"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "V"); err != nil {
		t.Fatal(err)
	}
	// The dock cuts a long message, so the wait is for its start. The full
	// view below holds the rest.
	toast := "Cannot paste the image."
	if err := term.WaitForText(toast, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: no toast refused the paste: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("$((1+1))", tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return contains(s.Text(), "pasted:2")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: something was typed into the pane: %v\n%s", err, term.Snapshot())
	}
	row, col := findText(t, term, toast)
	mouseClick(t, term, col+2, row, tuitest.MouseLeft, 0)
	// The view wraps the message, so it is read with the frame, the spaces
	// and the line breaks taken out.
	flat := strings.NewReplacer("│", "", " ", "", "\n", "")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := flat.Replace(s.Text())
		for _, p := range phrases {
			if !strings.Contains(text, flat.Replace(p)) {
				return false
			}
		}
		return true
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the message does not say %q: %v\n%s", phrases, err, term.Snapshot())
	}
	dir := artifactDir(t)
	saveArtifact(t, term, dir, "refused")
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "refused")
	for _, m := range []string{remote, base} {
		if files := append(pastedFiles(t, m), droppedFiles(t, m)...); len(files) != 0 {
			t.Fatalf("ASSERTION: a refused paste wrote %v", files)
		}
	}
	alive(t, term, "after a refused paste")
}
