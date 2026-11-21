package hooks

import (
	"slices"
	"strings"
	"testing"
)

const wantStderrCap = 1024

// TestStderrTailIsBounded is the bound. A hook is a user command and may write
// without limit; keeping all of it would let a hook grow the process that runs
// it. The output arrives in chunks the way a pipe delivers it, or in one piece,
// which is the branch a chunked write never takes. Either way the tail is kept.
func TestStderrTailIsBounded(t *testing.T) {
	if stderrTailLimit != wantStderrCap {
		t.Fatalf("stderrTailLimit is %d, want %d: the cap is the point of this file",
			stderrTailLimit, wantStderrCap)
	}
	chunk := []byte(strings.Repeat("A", 4096))
	for name, writes := range map[string][][]byte{
		"one megabyte in chunks": append(slices.Repeat([][]byte{chunk}, 256), []byte("THE-END")),
		"one oversize write":     {[]byte(strings.Repeat("B", 100000) + "THE-END")},
	} {
		tail := &tailBuffer{limit: stderrTailLimit}
		for _, w := range writes {
			_, _ = tail.Write(w)
		}
		got := tail.String()
		if len(got) > wantStderrCap {
			t.Errorf("%s: kept %d bytes of stderr, want at most %d", name, len(got), wantStderrCap)
		}
		if !strings.HasSuffix(got, "THE-END") {
			t.Errorf("%s: kept the wrong end of the output: %q", name, got[max(0, len(got)-32):])
		}
	}
}

// TestALoudHookIsBoundedEndToEnd drives the bound through a real command, so
// the limit is proved where it is wired and not only where it is implemented.
func TestALoudHookIsBoundedEndToEnd(t *testing.T) {
	m := NewManager()
	m.Register(AfterNewWindow, `awk 'BEGIN{for(i=0;i<10000;i++) printf "line %d of noise\n", i > "/dev/stderr"}'; exit 9`)

	m.Fire(AfterNewWindow, Context{})
	m.Wait()

	st := m.Statuses()[0]
	if st.LastExit != 9 {
		t.Fatalf("last exit = %d, want 9", st.LastExit)
	}
	if len(st.LastError) > wantStderrCap {
		t.Fatalf("last error is %d bytes, want at most %d", len(st.LastError), wantStderrCap)
	}
	if !strings.Contains(st.LastError, "of noise") {
		t.Errorf("last error kept none of the output: %q", st.LastError)
	}
	if !strings.Contains(st.LastError, "line 9999 of noise") {
		t.Errorf("last error kept the head of the output rather than the tail: %q", st.LastError)
	}
}
