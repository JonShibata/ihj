package tui

import (
	"time"

	"testing"

	"github.com/mikecsmith/ihj/internal/core"
)

// TestHandleDataReloaded_TitleEditDroppingOutOfFilter_KeepsDetailOnEditedIssue
// pins the fix for a bug where editing the currently-displayed issue's
// title so it no longer matches the active search text caused the detail
// pane to silently swap to an unrelated issue on the next reload/refresh.
func TestHandleDataReloaded_TitleEditDroppingOutOfFilter_KeepsDetailOnEditedIssue(t *testing.T) {
	m := newTestModel(t)

	// Active search filters the list down to just TEST-1 ("Epic One").
	m.list.search.SetValue("Epic")
	m.list.applyFilter()
	if m.list.SelectedIssue() == nil || m.list.SelectedIssue().ID != "TEST-1" {
		t.Fatalf("setup: expected TEST-1 selected under filter, got %+v", m.list.SelectedIssue())
	}
	m.syncDetail()
	if m.detail.Issue() == nil || m.detail.Issue().ID != "TEST-1" {
		t.Fatalf("setup: expected detail on TEST-1, got %+v", m.detail.Issue())
	}

	// Reload as if TEST-1 was renamed so its title no longer matches "Epic".
	renamed := &core.WorkItem{ID: "TEST-1", Summary: "Renamed Thing", Type: "Epic", Status: "In Progress"}
	other := &core.WorkItem{ID: "TEST-2", Summary: "Story One", Type: "Story", Status: "In Progress"}
	msg := dataReloadedMsg{
		filter:    m.filter,
		items:     []*core.WorkItem{renamed, other},
		fetchedAt: time.Now(),
		silent:    true,
	}

	updated, _ := m.handleDataReloaded(msg)
	am, ok := updated.(AppModel)
	if !ok {
		t.Fatalf("handleDataReloaded returned %T, want AppModel", updated)
	}

	// TEST-1 dropped out of the "Epic" filter (its new title doesn't match),
	// but it's still the issue the user was looking at — the detail pane
	// must keep showing it, with the fresh (renamed) data.
	got := am.detail.Issue()
	if got == nil || got.ID != "TEST-1" {
		t.Fatalf("detail.Issue() = %+v, want TEST-1 (renamed), not silently swapped", got)
	}
	if got.Summary != "Renamed Thing" {
		t.Errorf("detail.Issue().Summary = %q, want %q", got.Summary, "Renamed Thing")
	}
}
