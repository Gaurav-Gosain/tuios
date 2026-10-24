// Command shardplan splits the end-to-end suite across CI shards by measured
// time, so no shard runs into the go test timeout.
//
// The split used to be a hash of the test name. A hash balances the count of
// tests, not their time, and a few tests take minutes: one shard of seven ran
// 1054 seconds while another ran 525, and the long one crossed the 18 minute
// timeout. shardplan packs the tests by the durations in durations.txt instead,
// largest first onto the lightest shard, which keeps every shard within a few
// seconds of the others.
//
// Usage:
//
//	go test -list '^Test' ./... | shardplan -shards 9 -shard 3
//	    prints the tests of shard 3, one per line.
//	go test -list '^Test' ./... | shardplan -shards 9 -max 11m
//	    prints the estimated time of each shard and fails if one is over.
//	testselect ... | shardplan -shards 9 -shard 3 -fill 8m
//	    the same, but packs the tests into only as many shards as they need
//	    at about 8 minutes each. The other shards print nothing.
//	shardplan -update LOG...
//	    rewrites durations.txt from go test -v output, such as gh run view --log.
//
// A test missing from durations.txt is estimated at the mean of the known
// tests. Every shard computes the same plan from the same list and file, so
// the shards together run each test exactly once.
package main

import (
	"bufio"
	"cmp"
	"flag"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

func main() {
	durations := flag.String("durations", "shardplan/durations.txt", "file of measured test durations")
	shards := flag.Int("shards", 0, "number of shards")
	shard := flag.Int("shard", 0, "print the tests of this shard (1-based)")
	limit := flag.Duration("max", 0, "fail if a shard's estimated time is over this")
	fill := flag.Duration("fill", 0, "use only as many shards as the tests need at about this much each")
	update := flag.Bool("update", false, "rewrite the durations file from the go test -v logs given as arguments")
	flag.Parse()

	if *update {
		if err := writeDurations(*durations, flag.Args()); err != nil {
			fail(err)
		}
		return
	}
	if *shards < 1 || (*shard == 0) == (*limit == 0) || *shard > *shards {
		fail(fmt.Errorf("give -shards and one of -shard (1 to %d) or -max", *shards))
	}

	known, err := readDurations(*durations)
	if err != nil {
		fail(err)
	}
	names, err := readNames(os.Stdin)
	if err != nil {
		fail(err)
	}
	if len(names) == 0 {
		fail(fmt.Errorf("no test names on stdin: run go test -list with TUIOS_E2E set"))
	}
	used := *shards
	if *fill > 0 && *shard > 0 {
		used = shardsNeeded(names, known, *fill, *shards)
	}
	plan := pack(names, known, used)

	if *shard > 0 {
		if *shard > used {
			return
		}
		for _, name := range plan[*shard-1].names {
			fmt.Println(name)
		}
		return
	}

	over := false
	for i, s := range plan {
		mark := ""
		if s.load > limit.Seconds() {
			mark = "  over the limit"
			over = true
		}
		fmt.Printf("shard %d of %d: %3d tests, about %s%s\n", i+1, *shards, len(s.names),
			time.Duration(s.load*float64(time.Second)).Round(time.Second), mark)
	}
	missing := 0
	for _, name := range names {
		if _, ok := known[name]; !ok {
			missing++
		}
	}
	if missing > 0 {
		fmt.Printf("%d tests have no measured time and count as the mean.\n", missing)
	}
	if over {
		fmt.Printf("A shard is over %s. Add a shard in .github/workflows/e2e-tui.yml, "+
			"or refresh e2e/tui/shardplan/durations.txt (see its header).\n", *limit)
		os.Exit(1)
	}
}

// shardsNeeded is how many shards hold the tests at about fill each, at least
// one and at most shards. A selection of a few tests then starts a few
// runners, not all of them.
func shardsNeeded(names []string, known map[string]float64, fill time.Duration, shards int) int {
	total := 0.0
	for _, s := range pack(names, known, 1) {
		total += s.load
	}
	n := int(math.Ceil(total / fill.Seconds()))
	return max(1, min(n, shards))
}

type shardLoad struct {
	names []string
	load  float64
}

// pack assigns the longest test first to the shard with the least time so
// far. The order is fixed by time and then name, so the plan depends only on
// its inputs.
func pack(names []string, known map[string]float64, shards int) []shardLoad {
	fallback := 1.0
	if len(known) > 0 {
		sum := 0.0
		for _, d := range known {
			sum += d
		}
		fallback = sum / float64(len(known))
	}
	estimate := func(name string) float64 {
		if d, ok := known[name]; ok {
			return d
		}
		return fallback
	}

	order := slices.Clone(names)
	slices.SortFunc(order, func(a, b string) int {
		return cmp.Or(cmp.Compare(estimate(b), estimate(a)), cmp.Compare(a, b))
	})
	plan := make([]shardLoad, shards)
	for _, name := range order {
		lightest := 0
		for i := range plan {
			if plan[i].load < plan[lightest].load {
				lightest = i
			}
		}
		plan[lightest].names = append(plan[lightest].names, name)
		plan[lightest].load += estimate(name)
	}
	for i := range plan {
		slices.Sort(plan[i].names)
	}
	return plan
}

func readNames(r io.Reader) ([]string, error) {
	var names []string
	scan := bufio.NewScanner(r)
	for scan.Scan() {
		if line := strings.TrimSpace(scan.Text()); strings.HasPrefix(line, "Test") {
			names = append(names, line)
		}
	}
	slices.Sort(names)
	return slices.Compact(names), scan.Err()
}

func readDurations(path string) (map[string]float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read durations: %w", err)
	}
	known := map[string]float64{}
	for n, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, secs, ok := strings.Cut(line, " ")
		d, err := strconv.ParseFloat(strings.TrimSpace(secs), 64)
		if !ok || err != nil {
			return nil, fmt.Errorf("%s:%d: want \"TestName seconds\", got %q", path, n+1, line)
		}
		known[name] = d
	}
	return known, nil
}

// result matches a top-level test result. A subtest's line is indented, so
// the "--- " has to open the line or follow the timestamp gh run view --log
// puts in front of it.
var result = regexp.MustCompile(`(?:^|\dZ )--- (?:PASS|FAIL|SKIP): (Test\w+) \(([0-9.]+)s\)`)

const header = `# Measured run time in seconds of each end-to-end test on a GitHub runner,
# the mean over the runs it was refreshed from. shardplan packs the shards of
# .github/workflows/e2e-tui.yml by these numbers. Refresh it when the shard
# check fails or the shards drift apart:
#
#   cd e2e/tui
#   for id in $(gh run list -w e2e-tui.yml -b main -s success -L 10 --json databaseId -q '.[].databaseId'); do
#     gh run view "$id" --log > "/tmp/e2e-$id.log"
#   done
#   go run ./shardplan -update /tmp/e2e-*.log
`

func writeDurations(path string, logs []string) error {
	if len(logs) == 0 {
		return fmt.Errorf("give the go test -v logs to read")
	}
	sum := map[string]float64{}
	count := map[string]int{}
	for _, log := range logs {
		f, err := os.Open(log)
		if err != nil {
			return err
		}
		scan := bufio.NewScanner(f)
		scan.Buffer(make([]byte, 1<<20), 1<<20)
		for scan.Scan() {
			m := result.FindStringSubmatch(scan.Text())
			if m == nil {
				continue
			}
			d, err := strconv.ParseFloat(m[2], 64)
			if err != nil {
				continue
			}
			sum[m[1]] += d
			count[m[1]]++
		}
		f.Close()
		if err := scan.Err(); err != nil {
			return fmt.Errorf("read %s: %w", log, err)
		}
	}
	if len(sum) == 0 {
		return fmt.Errorf("no test results in the logs: they need go test -v output")
	}
	var b strings.Builder
	b.WriteString(header)
	for _, name := range slices.Sorted(maps.Keys(sum)) {
		fmt.Fprintf(&b, "%s %.1f\n", name, sum[name]/float64(count[name]))
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "shardplan:", err)
	os.Exit(2)
}
