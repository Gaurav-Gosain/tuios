package composer

const (
	undoSteps = 200
	undoBytes = 1 << 20
)

type edit struct {
	pos                   int
	del, ins              []cell
	before, after, anchor int
	typing                bool
	size                  int
}

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
	if m.anchor > len(m.cells) {
		m.anchor = -1
	}
	return del
}

func (m *Model) change(pos, n int, ins []cell, after int) {
	before := m.cur
	if m.anchor == before {
		m.anchor = -1
	}
	del := m.splice(pos, n, ins)
	m.cur = after
	m.syncMention()
	m.push(edit{pos: pos, del: del, ins: ins, before: before, after: after, anchor: m.anchor, size: editSize(del, ins)})
}

func (m *Model) typeCell(c cell) {
	pos := m.cur
	if n := len(m.undo); n > 0 && m.joinTyping {
		last := &m.undo[n-1]
		boundary := c.isSpace() && len(last.ins) > 0 && !last.ins[len(last.ins)-1].isSpace()
		if last.typing && last.pos+len(last.ins) == pos && !boundary {
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
	m.push(edit{pos: pos, ins: ins, before: pos, after: m.cur, anchor: -1, typing: true, size: c.bytes()})
	m.joinTyping = true
}

func (m *Model) push(e edit) {
	m.undo = append(m.undo, e)
	m.undoSize += e.size
	clear(m.redo)
	m.redo = m.redo[:0]
	m.joinTyping = false
	m.trimUndo()
}

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
	m.anchor = -1
	if e.anchor >= 0 && e.anchor <= len(m.cells) {
		m.anchor = e.anchor
	}
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
	m.anchor = -1
	m.undo = append(m.undo, e)
	m.undoSize += e.size
	m.joinTyping = false
	m.trimUndo()
	return true
}
