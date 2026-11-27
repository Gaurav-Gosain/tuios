# Remote desktop

Shows the desktop of a host in a pane. Your mouse and keys reach the desktop.

Open the palette and choose **Remote desktop: Remote desktop on a host**. Pick a host. The desktop opens in a zoomed pane.

The line at the bottom says who has control, and shows the frames a second and the delay. When the desktop does not change, it says "still, lossless": the picture is then exact.

- **Shared with agents** is the start. Your input reaches the desktop. The agents on the host can type when you have not typed for 2 seconds.
- Press `Ctrl+]` to take control. Then only your input reaches the desktop.
- Press `Ctrl+]` again to give control back.
- Press `Ctrl+\` to close the desktop.

## What you need

- `srterm` on your `PATH` on this machine.
- openh264 on this machine. On Arch Linux, install the `openh264` package. On macOS, run `brew install openh264`.
- A host in the `[hosts]` table that `tuios hosts` shows as up, and plain `ssh` to its address.
- screen-relay on the host. On the host, run `npm i -g screen-relay`, then `screen-relay install`.
- On a Mac host, two permissions that only you can give. On the Mac, open System Settings, then Privacy & Security. Turn on `relay-engine` in **Screen & System Audio Recording** and in **Accessibility**.

```sh
tuios plugins install Gaurav-Gosain/tuios/plugins/remote-desktop
```

## What it does

The pane runs `remote-desktop.sh`. Nothing runs before you open the pane.

1. The script connects to the host with `ssh` and starts screen-relay there if it does not run. On a Mac, the engine runs as the LaunchAgent `dev.srterm.relay-engine`. Then macOS asks for the permissions for `relay-engine` itself.
2. It opens an `ssh -L` tunnel from a free port on this machine to the engine. The engine listens only on the host's loopback address.
3. It runs srterm on the tunnel. srterm decodes the video here and draws it in the pane. Only the compressed video crosses the network.

When you close the pane, the script stops the tunnel. The engine keeps running on the host.

The plugin holds the `ui` grant. The pane uses it only to ask which host in the palette.
