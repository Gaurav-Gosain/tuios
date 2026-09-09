package session

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file crossing the socket as bytes: what a stash put from another machine
// sends, and what a get hands back. The store is the same one a local put
// fills, so a file that arrived as bytes is attachable, listed, deduped and
// evicted like any other, and a get serves only what the store holds.

func TestStashPutFromBytesStoresTheFile(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialLink(t, sp)

	content := base64.StdEncoding.EncodeToString([]byte("flame graph bytes"))
	res := result(t, c.call(t, fmt.Sprintf(`{"id":1,"verb":"stash-put","params":{"session":"work","path":"laptop:/tmp/flame.png","content":%q}}`, content)))
	stored, _ := res["path"].(string)
	if stored == "" || !strings.HasSuffix(stored, ".png") {
		t.Fatalf("ASSERTION: the bytes were not stored under the source's extension: %v", res)
	}
	data, err := os.ReadFile(stored)
	if err != nil || string(data) != "flame graph bytes" {
		t.Fatalf("ASSERTION: the stored file does not hold the bytes sent: %q %v", data, err)
	}
	if res["media_type"] != "image/png" || res["kind"] != "image" {
		t.Errorf("the stored file is classified as %v/%v, want image/png", res["media_type"], res["kind"])
	}
	if !d.stash.owns(sess.ID, stored) {
		t.Fatal("ASSERTION: the store does not own the file it just wrote")
	}

	// The listing keeps the sender's label as the source, so a person can
	// see where a file came from.
	list := result(t, c.call(t, `{"id":2,"verb":"stash-list","params":{"session":"work"}}`))
	entries, _ := list["entries"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["source"] != "laptop:/tmp/flame.png" {
		t.Errorf("the listing does not carry the source label: %v", list)
	}

	// And, being stashed, it is the one kind of file a message from another
	// machine may attach.
	result(t, sendJSON(t, c, 3, map[string]any{"session": "work", "text": "see", "attachments": []string{stored}}))
}

func TestStashGetServesOnlyStashedFiles(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialLink(t, sp)

	src := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(src, []byte("hello across"), 0o600); err != nil {
		t.Fatal(err)
	}
	put, err := d.stash.put(sess.ID, src, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := result(t, c.call(t, fmt.Sprintf(`{"id":1,"verb":"stash-get","params":{"session":"work","path":%q}}`, put.Entry.Path)))
	got, _ := base64.StdEncoding.DecodeString(res["content"].(string))
	if string(got) != "hello across" {
		t.Fatalf("ASSERTION: stash-get did not hand back the bytes: %q", got)
	}

	// A file that is not in the stash is not served, however real it is.
	secret := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(secret, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp := c.call(t, fmt.Sprintf(`{"id":2,"verb":"stash-get","params":{"session":"work","path":%q}}`, secret))
	mustRefuse(t, resp, ErrVerbInvalidParams, "stash-get served a file outside the stash")
	// Nor is a stashed file of another session's.
	other := makeSessionWithWindow(t, d, "other")
	put2, err := d.stash.put(other.ID, src, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp = c.call(t, fmt.Sprintf(`{"id":3,"verb":"stash-get","params":{"session":"work","path":%q}}`, put2.Entry.Path))
	mustRefuse(t, resp, ErrVerbInvalidParams, "stash-get served another session's file")
}

func TestStashTransferIsBounded(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	c := dialLink(t, sp)
	big := base64.StdEncoding.EncodeToString(make([]byte, stashTransferMaxBytes+1))
	resp := c.call(t, fmt.Sprintf(`{"id":1,"verb":"stash-put","params":{"session":"work","path":"x:/big.bin","content":%q}}`, big))
	mustRefuse(t, resp, ErrVerbInvalidParams, "content past the transfer cap was stored")
	list := result(t, c.call(t, `{"id":2,"verb":"stash-list","params":{"session":"work"}}`))
	if n, _ := list["total"].(float64); n != 0 {
		t.Fatalf("ASSERTION: the refused content left %v file(s) in the store", n)
	}
}
