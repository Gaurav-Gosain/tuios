package capture

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func needFontconfig(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("fc-match"); err != nil {
		t.Skip("no fc-match on this machine")
	}
	resetFontCache()
}

// TestALookupRefusesASubstitute is the trap this whole file exists for.
// fc-match never fails: asked for a font nobody has installed it answers with
// its own best guess and reports success, so a lookup that trusts the exit code
// silently draws the capture in Noto Sans.
//
// Negative control: making matchFont accept any answer with a file in it
// resolved "NoSuchFontFamilyXYZ-Regular" to Noto Sans and this failed.
func TestALookupRefusesASubstitute(t *testing.T) {
	needFontconfig(t)
	if face, ok := FontByPostScriptName("NoSuchFontFamilyXYZ-Regular"); ok {
		t.Errorf("a font nobody has resolved to %s (%s)", face.File, face.Name)
	}
	if face, ok := FontByFamily("No Such Font Family XYZ"); ok {
		t.Errorf("a family nobody has resolved to %s (%s)", face.File, face.Name)
	}
}

// TestAFontStackSkipsTheGenericNames checks a CSS stack is read the way a
// browser reads it. "monospace" is a category, not a font, and handing it to
// fontconfig gets whatever that machine calls monospace, which is a
// substitution wearing a different hat.
//
// Negative control: removing the genericFamilies guard resolved the stack
// "No Such Font XYZ, monospace" to the machine's default mono face and failed.
func TestAFontStackSkipsTheGenericNames(t *testing.T) {
	needFontconfig(t)
	if face, ok := FontByFamily("No Such Font XYZ, monospace, sans-serif"); ok {
		t.Errorf("a stack of nothing but generics resolved to %s", face.File)
	}
}

// TestTheHostFontWinsOverTheConfiguredFamily pins the order the spec sets: an
// explicit font_file first, then the font the terminal says it draws with, then
// the configured family.
//
// Negative control: swapping the host and family arms of resolveFontFiles made
// the second case resolve to the configured family's file and failed.
func TestTheHostFontWinsOverTheConfiguredFamily(t *testing.T) {
	needFontconfig(t)
	host, ok := FontByFamily("DejaVu Sans Mono")
	if !ok {
		t.Skip("this machine has no DejaVu Sans Mono to tell the two apart")
	}
	other, ok := FontByFamily("Noto Sans Mono")
	if !ok || other.File == host.File {
		t.Skip("this machine has no second family to tell the two apart")
	}

	s := SettingsFrom(config.ScreenshotConfig{}, "", "")
	s.FontFamily = "Noto Sans Mono"
	s.HostFontFamily = hostPostScriptNameOf(t, host.File)
	if s.HostFontFamily == "" {
		t.Skip("could not read a PostScript name back for the host face")
	}
	regular, _, warns := resolveFontFiles(s)
	if len(regular) == 0 {
		t.Fatalf("no font resolved at all: %v", warns)
	}
	wantSize := fileSize(t, host.File)
	if len(regular) != wantSize {
		t.Errorf("resolved %d bytes, want the host font's %d from %s",
			len(regular), wantSize, host.File)
	}
}

// hostPostScriptNameOf asks fontconfig what PostScript name a file carries, so
// the test can feed a host answer that is real on this machine.
func hostPostScriptNameOf(t *testing.T, file string) string {
	t.Helper()
	out, err := exec.Command("fc-query", file, "-f", "%{postscriptname}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// fileSize is how many bytes a font file has, for saying which one was read
// without the test holding a second copy of it.
func fileSize(t *testing.T, path string) int {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return int(info.Size())
}

// TestOnlyAnAskedForFontIsEmbedded keeps an export the size the user expects.
//
// A capture on kitty is now drawn in the terminal's own font, which is often a
// Nerd Font of two or three megabytes. SVG and HTML inline whatever font the
// frame carries, so without this a kilobyte of SVG quietly became megabytes for
// everyone on a kitty client, and nobody asked for that. Only font_file, which
// is a font the user named, earns the embed.
//
// Negative control: making Frame set EmbedFont whenever a font resolved put the
// host font into the SVG and this failed.
func TestOnlyAnAskedForFontIsEmbedded(t *testing.T) {
	needFontconfig(t)
	face, ok := FontByFamily("DejaVu Sans Mono")
	if !ok {
		t.Skip("this machine has no DejaVu Sans Mono to resolve")
	}
	p, _ := Palette("")

	host := SettingsFrom(config.ScreenshotConfig{}, "", "")
	host.HostFontFamily = hostPostScriptNameOf(t, face.File)
	if host.HostFontFamily == "" {
		t.Skip("could not read a PostScript name back for the host face")
	}
	f, _ := Frame(host, p, false)
	if len(f.FontData) == 0 {
		t.Fatal("the host font did not reach the raster")
	}
	if f.EmbedFont {
		t.Error("a font found by asking the terminal was marked for embedding")
	}

	named := SettingsFrom(config.ScreenshotConfig{FontFile: face.File}, "", "")
	f, _ = Frame(named, p, false)
	if len(f.FontData) == 0 {
		t.Fatal("the named font file did not reach the raster")
	}
	if !f.EmbedFont {
		t.Error("a font the user named by file was not marked for embedding")
	}
}
