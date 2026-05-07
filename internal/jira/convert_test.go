package jira

import (
	"encoding/json"
	"testing"

	"github.com/mikecsmith/ihj/internal/core"
)

func TestIssuesToWorkItems_ExtractsAllCustomFields(t *testing.T) {
	// Extraction uses the union map — all known custom fields are
	// extracted regardless of issue type. Display-time filtering
	// (via TypeConfig.Fields) controls per-type visibility.
	fields := &issueFields{
		Summary:   "A story",
		IssueType: issueType{ID: "10", Name: "Story"},
		Status:    status{Name: "To Do"},
		Customs: map[string]json.RawMessage{
			"customfield_10001": json.RawMessage(`"5"`),
			"customfield_10002": json.RawMessage(`"some value"`),
		},
	}

	customFields := map[string]customFieldBinding{
		"customfield_10001": {Alias: "story_points", Type: core.FieldString},
		"customfield_10002": {Alias: "bug_details", Type: core.FieldString},
	}

	items := issuesToWorkItems([]issue{{Key: "S-1", Fields: *fields}}, nil, customFields)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}

	// Both fields extracted — extraction is broad on purpose.
	if items[0].Fields["story_points"] != "5" {
		t.Errorf("story_points = %v; want \"5\"", items[0].Fields["story_points"])
	}
	if items[0].Fields["bug_details"] != "some value" {
		t.Errorf("bug_details = %v; want \"some value\"", items[0].Fields["bug_details"])
	}
}

func TestIssuesToWorkItems_RichTextExtracted(t *testing.T) {
	fields := &issueFields{
		Summary:   "Has AC",
		IssueType: issueType{ID: "10", Name: "Story"},
		Status:    status{Name: "Open"},
		Customs: map[string]json.RawMessage{
			"customfield_10003": json.RawMessage(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"criterion"}]}]}`),
		},
	}

	customFields := map[string]customFieldBinding{
		"customfield_10003": {Alias: "acceptance_criteria", Type: core.FieldRichText},
	}

	items := issuesToWorkItems([]issue{{Key: "S-1", Fields: *fields}}, nil, customFields)
	if items[0].Fields["acceptance_criteria"] == nil {
		t.Error("rich text field should be extracted as document node")
	}
}

func TestIssuesToWorkItems_MissingCustomFieldSkipped(t *testing.T) {
	// Custom field not present in Jira response — should not appear.
	fields := &issueFields{
		Summary:   "No customs",
		IssueType: issueType{ID: "10", Name: "Story"},
		Status:    status{Name: "Open"},
		Customs:   map[string]json.RawMessage{},
	}

	customFields := map[string]customFieldBinding{
		"customfield_10001": {Alias: "story_points", Type: core.FieldString},
	}

	items := issuesToWorkItems([]issue{{Key: "S-1", Fields: *fields}}, nil, customFields)
	if _, ok := items[0].Fields["story_points"]; ok {
		t.Error("field not in Jira response should not appear in Fields")
	}
}

func TestIssuesToWorkItems_LinksFromIssueLinksAndParent(t *testing.T) {
	fields := &issueFields{
		Summary:   "Has links",
		IssueType: issueType{ID: "11", Name: "Task"},
		Status:    status{Name: "In Progress"},
		Parent: &parentRef{
			Key: "EPIC-7",
			Fields: &struct {
				Summary   string    `json:"summary"`
				Status    status    `json:"status"`
				IssueType issueType `json:"issuetype"`
			}{Summary: "Parent epic", Status: status{Name: "In Progress"}, IssueType: issueType{Name: "Epic"}},
		},
		IssueLinks: []issueLink{
			{
				Type:        issueLinkType{Name: "Blocks", Outward: "blocks", Inward: "is blocked by"},
				OutwardIssue: &linkedIssue{Key: "DOWN-1", Fields: &struct {
					Summary   string    `json:"summary"`
					Status    status    `json:"status"`
					IssueType issueType `json:"issuetype"`
				}{Summary: "Downstream", Status: status{Name: "To Do"}, IssueType: issueType{Name: "Task"}}},
			},
			{
				Type:        issueLinkType{Name: "Blocks", Outward: "blocks", Inward: "is blocked by"},
				InwardIssue: &linkedIssue{Key: "UP-1", Fields: &struct {
					Summary   string    `json:"summary"`
					Status    status    `json:"status"`
					IssueType issueType `json:"issuetype"`
				}{Summary: "Upstream", Status: status{Name: "Done"}, IssueType: issueType{Name: "Task"}}},
			},
		},
	}

	items := issuesToWorkItems([]issue{{Key: "T-1", Fields: *fields}}, nil, nil)
	if len(items) != 1 {
		t.Fatalf("got %d items", len(items))
	}
	links := items[0].Links
	if len(links) != 3 {
		t.Fatalf("Links = %d; want 3 (parent + 2 issuelinks)", len(links))
	}
	// parent first (RelOrder 0).
	if links[0].RelType != "parent" || links[0].Target != "EPIC-7" {
		t.Errorf("[0] = %+v; want parent → EPIC-7", links[0])
	}
	// outward link's RelType is the type's Outward string.
	var hasBlocks, hasBlockedBy bool
	for _, l := range links[1:] {
		if l.RelType == "blocks" && l.Target == "DOWN-1" {
			hasBlocks = true
			if l.TargetSummary != "Downstream" {
				t.Errorf("blocks summary = %q; want Downstream", l.TargetSummary)
			}
		}
		if l.RelType == "is blocked by" && l.Target == "UP-1" {
			hasBlockedBy = true
		}
	}
	if !hasBlocks || !hasBlockedBy {
		t.Errorf("expected both blocks and is blocked by entries; got %+v", links)
	}
}
