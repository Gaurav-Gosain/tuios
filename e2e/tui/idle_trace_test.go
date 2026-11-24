package tuie2e

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/exp/trace"
)

// tuiosWakeups takes a runtime trace of the client on its --pprof port for d
// and counts the goroutines of tuios code that woke in it: a goroutine that
// went from waiting to runnable with a tuios, Bubble Tea or ultraviolet frame
// on the stack it was waiting on. The runtime's own goroutines (GC, the
// scavenger, the tracer) and the pprof server answering this request are not
// counted. It answers the count and the wait sites, most frequent first.
func tuiosWakeups(t *testing.T, port int, d time.Duration) (int, []string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		t.Fatalf("dial the client's pprof port: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(d + 30*time.Second))
	secs := max(int(d/time.Second), 1)
	if _, err := fmt.Fprintf(conn, "GET /debug/pprof/trace?seconds=%d HTTP/1.1\r\nHost: tuios\r\nConnection: close\r\n\r\n", secs); err != nil {
		t.Fatalf("ask for a trace: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read the trace response: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("trace: %s: %s", resp.Status, body)
	}
	r, err := trace.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("parse the trace: %v", err)
	}
	waitingAt := map[trace.GoID]string{}
	counts := map[string]int{}
	total := 0
	for {
		ev, err := r.ReadEvent()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read a trace event: %v", err)
		}
		if ev.Kind() != trace.EventStateTransition {
			continue
		}
		st := ev.StateTransition()
		if st.Resource.Kind != trace.ResourceGoroutine {
			continue
		}
		id := st.Resource.Goroutine()
		from, to := st.Goroutine()
		switch {
		case to == trace.GoWaiting:
			waitingAt[id] = tuiosWaitSite(st.Stack)
		case from == trace.GoWaiting && to == trace.GoRunnable:
			if site := waitingAt[id]; site != "" {
				counts[site]++
				total++
			}
		}
	}
	sites := make([]string, 0, len(counts))
	for site, n := range counts {
		sites = append(sites, fmt.Sprintf("%d at %s", n, site))
	}
	slices.Sort(sites)
	return total, sites
}

// tuiosWaitSite is the first frame of tuios, Bubble Tea or ultraviolet code
// on a wait stack, empty when there is none or the stack is the pprof
// server's.
func tuiosWaitSite(s trace.Stack) string {
	site := ""
	for f := range s.Frames() {
		fn := f.Func
		if strings.HasPrefix(fn, "main.servePprof") || strings.HasPrefix(fn, "main.pprof") {
			return ""
		}
		if site == "" && (strings.HasPrefix(fn, "github.com/Gaurav-Gosain/tuios/") || strings.HasPrefix(fn, "main.") ||
			strings.HasPrefix(fn, "charm.land/") || strings.HasPrefix(fn, "github.com/charmbracelet/")) {
			site = fmt.Sprintf("%s (%s:%d)", fn, f.File, f.Line)
		}
	}
	return site
}
