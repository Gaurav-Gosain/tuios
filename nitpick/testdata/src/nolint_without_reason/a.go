package nolint_without_reason

func f() (int, error) { return 0, nil }

func a() {
	// want +1 "the nolint directive has no reason"
	x, _ := f() //nolint:errcheck
	// want +1 "has no reason"
	y, _ := f() //nolint
	z, _ := f() //nolint:errcheck // the cache is best effort
	w, _ := f() //nolintx
	// The cache is best effort.
	v, _ := f() //nolint:errcheck
	_, _, _, _, _ = x, y, z, w, v
}
