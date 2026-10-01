package courier

// StoreDirForTest lets the external tests reach the store's directory.
func StoreDirForTest(s *Store) string { return s.dir }
