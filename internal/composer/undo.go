package composer

// Undo bounds: the stack keeps this many steps, and no more than this many
// bytes of text across them. The oldest step goes first.
const (
	undoSteps = 200
	undoBytes = 1 << 20
)

// edit is one undo step: at pos, del was taken out and ins put in. The cells
// keep their blocks, so undoing the delete of a block brings back the block
// with its payload, and no kill ring is needed.
type edit struct {
	pos      int
	del, ins []cell
	// before and after are the cursor before and after the edit.
	before, after int
	// typing says the step is a run of typed characters, which the next
	// typed character may join.
	typing bool
	size   int
}

// editSize is what an edit costs the undo bound.
func editSize(del, ins []cell) int {
	n := 0
	for _, c := range del {
		n += c.weight()
	}
	for _, c := range ins {
		n += c.weight()
	}
	return n
}

// splice puts ins in place of the n cells at pos and returns the cells it
// took out. The returned slice is a copy, safe to keep.
func (m *Model) splice(pos, n int, ins []cell) []cell {
	var del []cell
	if n > 0 {
		del = make([]cell, n)
		copy(del, m.cells[pos:pos+n])
	}
	for _, c := range del {
		m.size -= c.bytes()
	}
	for _, c := range ins {
		m.size += c.bytes()
	}
	switch d := len(ins) - n; {
	case d > 0:
		m.cells = append(m.cells, make([]cell, d)...)
		copy(m.cells[pos+len(ins):], m.cells[pos+n:len(m.cells)-d])
	case d < 0:
		copy(m.cells[pos+len(ins):], m.cells[pos+n:])
		clear(m.cells[len(m.cells)+d:])
		m.cells = m.cells[:len(m.cells)+d]
	}
	copy(m.cells[pos:], ins)
	m.ver++
	return del
}

// change replaces the n cells at pos with ins as one undo step and puts the
// cursor at after.
func (m *Model) change(pos, n int, ins []cell, after int) {
	before := m.cur
	del := m.splice(pos, n, ins)
	m.cur = after
	m.push(edit{pos: pos, del: del, ins: ins, before: before, after: after, size: editSize(del, ins)})
}

// typeCell inserts one typed cell at the cursor. A run of typed characters is
// one undo step, up to the first space after a word, so undo takes back a
// word at a time.
func (m *Model) typeCell(c cell) {
	pos := m.cur
	if n := len(m.undo); n > 0 && m.joinTyping {
		last := &m.undo[n-1]
		boundary := c.isSpace() && len(last.ins) > 0 && !last.ins[len(last.ins)-1].isSpace()
		if last.typing && len(last.del) == 0 && last.pos+len(last.ins) == pos && !boundary {
			m.splice(pos, 0, []cell{c})
			m.cur = pos + 1
			last.ins = append(last.ins, c)
			last.after = m.cur
			last.size += c.bytes()
			m.undoSize += c.bytes()
			m.redo = m.redo[:0]
			m.trimUndo()
			return
		}
	}
	ins := []cell{c}
	m.splice(pos, 0, ins)
	m.cur = pos + 1
	m.push(edit{pos: pos, ins: ins, before: pos, after: m.cur, typing: true, size: c.bytes()})
	m.joinTyping = true
}

// push records an edit and drops the redo steps, which no longer apply.
func (m *Model) push(e edit) {
	m.undo = append(m.undo, e)
	m.undoSize += e.size
	clear(m.redo)
	m.redo = m.redo[:0]
	m.joinTyping = false
	m.trimUndo()
}

// trimUndo drops the oldest steps past the bounds.
func (m *Model) trimUndo() {
	drop := 0
	for len(m.undo)-drop > undoSteps || (m.undoSize > undoBytes && len(m.undo)-drop > 1) {
		m.undoSize -= m.undo[drop].size
		drop++
	}
	if drop > 0 {
		clear(m.undo[:drop])
		m.undo = append(m.undo[:0], m.undo[drop:]...)
	}
}

// Undo takes back the last edit. It reports whether there was one.
func (m *Model) Undo() bool {
	n := len(m.undo)
	if n == 0 {
		return false
	}
	e := m.undo[n-1]
	m.undo[n-1] = edit{}
	m.undo = m.undo[:n-1]
	m.undoSize -= e.size
	m.splice(e.pos, len(e.ins), e.del)
	m.cur = e.before
	e.typing = false
	m.redo = append(m.redo, e)
	m.joinTyping = false
	return true
}

// Redo does again the last edit Undo took back. It reports whether there was
// one.
func (m *Model) Redo() bool {
	n := len(m.redo)
	if n == 0 {
		return false
	}
	e := m.redo[n-1]
	m.redo[n-1] = edit{}
	m.redo = m.redo[:n-1]
	m.splice(e.pos, len(e.del), e.ins)
	m.cur = e.after
	m.undo = append(m.undo, e)
	m.undoSize += e.size
	m.joinTyping = false
	m.trimUndo()
	return true
}
