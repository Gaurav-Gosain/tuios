package tuie2e

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestScreenshotFindsFontsInALinkedFontsFolder takes a capture with
// screenshot.font_family set, when the user's fonts folder is a symbolic link
// to the folder that holds the font files. Dotfile managers and scratch homes
// make the folder this way. The font index walked the folder with
// filepath.WalkDir, which does not go into a linked root, so it found no
// fonts, the capture fell back to the built-in face, and every icon drew as a
// dotted box.
//
// How this could pass wrongly, written down first:
//   - The font could be found some other way: in a system folder, or through
//     fontconfig, which follows links. The test skips when the font file is
//     in a system folder, and gives fontconfig an empty configuration.
//   - The capture could hold nothing the fallback face draws differently.
//     The pane prints a Nerd Font icon and the test waits for it on screen,
//     with no prompt after it, so both captures hold the same cells.
//   - The walk could follow links and never stop. The store holds a link
//     back to itself, and the test also links a sub-folder inside a real
//     fonts folder.
//
// The check compares the capture, pixel for pixel, with a reference capture
// that names the font file in screenshot.font_file. The PNGs are saved under
// artifactDir.
//
// Negative control: on origin/main, where buildFontIndex walks with
// filepath.WalkDir, both subtests fail: the capture is drawn in the built-in
// face, at another cell size, with the icon as a dotted box.
func TestScreenshotFindsFontsInALinkedFontsFolder(t *testing.T) {
	regular, _ := frameFontPaths()
	if regular == "" {
		t.Skip("JetBrainsMono Nerd Font Mono is not installed; set TUIOS_E2E_FONT")
	}
	for _, sys := range []string{"/usr/share/fonts", "/usr/local/share/fonts", "/Library/Fonts", "/System/Library/Fonts"} {
		if strings.HasPrefix(regular, sys+"/") {
			t.Skipf("the font is in the system folder %s, which the index finds without the link", sys)
		}
	}
	data, err := os.ReadFile(regular)
	if err != nil {
		t.Fatal(err)
	}

	// The reference capture names the font file itself, so it draws in the
	// font whatever the index finds. A capture through the linked folder that
	// found the font is the same picture, pixel for pixel.
	refBase := t.TempDir()
	refHome := filepath.Join(refBase, "home")
	if err := os.MkdirAll(refHome, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, refBase, "[screenshot]\nfont_file = \""+regular+"\"\n")
	reference := captureIcon(t, refBase, []string{"HOME=" + refHome}, "font-link-reference.png")

	for _, layout := range []string{"root", "subfolder"} {
		t.Run(layout, func(t *testing.T) {
			base := t.TempDir()
			home := filepath.Join(base, "home")
			store := filepath.Join(base, "dotfiles", "fonts")
			if err := os.MkdirAll(filepath.Join(store, "nerd"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(store, "nerd", filepath.Base(regular)), data, 0o644); err != nil {
				t.Fatal(err)
			}
			// A loop: a folder that links back to the store above it.
			if err := os.Symlink(store, filepath.Join(store, "nerd", "again")); err != nil {
				t.Fatal(err)
			}
			// The places the index reads on each system: the home's
			// Library/Fonts on macOS, XDG_DATA_HOME/fonts elsewhere.
			for _, dir := range []string{
				filepath.Join(home, "Library", "Fonts"),
				filepath.Join(xdgDir(base, "XDG_DATA_HOME"), "fonts"),
			} {
				if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
					t.Fatal(err)
				}
				switch layout {
				case "root":
					err = os.Symlink(store, dir)
				case "subfolder":
					if err = os.MkdirAll(dir, 0o755); err == nil {
						err = os.Symlink(store, filepath.Join(dir, "linked"))
					}
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			fcConf := filepath.Join(base, "fonts.conf")
			if err := os.WriteFile(fcConf, []byte("<?xml version=\"1.0\"?>\n<fontconfig></fontconfig>\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			writeConfig(t, base, "[screenshot]\nfont_family = \"JetBrainsMono Nerd Font Mono\"\n")
			got := captureIcon(t, base, []string{"HOME=" + home, "FONTCONFIG_FILE=" + fcConf}, "font-link-"+layout+".png")
			if diff := differentPixels(t, reference, got); diff != 0 {
				t.Fatalf("ASSERTION: the capture through the linked fonts folder (%s) is not drawn in JetBrainsMono Nerd Font Mono: %d pixels differ from %s; see %s",
					layout, diff, reference, got)
			}
		})
	}
}

// captureIcon starts a session under base, prints a line with a Nerd Font
// icon in it, holds the screen still, and saves a capture of it as name. It
// returns the path of the PNG.
func captureIcon(t *testing.T, base string, env []string, name string) string {
	t.Helper()
	const session = "e2e-fontlink"
	killDaemon(t, base)
	if out, err := tuiosCLIEnv(t, base, env, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	// sleep keeps the prompt off the screen, so the two captures hold the
	// same cells whatever each shell's prompt is.
	if out, err := tuiosCLIEnv(t, base, env, "send-keys", "-s", session, "-l",
		"clear; printf 'icon \\356\\202\\240 here\\n'; sleep 600\r"); err != nil {
		t.Fatalf("print the icon: %v\n%s", err, out)
	}
	deadline := time.Now().Add(shellTimeout)
	for {
		pane, _ := tuiosCLIEnv(t, base, env, "capture-pane", "-s", session)
		if strings.Contains(pane, "icon \ue0a0 here") && !strings.Contains(pane, "sleep") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never showed the icon alone:\n%s", pane)
		}
		time.Sleep(100 * time.Millisecond)
	}
	out := filepath.Join(artifactDir(t), name)
	if cliOut, err := tuiosCLIEnv(t, base, env, "screenshot", "-s", session, "--format", "png",
		"--frame", "none", "--theme", "catppuccin_mocha", "--out", out, "--no-copy"); err != nil {
		t.Fatalf("tuios screenshot: %v\n%s", err, cliOut)
	}
	return out
}

// differentPixels counts the pixels two PNGs do not share, and counts all
// of both when they are not one size.
func differentPixels(t *testing.T, a, b string) int {
	t.Helper()
	decode := func(path string) image.Image {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		return img
	}
	ia, ib := decode(a), decode(b)
	if ia.Bounds() != ib.Bounds() {
		// Another face has other cell metrics, so the picture is another
		// size: every pixel is a different one.
		return ia.Bounds().Dx()*ia.Bounds().Dy() + ib.Bounds().Dx()*ib.Bounds().Dy()
	}
	diff := 0
	r := ia.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if inkDistance(ia.At(x, y), ib.At(x, y)) != 0 {
				diff++
			}
		}
	}
	return diff
}
