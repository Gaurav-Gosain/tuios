package transcriptview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const piTestHeader = `{"type":"session","version":3,"id":"s"}` + "\n"

func piTestRecord(id, parent, role, text string) string {
	p := "null"
	if parent != "" {
		p = `"` + parent + `"`
	}
	return `{"type":"message","id":"` + id + `","parentId":` + p + `,"message":{"role":"` + role + `","content":"` + text + `"}}` + "\n"
}

func piTestTexts(t *testing.T, path string) string {
	t.Helper()
	page, err := Read(path, Options{Format: FormatPi})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var texts []string
	for _, e := range page.Entries {
		texts = append(texts, e.Text)
	}
	return strings.Join(texts, "|")
}

// A person pages through a long Pi session, and each page reads the same branch
// of a file that has not changed. The projection is kept for a short time, keyed
// by the file's size and time like a document's records, so a page does not read
// and parse the whole file again, and an append is still seen because it changes
// the size. Only a kept projection can show the old text under an unchanged
// size and time, which is how this tells the cache is used.
func TestPiReadKeepsItsProjectionBySizeAndTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	first := piTestHeader + piTestRecord("a", "", "user", "FIRST-TEXT")
	mod := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	write(first)
	if got := piTestTexts(t, path); got != "FIRST-TEXT" {
		t.Fatalf("first read = %q", got)
	}
	write(strings.Replace(first, "FIRST-TEXT", "OTHER-TEXT", 1))
	if got := piTestTexts(t, path); got != "FIRST-TEXT" {
		t.Fatalf("a file with the same size and time was read again: %q", got)
	}
	write(strings.Replace(first, "FIRST-TEXT", "OTHER-TEXT", 1) + piTestRecord("b", "a", "assistant", "APPENDED-TEXT"))
	if got := piTestTexts(t, path); got != "OTHER-TEXT|APPENDED-TEXT" {
		t.Fatalf("an appended file was not read again: %q", got)
	}
}

// One record past lineMax, such as a huge tool result, costs only itself: it keeps
// its place in the branch, so what follows it still has its parent, and it is left
// out of the view, as the other readers skip a line that long. A file past scanMax
// is refused whole.
func TestPiReadBounds(t *testing.T) {
	dir := t.TempDir()
	t.Run("long record", func(t *testing.T) {
		path := filepath.Join(dir, "long.jsonl")
		huge := piTestRecord("m2", "m1", "toolResult", strings.Repeat("x", lineMax))
		content := piTestHeader + piTestRecord("m1", "", "user", "BEFORE") + huge + piTestRecord("m3", "m2", "assistant", "AFTER")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := piTestTexts(t, path); got != "BEFORE|AFTER" {
			t.Fatalf("entries = %q, want the conversation around the long record", got)
		}
	})
	t.Run("long file", func(t *testing.T) {
		path := filepath.Join(dir, "long-file.jsonl")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		err = f.Truncate(scanMax + 1)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Read(path, Options{Format: FormatPi}); err != ErrTooLarge {
			t.Fatalf("a file past scanMax gave %v, want ErrTooLarge", err)
		}
	})
}
