package session

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzPreviewZip checks the zip listing that the preview reads from files from
// anywhere. The ways it can fail, written down before the code:
//
//  1. It panics or does not return on some file. The fuzz engine's own
//     per-input timeout covers a hang.
//  2. A listing has more than previewArchiveItems entries, or says it is
//     capped when it holds fewer.
//  3. A name is longer than previewArchiveName bytes, or a cut name is not the
//     start of the real one, ends inside a rune or lacks its "...".
//  4. It returns entries together with an error.
//  5. It disagrees with archive/zip on a file archive/zip reads: an error, or
//     other names, sizes, folder flags or order. Past previewArchiveItems
//     entries it stops by design, so there they are not compared.
//  6. It lists a directory whose length is not the count in a plain end
//     record (one at the very end, with no comment and no zip64 values).
func FuzzPreviewZip(f *testing.F) {
	build := func(prefix []byte, add func(w *zip.Writer)) []byte {
		var b bytes.Buffer
		b.Write(prefix)
		w := zip.NewWriter(&b)
		add(w)
		if err := w.Close(); err != nil {
			f.Fatal(err)
		}
		return b.Bytes()
	}
	file := func(w *zip.Writer, name, body string) {
		fw, err := w.Create(name)
		if err != nil {
			f.Fatal(err)
		}
		if _, err := fw.Write([]byte(body)); err != nil {
			f.Fatal(err)
		}
	}
	le := binary.LittleEndian
	one := build(nil, func(w *zip.Writer) { file(w, "readme.txt", "hello\n") })
	end := len(one) - previewZipEndLen
	dirSize, dirOff := le.Uint32(one[end+12:]), le.Uint32(one[end+16:])

	// An empty zip.
	f.Add(build(nil, func(*zip.Writer) {}))
	// One file.
	f.Add(one)
	// A folder and a file in it.
	f.Add(build(nil, func(w *zip.Writer) {
		if _, err := w.Create("docs/"); err != nil {
			f.Fatal(err)
		}
		file(w, "docs/notes.md", "# notes\n")
	}))
	// A 5000 byte name with a three byte rune across the cut.
	long := strings.Repeat("a", previewArchiveName-len("...")-1) + "世"
	long += strings.Repeat("b", 5000-len(long))
	f.Add(build(nil, func(w *zip.Writer) { file(w, long, "x") }))
	// One entry past the cap.
	f.Add(build(nil, func(w *zip.Writer) {
		for i := range previewArchiveItems + 1 {
			file(w, "e"+strings.Repeat("0", i%7)+string(rune('a'+i%26)), "")
		}
	}))
	// The one-file zip with a zip64 end record and its locator, the plain end
	// record saying to look there.
	z64 := append([]byte(nil), one[:end]...)
	at := uint64(len(z64))
	z64 = le.AppendUint32(z64, 0x06064b50)
	z64 = le.AppendUint64(z64, 44)
	z64 = le.AppendUint16(z64, 45)
	z64 = le.AppendUint16(z64, 45)
	z64 = le.AppendUint32(z64, 0)
	z64 = le.AppendUint32(z64, 0)
	z64 = le.AppendUint64(z64, 1)
	z64 = le.AppendUint64(z64, 1)
	z64 = le.AppendUint64(z64, uint64(dirSize))
	z64 = le.AppendUint64(z64, uint64(dirOff))
	z64 = le.AppendUint32(z64, 0x07064b50)
	z64 = le.AppendUint32(z64, 0)
	z64 = le.AppendUint64(z64, at)
	z64 = le.AppendUint32(z64, 1)
	z64 = le.AppendUint32(z64, 0x06054b50)
	z64 = le.AppendUint32(z64, 0)
	z64 = le.AppendUint16(z64, 0xffff)
	z64 = le.AppendUint16(z64, 0xffff)
	z64 = le.AppendUint32(z64, 0xffffffff)
	z64 = le.AppendUint32(z64, 0xffffffff)
	z64 = le.AppendUint16(z64, 0)
	f.Add(z64)
	// A self-extracting archive: a script before the zip, whose offsets do not
	// count it.
	f.Add(build([]byte("#!/bin/sh\necho self-extracting\nexit 0\n"), func(w *zip.Writer) { file(w, "payload.bin", "data") }))
	// The one-file zip cut inside its central directory.
	f.Add(one[:dirOff+10])
	// An end record that claims one more entry than the directory holds.
	more := append([]byte(nil), one...)
	le.PutUint16(more[end+8:], le.Uint16(more[end+8:])+1)
	le.PutUint16(more[end+10:], le.Uint16(more[end+10:])+1)
	f.Add(more)
	// An end record that understates the directory size, so only a header at the plain offset finds the directory.
	short := append([]byte(nil), one...)
	le.PutUint32(short[end+12:], dirSize-1)
	f.Add(short)
	// A 2 KB comment, which puts the end record beyond the last 1 KB.
	f.Add(build(nil, func(w *zip.Writer) {
		if err := w.SetComment(strings.Repeat("c", 2048)); err != nil {
			f.Fatal(err)
		}
		file(w, "readme.txt", "hello\n")
	}))
	// The one-file zip with its size maxed out and the real size in a zip64 extra field.
	hdr := one[dirOff : dirOff+dirSize]
	n, e := 46+int(le.Uint16(hdr[28:])), int(le.Uint16(hdr[30:]))
	big := append([]byte(nil), one[:dirOff]...)
	big = append(big, hdr[:n+e]...)
	le.PutUint32(big[int(dirOff)+24:], 0xffffffff)
	le.PutUint16(big[int(dirOff)+30:], uint16(e+12))
	big = le.AppendUint16(big, 1)
	big = le.AppendUint16(big, 8)
	big = le.AppendUint64(big, uint64(le.Uint32(hdr[24:])))
	big = append(big, hdr[n+e:]...)
	big = append(big, one[end:]...)
	le.PutUint32(big[len(big)-previewZipEndLen+12:], dirSize+12)
	f.Add(big)

	// One file per worker, rewritten for each input.
	in, err := os.Create(filepath.Join(f.TempDir(), "in.zip"))
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { in.Close() })

	f.Fuzz(func(t *testing.T, data []byte) {
		if err := in.Truncate(0); err != nil {
			t.Fatal(err)
		}
		if _, err := in.WriteAt(data, 0); err != nil {
			t.Fatal(err)
		}
		entries, capped, err := previewZip(in, int64(len(data)))

		if err != nil && len(entries) > 0 {
			t.Fatalf("an error (%v) came with %d entries", err, len(entries))
		}
		if len(entries) > previewArchiveItems {
			t.Fatalf("%d entries, the cap is %d", len(entries), previewArchiveItems)
		}
		if capped && len(entries) != previewArchiveItems {
			t.Fatalf("capped with %d entries", len(entries))
		}
		for _, e := range entries {
			if len(e.Name) > previewArchiveName {
				t.Fatalf("a name of %d bytes", len(e.Name))
			}
		}

		if n := len(data) - previewZipEndLen; err == nil && !capped && n >= 0 &&
			le.Uint32(data[n:]) == 0x06054b50 && le.Uint16(data[n+20:]) == 0 &&
			le.Uint16(data[n+10:]) != 0xffff && le.Uint32(data[n+12:]) != 0xffffffff && le.Uint32(data[n+16:]) != 0xffffffff {
			if want := int(le.Uint16(data[n+10:])); len(entries) != want {
				t.Fatalf("%d entries listed, the end record counts %d", len(entries), want)
			}
		}

		zr, zerr := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if zerr != nil && !errors.Is(zerr, zip.ErrInsecurePath) {
			return
		}
		if len(zr.File) > previewArchiveItems {
			return
		}
		if err != nil {
			t.Fatalf("archive/zip reads %d entries, previewZip fails: %v", len(zr.File), err)
		}
		if capped || len(entries) != len(zr.File) {
			t.Fatalf("%d entries (capped %v), archive/zip reads %d", len(entries), capped, len(zr.File))
		}
		for i, zf := range zr.File {
			got, full := entries[i], zf.Name
			if len(full) <= previewArchiveName {
				if got.Name != full {
					t.Fatalf("entry %d is named %q, archive/zip says %q", i, got.Name, full)
				}
			} else {
				kept, ok := strings.CutSuffix(got.Name, "...")
				if !ok || !strings.HasPrefix(full, kept) || !utf8.RuneStart(full[len(kept)]) {
					t.Fatalf("entry %d: the %d byte name ending %q is not a cut of the %d byte name at a rune start",
						i, len(got.Name), got.Name[max(0, len(got.Name)-8):], len(full))
				}
			}
			if got.Size != int64(zf.UncompressedSize64) || got.Dir != zf.FileInfo().IsDir() {
				t.Fatalf("entry %d %q: size %d dir %v, archive/zip says size %d dir %v",
					i, full, got.Size, got.Dir, zf.UncompressedSize64, zf.FileInfo().IsDir())
			}
		}
	})
}
