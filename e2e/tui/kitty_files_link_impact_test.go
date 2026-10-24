package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestKittyFilesLinkImpact measures what an image read from a file on the far
// machine costs the other panes on the link: their round trip latency and
// their output throughput, with no image moving, while a 20 MB PNG is sent
// again and again by path (t=f, which the far machine reads and the bytes
// cross on a bulk stream), and while the same PNG is streamed inline (t=d,
// what icat sends when files are refused).
//
// It is a measurement, not a check, and it takes a few minutes, so it runs
// only with TUIOS_E2E_LINK_IMPACT=1. TUIOS_E2E_LINK_RATE limits the far
// machine's side of the link to that many MB/s (linkload shape), to stand in
// for a real network. TUIOS_E2E_LINK_MODES picks the loads (file,stream) and
// TUIOS_E2E_LINK_RUNS the rounds. The results are written to link-impact.json in the artifact
// directory and logged.
func TestKittyFilesLinkImpact(t *testing.T) {
	if os.Getenv("TUIOS_E2E_LINK_IMPACT") == "" {
		t.Skip("set TUIOS_E2E_LINK_IMPACT=1 to measure")
	}
	rate := os.Getenv("TUIOS_E2E_LINK_RATE")
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	load := filepath.Join(t.TempDir(), "linkload")
	if out, err := exec.Command("go", "build", "-o", load, "./linkload").CombinedOutput(); err != nil {
		t.Fatalf("build linkload: %v\n%s", err, out)
	}
	if rate != "" {
		raw, err := os.ReadFile(ssh)
		if err != nil {
			t.Fatal(err)
		}
		limited := strings.Replace(string(raw), "exec /bin/sh -c \"$*\"\n", "/bin/sh -c \"$*\" | "+load+" shape "+rate+"\n", 1)
		if err := os.WriteFile(ssh, []byte(limited), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh, "TUIOS_SIXEL_GRAPHICS=0"}
	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("start the far daemon: %v\n%s", err, out)
	}
	// A host terminal that draws kitty graphics and keeps nothing: the
	// images add up to gigabytes over a run.
	sink := &kittySink{probe: make(chan struct{}, 1)}
	term := startIn(t, base, startOpts{cols: 160, rows: 50, args: []string{"new", "home"}, env: env, out: sink})
	answerKittyProbe(t, term, sink.probe, 160, 50)
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool { return containsAll(s, "build", "up") }, "the daemon never reported build up")

	dir := filepath.Join(xdgDir(remote, "HOME"), "pictures")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(dir, "noise.png")
	writeNoisePNG(t, img, 2600, 2000)
	fi, _ := os.Stat(img)
	t.Logf("image: %.1f MB", float64(fi.Size())/(1<<20))

	n := 0
	run := func(args ...string) map[string]any {
		t.Helper()
		n++
		out := filepath.Join(dir, fmt.Sprintf("result-%d.json", n))
		cli := append([]string{"new-window", fmt.Sprintf("probe%d", n), "-s", "home", "--host", "build", "--", load}, args...)
		cli = append(cli, out)
		if o, err := tuiosCLIEnv(t, base, env, cli...); err != nil {
			if os.Getenv("TUIOS_E2E_LINK_HOLD") != "" {
				t.Logf("HOLDING base=%s remote=%s", base, remote)
				time.Sleep(3 * time.Minute)
			}
			t.Fatalf("linkload %v: %v\n%s", args, err, o)
		}
		res, ok := waitResult(out, 5*time.Minute)
		if !ok {
			t.Fatalf("linkload %v wrote no result", args)
		}
		return res
	}
	mb := "64"
	if rate != "" {
		mb = "16"
	}
	measure := func() map[string]any {
		return map[string]any{"latency": run("latency", "1000"), "throughput": run("throughput", mb)}
	}
	rounds := 3
	if v, err := strconv.Atoi(os.Getenv("TUIOS_E2E_LINK_RUNS")); err == nil && v > 0 {
		rounds = v
	}
	modes := []string{"idle", "file", "stream"}
	if v := os.Getenv("TUIOS_E2E_LINK_MODES"); v != "" {
		modes = append([]string{"idle"}, strings.Split(v, ",")...)
	}
	runs := map[string][]map[string]any{}
	for r := range rounds {
		for _, mode := range modes {
			if mode == "idle" {
				runs[mode] = append(runs[mode], measure())
				continue
			}
			stop := filepath.Join(dir, fmt.Sprintf("running-%s-%d", mode, r))
			if err := os.WriteFile(stop, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, fmt.Sprintf("images-%s-%d.json", mode, r))
			if o, err := tuiosCLIEnv(t, base, env, "new-window", fmt.Sprintf("images-%s-%d", mode, r), "-s", "home", "--host", "build", "--no-focus", "--", load, "images", img, mode, stop, out); err != nil {
				t.Fatalf("start the image sender: %v\n%s", err, o)
			}
			time.Sleep(2 * time.Second)
			m := measure()
			_ = os.Remove(stop)
			if res, ok := waitResult(out, 2*time.Minute); ok {
				m["images"] = res
			}
			runs[mode] = append(runs[mode], m)
		}
	}

	// The median of each figure over the rounds, and the change against
	// idle.
	pick := func(m map[string]any, part, key string) float64 {
		sub, _ := m[part].(map[string]any)
		v, _ := sub[key].(float64)
		return v
	}
	median := func(mode, part, key string) float64 {
		var vs []float64
		for _, m := range runs[mode] {
			vs = append(vs, pick(m, part, key))
		}
		slices.Sort(vs)
		return vs[len(vs)/2]
	}
	summary := map[string]any{}
	var lines []string
	for _, mode := range modes {
		row := map[string]float64{
			"throughput_mb_s": median(mode, "throughput", "mb_per_s"),
			"latency_p50_ms":  median(mode, "latency", "p50_ms"),
			"latency_p99_ms":  median(mode, "latency", "p99_ms"),
			"latency_mean_ms": median(mode, "latency", "mean_ms"),
			"images_mb_s":     median(mode, "images", "mb_per_s"),
		}
		summary[mode] = row
		idle := summary["idle"].(map[string]float64)
		lines = append(lines, fmt.Sprintf("%-6s throughput %6.1f MB/s (%+5.1f%%)  rtt p50 %.3f ms (%+5.1f%%)  p99 %.3f ms  mean %.3f ms  images %6.1f MB/s",
			mode, row["throughput_mb_s"], pct(row["throughput_mb_s"], idle["throughput_mb_s"]),
			row["latency_p50_ms"], pct(row["latency_p50_ms"], idle["latency_p50_ms"]),
			row["latency_p99_ms"], row["latency_mean_ms"], row["images_mb_s"]))
	}
	results := map[string]any{"rate": rate, "image_bytes": fi.Size(), "rounds": rounds, "summary": summary, "runs": runs}
	raw, _ := json.MarshalIndent(results, "", "  ")
	_ = os.WriteFile(filepath.Join(artifactDir(t), "link-impact.json"), raw, 0o644)
	t.Logf("link rate %q, %d rounds, medians:\n%s", rate, rounds, strings.Join(lines, "\n"))
}

// kittySink is a host terminal that answers the kitty probe and drops
// everything else.
type kittySink struct{ probe chan struct{} }

func (k *kittySink) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(da1Query)) {
		select {
		case k.probe <- struct{}{}:
		default:
		}
	}
	return len(p), nil
}

// pct is the change from base to v in percent.
func pct(v, base float64) float64 {
	if base == 0 {
		return 0
	}
	return (v - base) / base * 100
}

// waitResult waits for linkload's JSON line at path.
func waitResult(path string, wait time.Duration) (map[string]any, bool) {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(path); err == nil && bytes.HasSuffix(raw, []byte("\n")) {
			var res map[string]any
			if json.Unmarshal(raw, &res) == nil {
				return res, true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, false
}

// writeNoisePNG writes a w by h PNG of random pixels, stored without
// compression so it is as large as its pixels and cannot shrink on the link.
func writeNoisePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewPCG(1, 2))
	for i := range img.Pix {
		img.Pix[i] = byte(r.Uint32())
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.NoCompression}
	if err := enc.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}
