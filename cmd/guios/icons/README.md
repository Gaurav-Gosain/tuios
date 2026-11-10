# Icons

- `f7-*.svg`: [Framework7 Icons](https://framework7.io/icons/) (MIT, © Vladimir Kharlampidi), drawn after Apple's SF Symbols.
  `f7-doc-badge-plus.svg` is two of them, as Framework7 has no `doc.badge.plus`: `doc`, masked around the badge of `folder-badge-plus`, moved to its lower corner, as SF Symbols draw it.
- `meteor-*.svg`: [Meteor Icons](https://github.com/zkreations/icons) (MIT, © zkreations).
- `si-*.svg`: [Simple Icons](https://simpleicons.org) (CC0), programs' logos; the brands are their owners' trademarks.
- `octicon-*.svg`: [Octicons](https://primer.style/octicons) (MIT, © GitHub), for what Framework7 has no icon of, as a file's diff.
- `lucide-*.svg`: [Lucide](https://lucide.dev) (ISC, © Lucide Contributors), with a heavier stroke than Iconify serves (`stroke-width`), as SF Symbols' semibold: 2.5 for `chevrons-up-down`, 3 for `check`.

- `files/`: [Pierre Icons](https://diffs.com/icons) (MIT, © The Pierre Computer Company, `files/LICENSE.md`), the file and folder icons of the Files and Changes trees: the complete theme, in its colors for the light appearance (`*_light.svg`) and the dark one. `go run ./scripts/fileicons <checkout>` brings them from a checkout of [pierrecomputer/vscode-icons](https://github.com/pierrecomputer/vscode-icons) built with `node scripts/build-icon-theme.mjs`, with the names and extensions they are for (`fileicons_gen.go`).

All but `files/` as [Iconify](https://iconify.design) serves them: `https://api.iconify.design/f7/<name>.svg`.

`tray-template.png` is the frog of the app's icon (`resources/icon.png`) as a silhouette of its green, without the dark outline, black on transparent for macOS to tint in the menu bar, with its eyes, mouth and paws cut out of it: 14 points tall, centered in 18×18 points, at 2×. `tray.png` is the app's icon at 64×64, for the notification area of Windows and Linux.
