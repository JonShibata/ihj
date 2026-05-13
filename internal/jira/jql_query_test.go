package jira

import "testing"

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
