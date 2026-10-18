package review

import "context"

// Git runs one git command in dir with the environment review uses: no
// GIT_DIR, GIT_WORK_TREE or GIT_INDEX_FILE from the caller, no credential
// prompt and no editor, plus extra. The error names the subcommand and
// holds git's message. tuios plugins install fetches with it.
func Git(ctx context.Context, dir string, extra []string, args ...string) (string, error) {
	return git(ctx, dir, extra, args...)
}
