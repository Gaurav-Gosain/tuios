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
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

func startPprofServer() {
	if pprofAddr == "" {
		return
	}
	runtime.SetBlockProfileRate(10000)
	runtime.SetMutexProfileFraction(100)
	ln, err := listenPprof(pprofAddr)
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

var (
	cpuProfileMu   sync.Mutex
	cpuProfileFile *os.File
)

func startCPUProfile() error {
	if cpuProfile == "" {
		return nil
	}
	cpuProfileMu.Lock()
	defer cpuProfileMu.Unlock()
	if cpuProfileFile != nil {
		return nil
	}
	f, err := os.Create(cpuProfile)
	if err != nil {
		return errfmt.Errorf("could not create CPU profile: %w", err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		_ = f.Close()
		return errfmt.Errorf("could not start CPU profile: %w", err)
	}
	cpuProfileFile = f
	return nil
}

func stopCPUProfile() {
	cpuProfileMu.Lock()
	defer cpuProfileMu.Unlock()
	if cpuProfileFile == nil {
		return
	}
	pprof.StopCPUProfile()
	if err := cpuProfileFile.Close(); err != nil {
		log.Printf("Warning: failed to close CPU profile file: %v", err)
	}
	cpuProfileFile = nil
}

func listenPprof(addr string) (net.Listener, error) {
	if host, port, err := net.SplitHostPort(addr); err == nil && host == "" {
		addr = net.JoinHostPort("127.0.0.1", port)
	}
	return net.Listen("tcp", addr)
}

func servePprof(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return
	}
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

func pprofSeconds(q url.Values, def int) time.Duration {
	sec, err := strconv.ParseFloat(q.Get("seconds"), 64)
	if err != nil || sec <= 0 {
		sec = float64(def)
	}
	return time.Duration(sec * float64(time.Second))
}

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

func pprofStream(w io.Writer, ctype string, write func(io.Writer) error) {
	var buf strings.Builder
	if err := write(&buf); err != nil {
		pprofReply(w, 500, "text/plain", err.Error()+"\n")
		return
	}
	pprofReply(w, 200, ctype, buf.String())
}
