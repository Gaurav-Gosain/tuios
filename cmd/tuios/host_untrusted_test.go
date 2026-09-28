package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestAStyledCaptureFromAHostKeepsOnlySGR: --ansi or --resolved from another
// machine keeps colour and style, and nothing else a terminal would act on.
func TestAStyledCaptureFromAHostKeepsOnlySGR(t *testing.T) {
	hostile := "a\x1b[31mred\x1b[0m" + // SGR, kept
		"\x1b]52;c;ZXZpbA==\x07" + // OSC 52 clipboard write, BEL-terminated
		"\x1b]8;;https://evil\x1b\\link\x1b]8;;\x1b\\" + // OSC 8 hyperlink, ST-terminated
		"\x1b[2A\x1b[10;1H\x1b[2K" + // cursor up, cursor position, erase line
		"\x1bP1$qm\x1b\\" + // DCS
		"\x1b[?1049h" + // private mode
		"\x1b[>4;1m" + // an m-final CSI that is not SGR
		"\xc2\x9b2J" + // C1 CSI erase screen
		"\x1b7\x1b8" + // save and restore cursor
		"b\u202Egnp.exe\u200B\n"
	raw, _ := json.Marshal(map[string]any{"content": hostile})
	var buf bytes.Buffer
	if err := printCapture(&buf, raw, "build", "0", true); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "\x1b[31mred\x1b[0m") {
		t.Errorf("ASSERTION: the SGR sequences were lost:\n%q", out)
	}
	for _, bad := range []string{"]52;", "]8;", "\x1b[2A", "\x1b[10;1H", "\x1b[2K", "\x1bP", "\x1b[?1049h", "\x1b[>4;1m", "\xc2\x9b", "\x1b7", "\x1b8", "\x07", "\u202E", "\u200B"} {
		if strings.Contains(out, bad) {
			t.Errorf("ASSERTION: %q from build reached the output under --ansi:\n%q", bad, out)
		}
	}
	if !strings.Contains(out, "linkb") || !strings.Contains(out, "gnp.exe") {
		t.Errorf("ASSERTION: the printable text was lost:\n%q", out)
	}
	// Every ESC left is the start of an SGR sequence.
	for i := strings.Index(out, "\x1b"); i >= 0; i = strings.Index(out[i+1:], "\x1b") + i + 1 {
		n, final, ok := scanCSI(out[i+2:])
		if out[i+1] != '[' || !ok || final != 'm' || !sgrParams(out[i+2:i+2+n-1]) {
			t.Fatalf("ASSERTION: a non-SGR escape survived at %d:\n%q", i, out)
		}
		if strings.Index(out[i+1:], "\x1b") < 0 {
			break
		}
	}
}

// TestAPlainCaptureFromAHostDropsInvisibleCharacters: bidi controls and
// zero-width characters from another machine are removed, so text reads as
// what it is.
func TestAPlainCaptureFromAHostDropsInvisibleCharacters(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"content": "invoice\u202Etxt.exe \u200Bzero\u200Dwidth\u2066iso\u2069 \uFEFFbom\n"})
	var buf bytes.Buffer
	if err := printCapture(&buf, raw, "build", "0", false); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"\u202E", "\u200B", "\u200D", "\u2066", "\u2069", "\uFEFF"} {
		if strings.Contains(buf.String(), bad) {
			t.Errorf("ASSERTION: %U from build reached the output:\n%q", []rune(bad)[0], buf.String())
		}
	}
	if !strings.Contains(buf.String(), "invoicetxt.exe zerowidthiso bom") {
		t.Errorf("ASSERTION: the visible text changed:\n%q", buf.String())
	}
}

// TestAttachToAHostDecidesByTheFarStashRoot: a path under the far session's
// stash root passes through unchanged, whatever this machine says about it.
// Every other path must be a file here, and it is sent and reported.
func TestAttachToAHostDecidesByTheFarStashRoot(t *testing.T) {
	dir := t.TempDir()
	here := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(here, []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("not named"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A far root whose paths fail here with ENOTDIR: a regular file stands
	// where the root's parent directory is.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	farRoot := filepath.Join(blocker, "stash", "sid")
	farNotDir := filepath.Join(farRoot, "abc.png")
	if _, err := os.Stat(farNotDir); !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("setup: stat of the far path gave %v, want ENOTDIR", err)
	}
	// And a far path that exists here too, as it does when both daemons run
	// on one machine. It must not be sent again.
	farExists := filepath.Join(dir, "far", "def.png")
	if err := os.MkdirAll(filepath.Dir(farExists), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(farExists, []byte("far"), 0o600); err != nil {
		t.Fatal(err)
	}

	var puts []map[string]any
	put := func(p map[string]any) (json.RawMessage, error) {
		puts = append(puts, p)
		return json.RawMessage(`{"path":"/far/stash/notes.txt"}`), nil
	}
	var notes bytes.Buffer
	got, err := stashAttachmentsWith([]string{here, farNotDir}, farRoot, "build", &notes, put)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "/far/stash/notes.txt" || got[1] != farNotDir {
		t.Errorf("ASSERTION: attachments = %v, want the stored path, then the far path unchanged", got)
	}
	if notes.String() != "Sent notes.txt to build's stash.\n" {
		t.Errorf("ASSERTION: the note is %q, want one line for the one file sent", notes.String())
	}
	if len(puts) != 1 {
		t.Fatalf("ASSERTION: %d stash puts, want 1 for the one file named here: %v", len(puts), puts)
	}
	if path, _ := puts[0]["path"].(string); !strings.HasSuffix(path, ":"+here) {
		t.Errorf("ASSERTION: the put names %q, want this machine's path %s", path, here)
	}
	if content, _ := puts[0]["content"].(string); content != base64.StdEncoding.EncodeToString([]byte("notes")) {
		t.Errorf("ASSERTION: the put sent %q, not the named file's bytes", content)
	}

	puts = nil
	got, err = stashAttachmentsWith([]string{farExists}, filepath.Dir(farExists), "build", &notes, put)
	if err != nil || len(puts) != 0 || got[0] != farExists {
		t.Errorf("ASSERTION: a far path that also exists here was sent: %v %v %v", got, err, puts)
	}

	// A path outside the far root that is not a file here is refused, not
	// passed on for the far side to guess at.
	if _, err := stashAttachmentsWith([]string{filepath.Join(dir, "missing.txt")}, farRoot, "build", &notes, put); err == nil {
		t.Error("ASSERTION: a path that is neither far nor here was accepted")
	}
	// A path that only looks inside the root is not.
	if _, err := stashAttachmentsWith([]string{farRoot + "/../escape.txt"}, farRoot, "build", &notes, put); err == nil {
		t.Error("ASSERTION: a path that climbs out of the far root passed through")
	}

	big := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(big, make([]byte, stashTransferMaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	puts = nil
	if _, err := stashAttachmentsWith([]string{big}, farRoot, "build", &notes, put); err == nil || len(puts) != 0 {
		t.Errorf("ASSERTION: a file over the cap was not refused before sending: err %v, puts %d", err, len(puts))
	}
	refused := func(map[string]any) (json.RawMessage, error) { return nil, errors.New("stash-put refused by policy") }
	if _, err := stashAttachmentsWith([]string{here}, farRoot, "build", &notes, refused); err == nil {
		t.Error("ASSERTION: a refused stash put did not stop the message")
	}
}

// TestAFarStashPathThisMachineCannotReadPassesThrough is the EACCES half:
// the far path is under a directory this user may not search.
func TestAFarStashPathThisMachineCannotReadPassesThrough(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory this user cannot search")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	farRoot := filepath.Join(locked, "stash", "sid")
	far := filepath.Join(farRoot, "abc.png")
	if _, err := os.Stat(far); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("setup: stat gave %v, want a permission error", err)
	}
	put := func(map[string]any) (json.RawMessage, error) {
		t.Fatal("ASSERTION: a far stash path was sent as a file here")
		return nil, nil
	}
	got, err := stashAttachmentsWith([]string{far}, farRoot, "build", &bytes.Buffer{}, put)
	if err != nil || len(got) != 1 || got[0] != far {
		t.Errorf("ASSERTION: the far path did not pass through: %v %v", got, err)
	}
}

// TestReadForTransferRefusesAFileThatGrows: the size is checked on the open
// file, and the read stops one byte past the cap, so a file that grows past
// the cap between the check and the read is refused.
func TestReadForTransferRefusesAFileThatGrows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "growing.log")
	if err := os.WriteFile(path, []byte("small"), 0o600); err != nil {
		t.Fatal(err)
	}
	afterTransferStat = func(*os.File) {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		if _, err := f.Write(make([]byte, stashTransferMaxBytes)); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { afterTransferStat = nil })
	if _, err := readForTransfer(path); err == nil || !strings.Contains(err.Error(), "capped") {
		t.Fatalf("ASSERTION: a file that grew past the cap after the check was read: %v", err)
	}
}

// TestAHostErrorIsMarkedUntrusted: a --json error another machine answered
// carries host and untrusted. A local error does not.
func TestAHostErrorIsMarkedUntrusted(t *testing.T) {
	far := &session.VerbCallError{Code: session.ErrVerbWindowNotFound, Message: "no window \x1b[2Jnamed x"}
	got := verbErrorJSON((&verbTarget{host: "build"}).explain("capture-pane", far))
	if got["host"] != "build" || got["untrusted"] != true || got["success"] != false {
		t.Errorf("ASSERTION: an error from build reads %v, want host build and untrusted true", got)
	}
	local := verbErrorJSON((&verbTarget{}).explain("capture-pane", far))
	if _, ok := local["untrusted"]; ok {
		t.Errorf("ASSERTION: a local error was marked untrusted: %v", local)
	}
	// The wrapper does not hide what it carries.
	if _, ok := errors.AsType[*session.VerbCallError]((&verbTarget{host: "build"}).explain("capture-pane", far)); !ok {
		t.Error("ASSERTION: the host error no longer unwraps to the verb error")
	}
}

// TestAllHostsListingsMarkTheFarRows: in ls --all-hosts and list-agents
// --all-hosts JSON, each other machine's entry is untrusted and this
// machine's own is not.
func TestAllHostsListingsMarkTheFarRows(t *testing.T) {
	raw := json.RawMessage(`{"type":"host_agents","hosts":[{"host":"local","agents":[]},{"host":"build","agents":[{"name":"x"}]},{"host":"gpu","error":"down"}]}`)
	var got struct {
		Hosts []map[string]any `json:"hosts"`
	}
	if err := json.Unmarshal(markHostRows(raw), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Hosts) != 3 {
		t.Fatalf("the listing lost entries: %v", got.Hosts)
	}
	if _, ok := got.Hosts[0]["untrusted"]; ok {
		t.Errorf("ASSERTION: this machine's entry was marked untrusted: %v", got.Hosts[0])
	}
	for _, h := range got.Hosts[1:] {
		if h["untrusted"] != true {
			t.Errorf("ASSERTION: the entry for %v is not marked untrusted: %v", h["host"], h)
		}
	}
}

// TestStashGetFromAHostIsMarkedUntrusted: the --json answer of stash get
// from another machine carries untrusted; from this one it does not.
func TestStashGetFromAHostIsMarkedUntrusted(t *testing.T) {
	if got := stashGetJSON("flame.png", 3, "build", "/far/x.png"); got["untrusted"] != true || got["host"] != "build" {
		t.Errorf("ASSERTION: stash get from build reads %v, want untrusted true", got)
	}
	if got := stashGetJSON("flame.png", 3, "", "/here/x.png"); got["untrusted"] != nil {
		t.Errorf("ASSERTION: a local stash get was marked untrusted: %v", got)
	}
}
