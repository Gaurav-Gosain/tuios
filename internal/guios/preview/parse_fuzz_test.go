package preview

import (
	"bytes"
	"testing"
)

// The parsers here read bytes of files the person did not write: a tag a
// download carries, a PPM a tool printed, an EXIF block. A file that lies
// about its lengths must not crash the viewer. The seeds run with the
// ordinary suite; go test -fuzz runs the mutator.

func FuzzTags(f *testing.F) {
	f.Add([]byte("ID3\x03\x00\x00\x00\x00\x00\x20TIT2\x00\x00\x00\x06\x00\x00\x00Quiet"))
	f.Add([]byte("ID3\x04\x00\x00\x00\x00\x00\x30APIC\x00\x00\x00\x20\x00\x00\x00image/png\x00\x03desc\x00\x89PNG"))
	f.Add([]byte("fLaC\x00\x00\x00\x22\x10\x00\x10\x00\x00\x00\x00\x00\x00\x00\x0a\xc4\x42\xf0\x00\x00\x00\x10\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x84\x00\x00\x10\x00\x00\x00\x00\x01\x00\x00\x00"))
	f.Add([]byte("RIFF\x24\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x02\x00\x44\xac\x00\x00\x10\xb1\x02\x00\x04\x00\x10\x00data\x00\x10\x00\x00"))
	f.Add([]byte("\xff\xfb\x90\x64" + string(make([]byte, 64))))
	f.Fuzz(func(t *testing.T, b []byte) {
		fa := Facts{Tags: map[string]string{}}
		var pic []byte
		id3Tags(b, &fa, &pic)
		flacTags(b, &fa, &pic)
		wavFacts(b, &fa)
		mp3Duration(b, int64(len(b)), &fa)
		vorbisComments(b, &fa)
	})
}

func FuzzImageHeaders(f *testing.F) {
	f.Add([]byte("P6\n2 1\n255\n\x00\x00\x00\xff\xff\xff"))
	f.Add([]byte("P6 # comment\n1 1 255 \x01\x02\x03"))
	f.Add([]byte("\xff\xd8\xff\xe1\x00\x16Exif\x00\x00II*\x00\x08\x00\x00\x00\x01\x00\x12\x01"))
	f.Add([]byte("....ftypheic\x00\x00ispe\x00\x00\x00\x00\x00\x00\x04\x00\x00\x00\x03\x00"))
	f.Fuzz(func(t *testing.T, b []byte) {
		if img, err := decodePPM(b); err == nil && img.Bounds().Empty() {
			t.Fatal("decodePPM returned an empty picture without an error")
		}
		if o := exifOrientation(b); o < 1 || o > 8 {
			t.Fatalf("exifOrientation = %d", o)
		}
		Detect("x", b)
		_ = bytes.Index(b, []byte("ispe"))
	})
}

func FuzzListings(f *testing.F) {
	f.Add("-rw-r--r--  0 user group  1234 Nov  4 16:35 dir/a file.txt")
	f.Add("lrwxrwxrwx  0 u g 0 Jan  1  2020 link -> target")
	f.Add("drwxr-xr-x 0 u g 0 Jan 1 2020 d/")
	f.Fuzz(func(t *testing.T, line string) {
		if e, ok := parseTarLine(line); ok && e.Size < 0 {
			t.Fatalf("negative size from %q", line)
		}
	})
}
