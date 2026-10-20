//go:build differential

package js

import (
	"encoding/binary"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The node script reads float64 inputs and writes Math.log10 and Math.log of each, then
// reads pairs and writes x ** y of each pair, as float64 in the same order. It runs every function twice, the second time after the
// loop is hot, so that the optimizing compiler's code is checked as well.
const logScript = `
const fs = require('fs');
const [,, input, pairs, output, powOutput] = process.argv;
const read = (file) => {
  const buf = fs.readFileSync(file);
  return new Float64Array(buf.buffer, buf.byteOffset, buf.length / 8);
};
const xs = read(input);
const out = new Float64Array(xs.length * 2);
for (let round = 0; round < 2; round++) {
  for (let i = 0; i < xs.length; i++) {
    out[i * 2] = Math.log10(xs[i]);
    out[i * 2 + 1] = Math.log(xs[i]);
  }
}
fs.writeFileSync(output, Buffer.from(out.buffer));
const ps = read(pairs);
const pow = new Float64Array(ps.length / 2);
for (let round = 0; round < 2; round++) {
  for (let i = 0; i < pow.length; i++) pow[i] = ps[i * 2] ** ps[i * 2 + 1];
}
fs.writeFileSync(powOutput, Buffer.from(pow.buffer));
`

func logInputs() []float64 {
	rng := rand.New(rand.NewPCG(1, 2))
	var xs []float64
	// Every exponent, any mantissa.
	for range 600_000 {
		bits := rng.Uint64() & 0x7fffffffffffffff
		if x := math.Float64frombits(bits); !math.IsNaN(x) {
			xs = append(xs, x)
		}
	}
	// Near 1, where fdlibm takes its short path.
	for range 200_000 {
		xs = append(xs, 0.5+1.5*rng.Float64())
	}
	// Around every power of ten, a few units in the last place either way.
	for k := -323; k <= 308; k++ {
		p := math.Pow(10, float64(k))
		x, y := p, p
		for range 60 {
			xs = append(xs, x, y)
			x, y = math.Nextafter(x, 0), math.Nextafter(y, math.Inf(1))
		}
	}
	// What a chart asks for: a span over a count of steps.
	for range 100_000 {
		span := float64(rng.IntN(2_000_000)) / float64(1+rng.IntN(1000))
		xs = append(xs, span/float64(2+rng.IntN(9)))
	}
	for i := 1; i <= 100_000; i++ {
		xs = append(xs, float64(i))
	}
	// Subnormals and the special values.
	for range 10_000 {
		xs = append(xs, math.Float64frombits(rng.Uint64()&0x000fffffffffffff))
	}
	for k := -400; k <= 400; k++ {
		xs = append(xs, float64(k))
	}
	xs = append(xs, 0, math.Copysign(0, -1), -1, math.Inf(1), math.Inf(-1), math.NaN(), 1, 10, 100, 1e-5)
	return xs
}

// powInputs are the pairs 10 ** k for every whole k a double can reach, and past it.
// xychart raises 10 to a whole power and to nothing else.
func powInputs() []float64 {
	var ps []float64
	for k := -400; k <= 400; k++ {
		ps = append(ps, 10, float64(k))
	}
	return ps
}

func writeFloats(t *testing.T, file string, xs []float64) {
	buf := make([]byte, 8*len(xs))
	for i, x := range xs {
		binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(x))
	}
	if err := os.WriteFile(file, buf, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFloats(t *testing.T, file string, n int) []float64 {
	buf, err := os.ReadFile(file)
	if err != nil || len(buf) != 8*n {
		t.Fatalf("node wrote %d bytes to %s, want %d: %v", len(buf), file, 8*n, err)
	}
	out := make([]float64, n)
	for i := range out {
		out[i] = math.Float64frombits(binary.LittleEndian.Uint64(buf[i*8:]))
	}
	return out
}

func same(a, b float64) bool {
	return math.Float64bits(a) == math.Float64bits(b) || (math.IsNaN(a) && math.IsNaN(b))
}

// TestMathAgainstNode checks Log, Log10 and Pow10 against node bit for bit.
// Run it with node on the path: go test -tags differential -run TestMathAgainstNode ./internal/js/
func TestMathAgainstNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on the path")
	}
	xs, ps := logInputs(), powInputs()
	dir := t.TempDir()
	in, pairs, out, powOut := filepath.Join(dir, "in"), filepath.Join(dir, "pairs"), filepath.Join(dir, "out"), filepath.Join(dir, "pow")
	script := filepath.Join(dir, "math.cjs")
	writeFloats(t, in, xs)
	writeFloats(t, pairs, ps)
	if err := os.WriteFile(script, []byte(logScript), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg, err := exec.Command(node, script, in, pairs, out, powOut).CombinedOutput(); err != nil {
		t.Fatalf("node: %v: %s", err, msg)
	}
	logs := readFloats(t, out, 2*len(xs))
	pows := readFloats(t, powOut, len(ps)/2)
	shown, badLog10, badLog, badPow := 0, 0, 0, 0
	show := func(format string, args ...any) {
		if shown++; shown <= 10 {
			t.Errorf(format, args...)
		}
	}
	for i, x := range xs {
		if got, want := Log10(x), logs[i*2]; !same(got, want) {
			badLog10++
			show("Log10(%v) = %v, node %v", x, got, want)
		}
		if got, want := Log(x), logs[i*2+1]; !same(got, want) {
			badLog++
			show("Log(%v) = %v, node %v", x, got, want)
		}
	}
	for i := range pows {
		x, y := ps[i*2], ps[i*2+1]
		if got, want := Pow10(int(y)), pows[i]; !same(got, want) {
			badPow++
			show("Pow10(%v) = %v, node %v ** %v = %v", y, got, x, y, want)
		}
	}
	t.Logf("Log10 and Log: %d inputs, %d and %d differences. Pow10: %d powers, %d differences", len(xs), badLog10, badLog, len(pows), badPow)
}
