package text

import "testing"

func TestVennText(t *testing.T) {
	src := "venn-beta\n  title Teams\n  set A[\"Frontend\"] : 20\n    text React\n  set B : 12\n  union A, B[\"Shared\"] : 3\n"
	drawPinned(t, src, 0, `
Teams
Sets               Size
  A      Frontend    20
    · React
  B                  12
Overlaps           Size
  A ∩ B  Shared       3
`)
	drawPinned(t, src, 16, `
Teams
Sets
  A (20)
    Frontend
    · React
  B (12)
Overlaps
  A ∩ B (3)
    Shared
`)
	fitsCorpus(t, "venn", 80)
	fitsCorpus(t, "venn", 20)
}
