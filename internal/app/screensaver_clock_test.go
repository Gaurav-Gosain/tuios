package app

import (
	"testing"

	tfx "github.com/Gaurav-Gosain/tuiffects"
)

// The saver's engine has to run on the rate this program paints at.
//
// tuiffects keeps a virtual clock: it steps a fixed fraction of a second once
// per engine update, so a run is reproducible from its seed and an effect
// written in seconds still lasts that many seconds. The fraction is one over
// the frame rate, and NewEngine assumes sixty.
//
// The saver paints at screensaverRate: the session's NormalFPS, capped at
// sixty. max_fps can set NormalFPS as low as ten, and then the saver paints at
// ten. A clock left at the library's default, or one built from NormalFPS
// rather than the paint rate, drifts from the wall: matrix rains for the wrong
// number of seconds, thunderstorm storms for the wrong number, and tuffbaby
// plays its ten frame a second clip at the wrong rate.
//
// Negative control: building the clock from s.NormalFPS instead of
// screensaverRate reports "at 240 fps, one second of paint moved the clock
// 0.250 seconds, want 1.000", and 0.500 at 120. Dropping the engine.Clock line
// reports 0.167 at 10 and 0.500 at 30. The 60, 120 and 240 cases pass when the
// line is dropped, because the library's default is also sixty.

// clockTestCapture is a screen of plain text, one cell per character, which is
// all these tests need of a capture.
func clockTestCapture(width, height int) [][]tfx.InputCell {
	const line = "tuios screensaver frame rate "
	capture := make([][]tfx.InputCell, height)
	for y := range capture {
		capture[y] = make([]tfx.InputCell, width)
		for x := range capture[y] {
			capture[y][x] = tfx.InputCell{Symbol: string(line[(y*width+x)%len(line)])}
		}
	}
	return capture
}

// TestSaverClockRunsAtTheRateThePaintingDoes is the direct measurement: one
// second of painting has to move the engine's clock one second.
func TestSaverClockRunsAtTheRateThePaintingDoes(t *testing.T) {
	for _, rate := range []int{10, 30, 60, 120, 240} {
		d, ok := tfx.Lookup("highlight")
		if !ok {
			t.Fatal("the engine no longer has highlight")
		}
		settings := saverSettings(rate)
		engine, ok := screensaverBuild(clockTestCapture(60, 20), 60, 20, d.New(), d.NeedsFillCharacters, settings)
		if !ok {
			t.Fatal("the effect would not build over the capture")
		}
		start := engine.Clock.Elapsed()
		// One second of paint is one second's worth of the saver's ticks.
		for range screensaverRate(settings) {
			engine.Update()
		}
		if moved := engine.Clock.Elapsed() - start; moved < 0.999 || moved > 1.001 {
			t.Errorf("at %d fps, one second of paint moved the clock %.3f seconds, want 1.000",
				rate, moved)
		}
	}
}

// TestSaverRunsTheRainForItsFullTime is the same fault seen through an effect.
// matrix rains for RainTime seconds of engine clock and then fills the columns
// in, so the length of its opening is a reading of the clock the saver gave it.
func TestSaverRunsTheRainForItsFullTime(t *testing.T) {
	const rainTime = 15.0
	for _, rate := range []int{30, 60, 120, 240} {
		settings := saverSettings(rate)
		effect := tfx.NewMatrix(tfx.DefaultMatrixConfig())
		engine, ok := screensaverBuild(clockTestCapture(60, 20), 60, 20, effect, false, settings)
		if !ok {
			t.Fatal("matrix would not build over the capture")
		}
		// The rain is over once the first character has stopped wearing a
		// rain symbol and settled, which the effect signals by moving off
		// its rain phase; from outside, the readable mark is the frame on
		// which the run stops changing every cell. Counting frames until
		// the effect leaves the rain is not reachable from here, so this
		// counts the frames of the whole run and reads the rain out of the
		// clock the effect was given, which is the thing under test.
		frames := 0
		for effect.Advance(engine) {
			frames++
			if frames > 200000 {
				t.Fatal("matrix never finished")
			}
		}
		// Every frame is one tick of the saver's timer, so the run's
		// length in seconds of paint is frames over the rate. The rain is
		// the part of it the clock decides, and it cannot be shorter than
		// the time it was asked for.
		seconds := float64(frames) / float64(screensaverRate(settings))
		if seconds < rainTime {
			t.Errorf("at %d fps the whole run lasted %.2f seconds of paint, "+
				"which is less than the %.0f seconds of rain it was asked for",
				rate, seconds, rainTime)
		}
	}
}
