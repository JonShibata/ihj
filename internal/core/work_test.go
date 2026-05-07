package core

import (
	"testing"
)

func TestWorkItem_Hashing(t *testing.T) {
	item1 := &WorkItem{
		ID:      "ENG-1",
		Type:    "Story",
		Summary: "Hash Test",
		Status:  "To Do",
		Fields:  map[string]any{"priority": "High", "sprint": 1},
	}

	// 1. Test Determinism
	hashA := item1.ContentHash()
	hashB := item1.ContentHash()
	if hashA != hashB {
		t.Errorf("ContentHash is not deterministic: %s != %s", hashA, hashB)
	}

	// 2. Test Core Field Change Detection
	item1.Summary = "Updated Hash Test"
	hashC := item1.ContentHash()
	if hashA == hashC {
		t.Error("ContentHash did not change when Summary was updated")
	}

	// 3. Test Flex Bucket Change Detection
	item1.Summary = "Hash Test" // revert
	item1.Fields["priority"] = "Low"
	hashD := item1.ContentHash()
	if hashA == hashD {
		t.Error("ContentHash did not change when Fields map was updated")
	}

}

func TestIsZeroFieldValue(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want bool
	}{
		{"nil", nil, true},
		{"empty string", "", true},
		{"non-empty string", "hello", false},
		{"empty string slice", []string{}, true},
		{"non-empty string slice", []string{"a"}, false},
		{"empty any slice", []any{}, true},
		{"non-empty any slice", []any{"a"}, false},
		{"false bool", false, true},
		{"true bool", true, false},
		{"integer (non-zero)", 42, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsZeroFieldValue(tt.val); got != tt.want {
				t.Errorf("IsZeroFieldValue(%v) = %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}

func TestDisplayStringField(t *testing.T) {
	tests := []struct {
		name          string
		fields        map[string]any
		displayFields map[string]any
		key           string
		want          string
	}{
		{
			name:   "string field",
			fields: map[string]any{"assignee": "alice"},
			key:    "assignee",
			want:   "alice",
		},
		{
			name:          "display override",
			fields:        map[string]any{"assignee": "alice@example.com"},
			displayFields: map[string]any{"assignee": "Alice"},
			key:           "assignee",
			want:          "Alice",
		},
		{
			name:   "string slice joined",
			fields: map[string]any{"labels": []string{"security", "q1"}},
			key:    "labels",
			want:   "security, q1",
		},
		{
			name:   "empty string slice",
			fields: map[string]any{"labels": []string{}},
			key:    "labels",
			want:   "",
		},
		{
			name:   "missing field",
			fields: map[string]any{},
			key:    "labels",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &WorkItem{Fields: tt.fields, DisplayFields: tt.displayFields}
			if got := w.DisplayStringField(tt.key); got != tt.want {
				t.Errorf("DisplayStringField(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestSortItems_StatusThenPriority(t *testing.T) {
	statusOrder := map[string]StatusOrderEntry{
		"to do":       {Weight: 10},
		"in progress": {Weight: 20},
		"done":        {Weight: 30},
	}
	items := []*WorkItem{
		{ID: "P-1", Status: "Done", Type: "Task", Fields: map[string]any{"priority": "Highest"}},
		{ID: "P-2", Status: "To Do", Type: "Task", Fields: map[string]any{"priority": "Low"}},
		{ID: "P-3", Status: "To Do", Type: "Task", Fields: map[string]any{"priority": "Highest"}},
		{ID: "P-4", Status: "In Progress", Type: "Task", Fields: map[string]any{"priority": "Medium"}},
		{ID: "P-5", Status: "To Do", Type: "Task", Fields: map[string]any{"priority": "High"}},
	}
	SortItems(items, statusOrder, nil, nil)

	want := []string{"P-3", "P-5", "P-2", "P-4", "P-1"}
	for i, w := range want {
		if items[i].ID != w {
			t.Errorf("position %d: got %s, want %s", i, items[i].ID, w)
		}
	}
}

func TestSortItems_CustomPriorityOrder(t *testing.T) {
	// Workspace overrides default — P0 outranks P1, etc.
	priorityOrder := map[string]int{"p0": 1, "p1": 2, "p2": 3}
	items := []*WorkItem{
		{ID: "C-1", Status: "Open", Fields: map[string]any{"priority": "P2"}},
		{ID: "C-2", Status: "Open", Fields: map[string]any{"priority": "P0"}},
		{ID: "C-3", Status: "Open", Fields: map[string]any{"priority": "P1"}},
	}
	SortItems(items, nil, priorityOrder, nil)
	want := []string{"C-2", "C-3", "C-1"}
	for i, w := range want {
		if items[i].ID != w {
			t.Errorf("position %d: got %s, want %s", i, items[i].ID, w)
		}
	}
}

func TestSortItems_UnknownPriorityFallback(t *testing.T) {
	// Items without a known priority should sort to the end (weight 99),
	// not crash or interleave randomly.
	items := []*WorkItem{
		{ID: "U-1", Fields: map[string]any{"priority": "Mystery"}},
		{ID: "U-2", Fields: map[string]any{"priority": "High"}},
		{ID: "U-3"},
	}
	SortItems(items, nil, nil, nil)
	if items[0].ID != "U-2" {
		t.Errorf("known priority should sort first; got %s", items[0].ID)
	}
}
