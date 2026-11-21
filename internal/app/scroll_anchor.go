package app

func (m *OS) applyScrollAnchors() {
	for _, w := range m.Windows {
		w.ApplyScrollAnchor()
	}
}

func (m *OS) recordScrollAnchors() {
	for _, w := range m.Windows {
		w.RecordScrollAnchor()
	}
}
