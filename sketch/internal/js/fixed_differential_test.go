//go:build differential

package js

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// The node script reads float64 inputs and writes, for each, toFixed, toExponential and
// Number(toPrecision) at the digit count given with it.
const fixedScript = `
const fs = require('fs');
const [,, input, output] = process.argv;
const buf = fs.readFileSync(input);
const xs = new Float64Array(buf.buffer, buf.byteOffset, buf.length / 8);
const out = [];
for (let i = 0; i < xs.length; i += 2) {
  const x = xs[i], d = xs[i + 1];
  out.push(x.toFixed(d), x.toExponential(d), String(Number(x.toPrecision(Math.max(1, d)))));
}
fs.writeFileSync(output, JSON.stringify(out));
`

// fixedInputs are pairs of a value and a digit count from 0 to 20. Ties sit on short binary
// fractions, so many values are multiples of a power of two, scaled by a power of ten.
func fixedInputs() []float64 {
	rng := rand.New(rand.NewPCG(5, 6))
	var xs []float64
	add := func(x float64) { xs = append(xs, x, float64(rng.IntN(21))) }
	for range 100_000 {
		add(math.Float64frombits(rng.Uint64() & 0x7fffffffffffffff))
	}
	for range 200_000 {
		add(float64(rng.IntN(1<<20)) / math.Ldexp(1, rng.IntN(12)))
	}
	for range 100_000 {
		add(float64(rng.IntN(1_000_000)) * math.Pow(10, float64(rng.IntN(30)-15)))
	}
	for range 100_000 {
		add(rng.Float64() * math.Pow(10, float64(rng.IntN(44)-22)))
	}
	for k := 0; k <= 2000; k++ {
		for d := 0; d <= 20; d++ {
			xs = append(xs, float64(k)/8, float64(d), float64(k)+0.5, float64(d), float64(k)/1000, float64(d))
		}
	}
	for _, x := range []float64{0, 1e21, 9.999999999999999e20, 1e-7, 5e-7, 0.5, 1.5, 2.5, 1.005, 1.45, 8.345, math.MaxFloat64, 5e-324} {
		for d := 0; d <= 20; d++ {
			xs = append(xs, x, float64(d))
		}
	}
	return xs
}

// TestFixedAgainstNode checks ToFixed, ToExponential and PrecisionNumber against node.
// Run it with node on the path: go test -tags differential -run TestFixedAgainstNode ./internal/js/
func TestFixedAgainstNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on the path")
	}
	xs := fixedInputs()
	dir := t.TempDir()
	in, out, script := filepath.Join(dir, "in"), filepath.Join(dir, "out"), filepath.Join(dir, "fixed.cjs")
	writeFloats(t, in, xs)
	if err := os.WriteFile(script, []byte(fixedScript), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg, err := exec.Command(node, script, in, out).CombinedOutput(); err != nil {
		t.Fatalf("node: %v: %s", err, msg)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	bad, goBad := 0, 0
	for i := 0; i < len(xs); i += 2 {
		x, d := xs[i], int(xs[i+1])
		got := [3]string{ToFixed(x, d), ToExponential(x, d), Num(PrecisionNumber(x, max(1, d)))}
		for k := range got {
			if got[k] != want[i/2*3+k] {
				if bad++; bad <= 10 {
					t.Errorf("%v at %d digits, function %d: got %s, node %s", x, d, k, got[k], want[i/2*3+k])
				}
			}
		}
		if x < 1e21 && strconv.FormatFloat(x, 'f', d, 64) != want[i/2*3] {
			goBad++
		}
	}
	t.Logf("%d values, %d differences; strconv's 'f' differs from toFixed on %d", len(xs)/2, bad, goBad)
}
