package text

import "testing"

func TestIshikawaText(t *testing.T) {
	src := "ishikawa-beta\n  Late train\n    Weather\n      Snow\n    Staff\n      Ill\n        Flu\n    Track\n"
	drawPinned(t, src, 0, `
          Weather
  Track   ├ Snow
━━┻━━━━━━━╋━━━━━━━━━━▶ Late train
          Staff
          └ Ill
            └ Flu
`)
	drawPinned(t, src, 20, `
Late train
├ Weather
│ └ Snow
├ Staff
│ └ Ill
│   └ Flu
└ Track
`)
	fitsCorpus(t, "ishikawa", 80)
	fitsCorpus(t, "ishikawa", 30)
}
