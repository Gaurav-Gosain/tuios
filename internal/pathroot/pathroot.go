// Package pathroot answers whether a path is inside a folder.
package pathroot

import "path/filepath"

// Within reports whether p is root or a path inside it, and returns p relative
// to root. It compares names only: it follows no link and reads nothing from
// disk, so a caller that must not be led out by a symlink resolves both paths
// first.
//
// A name that only starts with two dots, such as "..foo", is inside. The
// usual strings.HasPrefix(rel, "..") test refuses it.
func Within(root, p string) (rel string, ok bool) {
	rel, err := filepath.Rel(root, p)
	if err != nil || !filepath.IsLocal(rel) {
		return "", false
	}
	return rel, true
}
