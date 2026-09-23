package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// tuios worktree pull HOST:SESSION: a worktree session's work on another
// machine, brought into a new worktree session on this one.
//
// The far daemon's bundle-worktree hands over the commits as a git bundle and
// the uncommitted work as a patch, in chunks over this command's one
// connection. Here, the bundle is fetched into a branch that did not exist,
// the worktree is made by this machine's daemon the way worktree new makes
// one, and the patch is applied in it. Nothing on either machine is
// overwritten: a branch that exists here is refused, and the far side is only
// read.

// bundleReply is a bundle-worktree reply as this command reads it.
type bundleReply struct {
	Token       string `json:"token"`
	Content     string `json:"content"`
	Next        int64  `json:"next"`
	Done        bool   `json:"done"`
	Repo        string `json:"repo"`
	OriginURL   string `json:"origin_url"`
	Branch      string `json:"branch"`
	Base        string `json:"base"`
	BaseCommit  string `json:"base_commit"`
	Head        string `json:"head"`
	Full        bool   `json:"full"`
	BundleBytes int64  `json:"bundle_bytes"`
	PatchBytes  int64  `json:"patch_bytes"`
	Changes     int    `json:"changes"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
}

// bundleCallTimeout bounds one bundle-worktree call. The first one runs git
// bundle on the far side, which for a whole history takes a while.
const bundleCallTimeout = 3 * time.Minute

// openTransfer makes a transfer on the far daemon and returns its first reply.
func openTransfer(t *verbTarget, session string, full bool) (bundleReply, error) {
	var first bundleReply
	raw, err := t.client.CallWithTimeout("bundle-worktree", map[string]any{"session": session, "full": full}, bundleCallTimeout)
	if err != nil {
		return first, t.explain("bundle-worktree", err)
	}
	if err := json.Unmarshal(raw, &first); err != nil {
		return first, fmt.Errorf("failed to parse response: %w", err)
	}
	return first, nil
}

// releaseTransfer ends a transfer that will not be read to its end.
func releaseTransfer(t *verbTarget, r bundleReply) {
	if r.Done || r.Token == "" {
		return
	}
	_, _ = t.client.Call("bundle-worktree", map[string]any{"token": r.Token, "release": true})
}

// readTransfer reads a transfer from its first reply to its end, and checks
// its length and hash against what the first reply promised.
func readTransfer(t *verbTarget, first bundleReply) ([]byte, error) {
	var data bytes.Buffer
	reply := first
	for {
		chunk, err := base64.StdEncoding.DecodeString(reply.Content)
		if err != nil {
			releaseTransfer(t, reply)
			return nil, fmt.Errorf("the transfer from %s is not base64: %w", t.host, err)
		}
		data.Write(chunk)
		if int64(data.Len()) > first.Size {
			releaseTransfer(t, reply)
			return nil, fmt.Errorf("%s sent more than the %d bytes it said the transfer holds", t.host, first.Size)
		}
		if reply.Done {
			break
		}
		raw, err := t.client.CallWithTimeout("bundle-worktree", map[string]any{"token": first.Token, "offset": reply.Next}, bundleCallTimeout)
		if err != nil {
			return nil, t.explain("bundle-worktree", err)
		}
		reply = bundleReply{}
		if err := json.Unmarshal(raw, &reply); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
	}
	if int64(data.Len()) != first.Size || first.BundleBytes+first.PatchBytes != first.Size || first.BundleBytes < 0 || first.PatchBytes < 0 {
		return nil, fmt.Errorf("the transfer from %s is %d bytes and was said to be %d", t.host, data.Len(), first.Size)
	}
	sum := sha256.Sum256(data.Bytes())
	if hex.EncodeToString(sum[:]) != first.SHA256 {
		return nil, fmt.Errorf("the transfer from %s does not match its checksum. Nothing was changed here. Run the pull again", t.host)
	}
	return data.Bytes(), nil
}

func runWorktreePull(target, repo, branch, name string, detach, jsonOutput bool) error {
	host, sessName := splitHostSession(target)
	if host == "" || sessName == "" {
		return reportVerbError(&diagnosticError{
			What:  fmt.Sprintf("%q does not name a session on another machine.", target),
			Cause: "worktree pull brings work from another machine, and a worktree here is already here.",
			Fix:   "name it as HOST:SESSION, for example 'tuios worktree pull build:api-fan-add-retry-2'. 'tuios worktree ls --host HOST' lists them.",
		}, jsonOutput)
	}
	if err := ensureDaemon(); err != nil {
		return err
	}
	dir, err := repoArg(repo)
	if err != nil {
		return err
	}
	root, err := worktree.Root(dir)
	if err != nil {
		return reportVerbError(&diagnosticError{
			What:  "there is no repository here to pull into.",
			Cause: err.Error(),
			Fix:   "run the command inside your checkout of the repository, or pass --repo with its directory.",
		}, jsonOutput)
	}
	localOrigin, _ := worktree.OriginURL(root)

	t, err := dialHost(host)
	if err != nil {
		return err
	}
	defer t.Close()

	first, err := openTransfer(t, sessName, false)
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	if first.OriginURL != "" && localOrigin != "" && worktree.NormalizeRemote(first.OriginURL) != worktree.NormalizeRemote(localOrigin) {
		releaseTransfer(t, first)
		return reportVerbError(&diagnosticError{
			What:  fmt.Sprintf("%s on %s is a worktree of another repository.", sessName, host),
			Cause: fmt.Sprintf("its origin is %s and the repository here is %s.", first.OriginURL, localOrigin),
			Fix:   "run the pull inside a checkout of " + first.OriginURL + ", or pass --repo with one.",
		}, jsonOutput)
	}
	// A thin bundle needs the commit it starts from. Without it here, the
	// whole branch is asked for instead.
	if !first.Full && first.BaseCommit != "" && !worktree.HasCommit(root, first.BaseCommit) {
		releaseTransfer(t, first)
		if first, err = openTransfer(t, sessName, true); err != nil {
			return reportVerbError(err, jsonOutput)
		}
	}

	// What the far side said is data. The branch goes into a refspec and the
	// head into a git command line, so both are checked for shape before
	// either is used, and the size is held to the cap the far side keeps.
	if err := checkTransferShape(first); err != nil {
		releaseTransfer(t, first)
		return reportVerbError(fmt.Errorf("%s answered with a transfer this tuios will not use: %w", host, err), jsonOutput)
	}
	localBranch := branch
	if localBranch == "" {
		localBranch = first.Branch
	}
	if err := worktree.ValidBranch(localBranch); err != nil {
		releaseTransfer(t, first)
		return reportVerbError(err, jsonOutput)
	}
	if worktree.BranchExists(root, localBranch) {
		releaseTransfer(t, first)
		return reportVerbError(&diagnosticError{
			What:  fmt.Sprintf("branch %s already exists here.", localBranch),
			Cause: "a pull makes a new branch and never moves one that exists.",
			Fix:   fmt.Sprintf("pull it under another name: 'tuios worktree pull %s --branch %s-%s'.", target, localBranch, host),
		}, jsonOutput)
	}

	data, err := readTransfer(t, first)
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	tmp, err := os.MkdirTemp("", "tuios-pull-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if err := landBranch(root, tmp, host, localBranch, first, data); err != nil {
		return reportVerbError(err, jsonOutput)
	}

	client, err := dialVerb()
	if err != nil {
		return err
	}
	params := map[string]any{"repo": root, "branch": localBranch}
	if name != "" {
		params["name"] = name
	}
	raw, err := client.CallWithTimeout("new-worktree", params, 60*time.Second)
	_ = client.Close()
	if err != nil {
		return reportVerbError(fmt.Errorf("branch %s is here with the commits from %s, and its worktree could not be made: %w", localBranch, host, explainVerbError("new-worktree", err)), jsonOutput)
	}
	var made struct {
		Session string `json:"session"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(raw, &made); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	patchNote := ""
	if first.PatchBytes > 0 {
		keep := filepath.Join(os.TempDir(), "tuios-pull-"+worktree.Slug(localBranch)+".patch")
		patchPath := filepath.Join(tmp, "work.patch")
		if err := os.WriteFile(patchPath, data[first.BundleBytes:], 0o600); err != nil {
			return err
		}
		if err := worktree.ApplyPatch(made.Path, patchPath); err != nil {
			_ = os.WriteFile(keep, data[first.BundleBytes:], 0o600)
			patchNote = fmt.Sprintf("The uncommitted work did not apply (%v). It is kept at %s. Apply it with 'git apply %s' in %s.", err, keep, keep, made.Path)
		}
	}

	if jsonOutput {
		out := map[string]any{
			"success":     true,
			"host":        host,
			"from":        sessName,
			"session":     made.Session,
			"branch":      localBranch,
			"path":        made.Path,
			"head":        first.Head,
			"full":        first.Full,
			"changes":     first.Changes,
			"patch_error": patchNote,
		}
		return printJSON(out)
	}
	fmt.Printf("Pulled %s from %s into branch %s at %s.\n", sessName, host, localBranch, shortCommit(first.Head))
	switch {
	case patchNote != "":
		fmt.Println(patchNote)
	case first.Changes > 0:
		fmt.Printf("%d uncommitted %s applied in %s.\n", first.Changes, pluralWord(first.Changes, "change is", "changes are"), made.Path)
	}
	fmt.Printf("Created session '%s'.\n", made.Session)
	if detach {
		fmt.Printf("Attach with 'tuios attach %s'.\n", made.Session)
		return nil
	}
	return runDaemonSession(made.Session, false)
}

// landBranch makes localBranch in the repository at root from a transfer:
// fetched from the bundle, or made at the head when there were no commits to
// carry. It then checks the branch is at the head the far side reported.
//
// localBranch did not exist when the pull began, so on any failure here the
// branch is deleted again. A failed pull leaves nothing behind and can be run
// again without a 'branch already exists' refusal.
func landBranch(root, tmp, host, localBranch string, first bundleReply, data []byte) (err error) {
	defer func() {
		if err != nil && worktree.BranchExists(root, localBranch) {
			_ = worktree.DeleteBranch(root, localBranch)
		}
	}()
	if first.BundleBytes > 0 {
		bundlePath := filepath.Join(tmp, "commits.bundle")
		if err := os.WriteFile(bundlePath, data[:first.BundleBytes], 0o600); err != nil {
			return err
		}
		if err := worktree.FetchBundle(root, bundlePath, first.Branch, localBranch); err != nil {
			return fmt.Errorf("could not fetch the commits from %s: %w", host, err)
		}
	} else if err := worktree.CreateBranch(root, localBranch, first.Head); err != nil {
		return fmt.Errorf("could not make branch %s at %s: %w", localBranch, first.Head, err)
	}
	if got, err := worktree.BranchCommit(root, localBranch); err != nil || got != first.Head {
		return fmt.Errorf("branch %s is at %s here and %s on %s, so the pull did not carry the commits it said it would. Branch %s was removed again", localBranch, got, first.Head, host, localBranch)
	}
	return nil
}

// pullMaxBytes is the largest transfer a pull reads, the same cap the far
// daemon keeps.
const pullMaxBytes = 512 << 20

// checkTransferShape refuses a first reply whose branch, head or sizes could
// not have come from bundle-worktree.
func checkTransferShape(r bundleReply) error {
	if err := worktree.ValidBranch(r.Branch); err != nil {
		return err
	}
	if !isCommitHash(r.Head) {
		return fmt.Errorf("the head %q is not a commit hash", r.Head)
	}
	if r.BaseCommit != "" && !isCommitHash(r.BaseCommit) {
		return fmt.Errorf("the base commit %q is not a commit hash", r.BaseCommit)
	}
	if r.Size < 0 || r.Size > pullMaxBytes || r.BundleBytes < 0 || r.PatchBytes < 0 || r.BundleBytes+r.PatchBytes != r.Size {
		return fmt.Errorf("the sizes %d, %d and %d do not add up or are past the %d MB cap", r.BundleBytes, r.PatchBytes, r.Size, pullMaxBytes>>20)
	}
	if r.Token == "" && !r.Done {
		return fmt.Errorf("there is no token to read the rest with")
	}
	return nil
}

// isCommitHash reports whether s is a full SHA-1 or SHA-256 commit hash.
func isCommitHash(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// shortCommit is a commit hash cut to the length git prints.
func shortCommit(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
