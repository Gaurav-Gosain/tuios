# composer

`composer` is a multiline editor for Bubble Tea v2, made for writing to an
agent. tuios uses it for the Inbox reply, agent mail and the one-line
answers in the Inbox.

The package is internal for now. The plan is to publish it as
`tuios.dev/composer` after one tuios release. It imports no tuios package,
and a test in `cmd/tuios` keeps it that way, so the move is a rename.

## What it does that a textarea does not

The textarea of bubbles edits a string. The composer edits a draft of
cells, and a cell can hold a block.

- **Paste blocks.** A long paste becomes one block, drawn as a label such
  as `[Pasted text #1 · 42 lines]`. The cursor steps over it in one move,
  and a delete takes all of it. The text is put back in its place when the
  message is sent, so the label is never sent. Literal text that looks like
  a label is never replaced. `ctrl+o` opens a block read only, and `ctrl+e`
  turns it back into text.
- **Image attachments.** `AddImage` puts an image in the draft as a block.
  The host can show a preview through a `Previewer`, as cells or as
  terminal graphics. `SubmitParts` sends the text and the images in order.
- **Undo as one unit.** A paste, a block delete and a run of typed
  characters are each one undo step. Undo of a block delete brings back the
  block with its whole payload.
- **The rest of an agent prompt.** `@` names a pane, an agent or a file
  through a `Completer`. `ctrl+r` searches the history of sent messages.
  Text that keys from outside the keyboard wrote is marked, so the host can
  tell a person's answer from an automated one.

It costs nothing at rest: no timer, no goroutine. Render keeps its last
frame, so a frame with no change since the last one is a comparison.

## API

```go
m := composer.New(composer.Options{MaxRows: 8, Placeholder: "Type the reply.", Images: true})

// In Update: give it the keys and the pastes.
switch m.Update(msg, false) {
case composer.EventSend:
	parts, ok := m.SubmitParts()
	// send parts
case composer.EventClose:
	// hide the editor. The draft stays.
}

// In View: draw it in the host's look, on the ground of its panel.
lines := m.Render(width, styles, surface)
hints := m.Hints(styles.ASCII)
```

- `New(Options)` makes an editor. `Options` sets the rows, the paste size
  that makes a block, the draft limit, the placeholder, images and a
  one-line mode.
- `Update(key, remote)` handles a key and returns an `Event`: send, close,
  paste from the clipboard, open an image, open `$EDITOR`, or cleared.
  `Paste`, `Insert` and `AddImage` add text and images. `remote` marks
  input that did not come from the keyboard.
- `Submit` and `SubmitParts` return the message with every block expanded.
  `Parts`, `Text`, `SetText`, `Clear`, `Undo` and `Redo` read and change the
  draft. `Clone` and `PutBack` keep a draft while another one is edited.
- `Render(width, Styles, surface)` returns the editor as lines of exactly
  `width` cells. `Styles` is the whole look: the colours, the selection
  ground, the marks for a terminal of 16 colours, ASCII mode, and the
  functions that keep text readable and mix a block's ground. The composer
  has no colours of its own.
- `Hints(ascii)` lists the keys the editor offers now, for the host's
  footer.
- `SetCompleter`, `SetHistory` and `SetPreviewer` connect the `@` list,
  the history search and the image preview.
