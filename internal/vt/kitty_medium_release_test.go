//go:build unix

package vt

import (
	"os"
	"path/filepath"
	"testing"
)

// TestKittyMediumIsFrame is the security boundary for deleting a shared
// memory object or temp file a pane names. The ways it could fail:
//   - an object of another size is deleted, so a name printed in a pane removes
//     a segment another program of this user still uses;
//   - an object of another user is deleted;
//   - a compressed or PNG frame with no S= is deleted on a guess;
//   - the right frame is kept, which is the leak this check sits in front of.
func TestKittyMediumIsFrame(t *testing.T) {
	dir := t.TempDir()
	file := func(size int) os.FileInfo {
		t.Helper()
		p := filepath.Join(dir, "obj")
		if err := os.WriteFile(p, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	rgba := func(w, h int) *KittyCommand {
		return &KittyCommand{Medium: KittyMediumSharedMemory, Format: KittyFormatRGBA, Width: w, Height: h}
	}
	for _, tc := range []struct {
		name string
		cmd  *KittyCommand
		size int
		want bool
	}{
		{"f=32 right size", rgba(4, 3), 48, true},
		{"f=32 one byte more", rgba(4, 3), 49, false},
		{"f=32 one byte less", rgba(4, 3), 47, false},
		{"f=24 right size", &KittyCommand{Format: KittyFormatRGB, Width: 4, Height: 3}, 36, true},
		{"f=24 sized as f=32", &KittyCommand{Format: KittyFormatRGB, Width: 4, Height: 3}, 48, false},
		{"f=32 with O=", &KittyCommand{Format: KittyFormatRGBA, Width: 2, Height: 2, Offset: 10}, 26, true},
		{"S= given and right", &KittyCommand{Format: KittyFormatRGBA, Width: 4, Height: 3, Size: 20}, 20, true},
		{"S= given and wrong", &KittyCommand{Format: KittyFormatRGBA, Width: 4, Height: 3, Size: 20}, 48, false},
		{"o=z without S=", &KittyCommand{Format: KittyFormatRGBA, Width: 4, Height: 3, Compression: KittyCompressionZlib}, 48, false},
		{"o=z with S=", &KittyCommand{Format: KittyFormatRGBA, Compression: KittyCompressionZlib, Size: 30}, 30, true},
		{"PNG without S=", &KittyCommand{Format: KittyFormatPNG, Width: 4, Height: 3}, 48, false},
		{"PNG with S=", &KittyCommand{Format: KittyFormatPNG, Size: 30}, 30, true},
		{"no size at all", &KittyCommand{Format: KittyFormatRGBA}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := KittyMediumIsFrame(tc.cmd, file(tc.size)); got != tc.want {
				t.Errorf("KittyMediumIsFrame = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("not a regular file", func(t *testing.T) {
		info, err := os.Lstat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if KittyMediumIsFrame(rgba(1, 1), info) {
			t.Error("a directory passed as a frame")
		}
	})

	// A file another user owns takes root to make, so the test plays another
	// user instead. The positive half is the "f=32 right size" case above:
	// the same file passes as this user.
	t.Run("another user", func(t *testing.T) {
		info := file(48)
		if !KittyMediumIsFrame(rgba(4, 3), info) {
			t.Fatal("the right frame did not pass as this user")
		}
		real := kittyUID
		kittyUID = func() int { return real() + 1 }
		defer func() { kittyUID = real }()
		if KittyMediumIsFrame(rgba(4, 3), info) {
			t.Error("a frame owned by another user passed")
		}
	})
}
