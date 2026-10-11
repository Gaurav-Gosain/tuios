package progstatus_test

import (
	"fmt"
	"os"
	"strings"
	"time"

	"tuios.dev/progstatus"
)

// A program reports that it waits for an approval. The sequence goes to the
// terminal, so a program whose output goes to a file writes it to /dev/tty.
func ExampleSequence() {
	seq := progstatus.Sequence(progstatus.Report{
		State: progstatus.Blocked,
		Kind:  progstatus.Permission,
		App:   "deploy",
		Msg:   "Approve deploy to production?",
	})
	fmt.Fprint(os.Stdout, seq)
}

// A program builds a report from text it does not control. FitText and
// FitSegment make each value fit, so the terminal never discards the report.
func ExampleFitText() {
	branch := "feature/new login"
	msg := "Testing " + branch + "\n"
	seq := progstatus.Sequence(progstatus.Report{
		State:    progstatus.Working,
		ID:       progstatus.FitSegment(branch, progstatus.MaxSegment),
		App:      progstatus.FitSegment("ci", progstatus.MaxApp),
		Msg:      progstatus.FitText(msg, progstatus.MaxMsgDecoded),
		Progress: 40,
	})
	fmt.Printf("%q\n", seq)
}

// A terminal emulator reads the body of an OSC 7501 sequence, the bytes after
// "7501;", and keeps the result in a Store. Summary picks the record to show.
func ExampleStore() {
	var store progstatus.Store

	// The sequence as it came from the program, ended with ST (ESC \).
	seq := "\x1b]7501;state=working:app=build:progress=40\x1b\\"
	payload := strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b]"), "\x1b\\")
	body := []byte(strings.TrimPrefix(payload, "7501;"))

	if progstatus.IsQuery(body) {
		// Answer the feature query with the terminator the question used.
		fmt.Print(progstatus.QueryReply + "\x1b\\")
		return
	}
	report, ok := progstatus.Parse(body, progstatus.SequenceLen(len(payload), false))
	if !ok {
		return // discarded: apply nothing
	}
	store.Apply(report, time.Now().UnixNano())

	if rec, ok := progstatus.Summary(store.Records()); ok {
		fmt.Println(rec.EffectiveApp, rec.State, rec.Progress)
	}
}
