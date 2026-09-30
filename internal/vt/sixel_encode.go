package vt

import (
	"image"
	"image/color"
	"strconv"
)

// EncodeSixel writes the part of img inside src as a complete sixel DCS
// sequence (ESC P ... ST), scaled to dstW by dstH pixels by nearest neighbour.
//
// This is how a pane's picture is clipped. Sixel has no crop and no delete:
// whatever is sent is painted, from the cursor down and to the right. So when
// only part of an image is visible, because the pane is scrolled, covered by a
// popup or runs off the screen, tuios sends a new image holding just that part.
// The scale covers a host whose cells are a different size from the ones the
// guest drew for, which happens when several terminals share a session.
//
// The palette is the image's own, renumbered to the registers the crop uses,
// so nothing is re-quantised. Unpainted pixels stay transparent (P2=1).
func EncodeSixel(img *SixelImage, src image.Rectangle, dstW, dstH int) []byte {
	if img == nil || dstW <= 0 || dstH <= 0 {
		return nil
	}
	src = src.Intersect(image.Rect(0, 0, img.Width, img.Height))
	if src.Empty() {
		return nil
	}
	sw, sh := src.Dx(), src.Dy()
	// Source column for every destination column, computed once.
	xmap := make([]int, dstW)
	for dx := range xmap {
		xmap[dx] = src.Min.X + dx*sw/dstW
	}
	srcRow := func(dy int) []uint16 {
		y := src.Min.Y + dy*sh/dstH
		return img.Pix[y*img.Width : (y+1)*img.Width]
	}

	// Registers the crop uses, renumbered from zero.
	remap := make([]int, len(img.Palette)+1)
	for i := range remap {
		remap[i] = -1
	}
	var used []int
	for dy := 0; dy < dstH; dy++ {
		row := srcRow(dy)
		for _, sx := range xmap {
			v := row[sx]
			if v != 0 && remap[v] < 0 {
				remap[v] = len(used)
				used = append(used, int(v))
			}
		}
	}

	if len(used) > SixelMaxRegisters {
		// More colours than a pane is told it has: the rest take the
		// nearest of the first 256.
		keep := used[:SixelMaxRegisters]
		for _, v := range used[SixelMaxRegisters:] {
			remap[v] = nearestRegister(img, keep, img.Palette[v-1])
		}
		used = keep
	}

	out := make([]byte, 0, 64+len(used)*20+dstW*dstH/3)
	out = append(out, "\x1bP0;1;0q\"1;1;"...)
	out = strconv.AppendInt(out, int64(dstW), 10)
	out = append(out, ';')
	out = strconv.AppendInt(out, int64(dstH), 10)
	for i, v := range used {
		c := img.Palette[v-1]
		out = append(out, '#')
		out = strconv.AppendInt(out, int64(i), 10)
		out = append(out, ";2;"...)
		out = strconv.AppendInt(out, int64((int(c.R)*100+127)/255), 10)
		out = append(out, ';')
		out = strconv.AppendInt(out, int64((int(c.G)*100+127)/255), 10)
		out = append(out, ';')
		out = strconv.AppendInt(out, int64((int(c.B)*100+127)/255), 10)
	}

	// One row of sixel bits per colour for the band being encoded, the
	// colours the band uses in the order first seen, and the span of columns
	// each one touches, so a colour used in a few columns costs a few columns
	// and not the whole width.
	bits := make([][]byte, len(used))
	lo := make([]int, len(used))
	hi := make([]int, len(used))
	var bandColors []int
	for top := 0; top < dstH; top += 6 {
		bandColors = bandColors[:0]
		for dy := 0; dy < 6 && top+dy < dstH; dy++ {
			row := srcRow(top + dy)
			bit := byte(1) << dy
			for dx, sx := range xmap {
				v := row[sx]
				if v == 0 {
					continue
				}
				ci := remap[v]
				b := bits[ci]
				if b == nil {
					b = make([]byte, dstW)
					bits[ci] = b
					lo[ci], hi[ci] = dstW, -1
				}
				if hi[ci] < 0 {
					bandColors = append(bandColors, ci)
					lo[ci], hi[ci] = dx, dx
				} else if dx < lo[ci] {
					lo[ci] = dx
				} else if dx > hi[ci] {
					hi[ci] = dx
				}
				b[dx] |= bit
			}
		}
		for n, ci := range bandColors {
			if n > 0 {
				out = append(out, '$')
			}
			out = append(out, '#')
			out = strconv.AppendInt(out, int64(ci), 10)
			out = appendSixelRuns(out, bits[ci], lo[ci], hi[ci]+1)
			clear(bits[ci][lo[ci] : hi[ci]+1])
			hi[ci] = -1
		}
		if top+6 < dstH {
			out = append(out, '-')
		}
	}
	return append(out, "\x1b\\"...)
}

// appendSixelRuns appends one colour's row of sixels between columns from and
// to, run-length encoded, with the empty columns before from as one run.
func appendSixelRuns(out, row []byte, from, to int) []byte {
	out = appendRun(out, '?', from)
	for i := from; i < to; {
		j := i + 1
		for j < to && row[j] == row[i] {
			j++
		}
		out = appendRun(out, row[i]+'?', j-i)
		i = j
	}
	return out
}

func appendRun(out []byte, ch byte, n int) []byte {
	if n > 3 {
		out = append(out, '!')
		out = strconv.AppendInt(out, int64(n), 10)
		return append(out, ch)
	}
	for range n {
		out = append(out, ch)
	}
	return out
}

// nearestRegister is the index in keep of the colour closest to c.
func nearestRegister(img *SixelImage, keep []int, c color.RGBA) int {
	best, bestD := 0, -1
	for i, v := range keep {
		k := img.Palette[v-1]
		dr, dg, db := int(k.R)-int(c.R), int(k.G)-int(c.G), int(k.B)-int(c.B)
		if d := dr*dr + dg*dg + db*db; bestD < 0 || d < bestD {
			best, bestD = i, d
		}
	}
	return best
}
