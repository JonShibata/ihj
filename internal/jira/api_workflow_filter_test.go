package jira

import (
	"slices"
	"testing"
)

// filterTransitions must return Jira's verbatim status names. Title-casing
// or any other normalisation breaks the workspace.TransitionHooks lookup
// (the map is keyed on the verbatim Jira status name) and the resulting
// status update fires without the hook-collected fields, which Jira then
// rejects as a required-field error.
func TestFilterTransitions_PreservesVerbatimNames(t *testing.T) {
	transitions := []transition{
		{ID: "11", Name: "Triage",                To: status{Name: "Triage"}},
		{ID: "31", Name: "Start work",            To: status{Name: "Development in Progress"}},
		{ID: "81", Name: "Close | No Resolution", To: status{Name: "Closed | No Resolution"}},
		{ID: "91", Name: "Reopen",                To: status{Name: "To Do"}},
	}

	got := filterTransitions(transitions, "To Do")
	want := []string{"Triage", "Development in Progress", "Closed | No Resolution"}

	if !slices.Equal(got, want) {
		t.Errorf("filterTransitions = %q\nwant %q", got, want)
	}
}

func TestFilterTransitions_DropsSelfTransitionCaseInsensitive(t *testing.T) {
	transitions := []transition{
		{ID: "1", Name: "n/a", To: status{Name: "to do"}}, // same status, lower-case
		{ID: "2", Name: "n/a", To: status{Name: "Done"}},
	}

	got := filterTransitions(transitions, "To Do")
	want := []string{"Done"}

	if !slices.Equal(got, want) {
		t.Errorf("filterTransitions = %q\nwant %q", got, want)
	}
}
