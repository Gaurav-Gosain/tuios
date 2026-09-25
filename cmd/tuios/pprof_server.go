package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The --pprof server. It answers the requests net/http/pprof answers that
// tuios is profiled with, over a few lines of HTTP/1.1, so the binary does
// not carry net/http (1.7 MB with its dependencies) for a debugging flag.
//
// Ways it can differ from net/http/pprof, and what covers each:
//   - A client that needs a length or chunked body. Every response closes the
//     connection after the body, which HTTP/1.1 allows for a response with
//     neither; Go's client, curl and go tool pprof read it to the close. The
//     e2e soak, perf and daemon pprof tests read profiles this way.
//   - Keep-alive and pipelining. One request per connection, then close.
//   - Delta profiles (?seconds= on heap, allocs, block, mutex, goroutine),
//     which net/http/pprof builds by merging two profiles. They answer 400
//     with a message saying to take two profiles and diff them with
//     go tool pprof -diff_base. CPU profile and trace take seconds as before.
//   - /debug/pprof/symbol. Not served: profiles written by this runtime carry
//     their symbols, so go tool pprof does not ask for it.
//   - A slow client. The request must arrive within five seconds, as
//     ReadHeaderTimeout was.

// startPprofServer serves the profiles on --pprof when that flag is set.
//
// Block/mutex profiling is sampled, not exhaustive: rate 1 samples every event
// and adds heavy overhead under load, which is not worth it for representative
// contention data. Output is not printed so it cannot corrupt the TUI on stdout.
//
// Every path that runs the TUI calls this, including the daemon-attached one.
// Profiling an attached client is the only way to see the compositor under a
// real multi-pane session, which is where the interesting contention lives.
func startPprofServer() {
	if pprofAddr == "" {
		return
	}
	runtime.SetBlockProfileRate(10000) // one sample per ~10us blocked
	runtime.SetMutexProfileFraction(100)
	ln, err := net.Listen("tcp", pprofAddr)
	if err != nil {
		log.Printf("pprof server error: %v", err)
		return
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				log.Printf("pprof server error: %v", err)
				return
			}
			go servePprof(conn)
		}
	}()
}

// servePprof answers one request on conn and closes it.
func servePprof(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return
	}
	// The headers say nothing this server needs, but they are read so the
	// client is not reset while it is still sending them.
	for {
		h, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if strings.TrimRight(h, "\r\n") == "" {
			break
		}
	}
	_ = conn.SetReadDeadline(time.Time{})

	fields := strings.Fields(line)
	if len(fields) != 3 || !strings.HasPrefix(fields[2], "HTTP/") {
		pprofReply(conn, 400, "text/plain", "malformed request\n")
		return
	}
	if fields[0] != "GET" {
		pprofReply(conn, 405, "text/plain", "only GET is served\n")
		return
	}
	u, err := url.ParseRequestURI(fields[1])
	if err != nil {
		pprofReply(conn, 400, "text/plain", "malformed request\n")
		return
	}
	name, ok := strings.CutPrefix(u.Path, "/debug/pprof/")
	if !ok {
		pprofReply(conn, 404, "text/plain", "404 page not found\n")
		return
	}
	q := u.Query()
	switch name {
	case "":
		pprofReply(conn, 200, "text/plain; charset=utf-8", pprofIndex())
	case "cmdline":
		pprofReply(conn, 200, "text/plain; charset=utf-8", strings.Join(os.Args, "\x00"))
	case "profile":
		sec := pprofSeconds(q, 30)
		pprofStream(conn, "application/octet-stream", func(w io.Writer) error {
			if err := pprof.StartCPUProfile(w); err != nil {
				return err
			}
			time.Sleep(sec)
			pprof.StopCPUProfile()
			return nil
		})
	case "trace":
		sec := pprofSeconds(q, 1)
		pprofStream(conn, "application/octet-stream", func(w io.Writer) error {
			if err := trace.Start(w); err != nil {
				return err
			}
			time.Sleep(sec)
			trace.Stop()
			return nil
		})
	default:
		p := pprof.Lookup(name)
		if p == nil {
			pprofReply(conn, 404, "text/plain", "Unknown profile\n")
			return
		}
		if q.Get("seconds") != "" {
			pprofReply(conn, 400, "text/plain", "delta profiles are not served; take two profiles and compare them with go tool pprof -diff_base\n")
			return
		}
		if name == "heap" && q.Get("gc") != "" && q.Get("gc") != "0" {
			runtime.GC()
		}
		debug, _ := strconv.Atoi(q.Get("debug"))
		ctype := "application/octet-stream"
		if debug != 0 {
			ctype = "text/plain; charset=utf-8"
		}
		pprofStream(conn, ctype, func(w io.Writer) error { return p.WriteTo(w, debug) })
	}
}

// pprofSeconds reads ?seconds=, falling back to def when it is absent or not
// a positive number.
func pprofSeconds(q url.Values, def int) time.Duration {
	sec, err := strconv.ParseFloat(q.Get("seconds"), 64)
	if err != nil || sec <= 0 {
		sec = float64(def)
	}
	return time.Duration(sec * float64(time.Second))
}

// pprofIndex lists the profiles, one per line.
func pprofIndex() string {
	profiles := pprof.Profiles()
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name() < profiles[j].Name() })
	var b strings.Builder
	b.WriteString("count\tprofile\n")
	for _, p := range profiles {
		fmt.Fprintf(&b, "%d\t%s\n", p.Count(), p.Name())
	}
	b.WriteString("\tprofile (CPU, ?seconds=30)\n\ttrace (?seconds=1)\n")
	return b.String()
}

func pprofHeader(w io.Writer, status int, ctype string) {
	text := map[int]string{200: "OK", 400: "Bad Request", 404: "Not Found", 405: "Method Not Allowed", 500: "Internal Server Error"}[status]
	fmt.Fprintf(w, "HTTP/1.1 %d %s\r\nContent-Type: %s\r\nX-Content-Type-Options: nosniff\r\nConnection: close\r\n\r\n", status, text, ctype)
}

func pprofReply(w io.Writer, status int, ctype, body string) {
	pprofHeader(w, status, ctype)
	_, _ = io.WriteString(w, body)
}

// pprofStream writes a profile. It is gathered into memory first so a
// failure can still be answered with an error status.
func pprofStream(w io.Writer, ctype string, write func(io.Writer) error) {
	var buf strings.Builder
	if err := write(&buf); err != nil {
		pprofReply(w, 500, "text/plain", err.Error()+"\n")
		return
	}
	pprofReply(w, 200, ctype, buf.String())
}
