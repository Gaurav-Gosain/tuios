//go:build unix

package session

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
)

// TestFileImageBoundary holds the rules of the image files the daemon reads
// for the thread (transcript_file_image.go). A transcript is written by the
// agent, so each case is one a hostile transcript could try: the daemon must
// read only an image the person pasted into this daemon's paste folder, or an
// image an agent's read tool named, and send no bytes that are not an image.
func TestFileImageBoundary(t *testing.T) {
	root := t.TempDir()
	paste := filepath.Join(root, "paste")
	other := filepath.Join(root, "other")
	for _, d := range []string{paste, other} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAYAAAAECAIAAAAiZtkUAAAAEUlEQVR4nGM4YROFhhjIFQIA6bgg0aJ1P7sAAAAASUVORK5CYII=")
	write := func(p string, b []byte) string {
		t.Helper()
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := write(filepath.Join(paste, "tuios-paste-1.png"), png)
	key := write(filepath.Join(paste, "tuios-paste-key.png"), []byte("-----BEGIN OPENSSH PRIVATE KEY-----\n"))
	elsewhere := write(filepath.Join(other, "tuios-paste-2.png"), png)
	unnamed := write(filepath.Join(paste, "shot.png"), png)
	link := filepath.Join(paste, "tuios-paste-link.png")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(paste, "tuios-paste-fifo.png")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	huge := filepath.Join(paste, "tuios-paste-huge.png")
	f, err := os.Create(huge)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write(png)
	_ = f.Truncate(transcriptview.ImageMax + 1)
	_ = f.Close()
	dir := filepath.Join(paste, "tuios-paste-dir.png")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	prompt := func(id, text string) transcriptview.Entry {
		return transcriptview.Entry{ID: id, Role: "user", Kind: "text", Text: text}
	}
	call := func(id, tool, target string) transcriptview.Entry {
		return transcriptview.Entry{ID: id, Role: "assistant", Kind: "tool_call", Tool: tool, Target: target}
	}
	entries := []transcriptview.Entry{
		prompt("p1", "look at "+good+" please"),
		prompt("p2", "and "+key),
		prompt("p3", "and "+elsewhere),
		prompt("p4", "and "+unnamed),
		prompt("p5", "and "+link),
		prompt("p6", "and "+fifo),
		prompt("p7", "and "+huge),
		prompt("p8", "and "+dir),
		prompt("p9", "and "+paste+"/../other/tuios-paste-2.png"),
		prompt("p10", strings.Repeat(good+" ", 9)),
		call("c1", "view_image", elsewhere),
		call("c2", "view_image", "tuios-paste-2.png"),
		call("c3", "Read", good),
		{ID: "c3r", Role: "tool", Kind: "text", Text: "[image]", Image: &transcriptview.Image{MediaType: "image/png"}},
		call("c4", "Bash", good),
		call("c5", "read_file", key),
	}
	read := func(id string) error {
		_, data, err := readFileImage(entries, paste, id)
		if err == nil && len(data) == 0 {
			return errors.New("no data")
		}
		return err
	}
	page := withFileImages(entries, paste)
	carded := map[string]int{}
	for _, en := range page {
		if rest, ok := strings.CutPrefix(en.ID, fileImagePrefix); ok {
			carded[rest[:strings.LastIndexByte(rest, '.')]]++
		}
	}
	for _, tc := range []struct {
		id    string
		cards int
		read  bool
		why   string
	}{
		{"p1", 1, true, "a PNG the person pasted into this daemon's folder"},
		{"p2", 1, false, "a text file with a PNG name: a card, but no bytes go out"},
		{"p3", 0, false, "a paste name in another folder"},
		{"p4", 0, false, "a file in the paste folder without the paste prefix"},
		{"p5", 0, false, "a link in the paste folder"},
		{"p6", 0, false, "a FIFO, which would block the open"},
		{"p7", 0, false, "a file over ImageMax"},
		{"p8", 0, false, "a folder"},
		{"p9", 0, false, "a path that climbs out of the paste folder"},
		{"p10", fileImagesMax, true, "nine paths in one prompt"},
		{"c1", 1, true, "an absolute path a read tool named"},
		{"c2", 0, false, "a relative path a read tool named"},
		{"c3", 0, false, "a Read that already gave its image"},
		{"c4", 0, false, "a tool that is not a read tool"},
		{"c5", 1, false, "a read tool's text file with a PNG name: no bytes go out"},
	} {
		if carded[tc.id] != tc.cards {
			t.Errorf("%s (%s): %d cards, want %d", tc.id, tc.why, carded[tc.id], tc.cards)
		}
		err := read(fileImagePrefix + tc.id + ".0")
		if tc.read && err != nil {
			t.Errorf("%s (%s): the read failed: %v", tc.id, tc.why, err)
		}
		if !tc.read && !errors.Is(err, transcriptview.ErrNoImage) {
			t.Errorf("%s (%s): the read gave %v, want ErrNoImage", tc.id, tc.why, err)
		}
	}
	// The caller names an entry, never a path, and an index the entry has.
	for _, id := range []string{
		fileImagePrefix + good + ".0",
		fileImagePrefix + "p1.1",
		fileImagePrefix + "p1.-1",
		fileImagePrefix + "p1",
		fileImagePrefix + "nope.0",
		"p1.0",
		fileImagePrefix,
	} {
		if err := read(id); !errors.Is(err, transcriptview.ErrNoImage) {
			t.Errorf("image id %q gave %v, want ErrNoImage", id, err)
		}
	}
	// No paste folder: no prompt names an image.
	if paths := fileImagePaths(entries, 0, ""); len(paths) != 0 {
		t.Errorf("with no paste folder the prompt names %q", paths)
	}
	// The media type is the bytes', not the name's.
	jpg := write(filepath.Join(paste, "tuios-paste-3.jpg"), png)
	if mt, _, err := openFileImage(jpg); err != nil || mt != "image/png" {
		t.Errorf("a PNG named .jpg reads as %q, %v; want image/png", mt, err)
	}
}
