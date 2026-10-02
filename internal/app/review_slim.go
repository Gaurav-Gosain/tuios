//go:build slim

package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// tuios-slim has no review overlay. These stand in for review_overlay.go,
// review_compare.go, review_look.go and render_review.go. reviewSupported is
// false, so the review keys do what an unbound key does, and the prefix
// menu, the help and the palette leave them out, as they do on a daemon that
// cannot review.

// reviewState keeps the fields the core reads. open is never set.
type reviewState struct {
	open         bool
	loading      bool
	loadingSince time.Time
}

func (m *OS) reviewSupported() bool                          { return false }
func (m *OS) ReviewOpen() bool                               { return false }
func (m *OS) ReviewEditing() bool                            { return false }
func (m *OS) ReviewWheel(int)                                {}
func (m *OS) ReviewEditorType(string)                        {}
func (m *OS) ReviewFocusedPane() (tea.Cmd, bool)             { return nil, false }
func (m *OS) InboxReview() (tea.Cmd, bool)                   { return nil, false }
func (m *OS) SidebarAgentReview(_, _ string) (tea.Cmd, bool) { return nil, false }
func (m *OS) renderReview() string                           { return "" }

// The review's messages. Nothing sends them in tuios-slim.
type (
	ReviewDiffMsg    struct{}
	ReviewNotesMsg   struct{}
	ReviewSentMsg    struct{}
	ReviewCompareMsg struct{}
	ReviewVerifyMsg  struct{}
	ReviewKeptMsg    struct{}
	ReviewTickMsg    struct{}
)

func (m *OS) applyReviewDiff(ReviewDiffMsg)               {}
func (m *OS) applyReviewNotes(ReviewNotesMsg)             {}
func (m *OS) applyReviewSent(ReviewSentMsg)               {}
func (m *OS) applyReviewCompare(ReviewCompareMsg) tea.Cmd { return nil }
func (m *OS) applyReviewVerify(ReviewVerifyMsg) tea.Cmd   { return nil }
func (m *OS) applyReviewKept(ReviewKeptMsg) tea.Cmd       { return nil }
func (m *OS) applyReviewTick(ReviewTickMsg) tea.Cmd       { return nil }
