#!/bin/sh
# Publish a refreshed end-to-end test map on a branch and open a pull request
# for it.
#
# Usage: scripts/e2e-map-pr.sh [branch]
#   branch: the branch to publish on (default: ci/e2e-test-map)
#
# Run by .github/workflows/e2e-tui-map.yml after testselect -build changed
# e2e/tui/testselect/testmap.txt. The branch and its commit are made through
# the GitHub API with the job's GITHUB_TOKEN, as in contributors-pr.sh, so
# GitHub signs the commit and no git identity is needed. It never writes to
# main.
set -eu

BRANCH="${1:-ci/e2e-test-map}"
FILE=e2e/tui/testselect/testmap.txt
: "${GITHUB_REPOSITORY:?e2e-map-pr.sh: GITHUB_REPOSITORY is not set}"
API="repos/$GITHUB_REPOSITORY"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Start the branch from main each run, so the pull request holds one commit
# on top of the current main.
main_sha=$(gh api "$API/git/ref/heads/main" --jq .object.sha)
if gh api "$API/git/ref/heads/$BRANCH" >/dev/null 2>&1; then
  gh api -X PATCH "$API/git/refs/heads/$BRANCH" -f "sha=$main_sha" -F force=true >/dev/null
else
  gh api "$API/git/refs" -f "ref=refs/heads/$BRANCH" -f "sha=$main_sha" >/dev/null
fi

# The contents API replaces a file only when given the blob it replaces.
blob_sha=$(gh api "$API/contents/$FILE?ref=$BRANCH" --jq .sha)

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
base64 -w0 "$ROOT/$FILE" > "$WORK/content"
jq -n --rawfile content "$WORK/content" --arg sha "$blob_sha" --arg branch "$BRANCH" \
  '{message: "ci(e2e): refresh the test map", content: $content, sha: $sha, branch: $branch}' \
  > "$WORK/request.json"
gh api -X PUT "$API/contents/$FILE" --input "$WORK/request.json" --jq .commit.sha

open=$(gh pr list --head "$BRANCH" --state open --json number --jq 'length')
if [ "$open" = "0" ]; then
  gh pr create --base main --head "$BRANCH" \
    --title "ci(e2e): refresh the test map" \
    --body "This pull request refreshes the end-to-end test map. The e2e-tui test map workflow made it from a coverage run of every test. A pull request made by the workflow starts no CI. Close and reopen it to run CI. A change to the map runs the whole suite."
fi
