# Hello

A tuios plugin.

## Try it

```sh
tuios plugins dev .
```

`dev` installs this folder and reloads it each time a file changes. Open the command palette in tuios and choose **Hello: Say hello**.

## Check it

```sh
tuios plugins check .
```

The workflow in `.github/workflows/plugin.yml` runs the same check on each push. When you push a tag such as `v0.1.0`, it attaches an archive of the plugin and its checksum to a GitHub release.

## Install it

```sh
tuios plugins install OWNER/REPO
```

Grants are not a sandbox. A plugin runs with the rights of the person who installs it.
