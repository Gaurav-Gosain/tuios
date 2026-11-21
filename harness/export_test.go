package harness

func (r *ScreenRule) compile(foldCase bool) error {
	return r.Gate.compile(foldCase, 0, &gateBudget{})
}
