package text

import "testing"

func TestTreeViewText(t *testing.T) {
	src := "treeView-beta\n    title Project\n    src/\n        App.tsx :::highlight ## main component\n        lib/\n            util.ts\n    package.json ## manifest\n"
	drawPinned(t, src, 0, `
Project
src/
├── App.tsx      main component
└── lib/
    └── util.ts
package.json     manifest
`)
	drawPinned(t, src, 18, `
Project
src/
├ App.tsx
│   main component
└ lib/
  └ util.ts
package.json
  manifest
`)
	fitsCorpus(t, "treeview", 80)
	fitsCorpus(t, "treeview", 30)
}
