package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/tape/trust"
)

func checkTape(t *testing.T, store *trust.Store, dir string) trust.Result {
	t.Helper()
	res, err := store.Check(filepath.Join(dir, trust.TapeFileName))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return res
}

func TestReviewRunOnceDoesNotPersistTrust(t *testing.T) {
	m, store := newDetectOS(t, config.TapeAutorunAsk)
	dir := tapeDir(t, "Scope current\nType \"echo hi\" Enter\n")

	m.openTapeReviewForDir(dir)
	if !m.ShowTapeReview || m.TapeReview.Status != trust.StatusUntrusted {
		t.Fatalf("dialog not open on an untrusted tape: show=%v status=%v", m.ShowTapeReview, m.TapeReview)
	}

	if !m.HandleTapeReviewInput("r") {
		t.Fatalf("run-once key not consumed")
	}
	if !m.ScriptMode {
		t.Fatalf("ScriptMode = false, want the tape to have started")
	}
	if got := checkTape(t, store, dir).Status; got != trust.StatusUntrusted {
		t.Fatalf("trust status = %v after Run once, want still untrusted (must not persist)", got)
	}
	if m.ShowTapeReview {
		t.Fatalf("dialog still open after running")
	}
}

func TestReviewIneligibleOffersNoRun(t *testing.T) {
	m, _ := newDetectOS(t, config.TapeAutorunAsk)
	dir := tapeDir(t, "Type \"echo hi\" Enter\n")
	if err := os.Chmod(filepath.Join(dir, trust.TapeFileName), 0o666); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	m.openTapeReviewForDir(dir)
	if m.TapeReview == nil || m.TapeReview.Status != trust.StatusIneligible {
		t.Fatalf("status = %v, want ineligible", m.TapeReview)
	}
	m.HandleTapeReviewInput("r")
	m.HandleTapeReviewInput("t")
	if m.ScriptMode {
		t.Fatalf("ScriptMode = true, an ineligible tape must never run")
	}
	if !m.HandleTapeReviewInput("esc") || m.ShowTapeReview {
		t.Fatalf("ineligible tape should dismiss on esc")
	}
}

func TestReviewEditedTapeRepromptsAsChanged(t *testing.T) {
	m, store := newDetectOS(t, config.TapeAutorunAsk)
	dir := tapeDir(t, "Type \"one\" Enter\n")
	res := checkTape(t, store, dir)
	if err := store.Trust(res.Path, res.Hash); err != nil {
		t.Fatalf("trust: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, trust.TapeFileName), []byte("Type \"two\" Enter\n"), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	m.openTapeReviewForDir(dir)
	if m.TapeReview.Status != trust.StatusUntrusted {
		t.Fatalf("status = %v, want untrusted after edit", m.TapeReview.Status)
	}
	if !m.TapeReview.Changed {
		t.Fatalf("Changed = false, want the dialog to flag the tape as changed since trusted")
	}
}

func TestAutoModeEditedTrustedTapeDoesNotRun(t *testing.T) {
	m, store := newDetectOS(t, config.TapeAutorunAuto)
	dir := tapeDir(t, "Scope current\nType \"one\" Enter\n")
	res := checkTape(t, store, dir)
	if err := store.Trust(res.Path, res.Hash); err != nil {
		t.Fatalf("trust: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, trust.TapeFileName), []byte("Scope current\nType \"evil\" Enter\n"), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	drive(t, m, "focused", dir)
	if m.ScriptMode {
		t.Fatalf("ScriptMode = true, an edited-since-trusted tape must not auto-run")
	}
}

func TestOffModeRunsNothingFromReview(t *testing.T) {
	m, _ := newDetectOS(t, config.TapeAutorunOff)
	dir := tapeDir(t, "Type \"echo hi\" Enter\n")

	drive(t, m, "focused", dir)
	if m.ScriptMode || m.ShowTapeReview {
		t.Fatalf("off mode must do nothing: ScriptMode=%v show=%v", m.ScriptMode, m.ShowTapeReview)
	}
}

func TestRunRefusesWhileAnotherTapeRuns(t *testing.T) {
	m, _ := newDetectOS(t, config.TapeAutorunAsk)
	dir := tapeDir(t, "Scope current\nType \"echo hi\" Enter\n")
	m.ScriptMode = true

	before := m.ScriptPlayer
	m.runProjectTape([]byte("Scope current\nType \"x\" Enter\n"), dir)
	if m.ScriptPlayer != before {
		t.Fatalf("a second tape started while one was running")
	}
}

func TestAutoReviewDeniedNeverOpens(t *testing.T) {
	m, store := newDetectOS(t, config.TapeAutorunAsk)
	m.UserConfig.Tape.AutoReview = true
	dir := tapeDir(t, "Type \"echo hi\" Enter\n")
	res := checkTape(t, store, dir)
	if err := store.Deny(res.Path); err != nil {
		t.Fatalf("deny: %v", err)
	}

	drive(t, m, "focused", dir)

	if m.ShowTapeReview {
		t.Fatalf("a denied tape auto-opened the dialog; deny must be respected")
	}
}

func TestAutoReviewOffModeNothing(t *testing.T) {
	m, _ := newDetectOS(t, config.TapeAutorunOff)
	m.UserConfig.Tape.AutoReview = true
	dir := tapeDir(t, "Type \"echo hi\" Enter\n")

	drive(t, m, "focused", dir)

	if m.ShowTapeReview {
		t.Fatalf("off mode auto-opened the dialog; off means the feature is invisible")
	}
}

func TestReviewNeverDenies(t *testing.T) {
	m, store := newDetectOS(t, config.TapeAutorunAsk)
	dir := tapeDir(t, "Type \"echo hi\" Enter\n")

	m.openTapeReviewForDir(dir)
	if !m.HandleTapeReviewInput("n") {
		t.Fatalf("never key not consumed")
	}
	if m.ScriptMode {
		t.Fatalf("ScriptMode = true, want Never to run nothing")
	}
	if got := checkTape(t, store, dir).Status; got != trust.StatusDenied {
		t.Fatalf("trust status = %v after Never, want denied", got)
	}
	if _, active := m.tapeIndicatorStatus(); active {
		t.Fatalf("indicator still active after Never; a denied path shows nothing")
	}
}

func TestReviewNotNowDismisses(t *testing.T) {
	m, store := newDetectOS(t, config.TapeAutorunAsk)
	dir := tapeDir(t, "Type \"echo hi\" Enter\n")

	m.openTapeReviewForDir(dir)
	if !m.HandleTapeReviewInput("esc") {
		t.Fatalf("not-now key not consumed")
	}
	if m.ShowTapeReview {
		t.Fatalf("dialog still open after Not now")
	}
	if m.ScriptMode {
		t.Fatalf("ScriptMode = true, want Not now to run nothing")
	}
	if got := checkTape(t, store, dir).Status; got != trust.StatusUntrusted {
		t.Fatalf("trust status = %v after Not now, want still untrusted", got)
	}
}

func TestReviewTrustedTapeRunsAndRevokes(t *testing.T) {
	m, store := newDetectOS(t, config.TapeAutorunAsk)
	dir := tapeDir(t, "Scope current\nType \"echo hi\" Enter\n")
	res := checkTape(t, store, dir)
	if err := store.Trust(res.Path, res.Hash); err != nil {
		t.Fatalf("trust: %v", err)
	}

	m.openTapeReviewForDir(dir)
	if m.TapeReview.Status != trust.StatusTrusted {
		t.Fatalf("status = %v, want trusted", m.TapeReview.Status)
	}
	if !m.HandleTapeReviewInput("r") || !m.ScriptMode {
		t.Fatalf("trusted Run did not start the tape")
	}

	m.ScriptMode = false
	m.openTapeReviewForDir(dir)
	if !m.HandleTapeReviewInput("n") {
		t.Fatalf("revoke key not consumed")
	}
	if got := checkTape(t, store, dir).Status; got != trust.StatusUntrusted {
		t.Fatalf("status = %v after revoke, want untrusted", got)
	}
}

func TestReviewTrustAndRunPersists(t *testing.T) {
	m, store := newDetectOS(t, config.TapeAutorunAsk)
	dir := tapeDir(t, "Scope current\nType \"echo hi\" Enter\n")

	m.openTapeReviewForDir(dir)
	if !m.HandleTapeReviewInput("t") {
		t.Fatalf("trust-and-run key not consumed")
	}
	if !m.ScriptMode {
		t.Fatalf("ScriptMode = false, want the tape to have started")
	}
	if got := checkTape(t, store, dir).Status; got != trust.StatusTrusted {
		t.Fatalf("trust status = %v after Trust and run, want trusted", got)
	}
}
