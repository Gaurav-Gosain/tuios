# PR status

Shows the pull request and CI state of the focused pane's folder in the dock, such as `#123 running`. Click the cell to open the pull request in your browser.

You need [gh](https://cli.github.com), logged in.

```sh
tuios plugins install pr-status
```

A tuios that you built yourself takes `tuios plugins install Gaurav-Gosain/tuios/plugins/pr-status`.

The plugin holds the `ui` grant, which shows text only. A hook runs on each focus and exits. `gh` runs at most once a minute for one branch. Run **PR status: Refresh** from the palette to read it now.
