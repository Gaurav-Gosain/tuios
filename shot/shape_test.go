package shot

import (
	"encoding/binary"
	"slices"
	"testing"
)

func loopingMorx() []byte {
	be := binary.BigEndian
	var body []byte
	u16 := func(v int) { body = be.AppendUint16(body, uint16(v)) }
	u32 := func(v int) { body = be.AppendUint32(body, uint32(v)) }
	u32(4)
	u32(28)
	u32(34)
	u32(42)
	u32(48)
	u32(48)
	u32(48)
	u16(8)
	u16(0)
	u16(0)
	for range 4 {
		u16(0)
	}
	u16(0)
	u16(0x4000)
	u16(0)
	var t []byte
	t = be.AppendUint16(t, 2)
	t = be.AppendUint16(t, 0)
	t = be.AppendUint32(t, 1)
	t = be.AppendUint32(t, 1)
	t = be.AppendUint32(t, uint32(16+12+len(body)))
	t = be.AppendUint32(t, 0)
	t = be.AppendUint32(t, 1)
	t = be.AppendUint32(t, uint32(12+len(body)))
	t = be.AppendUint32(t, 0x20000002)
	t = be.AppendUint32(t, 1)
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
