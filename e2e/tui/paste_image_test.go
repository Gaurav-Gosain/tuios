package tuie2e

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"

	"github.com/Gaurav-Gosain/tuios/internal/shot"
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
		return contains(s.Text(), "tuios-paste-")
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
	if contains(term.Snapshot(), "tuios-paste-") {
		t.Fatalf("ASSERTION: a pasted path reached the pane:\n%s", term.Snapshot())
	}

	// The dock cuts the message. A click on it shows all of it.
	row, col := findText(t, term, toast)
	mouseClick(t, term, col+2, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "does not allow files from this machine.", "Add files to allow in [hosts.", "on build.")
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
