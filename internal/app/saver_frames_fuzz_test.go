package app

import (
	"strings"
	"testing"
)

// FuzzSaverFile checks the frames file parser, which reads a file a plugin
// brings: input nobody here wrote. Ways it could fail, each a seed below:
//
//   - a file that parses draws a frame out of range and panics, as when size
//     comes again after a frame and leaves the earlier frames short;
//   - a small file draws to far more than its own size, through blank rows,
//     the margin or padding to the box;
//   - tuios plugins check passes a file that cannot play.
func FuzzSaverFile(f *testing.F) {
	f.Add([]byte("tuios-saver 1\nsize 2 2\nframe\nab\ncd\nsize 3 3\nframe\nabc\ndef\nghi\n"))
	f.Add([]byte("tuios-saver 1\nsize 2 1\nframe\nab\ncolors\naa\nsize 2 3\nframe\nab\ncd\nef\ncolors\nab\nab\nab\ncolor a red\n"))
	f.Add([]byte("tuios-saver 1\nsize 3 2\ncolor a #ff5f87\ncolor b bright_blue\nframe 200\n╭─╮\n╰─╯\ncolors\nabab\nba\n"))
	f.Add([]byte("tuios-saver 1\nsize 1 1\n" + strings.Repeat("frame\n.\n", 2000)))
	f.Add([]byte("tuios-saver 1\nsize 400 200\n" + strings.Repeat("frame\n"+strings.Repeat("\n", 200), 20)))
	f.Fuzz(func(t *testing.T, data []byte) {
		sf, err := parseSaverFile(data)
		if err != nil {
			return
		}
		checked := CheckSaverFile(data)
		for _, sz := range [][2]int{{80, 24}, {1, 1}, {sf.cols, sf.rows}, {500, 150}} {
			frames, err := sf.render(sz[0], sz[1])
			if err != nil {
				if checked == nil {
					t.Fatalf("check passes a file that does not draw at %dx%d: %v", sz[0], sz[1], err)
				}
				continue
			}
			drawn := 0
			for _, fr := range frames {
				drawn += len(fr)
			}
			// A cell costs at most a colour run of about 22 bytes and a rune
			// of 4, and comes from at least one byte of the file.
			if bound := 32 * len(data); drawn > bound {
				t.Fatalf("a %d byte file draws to %d bytes at %dx%d, more than %d", len(data), drawn, sz[0], sz[1], bound)
			}
			p := &framesSaver{frames: frames, width: sz[0], height: sz[1], cols: min(sf.cols, sz[0]), rows: min(sf.rows, sz[1])}
			for i := range frames {
				if n := strings.Count(p.show(i), "\n") + 1; n != sz[1] {
					t.Fatalf("frame %d shows %d lines on a screen of %d", i, n, sz[1])
				}
			}
		}
	})
}
