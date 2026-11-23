package tuie2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRetryHostAnswersForThisMachine: retry-host for "local" is answered the
// way list-host-sessions answers for this machine, with no link to dial: a
// result of type host_retry, host local, status up, with no hosts configured
// and with one. Every other name keeps the answers it had.
//
// How this could pass wrongly, written down first:
//   - A daemon that answers "up" for every name. A name nobody configured
//     and the near misses "Local" and "local " must still be unknown hosts.
//   - A restricted caller served. On a connection restricted to scope own,
//     and on one restricted to scope all, retry-host for local must be
//     forbidden.
//   - The empty name taken for this machine. An empty host and no host must
//     still be invalid params.
//   - A retry for this machine that makes a link dial. The daemon is started
//     with host build and an ssh stand-in that counts its runs and refuses.
//     After its fourth run the link waits about 16 s for its own next dial.
//     Inside that wait the count must not move for 2.5 s, nor for 3 s after
//     two retries for local. A retry for build must then move it within 5 s,
//     so the count is seen to move when a retry is real.
//   - TUIOS_E2E_TIME_SCALE multiplies these holds but not the link's own
//     16 s wait, so above a scale of about 2.9 the link's own dial falls
//     inside them and the test fails on a correct daemon.
//
// Each request and reply line is kept in the artifact retry-host-local.txt.
//
// Negative controls: NEGATIVE_CONTROLS.md, "retry-host answers for this
// machine".
func TestRetryHostAnswersForThisMachine(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "retry-home", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	art := &atArtifact{t: t, name: "retry-host-local.txt"}
	defer art.save()
	sock := hubSocket(base)

	// Phase A: no hosts configured.
	t.Run("local is up with no hosts", func(t *testing.T) {
		c := dialLines(t, sock, art)
		wantLocalUp(t, c.call(`{"id":1,"verb":"retry-host","params":{"host":"local"}}`))
	})
	t.Run("a name nobody configured is an unknown host", func(t *testing.T) {
		c := dialLines(t, sock, art)
		wantVerbError(t, c.call(`{"id":1,"verb":"retry-host","params":{"host":"no-such-box"}}`), "unknown_host")
	})
	t.Run("an empty host is invalid", func(t *testing.T) {
		c := dialLines(t, sock, art)
		wantVerbError(t, c.call(`{"id":1,"verb":"retry-host","params":{"host":""}}`), "invalid_params")
		wantVerbError(t, c.call(`{"id":2,"verb":"retry-host","params":{}}`), "invalid_params")
	})
	t.Run("a restricted caller is refused", func(t *testing.T) {
		for _, scope := range []string{"own", "all"} {
			c := dialLines(t, sock, art)
			r := c.call(`{"id":1,"verb":"restrict-connection","params":{"scope":"` + scope + `"}}`)
			if r.code != "" || r.result == nil {
				t.Fatalf("restrict-connection scope %s was not answered: %s", scope, r.reply)
			}
			wantVerbError(t, c.call(`{"id":2,"verb":"retry-host","params":{"host":"local"}}`), "forbidden")
		}
	})

	// Phase B: host build, dialed through a stand-in that counts its runs.
	requireReal(t, "the daemon runs the ssh stand-in as a process of its own and the test counts its runs")
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}
	// The config is written only once the daemon is gone: a daemon still
	// running would take it as a reload and dial with the real ssh.
	waitUntil(t, "the daemon's socket stops answering after kill-server", uiTimeout, func() (bool, string) {
		conn, err := net.DialTimeout("unix", sock, time.Second)
		if err != nil {
			return true, ""
		}
		_ = conn.Close()
		return false, sock + " still accepts connections"
	})
	writeOneHostConfig(t, base, tuiosBin)
	runs := filepath.Join(t.TempDir(), "ssh-runs")
	ssh := writeCountingSSH(t, filepath.Join(base, "bin"), runs)
	started := time.Now()
	if out, err := tuiosCLIEnv(t, base, []string{"TUIOS_SSH=" + ssh}, "start-server"); err != nil {
		t.Fatalf("start-server: %v\n%s", err, out)
	}
	defer func() { t.Logf("the ssh stand-in ran at %s after start-server", standInRunTimes(runs, started)) }()

	c := dialLines(t, sock, art)
	waitUntil(t, "list-hosts names build", uiTimeout, func() (bool, string) {
		r := c.call(`{"id":1,"verb":"list-hosts"}`)
		return strings.Contains(r.reply, `"host":"build"`), r.reply
	})
	waitUntil(t, "the ssh stand-in has run, so the daemon that answers is the one started with it", uiTimeout, func() (bool, string) {
		n := standInRuns(runs)
		return n >= 1, fmt.Sprintf("%d runs", n)
	})

	t.Run("local is up with a host configured", func(t *testing.T) {
		c := dialLines(t, sock, art)
		wantLocalUp(t, c.call(`{"id":1,"verb":"retry-host","params":{"host":"local"}}`))
	})
	t.Run("a near miss of local is an unknown host", func(t *testing.T) {
		c := dialLines(t, sock, art)
		wantVerbError(t, c.call(`{"id":1,"verb":"retry-host","params":{"host":"Local"}}`), "unknown_host")
		wantVerbError(t, c.call(`{"id":2,"verb":"retry-host","params":{"host":"local "}}`), "unknown_host")
	})
	t.Run("a retry for local dials no link", func(t *testing.T) {
		c := dialLines(t, sock, art)
		count := func() (int, string) {
			n := standInRuns(runs)
			return n, fmt.Sprintf("the ssh stand-in ran %d times, at %s after start-server", n, standInRunTimes(runs, started))
		}
		step := func(when string) {
			_, state := count()
			t.Logf("%s: %s", when, state)
			art.add(t.Name()+", "+when, state)
		}
		waitUntil(t, "the ssh stand-in has run 4 times", 40*time.Second, func() (bool, string) {
			n, state := count()
			return n >= 4, state
		})
		step("after the fourth run")
		stayTrue(t, "the link dials nothing of its own for 2.5 s after its fourth dial", 2500*time.Millisecond, func() (bool, string) {
			n, state := count()
			return n == 4, state
		})
		step("after 2.5 s with no retry")
		wantLocalUp(t, c.call(`{"id":1,"verb":"retry-host","params":{"host":"local"}}`))
		wantLocalUp(t, c.call(`{"id":2,"verb":"retry-host","params":{"host":"local"}}`))
		stayTrue(t, "two retries for local made no link dial", 3*time.Second, func() (bool, string) {
			n, state := count()
			return n == 4, state
		})
		step("3 s after two retries for local")
		r := c.call(`{"id":3,"verb":"retry-host","params":{"host":"build"}}`)
		if r.code != "" || r.result == nil {
			t.Fatalf("ASSERTION: retry-host for build was not answered with a result: %s", r.reply)
		}
		waitUntil(t, "a retry for build makes its link dial", 5*time.Second, func() (bool, string) {
			n, state := count()
			return n > 4, state
		})
		step("after the retry for build")
	})
}

// lineConn is one raw connection to a daemon's socket. Each call writes one
// request line as given and reads one reply line, and both go in the
// artifact.
type lineConn struct {
	t    *testing.T
	conn net.Conn
	br   *bufio.Reader
	art  *atArtifact
}

// lineReply is one reply line and what it carried: a result, or an error
// code.
type lineReply struct {
	request, reply string
	result         map[string]any
	code           string
}

func dialLines(t *testing.T, sock string, art *atArtifact) *lineConn {
	t.Helper()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial the daemon: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &lineConn{t: t, conn: conn, br: bufio.NewReader(conn), art: art}
}

func (c *lineConn) call(request string) lineReply {
	c.t.Helper()
	_ = c.conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := c.conn.Write([]byte(request + "\n")); err != nil {
		c.t.Fatalf("send %s: %v", request, err)
	}
	line, err := c.br.ReadString('\n')
	if err != nil {
		c.t.Fatalf("read the reply to %s: %v", request, err)
	}
	r := lineReply{request: request, reply: strings.TrimSpace(line)}
	c.art.add(c.t.Name(), "> "+r.request+"\n< "+r.reply)
	var env struct {
		Result map[string]any `json:"result"`
		Error  *spVerbErr     `json:"error"`
	}
	if err := json.Unmarshal([]byte(r.reply), &env); err != nil {
		c.t.Fatalf("decode the reply to %s: %v: %s", request, err, r.reply)
	}
	r.result = env.Result
	if env.Error != nil {
		r.code = env.Error.Code
	}
	return r
}

// wantLocalUp is the answer for this machine: a result of type host_retry,
// host local, status up.
func wantLocalUp(t *testing.T, r lineReply) {
	t.Helper()
	if r.code != "" || r.result == nil {
		t.Fatalf("ASSERTION: %s was answered with an error, want a result of type host_retry, host local, status up: %s", r.request, r.reply)
	}
	for key, want := range map[string]string{"type": "host_retry", "host": "local", "status": "up"} {
		if got := r.result[key]; got != want {
			t.Errorf("ASSERTION: %s answered %s %v, want %q: %s", r.request, key, got, want, r.reply)
		}
	}
}

func wantVerbError(t *testing.T, r lineReply, code string) {
	t.Helper()
	if r.code != code {
		t.Errorf("ASSERTION: %s was answered %s, want the error code %s", r.request, r.reply, code)
	}
}

// writeCountingSSH puts an ssh stand-in in dir that adds one line to runs
// each time it runs, then fails the way ssh does against a machine that
// refuses the key. Nothing reaches the network and nothing reads the
// developer's ssh configuration.
func writeCountingSSH(t *testing.T, dir, runs string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ssh := filepath.Join(dir, "counting-ssh")
	script := "#!/bin/sh\ndate +%s.%N >> " + posixQuote(runs) + "\necho 'someone@buildbox: Permission denied (publickey).' >&2\nexit 255\n"
	if err := os.WriteFile(ssh, []byte(script), 0o700); err != nil { //nolint:gosec // an ssh stand-in this test runs
		t.Fatalf("write the ssh stand-in: %v", err)
	}
	return ssh
}

// standInRuns is how many times the counting ssh stand-in ran.
func standInRuns(runs string) int {
	data, _ := os.ReadFile(runs) //nolint:gosec // the test's own file
	return strings.Count(string(data), "\n")
}

// standInRunTimes is each run of the counting ssh stand-in, in seconds after
// since.
func standInRunTimes(runs string, since time.Time) string {
	data, _ := os.ReadFile(runs) //nolint:gosec // the test's own file
	var times []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		secs, err := strconv.ParseFloat(line, 64)
		if err != nil {
			times = append(times, line)
			continue
		}
		at := time.Unix(0, int64(secs*float64(time.Second)))
		times = append(times, fmt.Sprintf("%.2fs", at.Sub(since).Seconds()))
	}
	return "[" + strings.Join(times, " ") + "]"
}
