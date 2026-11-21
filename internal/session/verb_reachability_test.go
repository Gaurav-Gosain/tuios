package session

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/pastebuf"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

type exampleOutcome struct {
	errCode string
	blocks  bool
	why     string
}

var exampleOutcomes = map[string]exampleOutcome{
	"swap-window#0":          {errCode: ErrVerbInvalidParams, why: "the fixture's session floats its panes, so there is no layout to swap in"},
	"move-window#2":          {errCode: ErrVerbInvalidParams, why: "the fixture's session floats its panes, so there is no layout to move beside"},
	"plugin-cell#0":          {errCode: ErrVerbForbidden, why: "the fixture's caller is not a plugin's process"},
	"agent-preset#0":         {errCode: ErrVerbInvalidParams, why: "the fixture's daemon has no [agents.presets]"},
	"plugin-notify#0":        {errCode: ErrVerbForbidden, why: "the fixture's caller is not a plugin's process"},
	"block-task#0":           {errCode: ErrVerbNoTask, why: "the fixture's session holds no task card"},
	"block-task#1":           {errCode: ErrVerbNoTask, why: "the fixture's session holds no task card"},
	"claim-task#0":           {errCode: ErrVerbNoTask, why: "the fixture's session holds no task card"},
	"claim-task#1":           {errCode: ErrVerbNoTask, why: "the fixture's session holds no task card"},
	"finish-task#0":          {errCode: ErrVerbNoTask, why: "the fixture's session holds no task card"},
	"get-task#0":             {errCode: ErrVerbNoTask, why: "the fixture's session holds no task card"},
	"attach-task#0":          {errCode: ErrVerbNoTask, why: "the fixture's session holds no task card"},
	"attach-task#1":          {errCode: ErrVerbNoTask, why: "the fixture's session holds no task card"},
	"detach-task#0":          {errCode: ErrVerbNoTask, why: "the fixture's session holds no task card"},
	"get-task-attachment#0":  {errCode: ErrVerbNoTask, why: "the fixture's session holds no task card"},
	"release-task#0":         {errCode: ErrVerbNoTask, why: "the fixture's session holds no task card"},
	"remove-task#0":          {errCode: ErrVerbNoTask, why: "the fixture's session holds no task card"},
	"set-task-stage#0":       {errCode: ErrVerbNoTask, why: "the fixture's session holds no task card"},
	"list-dock-components#0": {errCode: ErrVerbNeedsClient, why: "the dock is drawn by a client"},
	"daemon-upgrade#0":       {errCode: ErrVerbUpgradeRefused, why: "the fixture daemon has no tuios command line to exec"},
	"apply-config#0":         {errCode: ErrVerbCommandFailed, why: "the fixture daemon reads no config file"},
	"apply-config#1":         {errCode: ErrVerbCommandFailed, why: "the fixture daemon reads no config file"},
	"list-dock-components#1": {errCode: ErrVerbNeedsClient, why: "the dock is drawn by a client"},
	"refresh-dock#0":         {errCode: ErrVerbNeedsClient, why: "the dock is drawn by a client"},
	"switch-session#0":       {errCode: ErrVerbNeedsClient, why: "switch-session moves an attached client"},
	"switch-session#1":       {errCode: ErrVerbNeedsClient, why: "switch-session moves an attached client"},
	"switch-session#2":       {errCode: ErrVerbNeedsClient, why: "switch-session moves an attached client"},
	"detach-client#0":        {errCode: ErrVerbInvalidParams, why: "the example names a client id, and the fixture has no attached client"},
	"refresh-dock#1":         {errCode: ErrVerbNeedsClient, why: "the dock is drawn by a client"},
	"pip#0":                  {errCode: ErrVerbNeedsClient, why: "the picture-in-picture view is drawn by a client"},
	"pip#1":                  {errCode: ErrVerbNeedsClient, why: "the picture-in-picture view is drawn by a client"},
	"popup#0":                {errCode: ErrVerbNeedsClient, why: "a popup is a client window"},
	"tml-open#0":             {errCode: ErrVerbNeedsClient, why: "a popup is a client window"},
	"tml-open#2":             {errCode: ErrVerbNeedsClient, why: "a popup is a client window"},
	"tml-close#0":            {errCode: ErrVerbNoSurface, why: "the example names a surface the fixture never opened"},
	"tml-input#0":            {errCode: ErrVerbNoSurface, why: "the example names a surface the fixture never opened"},
	"tml-patch#0":            {errCode: ErrVerbNoSurface, why: "the example names a surface the fixture never opened"},
	"tml-replace#0":          {errCode: ErrVerbNoSurface, why: "the example names a surface the fixture never opened"},
	"tml-get#0":              {errCode: ErrVerbNoSurface, why: "the example names a surface the fixture never opened"},
	"tml-place#0":            {errCode: ErrVerbNoSurface, why: "the example names a surface the fixture never opened"},
	"popup#1":                {errCode: ErrVerbNeedsClient, why: "a popup is a client window"},
	"popup#2":                {errCode: ErrVerbNeedsClient, why: "a popup is a client window"},
	"ask-human#0":            {blocks: true, why: "nobody in the fixture answers the question"},
	"ask-human#1":            {errCode: ErrVerbInvalidParams, why: "the example's request id is a placeholder"},
	"answer-ask#0":           {errCode: ErrVerbNotHuman, why: "only a client attached right now may answer, and none is"},
	"answer-prompt#0":        {errCode: ErrVerbNotHuman, why: "only a client attached right now may answer, and none is"},
	"resolve-pane#0":         {errCode: ErrVerbWindowNotFound, why: "the example's pids are not the fixture's shells"},
	"dismiss-attention#0":    {errCode: ErrVerbNotHuman, why: "only a client attached right now may dismiss, and none is"},
	"reply-approval#0":       {errCode: ErrVerbNotHuman, why: "only a client attached right now may answer, and none is"},
	"agent-transcript#0":     {errCode: ErrVerbNotHuman, why: "only a client attached right now may read a transcript, and none is"},
	"agent-transcript#1":     {errCode: ErrVerbNotHuman, why: "only a client attached right now may read a transcript, and none is"},
	"reply-history#0":        {errCode: ErrVerbNotHuman, why: "only a client attached right now reads the reply history, and none is"},
	"add-reply-history#0":    {errCode: ErrVerbNotHuman, why: "only a client attached right now adds to the reply history, and none is"},
	"agent-transcript#2":     {errCode: ErrVerbNotHuman, why: "only a client attached right now may read a transcript, and none is"},
	"agent-transcript#3":     {errCode: ErrVerbNotHuman, why: "only a client attached right now may read a transcript, and none is"},
	"register-push#0":        {errCode: ErrVerbNotHuman, why: "only the person may register a phone, with a live nonce"},
	"list-push#0":            {errCode: ErrVerbNotHuman, why: "only the person may list the phones, with a live nonce"},
	"remove-push#0":          {errCode: ErrVerbNotHuman, why: "only the person may remove a phone, with a live nonce"},
	"run-command#0":          {errCode: ErrVerbNeedsClient, why: "ToggleZoom is a client command"},
	"set-layout#0":           {errCode: ErrVerbNeedsClient, why: "tiling is the client's arithmetic"},
	"set-layout#1":           {errCode: ErrVerbNeedsClient, why: "the master-stack shape is laid out by a client"},

	"resume-agent#0": {errCode: ErrVerbNotResumable, why: "no conversation is recorded in the fixture"},
	"resume-agent#1": {errCode: ErrVerbNotResumable, why: "no conversation is recorded in the fixture"},

	"open-host-connection#0": {errCode: ErrVerbUnknownHost, why: "no hosts are configured in the fixture"},
	"retry-host#0":           {errCode: ErrVerbUnknownHost, why: "no hosts are configured in the fixture"},
	"list-attention#3":       {errCode: ErrVerbUnknownHost, why: "no hosts are configured in the fixture"},

	"resize-pane#0":       {errCode: ErrVerbUnknownPane, why: "the example's pane id is a literal, not one this daemon handed out"},
	"pane-cwd#0":          {errCode: ErrVerbUnknownPane, why: "the example's pane id is a literal, not one this daemon handed out"},
	"pane-agent#0":        {errCode: ErrVerbUnknownPane, why: "the example's pane id is a literal, not one this daemon handed out"},
	"pane-calls#0":        {errCode: ErrVerbUnknownPane, why: "the example's pane id is a literal, not one this daemon handed out"},
	"close-pane#0":        {errCode: ErrVerbUnknownPane, why: "the example's pane id is a literal, not one this daemon handed out"},
	"paste-pane-image#0":  {errCode: ErrVerbUnknownPane, why: "the example's pane id is a literal, not one this daemon handed out"},
	"pane-kitty-medium#0": {errCode: ErrVerbUnknownPane, why: "the example's pane id is a literal, not one this daemon handed out"},
	"paste-image#0":       {errCode: ErrVerbNotHuman, why: "the example's nonce is a placeholder, and no client here holds one"},

	"link-peer#0":             {errCode: ErrVerbForbidden, why: "the fixture calls on the daemon's own socket, not a link socket"},
	"run-task-verify#0":       {errCode: ErrVerbForbidden, why: "the fixture calls on the daemon's own socket, not a link socket"},
	"release-agent-message#0": {errCode: ErrVerbNotHuman, why: "only a client attached right now may release, and none is"},

	"ask-agent#0":           {errCode: ErrVerbWindowNotFound, why: "$TUIOS_PANE_ID is unexpanded"},
	"read-agent-messages#0": {errCode: ErrVerbWindowNotFound, why: "$TUIOS_PANE_ID is unexpanded"},
	"send-agent-message#0":  {errCode: ErrVerbWindowNotFound, why: "$TUIOS_PANE_ID is unexpanded"},
	"wait-for#2":            {errCode: ErrVerbWindowNotFound, why: "$TUIOS_PANE_ID is unexpanded"},
	"wait-for#3":            {errCode: ErrVerbWindowNotFound, why: "$TUIOS_PANE_ID is unexpanded"},

	"send-agent-message#4": {errCode: ErrVerbWindowNotFound, why: "no agent pane matches the selector in the fixture"},
	"ask-agent#1":          {errCode: ErrVerbWindowNotFound, why: "no agent pane matches the selector in the fixture"},

	"new-session#1":        {errCode: ErrVerbInvalidParams, why: "/src/api does not exist here"},
	"new-window#1":         {errCode: ErrVerbInvalidParams, why: "/src/api does not exist here"},
	"send-agent-message#2": {errCode: ErrVerbInvalidParams, why: "/tmp/flame.png does not exist here"},
	"send-agent-message#3": {errCode: ErrVerbInvalidParams, why: "message 12 does not exist here"},
	"stash-put#0":          {errCode: ErrVerbInvalidParams, why: "/tmp/flame.png does not exist here"},
	"stash-get#0":          {errCode: ErrVerbInvalidParams, why: "the example path is a placeholder, not a file this session stashed"},
	"file-list#0":          {errCode: ErrVerbNoFile, why: "~/src does not exist here"},
	"file-read#0":          {errCode: ErrVerbNoFile, why: "~/notes.md does not exist here"},
	"file-rename#0":        {errCode: ErrVerbNoFile, why: "~/a.txt does not exist here"},
	"file-hash#0":          {errCode: ErrVerbNoFile, why: "~/big.iso does not exist here"},
	"file-preview#0":       {errCode: ErrVerbNoFile, why: "~/Pictures/cat.jpg does not exist here"},
	"file-walk#0":          {errCode: ErrVerbNoFile, why: "~/photos does not exist here"},
	"stop-recording#0":     {errCode: ErrVerbNoRecording, why: "the fixture's fresh session records no pane"},
	"mark-recording#0":     {errCode: ErrVerbNoRecording, why: "the fixture's fresh session records no pane"},
	"file-commit#0":        {errCode: ErrVerbNoFile, why: "no copy to ~/big.iso is in flight here"},
	"open-file-stream#0":   {errCode: ErrVerbNoFile, why: "~/big.iso does not exist here"},
	"open-file-stream#1":   {errCode: ErrVerbInvalidParams, why: "no part of a copy to ~/big.iso is here to go on from"},
	"drop-files#0":         {errCode: ErrVerbNoFile, why: "~/Desktop/screenshot.png does not exist here"},
	"transfer-start#0":     {errCode: ErrVerbUnknownHost, why: "the fixture daemon has no hosts"},
	"transfer-cancel#0":    {errCode: ErrVerbNoTransfer, why: "the example's id is a placeholder"},
	"transfer-pause#0":     {errCode: ErrVerbNoTransfer, why: "the example's id is a placeholder"},
	"transfer-resume#0":    {errCode: ErrVerbNoTransfer, why: "the example's id is a placeholder"},
	"transfer-answer#0":    {errCode: ErrVerbNoTransfer, why: "the example's id is a placeholder"},
	"host-speed-test#0":    {errCode: ErrVerbUnknownHost, why: "the fixture daemon has no hosts"},
	"open-tree-stream#0":   {errCode: ErrVerbNoFile, why: "~/src does not exist here"},
	"peek-prompt#0":        {errCode: ErrVerbWindowNotFound, why: "the example's window id is a placeholder"},
	"respond#0":            {errCode: ErrVerbWindowNotFound, why: "the example's window id is a placeholder"},
	"pane-grants#1":        {errCode: ErrVerbForbidden, why: "the pane id and token are placeholders"},
	"set-pane-grants#0":    {errCode: ErrVerbWindowNotFound, why: "the example's window id is a placeholder"},
	"set-pane-grants#1":    {errCode: ErrVerbInvalidParams, why: "the caller runs in no pane, so it has no own pane to mean"},
	"set-pane-grants#2":    {errCode: ErrVerbWindowNotFound, why: "the example's window id is a placeholder"},

	"new-worktree#0":    {errCode: ErrVerbGitFailed, why: "/src/api is not a git repository here"},
	"new-worktree#1":    {errCode: ErrVerbGitFailed, why: "/src/api is not a git repository here"},
	"new-worktree#2":    {errCode: ErrVerbInvalidParams, why: "the fixture's home has no ~/src"},
	"new-worktree#3":    {errCode: ErrVerbGitFailed, why: "/src/api is not a git repository here"},
	"remove-worktree#0": {errCode: ErrVerbSessionNotFound, why: "api-feat-retry does not exist here"},
	"remove-worktree#1": {errCode: ErrVerbSessionNotFound, why: "api-feat-retry does not exist here"},
	"fan#0":             {errCode: ErrVerbGitFailed, why: "/src/api is not a git repository here"},
	"fan#1":             {errCode: ErrVerbGitFailed, why: "/src/api is not a git repository here"},
	"place-agent#0":     {errCode: ErrVerbNoMachine, why: "the fixture has no linked machine and claude is not signed in here"},
	"place-agent#1":     {errCode: ErrVerbNoMachine, why: "the fixture has no codex and no linked machine"},
	"place-agent#2":     {errCode: ErrVerbSessionNotFound, why: "the fixture has no session api"},
	"start-agent#0":     {errCode: ErrVerbInvalidParams, why: "/src/api does not exist here"},
	"start-agent#1":     {errCode: ErrVerbInvalidParams, why: "/src/api does not exist here"},
	"start-agent#2":     {errCode: ErrVerbInvalidParams, why: "the fixture's home has no ~/src"},
	"start-agent#3":     {errCode: ErrVerbInvalidParams, why: "/src/api does not exist here"},
	"delegate#0":        {errCode: ErrVerbInvalidParams, why: "the fixture's caller is in no agent pane, so the answer has no parent to go to"},
	"delegate#1":        {errCode: ErrVerbInvalidParams, why: "the fixture has no agent pane whose folder is a git checkout"},
	"bundle-worktree#0": {errCode: ErrVerbSessionNotFound, why: "api-fan-add-retry-2 does not exist here"},
	"bundle-worktree#1": {errCode: ErrVerbInvalidParams, why: "the token is a placeholder, and only the connection that made a transfer can read it"},

	"list-host-agents#1":   {errCode: ErrVerbUnknownHost, why: "no hosts are configured here"},
	"list-host-sessions#1": {errCode: ErrVerbUnknownHost, why: "no hosts are configured here"},

	"unsubscribe#0": {errCode: ErrVerbInvalidRequest, why: "the connection has no subscription"},

	"wait-for#0": {blocks: true, why: "no pane prints \"done\""},
	"wait-for#1": {blocks: true, why: "no pane reports an agent state"},
	"wait-for#4": {blocks: true, why: "no pane in any session reports an agent state"},
	"wait-for#5": {blocks: true, why: "no shell in the fixture marks its commands"},
	"wait-for#6": {blocks: true, why: "no pane matches the selector, so every pane matching can never hold"},
	"wait-for#7": {blocks: true, why: "no pane prints \"BUILD OK\" after the wait starts"},

	"run#0": {errCode: ErrVerbNoShellIntegration, why: "no shell in the fixture marks its commands"},
	"run#1": {errCode: ErrVerbNoShellIntegration, why: "no shell in the fixture marks its commands"},

	"review-diff#0":        {errCode: ErrVerbSessionNotFound, why: "api-fan-retry-2 does not exist here"},
	"send-review#0":        {errCode: ErrVerbInvalidParams, why: "the fixture's build window runs no agent"},
	"review-stage#0":       {errCode: ErrVerbHunkChanged, why: "the fixture's repository has no change to api/retry.go"},
	"review-stage#1":       {errCode: ErrVerbHunkChanged, why: "the fixture's repository has no change to api/retry.go"},
	"review-stage#2":       {errCode: ErrVerbInvalidParams, why: "the example's sha is a placeholder"},
	"checkpoint-diff#0":    {errCode: ErrVerbNoCheckpoint, why: "no agent finished a turn, so build has no checkpoint 2"},
	"restore-checkpoint#0": {errCode: ErrVerbNoCheckpoint, why: "no agent finished a turn, so build has no checkpoint 1"},
	"queue-prompt#0":       {errCode: ErrVerbInvalidParams, why: "the fixture's build window runs no agent"},
	"queue-prompt#1":       {errCode: ErrVerbInvalidParams, why: "the fixture's build window runs no agent; its parts are read and checked first"},
	"cancel-queued#1":      {errCode: ErrVerbInvalidParams, why: "nothing is queued, so q3 is not an entry"},
	"compare-fan#0":        {errCode: ErrVerbSessionNotFound, why: "api-fan-retry-1 does not exist here"},
	"verify-fan#0":         {errCode: ErrVerbSessionNotFound, why: "api-fan-retry-1 does not exist here"},
	"keep-fan#0":           {errCode: ErrVerbSessionNotFound, why: "api-fan-retry-1 does not exist here"},
	"keep-fan#1":           {errCode: ErrVerbSessionNotFound, why: "api-fan-retry-1 does not exist here"},
	"keep-fan#2":           {errCode: ErrVerbSessionNotFound, why: "api-fan-retry-1 does not exist here"},
	"set-fan-verdict#0":    {errCode: ErrVerbSessionNotFound, why: "api-fan-retry does not exist here"},
	"judge-fan#0":          {errCode: ErrVerbSessionNotFound, why: "api-fan-retry does not exist here"},
	"ship-commit#0":        {errCode: ErrVerbNothingToCommit, why: "the fixture's repository has no change"},
	"ship-merge#0":         {errCode: ErrVerbNotWorktree, why: "build is in the main checkout, not a worktree"},
	"ship-merge#1":         {errCode: ErrVerbNotWorktree, why: "build is in the main checkout, not a worktree"},
	"ship-push#0":          {errCode: ErrVerbNoRemote, why: "the fixture's repository has no remote"},
	"ship-pr#0":            {errCode: ErrVerbNoRemote, why: "the fixture's repository has no remote"},
	"get-approval#0":       {errCode: ErrVerbInvalidParams, why: "no approval is held in the fixture"},
	"set-agent-quiet#0":    {errCode: ErrVerbInvalidParams, why: "the fixture's pane runs no agent. TestQuietPaneKeepsOutOfTheInbox proves the verb"},
	"mark-attention#0":     {errCode: ErrVerbNotHuman, why: "only a client attached right now may snooze, and none is"},
	"mark-attention#1":     {errCode: ErrVerbNotHuman, why: "only a client attached right now may settle, and none is"},
}

const blockBudget = 750 * time.Millisecond

const answerBudget = time.Minute

// TestEveryVerbExampleReachesItsHandler runs every example in the registry
// against a real daemon.
//
// The failure it exists for is a verb that answers without doing anything. A
// handler replaced with one that returns an empty result and no error fails
// here for every verb at once: an empty result carries no field, a verb that
// documents what it returns names none of them, and a verb that is meant to
// block answers immediately.
func TestEveryVerbExampleReachesItsHandler(t *testing.T) {
	for _, env := range []string{"HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(env, t.TempDir())
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Chdir(testutil.GitRepo(t))
	d, socketPath := startTestDaemon(t)

	names := make([]string, 0, len(verbRegistry()))
	for name := range verbRegistry() {
		names = append(names, name)
	}
	sort.Strings(names)

	seen := map[string]bool{}
	for _, name := range names {
		entry := verbRegistry()[name]
		if len(entry.examples) == 0 {
			t.Errorf("verb %q documents no example, so nothing here can call it", name)
			continue
		}
		for i, example := range entry.examples {
			key := exampleKey(name, i)
			seen[key] = true
			t.Run(key, func(t *testing.T) {
				freshWorkSession(t, d)

				skipIfExampleNeedsAMissingProgram(t, example)

				want := exampleOutcomes[key]
				budget := answerBudget
				if want.blocks {
					budget = blockBudget
				}
				resp, err := callOnce(t, socketPath, example, budget)
				if want.blocks {
					if err == nil {
						t.Fatalf("%s answered %v, but this example has nothing to wait for: "+
							"a wait that returns at once is a wait that is not waiting", key, resp)
					}
					return
				}
				if err != nil {
					t.Fatalf("%s did not answer within %v: %v", key, budget, err)
				}

				if e, ok := resp["error"].(map[string]any); ok && e != nil {
					code, _ := e["code"].(string)
					if want.errCode == "" {
						t.Fatalf("%s failed with %q (%v). If the fixture cannot make this "+
							"example work, add it to exampleOutcomes and say why", key, code, e["message"])
					}
					if code != want.errCode {
						t.Fatalf("%s failed with %q, expected %q (%s)", key, code, want.errCode, want.why)
					}
					return
				}
				if want.errCode != "" {
					t.Fatalf("%s succeeded, but exampleOutcomes expects %q (%s): "+
						"drop the row if the verb now works here", key, want.errCode, want.why)
				}

				res, ok := resp["result"].(map[string]any)
				if !ok || len(res) == 0 {
					t.Fatalf("%s answered with an empty result: the handler ran and "+
						"produced nothing (%v)", key, resp)
				}
				assertDocumentedFields(t, key, entry, res)
			})
		}
	}

	for key, want := range exampleOutcomes {
		if !seen[key] {
			t.Errorf("exampleOutcomes names %s (%s), which the registry no longer has", key, want.why)
		}
	}
}

func assertDocumentedFields(t *testing.T, key string, entry verbEntry, res map[string]any) {
	t.Helper()
	if len(entry.returns) == 0 {
		return
	}
	for _, field := range entry.returns {
		if _, ok := res[field.Name]; ok {
			return
		}
	}
	documented := make([]string, 0, len(entry.returns))
	for _, field := range entry.returns {
		documented = append(documented, field.Name)
	}
	got := make([]string, 0, len(res))
	for field := range res {
		got = append(got, field)
	}
	sort.Strings(got)
	t.Errorf("%s returned %v and none of the fields its schema names (%v)", key, got, documented)
}

var verbsWithNoDocumentedResult = map[string]bool{
	"close-window":         true,
	"explain-agent-screen": true, "hello": true,
	"kill-session": true, "list-sessions": true, "list-verbs": true,
	"list-windows": true, "resize": true, "send-text": true,
	"session-info": true, "set-session-accent": true,
	"set-session-name": true, "set-workspace-name": true,
	"set-workspace-order": true, "unsubscribe": true,
}

// TestVerbResultSchemasAreDeclaredOrListed keeps the gap above from widening.
// A result schema is what lets the test check that a handler filled anything
// in, so a verb without one is a verb the check cannot hold to much.
func TestVerbResultSchemasAreDeclaredOrListed(t *testing.T) {
	for name, entry := range verbRegistry() {
		if len(entry.returns) == 0 && !verbsWithNoDocumentedResult[name] {
			t.Errorf("verb %q names no return fields: document them in its registry "+
				"entry, or add it to verbsWithNoDocumentedResult", name)
		}
		if len(entry.returns) > 0 && verbsWithNoDocumentedResult[name] {
			t.Errorf("verb %q documents its result now: drop it from verbsWithNoDocumentedResult", name)
		}
	}
	for name := range verbsWithNoDocumentedResult {
		if _, ok := verbRegistry()[name]; !ok {
			t.Errorf("verbsWithNoDocumentedResult names %q, which the registry does not have", name)
		}
	}
}

func exampleKey(verb string, i int) string {
	return verb + "#" + strconv.Itoa(i)
}

func freshWorkSession(t *testing.T, d *Daemon) {
	t.Helper()
	if d.manager.GetSession("work") != nil {
		_ = d.manager.DeleteSession("work")
	}
	sess, err := d.manager.CreateSession("work", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("create the session the examples name: %v", err)
	}
	for _, name := range []string{"build", "review"} {
		if _, err := sess.AddDaemonWindow(name, nil); err != nil {
			t.Fatalf("add window %q: %v", name, err)
		}
	}
	if d.manager.GetSession("review") != nil {
		_ = d.manager.DeleteSession("review")
	}
	if _, err := d.manager.CreateSession("review", &SessionConfig{}, 80, 24); err != nil {
		t.Fatalf("create the session the move-window example names: %v", err)
	}
	d.buffers = pastebuf.New(pastebuf.DefaultLimit, pastebuf.DefaultMaxBytes)
	for range 4 {
		if _, err := d.buffers.Add("echo automatic", pastebuf.Owner{}); err != nil {
			t.Fatalf("add a buffer: %v", err)
		}
		if _, err := d.buffers.Set("", "echo next", false, pastebuf.Owner{}, nil); err != nil {
			t.Fatalf("set a buffer: %v", err)
		}
	}
	if _, err := d.buffers.Set("deploy", "echo deploy", false, pastebuf.Owner{}, nil); err != nil {
		t.Fatalf("set buffer deploy: %v", err)
	}
}

func callOnce(t *testing.T, socketPath, line string, budget time.Duration) (map[string]any, error) {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, 3*time.Second)
	if err != nil {
		t.Fatalf("dial the daemon: %v", err)
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("write the request: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(budget))
	raw, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var resp map[string]any
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("the daemon answered something that is not JSON: %q (%v)", raw, err)
	}
	return resp, nil
}

func skipIfExampleNeedsAMissingProgram(t *testing.T, example string) {
	t.Helper()
	var call struct {
		Params struct {
			Command []string `json:"command"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(example), &call); err != nil || len(call.Params.Command) == 0 {
		return
	}
	prog := call.Params.Command[0]
	if !filepath.IsAbs(prog) {
		return
	}
	if _, err := os.Stat(prog); err != nil {
		t.Skipf("the example runs %s, which this machine does not have", prog)
	}
}
