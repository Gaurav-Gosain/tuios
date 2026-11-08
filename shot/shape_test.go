package shot

import (
	"encoding/binary"
	"slices"
	"testing"
)

// loopingMorx is a morx table whose one ligature subtable holds every glyph
// forever: its only entry has dontAdvance and goes back to the same state. A
// renderer that trusts the table loops on the first cluster of two glyphs.
func loopingMorx() []byte {
	be := binary.BigEndian
	var body []byte
	u16 := func(v int) { body = be.AppendUint16(body, uint16(v)) }
	u32 := func(v int) { body = be.AppendUint32(body, uint32(v)) }
	u32(4)  // nClasses
	u32(28) // class table
	u32(34) // state array
	u32(42) // entry table
	u32(48) // ligature actions
	u32(48) // components
	u32(48) // ligatures
	u16(8)  // class table: trimmed array of no glyphs, so every glyph is class 1
	u16(0)
	u16(0)
	for range 4 {
		u16(0) // state 0: every class goes to entry 0
	}
	u16(0)      // entry 0: back to state 0,
	u16(0x4000) // without advancing,
	u16(0)      // no action
	var t []byte
	t = be.AppendUint16(t, 2)                       // version
	t = be.AppendUint16(t, 0)                       // unused
	t = be.AppendUint32(t, 1)                       // chains
	t = be.AppendUint32(t, 1)                       // default flags
	t = be.AppendUint32(t, uint32(16+12+len(body))) // chain length
	t = be.AppendUint32(t, 0)                       // features
	t = be.AppendUint32(t, 1)                       // subtables
	t = be.AppendUint32(t, uint32(12+len(body)))
	t = be.AppendUint32(t, 0x20000002) // ligature, any direction
	t = be.AppendUint32(t, 1)          // on by default
	return append(t, body...)
}

// TestShapeRefusesALoopingStateTable: a font file is input, and a morx state
// machine that never advances must end the shaping, leave the cluster as it
// was, and turn the face's shaping off.
func TestShapeRefusesALoopingStateTable(t *testing.T) {
	s := &shaper{morx: loopingMorx()}
	in := []int{10, 11, 12}
	out := s.shape(in)
	if !slices.Equal(out, in) {
		t.Errorf("a looping table changed the glyphs to %v", out)
	}
	if !s.bad {
		t.Errorf("a looping table left the face shaping")
	}
}

// FuzzShape runs the shaper on any table bytes: it must end, never panic,
// and leave at least one glyph.
func FuzzShape(f *testing.F) {
	f.Add(loopingMorx(), false, []byte{0, 10, 0, 11})
	f.Add([]byte{0, 1, 0, 0, 0, 10, 0, 14, 0, 18}, true, []byte{0, 1, 0, 2, 0, 3})
	f.Fuzz(func(t *testing.T, table []byte, gsub bool, glyphBytes []byte) {
		var glyphs []int
		for i := 0; i+1 < len(glyphBytes) && len(glyphs) < maxShapeGlyphs; i += 2 {
			glyphs = append(glyphs, int(binary.BigEndian.Uint16(glyphBytes[i:])))
		}
		s := &shaper{morx: table}
		if gsub {
			s = &shaper{gsub: table}
		}
		if out := s.shape(glyphs); len(glyphs) > 0 && len(out) == 0 {
			t.Fatalf("shaping %v left no glyph", glyphs)
		}
	})
}
