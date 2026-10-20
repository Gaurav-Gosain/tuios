//go:build differential

package js

import (
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The node script reads float64 inputs and writes Math.asin and Math.atan of each, then
// reads pairs and writes Math.atan2 of each pair. It runs every function twice, the
// second time after the loop is hot, so that the optimizing compiler's code is checked
// as well.
const atanScript = `
const fs = require('fs');
const [,, input, pairs, output, atan2Output] = process.argv;
const read = (file) => {
  const buf = fs.readFileSync(file);
  return new Float64Array(buf.buffer, buf.byteOffset, buf.length / 8);
};
const xs = read(input);
const out = new Float64Array(xs.length * 2);
for (let round = 0; round < 2; round++) {
  for (let i = 0; i < xs.length; i++) {
    out[i * 2] = Math.asin(xs[i]);
    out[i * 2 + 1] = Math.atan(xs[i]);
  }
}
fs.writeFileSync(output, Buffer.from(out.buffer));
const ps = read(pairs);
const at = new Float64Array(ps.length / 2);
for (let round = 0; round < 2; round++) {
  for (let i = 0; i < at.length; i++) at[i] = Math.atan2(ps[i * 2], ps[i * 2 + 1]);
}
fs.writeFileSync(atan2Output, Buffer.from(at.buffer));
`

// randomBits gives a double of any exponent and sign, not NaN.
func randomBits(rng *rand.Rand) float64 {
	for {
		if x := math.Float64frombits(rng.Uint64()); !math.IsNaN(x) {
			return x
		}
	}
}

func atanInputs() []float64 {
	rng := rand.New(rand.NewPCG(3, 4))
	var xs []float64
	// Every exponent, any mantissa.
	for range 300_000 {
		xs = append(xs, randomBits(rng))
	}
	// The domain of asin, evenly, and near its ends.
	for range 400_000 {
		xs = append(xs, 2*rng.Float64()-1)
	}
	for range 100_000 {
		d := math.Ldexp(rng.Float64(), -rng.IntN(60))
		xs = append(xs, 1-d, d-1)
	}
	// A few units in the last place either way of every bound fdlibm branches on.
	for _, b := range []float64{
		0.5, 0.975, 1, 0.4375, 11.0 / 16, 19.0 / 16, 39.0 / 16, math.Ldexp(1, -27), math.Ldexp(1, 66),
		math.Float64frombits(0x3fef333300000000), math.Float64frombits(0x3fdc000000000000),
		math.Float64frombits(0x3fe6000000000000), math.Float64frombits(0x3ff3000000000000),
		math.Float64frombits(0x4003800000000000), math.Float64frombits(0x4410000000000000),
	} {
		lo, hi := b, b
		for range 200 {
			xs = append(xs, lo, hi, -lo, -hi)
			lo, hi = math.Nextafter(lo, 0), math.Nextafter(hi, math.Inf(1))
		}
	}
	// What a diagram asks for: ratios of coordinates.
	for range 200_000 {
		xs = append(xs, float64(rng.IntN(4000)-2000)/float64(1+rng.IntN(500)))
	}
	xs = append(xs, 0, math.Copysign(0, -1), math.Inf(1), math.Inf(-1), math.NaN(), 1, -1, 2, -2)
	return xs
}

func atan2Inputs() []float64 {
	rng := rand.New(rand.NewPCG(5, 6))
	var ps []float64
	for range 300_000 {
		ps = append(ps, randomBits(rng), randomBits(rng))
	}
	// Coordinate differences in a drawing, whole and fractional.
	for range 400_000 {
		ps = append(ps, float64(rng.IntN(2001)-1000), float64(rng.IntN(2001)-1000))
	}
	for range 400_000 {
		ps = append(ps, 1000*(2*rng.Float64()-1), 1000*(2*rng.Float64()-1))
	}
	// Ratios past 2^60 either way.
	for range 20_000 {
		y := math.Ldexp(rng.Float64(), rng.IntN(200)-100)
		ps = append(ps, y, math.Ldexp(y, rng.IntN(140)-70)*float64(1-2*rng.IntN(2)))
	}
	special := []float64{0, math.Copysign(0, -1), 1, -1, math.Inf(1), math.Inf(-1), math.NaN(), 3, -3, 1e-300, -1e300}
	for _, y := range special {
		for _, x := range special {
			ps = append(ps, y, x)
		}
	}
	return ps
}

// TestAtanAgainstNode checks Asin, Atan and Atan2 against node bit for bit. Go's math
// functions are the negative control: they must differ from node somewhere, or the
// inputs do not reach what the port is for.
// Run it with node on the path: go test -tags differential -run TestAtanAgainstNode ./internal/js/
func TestAtanAgainstNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on the path")
	}
	xs, ps := atanInputs(), atan2Inputs()
	dir := t.TempDir()
	in, pairs, out, atan2Out := filepath.Join(dir, "in"), filepath.Join(dir, "pairs"), filepath.Join(dir, "out"), filepath.Join(dir, "atan2")
	script := filepath.Join(dir, "atan.cjs")
	writeFloats(t, in, xs)
	writeFloats(t, pairs, ps)
	if err := os.WriteFile(script, []byte(atanScript), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg, err := exec.Command(node, script, in, pairs, out, atan2Out).CombinedOutput(); err != nil {
		t.Fatalf("node: %v: %s", err, msg)
	}
	res := readFloats(t, out, 2*len(xs))
	at := readFloats(t, atan2Out, len(ps)/2)
	shown, badAsin, badAtan, badAtan2 := 0, 0, 0, 0
	goAsin, goAtan, goAtan2 := 0, 0, 0
	show := func(format string, args ...any) {
		if shown++; shown <= 10 {
			t.Errorf(format, args...)
		}
	}
	for i, x := range xs {
		if got, want := Asin(x), res[i*2]; !same(got, want) {
			badAsin++
			show("Asin(%v) = %v, node %v", x, got, want)
		}
		if !same(math.Asin(x), res[i*2]) {
			goAsin++
		}
		if got, want := Atan(x), res[i*2+1]; !same(got, want) {
			badAtan++
			show("Atan(%v) = %v, node %v", x, got, want)
		}
		if !same(math.Atan(x), res[i*2+1]) {
			goAtan++
		}
	}
	for i := range at {
		y, x := ps[i*2], ps[i*2+1]
		if got, want := Atan2(y, x), at[i]; !same(got, want) {
			badAtan2++
			show("Atan2(%v, %v) = %v, node %v", y, x, got, want)
		}
		if !same(math.Atan2(y, x), at[i]) {
			goAtan2++
		}
	}
	t.Logf("Asin and Atan: %d inputs, %d and %d differences. Atan2: %d pairs, %d differences", len(xs), badAsin, badAtan, len(at), badAtan2)
	t.Logf("Go's math, the control: Asin %d, Atan %d, Atan2 %d differences", goAsin, goAtan, goAtan2)
	if goAsin == 0 || goAtan == 0 || goAtan2 == 0 {
		t.Error("Go's math agrees with node everywhere: the inputs do not test the port")
	}
}
