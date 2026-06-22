package jira

import (
	"testing"

	"github.com/mikecsmith/ihj/internal/core"
)

func TestCombineJQL(t *testing.T) {
	cases := []struct {
		name   string
		base   string
		filter string
		want   string
	}{
		{
			name:   "base with clause and ORDER BY",
			base:   "project = DEFBE ORDER BY Rank ASC",
			filter: "assignee = currentUser()",
			want:   "(project = DEFBE) AND (assignee = currentUser()) ORDER BY Rank ASC",
		},
		{
			name:   "base with clause only",
			base:   "project = DEFBE",
			filter: "assignee = currentUser()",
			want:   "(project = DEFBE) AND (assignee = currentUser())",
		},
		{
			name:   "base is ORDER BY only",
			base:   "ORDER BY Rank ASC",
			filter: "assignee = currentUser()",
			want:   "(assignee = currentUser()) ORDER BY Rank ASC",
		},
		{
			name:   "base is ORDER BY with leading whitespace",
			base:   "  ORDER BY updated DESC",
			filter: "comment ~ currentUser()",
			want:   "(comment ~ currentUser()) ORDER BY updated DESC",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := combineJQL(tc.base, tc.filter)
			if got != tc.want {
				t.Errorf("combineJQL(%q, %q) =\n  %q\nwant\n  %q", tc.base, tc.filter, got, tc.want)
			}
		})
	}
}

func TestAsJQLClause(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"PROJ-123", "key = PROJ-123"},
		{"proj-123", "key = PROJ-123"}, // bare keys are upper-cased
		{"  ABC-7  ", "key = ABC-7"},   // and trimmed
		{"status = Done", "status = Done"},
		{"sprint = 5 AND assignee = currentUser()", "sprint = 5 AND assignee = currentUser()"},
		{"PROJ-1 OR PROJ-2", "PROJ-1 OR PROJ-2"}, // not a single key → verbatim
	}
	for _, tc := range cases {
		if got := asJQLClause(tc.in); got != tc.want {
			t.Errorf("asJQLClause(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestBuildJQL_AdHocFilter covers the find-by-key/JQL path: an unregistered
// filter name is treated as an ad-hoc clause, while registered names still
// resolve to their configured clause.
func TestBuildJQL_AdHocFilter(t *testing.T) {
	ws := &core.Workspace{
		Slug:    "team",
		Filters: map[string]string{"active": "statusCategory != Done"},
	}
	cfg := &Config{JQL: "project = DEFBE ORDER BY Rank ASC"}

	cases := []struct {
		name       string
		filterName string
		want       string
	}{
		{"registered filter", "active", "(project = DEFBE) AND (statusCategory != Done) ORDER BY Rank ASC"},
		{"bare key ad-hoc", "PROJ-9", "(project = DEFBE) AND (key = PROJ-9) ORDER BY Rank ASC"},
		{"raw JQL ad-hoc", "status = Done", "(project = DEFBE) AND (status = Done) ORDER BY Rank ASC"},
		{"empty filter", "", "project = DEFBE ORDER BY Rank ASC"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildJQL(ws, cfg, tc.filterName)
			if err != nil {
				t.Fatalf("buildJQL(%q) error: %v", tc.filterName, err)
			}
			if got != tc.want {
				t.Errorf("buildJQL(%q) =\n  %q\nwant\n  %q", tc.filterName, got, tc.want)
			}
		})
	}
}
