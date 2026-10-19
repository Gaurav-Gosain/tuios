package transcriptview

// The opencode reader reads a file that holds a password and sends that
// password over the network, so it is a security boundary as well as a
// decoder. Ways it can fail, which the cases below cover:
//   - decode a message wrong or lose a tool's status or diff (the page);
//   - send the password to an address other than 127.0.0.1, or follow a
//     service.json another user could have written (no request is made);
//   - trust a port another process holds now (pid check);
//   - put the password in an error or a page;
//   - put a reported session id into a URL or an argument unchecked;
//   - fail to fall back to the export when the service does not answer.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

const ocSession = "ses_01Fixture"

// ocPassword is made when the test runs, so no secret scanner reads a
// literal password in the source.
var ocPassword = strings.Repeat("x", 8) + strconv.Itoa(os.Getpid())

// fakeOpenCode is a service that answers as opencode 2.0.32 does, from the
// fixture, and counts the requests that reach it.
type fakeOpenCode struct {
	srv  *httptest.Server
	hits atomic.Int32
	pid  int
}

func newFakeOpenCode(t *testing.T, page []byte) *fakeOpenCode {
	t.Helper()
	f := &fakeOpenCode{pid: 4242}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		user, pass, ok := r.BasicAuth()
		if !ok || user != "opencode" || pass != ocPassword {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/api/info":
			fmt.Fprintf(w, `{"version":"2.0.32","pid":%d,"urls":[],"paths":{},"capabilities":{}}`, f.pid)
		case r.URL.Path == "/api/session/"+ocSession+"/message" && r.URL.Query().Get("order") == "desc":
			_, _ = w.Write(page)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// writeService writes a service.json for url with the given mode.
func writeService(t *testing.T, url string, pid int, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "service.json")
	data, _ := json.Marshal(map[string]any{"id": "svc", "version": "2.0.32", "url": url, "pid": pid, "password": ocPassword})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// noOpenCode leaves the PATH with no opencode, so the export cannot run.
func noOpenCode(t *testing.T) { t.Setenv("PATH", t.TempDir()) }

var openCodeWant = []string{
	"user text: Set b to three.",
	"assistant thinking: The value is in app.ts.",
	"assistant text: Editing app.ts.",
	"assistant tool_call edit [/work/project/app.ts] ok diff /work/project/app.ts +1 -1",
	"tool tool_result ok diff /work/project/app.ts +1 -1",
	"assistant tool_call bash [npm test] error",
	"tool tool_result error",
	"assistant todos todowrite ok todos 2",
	"tool tool_result ok",
	"assistant tool_call read [/work/project/README.md] running",
	"assistant text: b is three. One test fails.",
	"assistant error: Rate limit reached.",
	"assistant tool_call shell [git status] ok",
	"tool tool_result ok",
}

func checkOpenCodePage(t *testing.T, page Page) {
	t.Helper()
	var got []string
	for _, e := range page.Entries {
		got = append(got, summary(e))
	}
	if strings.Join(got, "\n") != strings.Join(openCodeWant, "\n") {
		t.Errorf("entries:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(openCodeWant, "\n"))
	}
	out, _ := json.Marshal(page)
	if strings.Contains(string(out), ocPassword) {
		t.Error("the password is in the page")
	}
}

func TestOpenCodeService(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("testdata", "opencode-messages.json"))
	if err != nil {
		t.Fatal(err)
	}
	read := func(path, session string) (Page, error) {
		return Read(path, Options{Format: FormatOpenCode, Session: session})
	}
	refused := func(t *testing.T, f *fakeOpenCode, p Page, err error) {
		t.Helper()
		if err != ErrService || len(p.Entries) != 0 {
			t.Fatalf("got %d entries and %v, want ErrService", len(p.Entries), err)
		}
		if strings.Contains(err.Error(), ocPassword) {
			t.Fatal("the password is in the error")
		}
	}

	t.Run("reads the session", func(t *testing.T) {
		noOpenCode(t)
		f := newFakeOpenCode(t, page)
		p, err := read(writeService(t, f.srv.URL, f.pid, 0o600), ocSession)
		if err != nil {
			t.Fatal(err)
		}
		checkOpenCodePage(t, p)
		after, err := Read(writeService(t, f.srv.URL, f.pid, 0o600), Options{Format: FormatOpenCode, Session: ocSession, After: p.Cursor})
		if err != nil || !after.Reset || len(after.Entries) != len(p.Entries) {
			t.Errorf("a read after the cursor: %d entries, reset %v, err %v; want the newest page again with Reset", len(after.Entries), after.Reset, err)
		}
	})
	t.Run("wrong password", func(t *testing.T) {
		noOpenCode(t)
		f := newFakeOpenCode(t, page)
		path := writeService(t, f.srv.URL, f.pid, 0o600)
		data, _ := os.ReadFile(path)
		_ = os.WriteFile(path, []byte(strings.Replace(string(data), ocPassword, "other", 1)), 0o600)
		p, err := read(path, ocSession)
		refused(t, f, p, err)
	})
	t.Run("a file others can write", func(t *testing.T) {
		noOpenCode(t)
		f := newFakeOpenCode(t, page)
		for _, mode := range []os.FileMode{0o620, 0o602, 0o666} {
			p, err := read(writeService(t, f.srv.URL, f.pid, mode), ocSession)
			refused(t, f, p, err)
		}
		if n := f.hits.Load(); n != 0 {
			t.Fatalf("%d requests reached the service from a file others can write", n)
		}
	})
	t.Run("an address other than 127.0.0.1", func(t *testing.T) {
		noOpenCode(t)
		f := newFakeOpenCode(t, page)
		port := f.srv.URL[strings.LastIndexByte(f.srv.URL, ':'):]
		for _, url := range []string{"http://localhost" + port, "http://[::1]" + port, "https://127.0.0.1" + port, "http://user@127.0.0.1" + port, "http://127.0.0.2" + port} {
			p, err := read(writeService(t, url, f.pid, 0o600), ocSession)
			refused(t, f, p, err)
		}
		if n := f.hits.Load(); n != 0 {
			t.Fatalf("%d requests reached the service at an address other than 127.0.0.1", n)
		}
	})
	t.Run("another process on the port", func(t *testing.T) {
		noOpenCode(t)
		f := newFakeOpenCode(t, page)
		p, err := read(writeService(t, f.srv.URL, f.pid+1, 0o600), ocSession)
		refused(t, f, p, err)
		if n := f.hits.Load(); n != 1 {
			t.Fatalf("%d requests, want only /api/info", n)
		}
	})
	t.Run("a session id that is not one", func(t *testing.T) {
		f := newFakeOpenCode(t, page)
		dir := t.TempDir()
		marker := filepath.Join(dir, "ran")
		fakeExport(t, dir, "touch "+marker)
		for _, id := range []string{"", "../api/info", "ses_a/b", "ses_a?x=1", "ses_" + strings.Repeat("a", 80), "x_123"} {
			p, err := read(writeService(t, f.srv.URL, f.pid, 0o600), id)
			refused(t, f, p, err)
		}
		if n := f.hits.Load(); n != 0 {
			t.Fatalf("%d requests for a session id that is not one", n)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("the export ran for a session id that is not one")
		}
	})
	t.Run("export when no service answers", func(t *testing.T) {
		export, err := filepath.Abs(filepath.Join("testdata", "opencode-export.json"))
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		fakeExport(t, dir, `[ "$*" = "session export --standalone `+ocSession+`" ] || exit 3; cat `+export)
		p, err := read(filepath.Join(t.TempDir(), "service.json"), ocSession)
		if err != nil {
			t.Fatal(err)
		}
		checkOpenCodePage(t, p)
	})
}

// fakeExport puts an opencode on the PATH that runs body.
func fakeExport(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
}
