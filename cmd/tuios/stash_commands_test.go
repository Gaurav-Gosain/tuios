package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestStashPutPrintsThePathFirst holds the one output contract the command has.
// The skill tells an agent to read the first line and pass it to --attach, so a
// note printed above the path would break every caller that followed the skill.
func TestStashPutPrintsThePathFirst(t *testing.T) {
	raw := json.RawMessage(`{"path":"/run/user/1000/tuios/stash/s1/abc.png","bytes":2048,"deduped":false,"evicted":0,"evictions":0}`)
	var out bytes.Buffer
	if err := printStashPut(&out, raw); err != nil {
		t.Fatalf("printStashPut: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("printed %d lines, want the path and one note: %q", len(lines), out.String())
	}
	if lines[0] != "/run/user/1000/tuios/stash/s1/abc.png" {
		t.Errorf("the first line is %q, not the stored path", lines[0])
	}
	if !strings.Contains(lines[1], "2.0 KB") {
		t.Errorf("the note does not say the size: %q", lines[1])
	}
}

// TestStashPutSaysWhenItStoredNothing and TestStashPutSaysWhenItDropped keep the
// two things a caller has to be told: that a put was a duplicate, and that older
// files went to make room for it.
func TestStashPutSaysWhenItStoredNothing(t *testing.T) {
	raw := json.RawMessage(`{"path":"/tmp/s/abc.txt","bytes":10,"deduped":true}`)
	var out bytes.Buffer
	if err := printStashPut(&out, raw); err != nil {
		t.Fatalf("printStashPut: %v", err)
	}
	if !strings.Contains(out.String(), "already stored") {
		t.Errorf("a duplicate put does not say so: %q", out.String())
	}
}

func TestStashPutSaysWhenItDropped(t *testing.T) {
	raw := json.RawMessage(`{"path":"/tmp/s/abc.txt","bytes":10,"evicted":3,"evictions":5}`)
	var out bytes.Buffer
	if err := printStashPut(&out, raw); err != nil {
		t.Fatalf("printStashPut: %v", err)
	}
	if !strings.Contains(out.String(), "dropped 3 older file") {
		t.Errorf("the eviction is not reported: %q", out.String())
	}
}

// TestStashListSaysWhatIsProtected checks the listing carries the two facts that
// let a reader predict what goes next: which files a message still names, and
// that everything here dies with the session.
func TestStashListSaysWhatIsProtected(t *testing.T) {
	raw := json.RawMessage(`{"dir":"/tmp/s","total":2,"bytes":2048,"evicted":1,"max_bytes":268435456,"entries":[
		{"path":"/tmp/s/aa.txt","name":"aa.txt","bytes":1024,"media_type":"text/plain","source":"/home/a/one.txt","referenced":true},
		{"path":"/tmp/s/bb.txt","name":"bb.txt","bytes":1024,"media_type":"text/plain","source":"/home/a/two.txt","referenced":false,"missing":true}]}`)
	var out bytes.Buffer
	if err := printStashList(&out, raw); err != nil {
		t.Fatalf("printStashList: %v", err)
	}
	got := out.String()
	for _, want := range []string{"USED", "yes", "MISSING", "/tmp/s", "deleted when the session is killed", "dropped to make room"} {
		if !strings.Contains(got, want) {
			t.Errorf("the listing does not say %q:\n%s", want, got)
		}
	}
}

func TestStashListSaysWhenItIsEmpty(t *testing.T) {
	var out bytes.Buffer
	if err := printStashList(&out, json.RawMessage(`{"dir":"/tmp/s","total":0,"entries":[]}`)); err != nil {
		t.Fatalf("printStashList: %v", err)
	}
	if !strings.Contains(out.String(), "stash put") {
		t.Errorf("the empty listing does not say how to fill it: %q", out.String())
	}
}

// TestStashCommandTreeResolves checks the two subcommands are reachable with the
// arguments the skill shows, since the skill is asserted against this tree.
func TestStashCommandTreeResolves(t *testing.T) {
	for _, args := range [][]string{
		{"stash", "put", "/tmp/flame.png"},
		{"stash", "list"},
		{"stash", "list", "-s", "work"},
	} {
		root := newRootCommand()
		cmd, rest, err := root.Find(args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if err := cmd.ParseFlags(rest); err != nil {
			t.Fatalf("%v: flags rejected: %v", args, err)
		}
		if err := cmd.ValidateArgs(cmd.Flags().Args()); err != nil {
			t.Fatalf("%v: arguments rejected: %v", args, err)
		}
	}
}
