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
// A shard is one runner, and its lanes run side by side on it. The tests in
// alone.txt go to one more shard of their own, which runs them one at a time
// with nothing beside them.
//
// Usage:
//
//	go test -list '^Test' ./... | shardplan -shards 9 -shard 3
//	    prints the tests of shard 3, one per line.
//	go test -list '^Test' ./... | shardplan -shards 4 -lanes 4 -shard 3 -lane 2
//	    prints the tests of lane 2 of shard 3. Shard 5, lane 0, is the tests
//	    that run alone.
//	go test -list '^Test' ./... | shardplan -shards 9 -max 11m
//	    prints the estimated time of each shard and fails if one is over.
//	testselect ... | shardplan -shards 9 -shard 3 -fill 8m
//	    the same, but packs the tests into only as many shards as they need
//	    at about 8 minutes each. The other shards print nothing.
//	shardplan -update LOG...
//	    rewrites durations.txt from go test -v output, such as gh run view --log.
//
// A test missing from durations.txt is estimated at the mean of the known
// tests. The tests on one line of together.txt always land in one lane, in
// the order the lane runs them, so they never run at the same time as each
// other. Every lane computes the same plan from the same list and files, so
// the lanes together run each test exactly once.
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
	togetherFile := flag.String("together", "shardplan/together.txt", "file of test groups that share a lane")
	aloneFile := flag.String("alone", "shardplan/alone.txt", "file of tests that run with no other test beside them")
	shards := flag.Int("shards", 0, "number of shards")
	lanes := flag.Int("lanes", 1, "number of lanes in each shard")
	shard := flag.Int("shard", 0, "print the tests of this shard (1-based)")
	lane := flag.Int("lane", -1, "with -shard: print the tests of this lane (1-based), or with 0 the tests that run alone (shard -shards plus 1)")
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
	if *shards < 1 || *lanes < 1 || (*shard == 0) == (*limit == 0) || *shard > *shards+1 {
		fail(fmt.Errorf("give -shards and one of -shard (1 to %d) or -max", *shards+1))
	}
	if *shard > 0 && (*lane > *lanes || (*lane < 0 && *lanes > 1)) {
		fail(fmt.Errorf("give -lane 0 to %d with -shard when -lanes is over 1", *lanes))
	}

	known, err := readDurations(*durations)
	if err != nil {
		fail(err)
	}
	groups, err := readGroups(*togetherFile)
	if err != nil {
		fail(err)
	}
	alone, err := readAlone(*aloneFile)
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
		used = shardsNeeded(names, known, groups, alone, *fill, *shards, *lanes)
	}
	plan := pack(names, known, groups, alone, used, *lanes)

	if *shard > 0 {
		if *shard == *shards+1 {
			*shard = used + 1
		} else if *shard > used {
			return
		}
		s := plan[*shard-1]
		var out []string
		switch {
		case *lane > 0:
			if *lane <= len(s.lanes) {
				out = s.lanes[*lane-1].names
			}
		case *lane == 0:
			out = s.alone.names
		default:
			if len(s.lanes) > 0 {
				out = s.lanes[0].names
			}
			out = append(slices.Clone(out), s.alone.names...)
		}
		for _, name := range out {
			fmt.Println(name)
		}
		return
	}

	secs := func(s float64) time.Duration { return time.Duration(s * float64(time.Second)).Round(time.Second) }
	over := false
	for i, s := range plan {
		mark := ""
		if s.load() > limit.Seconds() {
			mark = "  over the limit"
			over = true
		}
		n := len(s.alone.names)
		for _, l := range s.lanes {
			n += len(l.names)
		}
		if len(s.lanes) == 0 {
			if n == 0 {
				continue
			}
			fmt.Printf("shard %d of %d: %3d tests alone, one at a time, about %s%s\n", i+1, *shards+1, n, secs(s.load()), mark)
			continue
		}
		fmt.Printf("shard %d of %d: %3d tests, about %s%s\n", i+1, *shards, n, secs(s.load()), mark)
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

func shardsNeeded(names []string, known map[string]float64, groups [][]string, alone map[string]bool, fill time.Duration, shards, lanes int) int {
	s := pack(names, known, groups, alone, 1, 1)[0]
	total := s.lanes[0].load / float64(lanes)
	n := int(math.Ceil(total / fill.Seconds()))
	return max(1, min(n, shards))
}

type laneLoad struct {
	names []string
	load  float64
}

type shardLoad struct {
	lanes []laneLoad
	alone laneLoad
}

func (s shardLoad) load() float64 {
	longest := 0.0
	for _, l := range s.lanes {
		longest = max(longest, l.load)
	}
	return longest + s.alone.load
}

func pack(names []string, known map[string]float64, groups [][]string, alone map[string]bool, shards, lanes int) []shardLoad {
	fallback := 1.0
	if len(known) > 0 {
		sum := 0.0
		for _, name := range slices.Sorted(maps.Keys(known)) {
			sum += known[name]
		}
		fallback = sum / float64(len(known))
	}
	estimate := func(name string) float64 {
		if d, ok := known[name]; ok {
			return d
		}
		return fallback
	}

	type unit struct {
		names []string
		load  float64
		alone bool
	}
	selected := map[string]bool{}
	for _, name := range names {
		selected[name] = true
	}
	grouped := map[string]bool{}
	var units []unit
	for _, group := range groups {
		var u unit
		for _, name := range group {
			if selected[name] && !grouped[name] {
				grouped[name] = true
				u.names = append(u.names, name)
				u.load += estimate(name)
				u.alone = u.alone || alone[name]
			}
		}
		if len(u.names) > 0 {
			units = append(units, u)
		}
	}
	for _, name := range names {
		if !grouped[name] {
			units = append(units, unit{names: []string{name}, load: estimate(name), alone: alone[name]})
		}
	}
	slices.SortFunc(units, func(a, b unit) int {
		return cmp.Or(cmp.Compare(b.load, a.load), cmp.Compare(a.names[0], b.names[0]))
	})

	plan := make([]shardLoad, shards+1)
	for i := range shards {
		plan[i].lanes = make([]laneLoad, lanes)
	}
	apart := &plan[shards].alone
	for _, u := range units {
		if u.alone {
			apart.names = append(apart.names, u.names...)
			apart.load += u.load
			continue
		}
		fs, fl := 0, 0
		for i := range shards {
			for j := range plan[i].lanes {
				if plan[i].lanes[j].load < plan[fs].lanes[fl].load {
					fs, fl = i, j
				}
			}
		}
		plan[fs].lanes[fl].names = append(plan[fs].lanes[fl].names, u.names...)
		plan[fs].lanes[fl].load += u.load
	}
	for i := range plan {
		for j := range plan[i].lanes {
			slices.Sort(plan[i].lanes[j].names)
		}
		slices.Sort(plan[i].alone.names)
	}
	return plan
}

func readAlone(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read alone: %w", err)
	}
	alone := map[string]bool{}
	for line := range strings.SplitSeq(string(data), "\n") {
		line, _, _ = strings.Cut(line, "#")
		for name := range strings.FieldsSeq(line) {
			alone[name] = true
		}
	}
	return alone, nil
}

func readGroups(path string) ([][]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read groups: %w", err)
	}
	var groups [][]string
	for line := range strings.SplitSeq(string(data), "\n") {
		line, _, _ = strings.Cut(line, "#")
		if fields := strings.Fields(line); len(fields) > 1 {
			groups = append(groups, fields)
		}
	}
	return groups, nil
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
