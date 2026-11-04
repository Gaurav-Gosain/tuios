package review

import "context"

// Git runs one git command in dir with the environment review uses: no
// GIT_DIR, GIT_WORK_TREE or GIT_INDEX_FILE from the caller, no credential
// prompt and no editor, plus extra. The error names the subcommand and
// holds git's message. tuios plugins install fetches with it.
func Git(ctx context.Context, dir string, extra []string, args ...string) (string, error) {
	return git(ctx, dir, extra, args...)
}

// GitHead is Git that returns only the first limit bytes of git's output. When
// git writes more, it is killed at the first byte past the limit and the kept
// bytes are returned with no error; output of exactly limit bytes is returned
// whole. A limit of 0 or less means no limit, and GitHead is then Git.
func GitHead(ctx context.Context, dir string, extra []string, limit int, args ...string) (string, error) {
	return gitHead(ctx, dir, extra, limit, args...)
}
