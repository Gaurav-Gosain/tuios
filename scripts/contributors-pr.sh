#!/bin/sh
# Publish a redrawn README on a branch and open a pull request for it.
#
# Usage: scripts/contributors-pr.sh [branch]
#   branch: the branch to publish on (default: chore/contributors)
#
# Run by .github/workflows/contributors.yml after scripts/contributors.sh has
# changed README.md. The branch and its commit are made through the GitHub API
# with the job's GITHUB_TOKEN, so GitHub attributes the commit to
# github-actions[bot] and no git identity is needed anywhere. It never writes
# to main.
set -eu

BRANCH="${1:-chore/contributors}"
: "${GITHUB_REPOSITORY:?contributors-pr.sh: GITHUB_REPOSITORY is not set}"
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
blob_sha=$(gh api "$API/contents/README.md?ref=$BRANCH" --jq .sha)

# The request is built as a file: the base64 README is too large to pass
# safely as a command-line argument.
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
base64 -w0 "$ROOT/README.md" > "$WORK/content"
jq -n --rawfile content "$WORK/content" --arg sha "$blob_sha" --arg branch "$BRANCH" \
  '{message: "docs(readme): update the contributors grid", content: $content, sha: $sha, branch: $branch}' \
  > "$WORK/request.json"
gh api -X PUT "$API/contents/README.md" --input "$WORK/request.json" --jq .commit.sha

# The branch update above refreshes an open pull request, so only a missing
# one needs to be created.
open=$(gh pr list --head "$BRANCH" --state open --json number --jq 'length')
if [ "$open" = "0" ]; then
  gh pr create --base main --head "$BRANCH" \
    --title "docs(readme): update the contributors grid" \
    --body "This pull request updates the contributors grid in the README. The contributors workflow made it from the GitHub contributors list."
fi
