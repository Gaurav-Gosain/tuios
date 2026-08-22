//go:build unix

package applist

import (
	"os"
	"path/filepath"
	"testing"
)

// iconTree builds a throwaway icon hierarchy and points the environment at it,
// so every lookup in these tests sees exactly the themes written here and none
// of the ones installed on the machine running them.
//
// The layout is three themes in the system base directory. Test declares one
// directory per size plus a scalable one and inherits Parent; Parent holds a
// single 24 pixel directory and inherits nothing, which is what forces the walk
// on to hicolor; hicolor holds one 48 pixel directory, so an icon that only
// exists there proves the last themed step was taken.
func iconTree(t *testing.T) (user, sys string) {
	t.Helper()
	root := t.TempDir()
	user = filepath.Join(root, "data")
	sys = filepath.Join(root, "sys")
	t.Setenv("XDG_DATA_HOME", user)
	t.Setenv("XDG_DATA_DIRS", sys)

	writeIconIndex(t, sys, "Test", `[Icon Theme]
Name=Test
Inherits=Parent

Directories=16x16/apps,22x22/apps,32x32/apps,scalable/apps,

[16x16/apps]
Size=16
Type=Fixed

[22x22/apps]
Size=22
Type=Fixed

[32x32/apps]
Size=32
Type=Threshold
Threshold=4

[scalable/apps]
Size=48
MinSize=8
MaxSize=512
Type=Scalable
`)
	writeIconIndex(t, sys, "Parent", `[Icon Theme]
Name=Parent
Directories=24x24/apps

[24x24/apps]
Size=24
Type=Fixed
`)
	writeIconIndex(t, sys, HicolorTheme, `[Icon Theme]
Name=Hicolor
Directories=48x48/apps

[48x48/apps]
Size=48
Type=Fixed
`)
	return user, sys
}

func writeIconIndex(t *testing.T, base, theme, index string) {
	t.Helper()
	dir := filepath.Join(base, "icons", theme)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.theme"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeIcon creates an icon file and returns its path. The contents do not
// matter: nothing here decodes an image, only finds one.
func writeIcon(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(parts...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("icon"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIconFinderAbsolutePath(t *testing.T) {
	iconTree(t)
	direct := writeIcon(t, t.TempDir(), "somewhere", "logo.png")

	f := NewIconFinder("Test")
	if got := f.Find(direct, 20); got != direct {
		t.Errorf("Find(%q) = %q, want the path itself", direct, got)
	}

	missing := filepath.Join(t.TempDir(), "gone.png")
	if got := f.Find(missing, 20); got != "" {
		t.Errorf("Find(%q) = %q, want \"\" for a path that is not there", missing, got)
	}
}

func TestIconFinderAbsoluteSVGSkippedWhenRasterOnly(t *testing.T) {
	iconTree(t)
	vector := writeIcon(t, t.TempDir(), "logo.svg")

	f := NewIconFinder("Test")
	if got := f.Find(vector, 20); got != vector {
		t.Errorf("Find(%q) = %q, want the path itself", vector, got)
	}
	f.RasterOnly = true
	if got := f.Find(vector, 20); got != "" {
		t.Errorf("RasterOnly Find(%q) = %q, want \"\" because the caller cannot decode SVG", vector, got)
	}
}

func TestIconFinderExactSize(t *testing.T) {
	_, sys := iconTree(t)
	want := writeIcon(t, sys, "icons", "Test", "22x22", "apps", "tuios-exact.png")
	writeIcon(t, sys, "icons", "Test", "32x32", "apps", "tuios-exact.png")

	if got := NewIconFinder("Test").Find("tuios-exact", 22); got != want {
		t.Errorf("Find at 22 = %q, want the 22x22 copy %q", got, want)
	}
}

func TestIconFinderNearestSize(t *testing.T) {
	_, sys := iconTree(t)
	want := writeIcon(t, sys, "icons", "Test", "16x16", "apps", "tuios-near.png")
	writeIcon(t, sys, "icons", "Test", "32x32", "apps", "tuios-near.png")

	// At 20 the fixed 16 directory is 4 away while the 32 pixel threshold
	// directory is 12 away, so the smaller art wins even though nothing matches
	// outright.
	if got := NewIconFinder("Test").Find("tuios-near", 20); got != want {
		t.Errorf("Find at 20 = %q, want the nearest copy %q", got, want)
	}
}

func TestIconFinderInheritsFallback(t *testing.T) {
	_, sys := iconTree(t)
	want := writeIcon(t, sys, "icons", "Parent", "24x24", "apps", "tuios-inherited.png")

	if got := NewIconFinder("Test").Find("tuios-inherited", 20); got != want {
		t.Errorf("Find = %q, want the inherited theme's copy %q", got, want)
	}
}

func TestIconFinderHicolorFallback(t *testing.T) {
	_, sys := iconTree(t)
	want := writeIcon(t, sys, "icons", HicolorTheme, "48x48", "apps", "tuios-hicolor.png")

	if got := NewIconFinder("Test").Find("tuios-hicolor", 20); got != want {
		t.Errorf("Find = %q, want the hicolor copy %q", got, want)
	}
}

// TestIconFinderThemeOrder pins the order the themes are tried in, because
// every fallback test above passes just as well if the finder searches them all
// and picks any hit.
func TestIconFinderThemeOrder(t *testing.T) {
	_, sys := iconTree(t)
	want := writeIcon(t, sys, "icons", "Test", "16x16", "apps", "tuios-everywhere.png")
	writeIcon(t, sys, "icons", "Parent", "24x24", "apps", "tuios-everywhere.png")
	writeIcon(t, sys, "icons", HicolorTheme, "48x48", "apps", "tuios-everywhere.png")

	// 24 is the exact size and hicolor's 48 is nearest to nothing, yet the
	// requested theme answers first: the size only decides between directories
	// of one theme, never between themes.
	if got := NewIconFinder("Test").Find("tuios-everywhere", 24); got != want {
		t.Errorf("Find = %q, want the requested theme's copy %q", got, want)
	}
}

func TestIconFinderUserBaseDirWins(t *testing.T) {
	user, sys := iconTree(t)
	writeIcon(t, sys, "icons", "Test", "22x22", "apps", "tuios-local.png")
	want := writeIcon(t, user, "icons", "Test", "22x22", "apps", "tuios-local.png")

	if got := NewIconFinder("Test").Find("tuios-local", 22); got != want {
		t.Errorf("Find = %q, want the user's own copy %q", got, want)
	}
}

func TestIconFinderPixmapsFallback(t *testing.T) {
	_, sys := iconTree(t)
	want := writeIcon(t, sys, "pixmaps", "tuios-pixmap.png")

	if got := NewIconFinder("Test").Find("tuios-pixmap", 20); got != want {
		t.Errorf("Find = %q, want the unthemed pixmaps copy %q", got, want)
	}
}

func TestIconFinderRasterOnlyPrefersWorseSizedPNG(t *testing.T) {
	_, sys := iconTree(t)
	vector := writeIcon(t, sys, "icons", "Test", "scalable", "apps", "tuios-vector.svg")
	raster := writeIcon(t, sys, "icons", "Test", "48x48", "apps", "tuios-vector.png")
	writeIconIndex(t, sys, "Test", `[Icon Theme]
Name=Test
Directories=scalable/apps,48x48/apps

[scalable/apps]
Size=48
MinSize=8
MaxSize=512
Type=Scalable

[48x48/apps]
Size=48
Type=Fixed
`)

	if got := NewIconFinder("Test").Find("tuios-vector", 20); got != vector {
		t.Errorf("Find at 20 = %q, want the scalable copy %q, which is the only exact match", got, vector)
	}

	f := NewIconFinder("Test")
	f.RasterOnly = true
	if got := f.Find("tuios-vector", 20); got != raster {
		t.Errorf("RasterOnly Find at 20 = %q, want the badly sized %q, since the caller has no SVG renderer", got, raster)
	}
}

// TestIconFinderDottedName is a regression test. Most Icon= values on a current
// system are reverse domain names, and taking whatever follows the last dot for
// an extension turns com.visualstudio.code.oss into com.visualstudio.code and
// loses the icon. On this developer's machine that one reading was the
// difference between 81% and 98% of installed applications resolving.
func TestIconFinderDottedName(t *testing.T) {
	_, sys := iconTree(t)
	want := writeIcon(t, sys, "icons", "Test", "22x22", "apps", "com.example.tuios.oss.png")

	if got := NewIconFinder("Test").Find("com.example.tuios.oss", 22); got != want {
		t.Errorf("Find = %q, want %q; the name is not an extension", got, want)
	}
}

func TestIconFinderStripsImageExtension(t *testing.T) {
	_, sys := iconTree(t)
	want := writeIcon(t, sys, "icons", "Test", "22x22", "apps", "tuios-suffixed.png")

	// A relative name with an extension names an icon, not a file, since there
	// is no directory to resolve it against. The extension is dropped and the
	// theme answers.
	if got := NewIconFinder("Test").Find("tuios-suffixed.png", 22); got != want {
		t.Errorf("Find = %q, want %q", got, want)
	}
}

func TestIconFinderMissing(t *testing.T) {
	iconTree(t)
	if got := NewIconFinder("Test").Find("tuios-no-such-icon", 20); got != "" {
		t.Errorf("Find = %q, want \"\" for an icon that is nowhere", got)
	}
	if got := NewIconFinder("Test").Find("", 20); got != "" {
		t.Errorf("Find(\"\") = %q, want \"\"", got)
	}
}

// TestIconFinderNegativeCache proves a miss is remembered. The icon is created
// after the first lookup answers "", so a second lookup that finds it has gone
// back to the filesystem, which is the walk the cache exists to avoid: a miss
// is both the common case for a launcher's list and the only case that visits
// every directory of every theme.
func TestIconFinderNegativeCache(t *testing.T) {
	_, sys := iconTree(t)
	f := NewIconFinder("Test")
	if got := f.Find("tuios-late", 20); got != "" {
		t.Fatalf("Find = %q, want \"\" before the icon exists", got)
	}

	writeIcon(t, sys, "icons", "Test", "22x22", "apps", "tuios-late.png")
	if got := f.Find("tuios-late", 20); got != "" {
		t.Errorf("Find = %q after the miss was cached, so the theme walk ran again", got)
	}
	if got := NewIconFinder("Test").Find("tuios-late", 20); got == "" {
		t.Error("a fresh finder still misses, so the first result was not a cache effect")
	}
}

// TestIconFinderCachesPerSizeAndMode checks the cache key, because a cache that
// answers one size with another size's file is worse than no cache.
func TestIconFinderCachesPerSizeAndMode(t *testing.T) {
	_, sys := iconTree(t)
	small := writeIcon(t, sys, "icons", "Test", "16x16", "apps", "tuios-both.png")
	big := writeIcon(t, sys, "icons", "Test", "32x32", "apps", "tuios-both.png")

	f := NewIconFinder("Test")
	if got := f.Find("tuios-both", 16); got != small {
		t.Fatalf("Find at 16 = %q, want %q", got, small)
	}
	if got := f.Find("tuios-both", 32); got != big {
		t.Errorf("Find at 32 = %q, want %q", got, big)
	}
}

func TestIconFinderInheritCycle(t *testing.T) {
	_, sys := iconTree(t)
	writeIconIndex(t, sys, "Ring", `[Icon Theme]
Name=Ring
Inherits=Ring,Test
Directories=
`)
	writeIcon(t, sys, "icons", "Test", "22x22", "apps", "tuios-ring.png")

	// A theme that inherits itself must be walked once and then left, or the
	// lookup never returns at all.
	if got := NewIconFinder("Ring").Find("tuios-ring", 22); got == "" {
		t.Error("Find = \"\", want the copy reached through the cycle")
	}
}

func TestIconFinderUnknownTheme(t *testing.T) {
	_, sys := iconTree(t)
	want := writeIcon(t, sys, "icons", HicolorTheme, "48x48", "apps", "tuios-orphan.png")

	// A theme named in a config file but not installed must not take hicolor
	// down with it.
	if got := NewIconFinder("NotInstalled").Find("tuios-orphan", 20); got != want {
		t.Errorf("Find = %q, want the hicolor copy %q", got, want)
	}
}

func TestIconFinderScaledDirectory(t *testing.T) {
	_, sys := iconTree(t)
	writeIconIndex(t, sys, "Scaled", `[Icon Theme]
Name=Scaled
Directories=16x16/apps,16x16@2/apps

[16x16/apps]
Size=16
Type=Fixed

[16x16@2/apps]
Size=16
Scale=2
Type=Fixed
`)
	writeIcon(t, sys, "icons", "Scaled", "16x16", "apps", "tuios-scaled.png")
	want := writeIcon(t, sys, "icons", "Scaled", "16x16@2", "apps", "tuios-scaled.png")

	// A Scale=2 directory holds 32 pixel art, and at 30 pixels that is nearer
	// than the 16 pixel copy. A terminal has no scale factor of its own to
	// match against, so the pixels are all there is to go on.
	if got := NewIconFinder("Scaled").Find("tuios-scaled", 30); got != want {
		t.Errorf("Find at 30 = %q, want the 2x copy %q", got, want)
	}
}

func TestIconFinderConcurrent(t *testing.T) {
	_, sys := iconTree(t)
	want := writeIcon(t, sys, "icons", "Test", "22x22", "apps", "tuios-shared.png")

	f := NewIconFinder("Test")
	done := make(chan string, 8)
	for range cap(done) {
		go func() { done <- f.Find("tuios-shared", 22) }()
	}
	for range cap(done) {
		if got := <-done; got != want {
			t.Errorf("concurrent Find = %q, want %q", got, want)
		}
	}
}

func TestCurrentIconTheme(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)

	if got := CurrentIconTheme(); got != HicolorTheme {
		t.Errorf("CurrentIconTheme with no config = %q, want %q", got, HicolorTheme)
	}

	kde := filepath.Join(config, "kdeglobals")
	if err := os.WriteFile(kde, []byte("[Icons]\nTheme=breeze\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CurrentIconTheme(); got != "breeze" {
		t.Errorf("CurrentIconTheme = %q, want breeze from kdeglobals", got)
	}

	gtk := filepath.Join(config, "gtk-3.0")
	if err := os.MkdirAll(gtk, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gtk, "settings.ini"),
		[]byte("[Settings]\ngtk-icon-theme-name=Papirus\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// GTK is read before KDE, so the two disagreeing is settled the same way
	// every time rather than by whichever file happens to be there.
	if got := CurrentIconTheme(); got != "Papirus" {
		t.Errorf("CurrentIconTheme = %q, want Papirus from the GTK settings", got)
	}
}

func TestNewIconFinderDefaultsToHicolor(t *testing.T) {
	if got := NewIconFinder("").Theme(); got != HicolorTheme {
		t.Errorf("NewIconFinder(\"\").Theme() = %q, want %q", got, HicolorTheme)
	}
}
