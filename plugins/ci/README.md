# CI

Shows the GitHub Actions builds of a repository in a tuios pane: how long a running build has left, its jobs on a timeline, the steps of a job and its log. A failed build opens on the failed job, with the log at the test that failed.

You need [gh](https://cli.github.com), logged in. Every request goes through `gh`, so the plugin never reads or stores a token.

```sh
tuios plugins install ci
```

The plugin comes from the release of the tuios that runs, with its program already built. A tuios that you built yourself has no release. Install the plugin from its folder, which needs Go:

```sh
tuios plugins install Gaurav-Gosain/tuios/plugins/ci
```

Open the command palette and choose **CI: Builds**. The pane shows the repository of the focused pane's folder. Press `G` to choose another repository.

You can also run the program in any terminal:

```sh
bin/tuios-ci                        # the repository of this folder
bin/tuios-ci OWNER/REPO             # another repository
bin/tuios-ci OWNER/REPO --sha SHA   # select the build of one commit
bin/tuios-ci wait                   # wait for the build of HEAD to end
```

## The dock

The dock shows the build of the focused pane's commit:

| Cell | Build |
| --- | --- |
| `CI 2m 10s left` | The build runs. The time comes from the earlier builds. |
| `CI running 3m 5s` | The build runs, and fewer than three earlier builds passed. |
| `CI failed: TestFoo` | The build failed. The cell names the first failing test. |
| `CI ok` | The build passed while you worked. The cell goes after 30 seconds. |
| No cell | The commit has no build, or its build passed earlier. |

Click the cell to open the builds on that commit.

When a build that you watched in the dock ends, a message says so: "Build failed: TestFoo in ci.yml", or "Build passed" with its title. A focus on a build that has ended shows its cell and sends no message.

## Wait for a build

`tuios-ci wait` waits for the build of a commit to end. An agent can run it after a push.

```sh
bin/tuios-ci wait                       # the build of HEAD in this folder
bin/tuios-ci wait --sha SHA             # the build of another commit
bin/tuios-ci wait OWNER/REPO --sha SHA  # a commit of another repository
```

It prints one line for each failed job, with the test, its file and line, and the link of the job:

```text
Failed: Add gift cards (bbbbbbb).
✗ test in ci.yml: TestOrderTotal at order_test.go:18 https://github.com/acme/shop/actions/runs/201/job/2002
```

| Exit code | Build |
| --- | --- |
| 0 | It passed. |
| 1 | It failed, or it was cancelled. |
| 2 | The commit has no build. |

After a push, GitHub takes a few seconds to start a build. `wait` looks for one for a minute before it says that there is no build. `--start 10s` changes that time.

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
| `/` | Search the log. `ctrl+n` and `ctrl+p` go to the next and previous match. `esc` closes the search. |
| `c` | Copy the link of the job |
| `o` | Open the job on GitHub |
| `O` | Open the pull request of the build on GitHub |
| `R` | Run the failed jobs of the build again. Press it twice. |
| `ctrl+r` | Run all the jobs of the build again. Press it twice. |
| `m` `M` | Show only the builds of the default branch |
| `f` `F` | Show only the branch of the selected build |
| `a` `A` | Show only the builds that run or wait for a runner |
| `s` `S` | Show the builds in which every run was skipped |
| `#` | Show or hide the line numbers of the log |
| `z` | Give the whole height to the log |
| `pgup` `pgdown` `home` `end` | Scroll the log. `end` follows the end again. |
| `r` `space` | Read the builds now |
| `G` | Choose another repository. Type to narrow the list. |
| `?` | Show the keys |
| `q` | Quit |

`c` copies through the terminal (OSC 52), so it works over SSH too. A rerun costs runner time, so it waits for a second press.

## Mouse

| Action | Result |
| --- | --- |
| Click a build | Select the build |
| Click a job, its name or its bar | Select the job |
| Click a step | Show where the step ends in the log |
| Click a failure above the log | Show the failure in the log |
| Click a filter, an action or a key at the bottom | Run it, as its key does. A rerun waits for a second click. |
| Click the pull request pill | Open the pull request on GitHub |
| Click the name of the job above the log | Open the job on GitHub |
| Turn the wheel | Scroll the builds, the steps or the log under the pointer |
| Drag the row above the log | Change the height of the jobs |
| Click a repository in the menu | Watch that repository. A click outside the menu closes it. |

The row under the pointer shows in a lighter shade. The plugin gets mouse motion only while the pointer moves over its pane.

The plugin takes the mouse, so a drag in the log does not select text. To copy text from the log, use the copy mode of tuios. In most terminals, you can also hold `shift` while you drag.

## What it costs

- While a build runs, the plugin asks GitHub every 6 seconds. When an answer does not change, it waits twice as long, up to 48 seconds.
- When no build runs, the plugin asks nothing. It reads the builds again when you press `r` or come back to its pane.
- A focus asks GitHub once for the build of the commit, and twice more for the log of a failed build. Nothing stays in memory after the focus.
- While the build of the focused commit runs, one process for each repository asks as the pane does. It stops when the builds it watches end.
- The lists ask with the ETag of the last answer. GitHub does not count a "not modified" answer against your hourly limit.
- The log of a running job is one request each time. The jobs of a run that ended are read once.
- The colours come from the palette of the terminal, which a tuios pane takes from the tuios theme. The plugin asks again when the theme changes, so it follows an Omarchy theme switch at once.

The plugin writes three kinds of file in its state folder. `last-repo` holds the name of the last repository, so the next pane opens on it outside a checkout. `focus.json` holds the repository, the branch and the commit of the focused pane. A `watch-OWNER-REPO.lock` file is there while a build of that repository is watched.

## Credits

The design follows [omaci](https://github.com/jankeesvw/omaci) by Jankees van Woezik, under the MIT License. The notice is in [NOTICE](NOTICE).
