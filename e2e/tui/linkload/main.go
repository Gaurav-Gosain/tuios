// Command linkload measures a pane's output path, and loads it, for the test
// that measures what one pane's images cost the other panes on a link.
//
// Usage:
//
//	linkload latency N OUT      N terminal round trips (DSR 6), one at a time
//	linkload throughput MB OUT  MB of text, then one round trip that waits for it
//	linkload images PATH MODE STOPFILE OUT
//	                            send the PNG at PATH until STOPFILE is gone:
//	                            MODE file names the path (t=f), MODE stream
//	                            sends the bytes (t=d), as icat does in each mode
//	linkload shape MBPS         copy stdin to stdout at MBPS megabytes a
//	                            second, in 4 KiB steps: a link of that speed
//
// Each writes one JSON line of results to OUT and prints LINKLOAD-DONE.
package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "shape" {
		shape(os.Args[2])
		return
	}
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: linkload latency N OUT | throughput MB OUT | images PATH MODE STOPFILE OUT")
		os.Exit(2)
	}
	saved, _ := exec.Command("stty", "-g").CombinedOutput()
	stty("raw", "-echo")
	defer stty(strings.TrimSpace(string(saved)))
	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriterSize(os.Stdout, 64<<10)

	var result any
	var path string
	switch os.Args[1] {
	case "latency":
		n, _ := strconv.Atoi(os.Args[2])
		path = os.Args[3]
		var ds []time.Duration
		for range n {
			t0 := time.Now()
			roundTrip(out, in)
			ds = append(ds, time.Since(t0))
		}
		slices.Sort(ds)
		ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
		var sum time.Duration
		for _, d := range ds {
			sum += d
		}
		result = map[string]any{
			"n": n, "mean_ms": ms(sum / time.Duration(n)),
			"p50_ms": ms(ds[n/2]), "p90_ms": ms(ds[n*9/10]), "p99_ms": ms(ds[n*99/100]), "max_ms": ms(ds[n-1]),
		}
	case "throughput":
		mb, _ := strconv.Atoi(os.Args[2])
		path = os.Args[3]
		line := []byte(strings.Repeat("the quick brown fox jumps over the lazy dog 0123456789 ", 2)[:110] + "\r\n")
		t0 := time.Now()
		for written := 0; written < mb<<20; written += len(line) {
			_, _ = out.Write(line)
		}
		roundTrip(out, in)
		secs := time.Since(t0).Seconds()
		result = map[string]any{"mb": mb, "seconds": secs, "mb_per_s": float64(mb) / secs}
	case "images":
		if len(os.Args) < 6 {
			os.Exit(2)
		}
		img, err := os.ReadFile(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		mode, stop := os.Args[3], os.Args[4]
		path = os.Args[5]
		count, bad := 0, 0
		t0 := time.Now()
		for {
			if _, err := os.Stat(stop); err != nil {
				break
			}
			switch mode {
			case "file":
				fmt.Fprintf(out, "\x1b_Ga=t,i=900,f=100,t=f;%s\x1b\\", base64.StdEncoding.EncodeToString([]byte(os.Args[2])))
			default:
				enc := base64.StdEncoding.EncodeToString(img)
				for i := 0; i < len(enc); i += 4096 {
					end := min(i+4096, len(enc))
					more := 0
					if end < len(enc) {
						more = 1
					}
					if i == 0 {
						fmt.Fprintf(out, "\x1b_Ga=t,i=900,f=100,m=%d;%s\x1b\\", more, enc[i:end])
					} else {
						fmt.Fprintf(out, "\x1b_Gm=%d;%s\x1b\\", more, enc[i:end])
					}
				}
			}
			_ = out.Flush()
			if reply := readAPC(in); !strings.Contains(reply, ";OK") {
				bad++
			}
			count++
		}
		secs := time.Since(t0).Seconds()
		result = map[string]any{"mode": mode, "images": count, "failed": bad, "bytes": len(img), "seconds": secs,
			"mb_per_s": float64(count*len(img)) / (1 << 20) / secs}
	}
	if m, ok := result.(map[string]any); ok {
		// Says the pane really ran on the far machine.
		m["hosted"] = os.Getenv("TUIOS_PANE_HOSTED") == "1"
	}
	line, _ := json.Marshal(result)
	_ = os.WriteFile(path, append(line, '\n'), 0o644)
	_, _ = out.WriteString("\r\nLINKLOAD-DONE\r\n")
	_ = out.Flush()
}

// shape copies stdin to stdout at a fixed rate. It reads what is there, up
// to 4 KiB, and sends it when the rate allows, so it keeps no queue of its
// own beyond one step: what waits, waits in the pipes, as it would in a
// network's buffers.
func shape(arg string) {
	mbps, err := strconv.ParseFloat(arg, 64)
	if err != nil || mbps <= 0 {
		os.Exit(2)
	}
	rate := mbps * (1 << 20)
	buf := make([]byte, 4096)
	start := time.Now()
	var sent float64
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			sent += float64(n)
			due := start.Add(time.Duration(sent / rate * float64(time.Second)))
			if wait := time.Until(due); wait > 0 {
				time.Sleep(wait)
			} else if wait < -50*time.Millisecond {
				// Idle time is not saved up into a burst.
				start = start.Add(-wait - 50*time.Millisecond)
			}
			if _, werr := os.Stdout.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// roundTrip asks for the cursor position and waits for the answer.
func roundTrip(out *bufio.Writer, in *bufio.Reader) {
	_, _ = out.WriteString("\x1b[6n")
	_ = out.Flush()
	for {
		b, err := in.ReadByte()
		if err != nil || b == 'R' {
			return
		}
	}
}

// readAPC reads one graphics reply.
func readAPC(in *bufio.Reader) string {
	var b []byte
	for {
		c, err := in.ReadByte()
		if err != nil {
			return string(b)
		}
		if len(b) == 0 && c != 0x1b {
			continue
		}
		b = append(b, c)
		if bytes.HasSuffix(b, []byte("\x1b\\")) {
			return string(b)
		}
	}
}

func stty(args ...string) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = os.Stdin
	_ = cmd.Run()
}
