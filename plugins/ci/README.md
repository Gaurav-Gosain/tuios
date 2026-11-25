# CI

Shows the GitHub Actions builds of a repository in a tuios pane: how long a running build has left, its jobs on a timeline, the steps of a job and its log. A failed build opens on the failed job, with the log at the test that failed.

You need [gh](https://cli.github.com), logged in. Every request goes through `gh`, so the plugin never reads or stores a token. You also need Go to build the plugin.

```sh
tuios plugins install Gaurav-Gosain/tuios/plugins/ci
```

Open the command palette and choose **CI: Builds**. The pane shows the repository of the focused pane's folder. Press `G` to choose another repository.

You can also run the program in any terminal:

```sh
bin/tuios-ci                        # the repository of this folder
bin/tuios-ci OWNER/REPO             # another repository
bin/tuios-ci OWNER/REPO --sha SHA   # select the build of one commit
```

## What a build is

A build is a commit. All the runs that a push, a pull request or a dispatch started for one commit are one row. The build runs while one of its runs runs. It is red when one of its runs failed. Its time is from the first start to the last end. A run that a schedule or a comment started is a build of its own.

The list does not show a build in which every run was skipped. Press `S` to show them.

## The time left

The plugin compares the build with the earlier builds that passed: on the same branch when there are three or more, else the builds of the same event. It plays the jobs of the build forward with the times of the last six such builds. The text says "About 3m 20s left". The bar shows the time gone, a tick at the usual end, and a shaded band from the quick end to the slow end. The characters after the bar are the earlier builds, one character each, as tall as the build was long.

With fewer than three earlier builds, the text shows how long the build runs.

## Keys

| Key | Action |
| --- | --- |
| `j` `k` | Next or previous build |
| `h` `l` `tab` | Previous or next job |
| `n` `p` | Next or previous step. The log shows where the step ends. |
| `e` `E` | Next or previous failure |
| `c` | Copy the link of the job |
| `M` | Show only the builds of the default branch |
| `F` | Show only the branch of the selected build |
| `A` | Show only the builds that run or wait for a runner |
| `S` | Show the builds in which every run was skipped |
| `z` | Give the whole height to the log |
| `pgup` `pgdown` `home` `end` | Scroll the log. `end` follows the end again. |
| `r` `space` | Read the builds now |
| `G` | Choose another repository. Type to narrow the list. |
| `?` | Show the keys |
| `q` | Quit |

`c` copies through the terminal (OSC 52), so it works over SSH too.

## What it costs

- While a build runs, the plugin asks GitHub every 6 seconds. When an answer does not change, it waits twice as long, up to 48 seconds.
- When no build runs, the plugin asks nothing. It reads the builds again when you press `r` or come back to its pane.
- The lists ask with the ETag of the last answer. GitHub does not count a "not modified" answer against your hourly limit.
- The log of a running job is one request each time. The jobs of a run that ended are read once.
- The colours are the 16 colours of the terminal, so the plugin follows the tuios theme.

The one file the plugin writes is `last-repo` in its state folder. It holds the name of the last repository, so the next pane opens on it outside a checkout.

## Credits

The design follows [omaci](https://github.com/jankeesvw/omaci) by Jankees van Woezik, under the MIT License. The notice is in [NOTICE](NOTICE).
