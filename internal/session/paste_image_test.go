package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testPNG is the smallest byte string sniffImage takes as a PNG, with a tail
// so two pastes can differ.
func testPNG(tail string) []byte {
	return append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), tail...)
}

func TestSniffImageReadsTheTypeFromTheBytes(t *testing.T) {
	cases := map[string]struct {
		data []byte
		want string
	}{
		"png":          {testPNG("x"), ".png"},
		"jpeg":         {[]byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10}, ".jpg"},
		"gif":          {[]byte("GIF89a\x01\x00"), ".gif"},
		"webp":         {[]byte("RIFF\x10\x00\x00\x00WEBPVP8 "), ".webp"},
		"tiff":         {[]byte("II*\x00\x08\x00"), ".tiff"},
		"shell":        {[]byte("#!/bin/sh\nrm -rf ~\n"), ""},
		"text":         {[]byte("hello"), ""},
		"empty":        {nil, ""},
		"short riff":   {[]byte("RIFF"), ""},
		"bm too short": {[]byte("BM"), ""},
	}
	for name, c := range cases {
		if got := sniffImage(c.data); got != c.want {
			t.Errorf("%s: sniffImage = %q, want %q", name, got, c.want)
		}
	}
}

// newTestPasteStore is a store in a temp directory with a clock the test sets.
func newTestPasteStore(t *testing.T) (*pasteStore, string, *time.Time) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "paste")
	now := time.Now()
	s := &pasteStore{
		dir:     func() (string, error) { return dir, nil },
		now:     func() time.Time { return now },
		ttl:     time.Hour,
		written: map[string]bool{},
	}
	return s, dir, &now
}

func TestAPastedImageIsTheOwnersAloneAndNamedByItsType(t *testing.T) {
	s, dir, _ := newTestPasteStore(t)
	data := testPNG("one")
	path, err := s.save(data)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if filepath.Dir(path) != dir || !strings.HasPrefix(filepath.Base(path), pasteFilePrefix) || filepath.Ext(path) != ".png" {
		t.Errorf("the image was written to %s, want %s/%s*.png", path, dir, pasteFilePrefix)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("the file holds %q (%v), want the pasted bytes", got, err)
	}
	for p, want := range map[string]os.FileMode{path: pasteFilePerm, dir: pasteDirPerm} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s has mode %v, want %v", p, info.Mode().Perm(), want)
		}
	}

	// Two pastes of the same image are two files: a pane that deleted the
	// first must not lose the second.
	again, err := s.save(data)
	if err != nil || again == path {
		t.Errorf("a second paste wrote %s (%v), want a new file", again, err)
	}
}

func TestAPasteThatIsNotAnImageOrTooLargeWritesNothing(t *testing.T) {
	s, dir, _ := newTestPasteStore(t)
	if _, err := s.save([]byte("#!/bin/sh\necho hi\n")); err == nil {
		t.Error("a shell script was written as a pasted image")
	}
	big := append(testPNG(""), make([]byte, pasteImageMaxBytes)...)
	if _, err := s.save(big); err == nil {
		t.Error("an image over the limit was written")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a refused paste left %d files behind", len(entries))
	}
}

func TestPastedImagesGoAfterTheirTTLAndWhenTheDaemonStops(t *testing.T) {
	s, dir, now := newTestPasteStore(t)
	old, err := s.save(testPNG("old"))
	if err != nil {
		t.Fatal(err)
	}
	// A file somebody else put in the directory is not the store's to take.
	foreign := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * time.Hour)
	for _, p := range []string{old, foreign} {
		if err := os.Chtimes(p, stale, stale); err != nil {
			t.Fatal(err)
		}
	}

	fresh, err := s.save(testPNG("fresh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("an image past its TTL is still there after the next paste: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Errorf("the sweep took a file that is not a pasted image: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("the new image is gone: %v", err)
	}

	// The clock moving on is what a later daemon start sees.
	*now = now.Add(2 * time.Hour)
	s.sweep()
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Errorf("sweep kept an image past its TTL: %v", err)
	}

	last, err := s.save(testPNG("last"))
	if err != nil {
		t.Fatal(err)
	}
	s.removeWritten()
	if _, err := os.Stat(last); !os.IsNotExist(err) {
		t.Errorf("a stop left the image it wrote: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Errorf("a stop took a file it did not write: %v", err)
	}
}

func pasteParams(session, window, nonce string, data []byte) map[string]any {
	p := map[string]any{"session": session, "content": base64.StdEncoding.EncodeToString(data)}
	if window != "" {
		p["window"] = window
	}
	if nonce != "" {
		p["human_nonce"] = nonce
	}
	return p
}

func callPaste(t *testing.T, c *verbConn, verb string, params map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"id": 1, "verb": verb, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	return c.call(t, string(raw))
}

// TestPasteImageIsThePersonsAct: only the attached client's nonce, for the
// session the pane is in, writes an image. Anything else writes nothing.
func TestPasteImageIsThePersonsAct(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "work")
	makeSessionWithWindow(t, d, "other")
	conn := dialVerb(t, sp)
	tui := attachTUI(t, sp, "work")
	other := attachTUI(t, sp, "other")
	data := testPNG("from the clipboard")

	res := result(t, callPaste(t, conn, "paste-image", pasteParams("work", a, tui.HumanNonce(), data)))
	path, _ := res["path"].(string)
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("paste-image answered %v and the file holds %q (%v)", res, got, err)
	}
	if want := filepath.Join(filepath.Dir(sp), "paste"); filepath.Dir(path) != want {
		t.Errorf("the image went to %s, want the paste directory beside the socket, %s", path, want)
	}

	for name, nonce := range map[string]string{
		"no nonce":                     "",
		"a wrong nonce":                "00112233445566778899aabbccddeeff",
		"a nonce from another session": other.HumanNonce(),
	} {
		if code := errCode(t, callPaste(t, conn, "paste-image", pasteParams("work", a, nonce, data))); code != ErrVerbNotHuman {
			t.Errorf("paste-image with %s: %s, want %s", name, code, ErrVerbNotHuman)
		}
	}

	for name, content := range map[string][]byte{
		"a script":  []byte("#!/bin/sh\necho owned\n"),
		"too large": append(testPNG(""), make([]byte, pasteImageMaxBytes)...),
	} {
		if code := errCode(t, callPaste(t, conn, "paste-image", pasteParams("work", a, tui.HumanNonce(), content))); code != ErrVerbInvalidParams {
			t.Errorf("paste-image of %s: %s, want %s", name, code, ErrVerbInvalidParams)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("the paste directory holds %d files, want the one accepted paste", len(entries))
	}
}

// TestAPastedImageForAPaneOnAnotherMachineIsWrittenThere drives the far half
// through a hosted pane: the owner's paste crosses on its own connection and
// the far daemon writes it, and only the owner's token is taken.
func TestAPastedImageForAPaneOnAnotherMachineIsWrittenThere(t *testing.T) {
	_, socketPath := startTestDaemon(t)
	fed := &socketFederation{socketPath: socketPath}
	p := openTestPane(t, fed, hostedPaneSpec{Width: 80, Height: 24, Command: []string{"/bin/sh"}, Window: "w1"})
	if p.callsToken == "" {
		t.Fatal("the far daemon gave the pane no token")
	}
	data := testPNG("for the far pane")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path, err := p.pasteImage(ctx, data)
	if err != nil {
		t.Fatalf("paste to the far pane: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("the far file %s holds %q (%v), want the pasted bytes", path, got, err)
	}

	conn := dialVerb(t, socketPath)
	for name, token := range map[string]string{"no token": "", "a wrong token": "not-the-token"} {
		params := map[string]any{"pane": p.id, "content": base64.StdEncoding.EncodeToString(data)}
		if token != "" {
			params["token"] = token
		}
		resp := callPaste(t, conn, "paste-pane-image", params)
		if code := errCode(t, resp); code != ErrVerbForbidden && code != ErrVerbInvalidParams {
			t.Errorf("paste-pane-image with %s: %s, want it refused", name, code)
		}
	}

	// A pane from a daemon that gave no token cannot be pasted into.
	old := &remotePane{host: p.host, id: p.id, fed: p.fed}
	if _, err := old.pasteImage(ctx, data); err == nil {
		t.Error("a pane with no token took a paste")
	}
}
