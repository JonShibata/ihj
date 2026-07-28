package tui_test

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/mikecsmith/ihj/internal/core"
	"github.com/mikecsmith/ihj/internal/terminal"
	"github.com/mikecsmith/ihj/internal/tui"
)

func newHistoryModel(t *testing.T) tui.HistoryModel {
	t.Helper()
	theme := terminal.DefaultTheme(true)
	styles := terminal.NewStyles(theme, nil, "")
	hm := tui.NewHistoryModel(styles)
	hm.SetSize(80, 20)
	return hm
}

func TestHistoryModel_RendersEntries(t *testing.T) {
	hm := newHistoryModel(t)
	hm.SetEntries("ENG-1", []core.HistoryEntry{
		{
			Author:  "Alex Rivera",
			Created: "02 Jan 2026, 10:30",
			Changes: []core.HistoryChange{
				{Field: "status", From: "To Do", To: "In Progress"},
				{Field: "assignee", From: "", To: "Alex Rivera"},
			},
		},
		{
			Author:  "Sarah Kim",
			Created: "01 Jan 2026, 09:00",
			Changes: []core.HistoryChange{
				{Field: "priority", From: "Medium", To: "High"},
			},
		},
	})

	view := stripANSI(hm.View())

	for _, frag := range []string{
		"Alex Rivera", "Sarah Kim", // authors
		"02 Jan 2026, 10:30",             // timestamp
		"status", "assignee", "priority", // field names
		"To Do", "In Progress", "High", // change values
		core.GlyphArrow, // from → to separator
	} {
		if !strings.Contains(view, frag) {
			t.Errorf("history view missing %q\n---\n%s", frag, view)
		}
	}
}

func TestHistoryModel_WrapsLongValues(t *testing.T) {
	theme := terminal.DefaultTheme(true)
	styles := terminal.NewStyles(theme, nil, "")
	hm := tui.NewHistoryModel(styles)
	const width = 50
	hm.SetSize(width, 40)

	long := "This is a very long description value that should wrap across " +
		"several lines instead of being truncated at the edge of the overlay " +
		"box, because wrapping matters"
	hm.SetEntries("ENG-9", []core.HistoryEntry{{
		Author:  "Alex Rivera",
		Created: "02 Jan 2026, 10:30",
		Changes: []core.HistoryChange{{Field: "description", From: "old text", To: long}},
	}})

	view := hm.View()
	for _, ln := range strings.Split(view, "\n") {
		if w := lipgloss.Width(ln); w > width {
			t.Errorf("line exceeds viewport width %d (got %d): %q", width, w, ln)
		}
	}
	// The tail word of the long value must survive — proof it wrapped onto
	// further lines rather than being truncated at the right edge.
	if !strings.Contains(stripANSI(view), "matters") {
		t.Errorf("long value was truncated; tail missing from:\n%s", stripANSI(view))
	}
}

func TestHistoryModel_EmptyPlaceholder(t *testing.T) {
	hm := newHistoryModel(t)
	hm.SetEntries("ENG-2", nil)

	view := stripANSI(hm.View())
	if !strings.Contains(view, "No history") {
		t.Errorf("empty history should show a placeholder, got:\n%s", view)
	}
}

func TestHistoryModel_IssueID(t *testing.T) {
	hm := newHistoryModel(t)
	hm.SetEntries("ENG-3", nil)
	if hm.IssueID() != "ENG-3" {
		t.Errorf("IssueID() = %q, want ENG-3", hm.IssueID())
	}
}
