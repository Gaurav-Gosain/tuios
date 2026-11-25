# tuios in Omarchy

[Omarchy](https://omarchy.org) 4 has a bar and a menu of its own. tuios adds
the tuios agents widget to the bar and a tuios row to the menu.

```sh
tuios integration install omarchy
```

## What install does

1. It writes the tuios agents widget and the tuios menu to
   `~/.config/omarchy/plugins/tuios.agents`. Both run this tuios, by its full
   path.
2. It adds a tuios row to `~/.config/omarchy/extensions/omarchy-menu.jsonc`,
   as the first row of the file, under a marked comment line. Your own rows do
   not change. The Omarchy menu reads the file again by itself.
3. It runs `omarchy-shell shell rescanPlugins` and
   `omarchy plugin enable tuios.agents --yes`, so the widget is in the bar.

A `tuios.agents` folder that tuios did not write is left as it is. `--force`
replaces it.

```sh
tuios integration status omarchy      # the widget and the menu row
tuios integration uninstall omarchy   # turns the widget off, removes both
```

Uninstall gives the menu file back as it was before install.

## The widget

The widget shows a count of the items that need you: approvals, questions,
plans and agents that failed. The icon takes the urgent colour of the theme
while an item waits. When nothing needs you, it shows the mail for you, the
agents that work, or the turns to review. With none of these, the widget is
not shown. Set `showIdle` in the widget settings to show it always. When
the daemon refuses a call of the widget, it shows a warning icon, and the
tooltip says why.

The tooltip lists the items that need you, oldest first.

| Click | What it does |
| --- | --- |
| Left | Opens the tuios menu |
| Right | Opens the tuios menu on your sessions |

The widget runs `tuios bar-status --follow`. It waits on the tuios event
stream and does no work while nothing changes. See
[STATUS_BAR.md](STATUS_BAR.md) for the lines it reads.

## The menu

Open it from the tuios row of the Omarchy menu (Super+Space), or with a left
click on the widget. The menu is the script `tuios-menu` in the plugin folder.
It needs `jq`, which Omarchy installs. You can read and change the script.
A later install replaces it.

| Row | What it does |
| --- | --- |
| An item that needs you | Moves the focus to its pane. The terminal that shows the session comes to the front. With no client, a new terminal attaches the session |
| Open the inbox | Opens the agents view in the tuios client you used last, and brings its terminal to the front |
| Sessions, This machine | Lists the sessions on this machine. Choose one to attach it in a new terminal |
| Sessions, a host | Lists the sessions on a linked host whose link is up |

A new terminal opens through `omarchy-launch-tui`, as the rows of Omarchy
do. When tuios does not run, the menu has one row, Start tuios.

## Without Omarchy 4

Omarchy 3 uses Waybar. See [STATUS_BAR.md](STATUS_BAR.md) for a Waybar module.
tuios follows the Omarchy theme in both versions: see "Omarchy themes" in
[CONFIGURATION.md](CONFIGURATION.md#omarchy-themes).
