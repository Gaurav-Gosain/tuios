//go:build unix

package applist

import (
	"os"
	"path/filepath"
	"testing"
)

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

	if got := NewIconFinder("Test").Find("tuios-suffixed.png", 22); got != want {
		t.Errorf("Find = %q, want %q", got, want)
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

	if got := NewIconFinder("Ring").Find("tuios-ring", 22); got == "" {
		t.Error("Find = \"\", want the copy reached through the cycle")
	}
}
