//go:build differential

package js

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// The node script reads arrays of numbers, with null for NaN and strings for the
// infinities, sorts the indexes of each by (a, b) => a - b, and writes them.
const sortScript = `
const fs = require('fs');
const [,, input, output] = process.argv;
const value = (v) => (v === null ? NaN : v === 'Inf' ? Infinity : v === '-Inf' ? -Infinity : v);
const arrays = JSON.parse(fs.readFileSync(input, 'utf8')).map((a) => a.map(value));
const out = arrays.map((a) => a.map((v, i) => i).sort((i, j) => a[i] - a[j]));
fs.writeFileSync(output, JSON.stringify(out));
`

// TestSortAgainstNode checks Sort against V8's Array.prototype.sort with a comparator that
// gives NaN, where the order depends on the algorithm, for arrays of 1 to 63 elements.
// Run it with node on the path: go test -tags differential -run TestSortAgainstNode ./internal/js/
func TestSortAgainstNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on the path")
	}
	rng := rand.New(rand.NewPCG(3, 4))
	values := []float64{math.NaN(), math.Inf(1), math.Inf(-1), 0, 1, 2, 3, 0.5, -1}
	var arrays [][]float64
	for range 30_000 {
		a := make([]float64, 1+rng.IntN(63))
		for i := range a {
			if rng.IntN(10) < 7 {
				a[i] = values[rng.IntN(len(values))]
			} else {
				a[i] = float64(rng.IntN(5))
			}
		}
		arrays = append(arrays, a)
	}
	encoded := make([][]any, len(arrays))
	for k, a := range arrays {
		encoded[k] = make([]any, len(a))
		for i, v := range a {
			switch {
			case math.IsNaN(v):
				encoded[k][i] = nil
			case math.IsInf(v, 1):
				encoded[k][i] = "Inf"
			case math.IsInf(v, -1):
				encoded[k][i] = "-Inf"
			default:
				encoded[k][i] = v
			}
		}
	}
	dir := t.TempDir()
	in, out, script := filepath.Join(dir, "in.json"), filepath.Join(dir, "out.json"), filepath.Join(dir, "sort.cjs")
	data, _ := json.Marshal(encoded)
	if err := os.WriteFile(in, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte(sortScript), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg, err := exec.Command(node, script, in, out).CombinedOutput(); err != nil {
		t.Fatalf("node: %v: %s", err, msg)
	}
	data, err = os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var want [][]int
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	bad := 0
	for k, a := range arrays {
		got := make([]int, len(a))
		for i := range got {
			got[i] = i
		}
		Sort(got, func(i, j int) float64 { return a[i] - a[j] })
		if !slices.Equal(got, want[k]) {
			if bad++; bad <= 10 {
				t.Errorf("%v: Sort gives %v, node %v", a, got, want[k])
			}
		}
	}
	t.Logf("%d arrays, %d differences", len(arrays), bad)
}
