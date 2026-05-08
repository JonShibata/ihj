package commands_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/mikecsmith/ihj/internal/commands"
	"github.com/mikecsmith/ihj/internal/core"
	"github.com/mikecsmith/ihj/internal/testutil"
)

func TestTransition_TextHookCollected(t *testing.T) {
	ui := &testutil.MockUI{
		SelectReturn:    0, // pick first transition target ("Done")
		InputTextReturn: "5",
	}
	provider := &testutil.MockProvider{
		Caps:              core.Capabilities{HasTransitions: true},
		TransitionOptions: []string{"Done"},
		Registry: map[string]*core.WorkItem{
			"ENG-1": {ID: "ENG-1", Type: "Task", Status: "In Progress"},
		},
	}
	ws := testutil.NewTestSession(ui)
	ws.Provider = provider
	ws.Workspace.TransitionHooks = map[string][]core.TransitionHook{
		"Done": {{Field: "actual_story_points", Prompt: "Actual SP", Type: "text", Required: true}},
	}

	if err := commands.Transition(context.Background(), ws, "ENG-1", ""); err != nil {
		t.Fatal(err)
	}
	call := provider.UpdateCalls[0]
	if call.Changes.Fields["actual_story_points"] != "5" {
		t.Errorf("actual_story_points = %v; want \"5\"", call.Changes.Fields["actual_story_points"])
	}
}

func TestTransition_CSVHookSplits(t *testing.T) {
	ui := &testutil.MockUI{
		SelectReturn:    0,
		InputTextReturn: "26.4, master, ",
	}
	provider := &testutil.MockProvider{
		Caps:              core.Capabilities{HasTransitions: true},
		TransitionOptions: []string{"Done"},
		Registry:          map[string]*core.WorkItem{"ENG-1": {ID: "ENG-1", Type: "Task"}},
	}
	ws := testutil.NewTestSession(ui)
	ws.Provider = provider
	ws.Workspace.TransitionHooks = map[string][]core.TransitionHook{
		"Done": {{Field: "dev_fix_release", Prompt: "Fix Release", Type: "csv"}},
	}

	if err := commands.Transition(context.Background(), ws, "ENG-1", ""); err != nil {
		t.Fatal(err)
	}
	got := provider.UpdateCalls[0].Changes.Fields["dev_fix_release"]
	want := []string{"26.4", "master"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dev_fix_release = %v; want %v", got, want)
	}
}

func TestTransition_VersionsHookLabelsField(t *testing.T) {
	// dev_fix_release is a labels-typed custom field — the hook should
	// emit raw []string so Jira accepts it as a labels write.
	ui := &testutil.MockUI{
		SelectReturn:      0,
		SelectMultiReturn: []int{0, 2},
	}
	provider := &testutil.MockProvider{
		Caps:              core.Capabilities{HasTransitions: true},
		TransitionOptions: []string{"Done"},
		Registry:          map[string]*core.WorkItem{"ENG-1": {ID: "ENG-1", Type: "Task"}},
		VersionsReturn: []core.Version{
			{Name: "26.4"}, {Name: "26.5"}, {Name: "master"},
		},
	}
	ws := testutil.NewTestSession(ui)
	ws.Provider = provider
	ws.Workspace.TransitionHooks = map[string][]core.TransitionHook{
		"Done": {{Field: "dev_fix_release", Prompt: "Fix Release(s)", Type: "versions", Required: true}},
	}

	if err := commands.Transition(context.Background(), ws, "ENG-1", ""); err != nil {
		t.Fatal(err)
	}
	got := provider.UpdateCalls[0].Changes.Fields["dev_fix_release"]
	want := []string{"26.4", "master"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dev_fix_release = %v; want %v", got, want)
	}
}

func TestTransition_VersionsHookVersionField(t *testing.T) {
	// fixVersions is a true Version field — its FieldDef has Enum populated
	// from createmeta allowedValues — the hook should wrap each pick as
	// {"name": "X"} for the API.
	ui := &testutil.MockUI{
		SelectReturn:      0,
		SelectMultiReturn: []int{1},
	}
	provider := &testutil.MockProvider{
		Caps:              core.Capabilities{HasTransitions: true},
		TransitionOptions: []string{"Done"},
		Registry:          map[string]*core.WorkItem{"ENG-1": {ID: "ENG-1", Type: "Task"}},
	}
	ws := testutil.NewTestSession(ui)
	ws.Provider = provider
	ws.Workspace.Types = []core.TypeConfig{{
		Name: "Task",
		Fields: core.FieldDefs{{
			Key:  "fixVersions",
			Type: core.FieldEnum,
			Enum: []string{"26.4", "26.5", "master"},
		}},
	}}
	ws.Workspace.TransitionHooks = map[string][]core.TransitionHook{
		"Done": {{Field: "fixVersions", Prompt: "Fix Version", Type: "versions", Required: true}},
	}

	if err := commands.Transition(context.Background(), ws, "ENG-1", ""); err != nil {
		t.Fatal(err)
	}
	got := provider.UpdateCalls[0].Changes.Fields["fixVersions"]
	want := []map[string]any{{"name": "26.5"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fixVersions = %v; want %v", got, want)
	}
}

func TestTransition_VersionsHookPrioritySort(t *testing.T) {
	// Priority list: master first, then YY.MM versions sorted version-desc
	// (newest first), then everything else. Picker offers them in that
	// post-sort order; the user picks the second item (index 1) which
	// should be the newest YY.MM after master.
	ui := &testutil.MockUI{
		SelectReturn:      0,
		SelectMultiReturn: []int{1},
	}
	provider := &testutil.MockProvider{
		Caps:              core.Capabilities{HasTransitions: true},
		TransitionOptions: []string{"Done"},
		Registry:          map[string]*core.WorkItem{"ENG-1": {ID: "ENG-1", Type: "Task"}},
		VersionsReturn: []core.Version{
			{Name: "1.4.0"}, {Name: "26.4"}, {Name: "26.10"}, {Name: "26.7"},
			{Name: "master"}, {Name: "ubuntu_18"},
		},
	}
	ws := testutil.NewTestSession(ui)
	ws.Provider = provider
	ws.Workspace.TransitionHooks = map[string][]core.TransitionHook{
		"Done": {{
			Field: "dev_fix_release", Prompt: "Fix Release", Type: "versions", Required: true,
			Priority: []core.PriorityRule{
				{Match: "^master$"},
				{Match: `^\d{2}\.\d{1,2}$`, Sort: "version-desc"},
			},
		}},
	}

	if err := commands.Transition(context.Background(), ws, "ENG-1", ""); err != nil {
		t.Fatal(err)
	}
	got := provider.UpdateCalls[0].Changes.Fields["dev_fix_release"]
	// After sort: [master, 26.10, 26.7, 26.4, 1.4.0, ubuntu_18]; index 1 = 26.10.
	want := []string{"26.10"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("priority+version-desc: dev_fix_release = %v; want %v", got, want)
	}
}

func TestTransition_VersionsHookTargetedFetchByPriority(t *testing.T) {
	// Priority rules carry seeds (auto-derived for "^master$", explicit
	// "26" for the YY.MM rule). The picker should query only those two
	// prefixes — never the broad alphabet fanout — and the resulting set
	// should be filtered to entries that match at least one rule.
	ui := &testutil.MockUI{
		SelectReturn:      0,
		SelectMultiReturn: []int{0, 1, 2},
	}
	provider := &testutil.MockProvider{
		Caps:              core.Capabilities{HasTransitions: true},
		TransitionOptions: []string{"Done"},
		Registry:          map[string]*core.WorkItem{"ENG-1": {ID: "ENG-1", Type: "Task"}},
		SuggestLabelsByPrefix: map[string][]string{
			"master": {"master", "master-css-refactor", "mastert"},
			"26":     {"26.4", "26.7", "26.10", "26.7-cox"},
		},
	}
	ws := testutil.NewTestSession(ui)
	ws.Provider = provider
	ws.Workspace.Types = []core.TypeConfig{{
		Name: "Task",
		Fields: core.FieldDefs{{
			Key: "dev_fix_release", FieldID: "customfield_12901", Type: core.FieldStringArray,
		}},
	}}
	ws.Workspace.TransitionHooks = map[string][]core.TransitionHook{
		"Done": {{
			Field: "dev_fix_release", Prompt: "Fix Release", Type: "versions", Required: true,
			Priority: []core.PriorityRule{
				{Match: "^master$"},
				{Match: `^\d{2}\.\d{1,2}$`, Sort: "version-desc", Seed: []string{"26"}},
			},
		}},
	}

	if err := commands.Transition(context.Background(), ws, "ENG-1", ""); err != nil {
		t.Fatal(err)
	}

	// Only the two prefixes should have been queried — and exactly once each.
	called := map[string]int{}
	for _, p := range provider.SuggestLabelsCalls {
		called[p]++
	}
	if called["master"] != 1 || called["26"] != 1 {
		t.Errorf("SuggestLabels prefixes = %v; want master=1, 26=1", called)
	}
	if len(called) != 2 {
		t.Errorf("expected exactly 2 distinct prefixes; got %v", called)
	}

	// After filtering by patterns + priority+version-desc sort:
	//   rank 0 (^master$):       master
	//   rank 1 YY.MM version-desc: 26.10, 26.7, 26.4
	// User picks indices [0,1,2] → master, 26.10, 26.7.
	got := provider.UpdateCalls[0].Changes.Fields["dev_fix_release"]
	want := []string{"master", "26.10", "26.7"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dev_fix_release = %v; want %v", got, want)
	}
}

func TestTransition_VersionsHookYearsAutoExpands(t *testing.T) {
	// Years: 3 should fan out to the current year and the previous two,
	// generate the matching regex, and seed the suggest queries with
	// just those years — no broad alphabet fanout.
	now := time.Now()
	yy := now.Year() % 100
	curr := fmt.Sprintf("%02d", yy)
	prev := fmt.Sprintf("%02d", (yy-1+100)%100)
	prev2 := fmt.Sprintf("%02d", (yy-2+100)%100)

	ui := &testutil.MockUI{
		SelectReturn:      0,
		SelectMultiReturn: []int{0, 1},
	}
	provider := &testutil.MockProvider{
		Caps:              core.Capabilities{HasTransitions: true},
		TransitionOptions: []string{"Done"},
		Registry:          map[string]*core.WorkItem{"ENG-1": {ID: "ENG-1", Type: "Task"}},
		SuggestLabelsByPrefix: map[string][]string{
			// Mix of valid months and noise — the rule's match regex must
			// drop ".0" (not a month), ".13" (out of range), and ".5-cox"
			// (extra suffix breaks the ^…$ anchors).
			curr:  {curr + ".4", curr + ".7", curr + ".10", curr + ".0", curr + ".13", curr + ".5-cox"},
			prev:  {prev + ".11", prev + ".12"},
			prev2: {prev2 + ".5"},
		},
	}
	ws := testutil.NewTestSession(ui)
	ws.Provider = provider
	ws.Workspace.Types = []core.TypeConfig{{
		Name: "Task",
		Fields: core.FieldDefs{{
			Key: "dev_fix_release", FieldID: "customfield_12901", Type: core.FieldStringArray,
		}},
	}}
	ws.Workspace.TransitionHooks = map[string][]core.TransitionHook{
		"Done": {{
			Field: "dev_fix_release", Prompt: "Fix Release", Type: "versions", Required: true,
			Priority: []core.PriorityRule{
				{Match: "^master$"},
				{Years: 3, Sort: "version-desc"},
			},
		}},
	}

	if err := commands.Transition(context.Background(), ws, "ENG-1", ""); err != nil {
		t.Fatal(err)
	}

	// Only the master + 3 year prefixes should have been queried.
	called := map[string]int{}
	for _, p := range provider.SuggestLabelsCalls {
		called[p]++
	}
	for _, want := range []string{"master", curr, prev, prev2} {
		if called[want] != 1 {
			t.Errorf("expected exactly one suggest call for %q; got %d (all=%v)", want, called[want], called)
		}
	}

	// version-desc within YY-rank, alphabetical doesn't apply across years.
	// First two picks: curr.10 (newest) and curr.7.
	got := provider.UpdateCalls[0].Changes.Fields["dev_fix_release"]
	want := []string{curr + ".10", curr + ".7"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dev_fix_release = %v; want %v", got, want)
	}
}

func TestTransition_VersionsHookCancelOnEmpty(t *testing.T) {
	ui := &testutil.MockUI{
		SelectReturn:      0,
		SelectMultiReturn: nil, // user cancels picker
	}
	provider := &testutil.MockProvider{
		Caps:              core.Capabilities{HasTransitions: true},
		TransitionOptions: []string{"Done"},
		Registry:          map[string]*core.WorkItem{"ENG-1": {ID: "ENG-1", Type: "Task"}},
		VersionsReturn:    []core.Version{{Name: "26.4"}},
	}
	ws := testutil.NewTestSession(ui)
	ws.Provider = provider
	ws.Workspace.TransitionHooks = map[string][]core.TransitionHook{
		"Done": {{Field: "dev_fix_release", Prompt: "Fix Release", Type: "versions", Required: true}},
	}

	err := commands.Transition(context.Background(), ws, "ENG-1", "")
	if err == nil || !commands.IsCancelled(err) {
		t.Fatalf("err = %v; want CancelledError on empty pick", err)
	}
}

func TestTransition_RequiredEmptyCancels(t *testing.T) {
	ui := &testutil.MockUI{
		SelectReturn:    0,
		InputTextReturn: "", // user submits empty for a required field
	}
	provider := &testutil.MockProvider{
		Caps:              core.Capabilities{HasTransitions: true},
		TransitionOptions: []string{"Done"},
		Registry:          map[string]*core.WorkItem{"ENG-1": {ID: "ENG-1", Type: "Task"}},
	}
	ws := testutil.NewTestSession(ui)
	ws.Provider = provider
	ws.Workspace.TransitionHooks = map[string][]core.TransitionHook{
		"Done": {{Field: "actual_story_points", Prompt: "Actual SP", Type: "text", Required: true}},
	}

	err := commands.Transition(context.Background(), ws, "ENG-1", "")
	if !commands.IsCancelled(err) {
		t.Errorf("want CancelledError, got %v", err)
	}
	if len(provider.UpdateCalls) != 0 {
		t.Errorf("Update should not run on cancel; got %d calls", len(provider.UpdateCalls))
	}
}

func TestTransition_WhenPredicateSkipsNonBug(t *testing.T) {
	ui := &testutil.MockUI{
		SelectReturn:     0,
		InputTextReturns: []string{"5"}, // only one prompt should fire
	}
	provider := &testutil.MockProvider{
		Caps:              core.Capabilities{HasTransitions: true},
		TransitionOptions: []string{"Done"},
		Registry:          map[string]*core.WorkItem{"ENG-1": {ID: "ENG-1", Type: "Task"}},
	}
	ws := testutil.NewTestSession(ui)
	ws.Provider = provider
	ws.Workspace.TransitionHooks = map[string][]core.TransitionHook{
		"Done": {
			{Field: "actual_story_points", Prompt: "Actual SP", Type: "text", Required: true},
			{Field: "root_cause_description", Prompt: "Root Cause", Type: "text", Required: true, When: "issuetype == Bug"},
		},
	}

	if err := commands.Transition(context.Background(), ws, "ENG-1", ""); err != nil {
		t.Fatal(err)
	}
	if ui.InputTextCalls != 1 {
		t.Errorf("InputText calls = %d; want 1 (when=Bug should skip on Task)", ui.InputTextCalls)
	}
	fields := provider.UpdateCalls[0].Changes.Fields
	if _, has := fields["root_cause_description"]; has {
		t.Errorf("root_cause_description should not be written for non-Bug; got %v", fields["root_cause_description"])
	}
}

func TestTransition_WhenPredicateAppliesForBug(t *testing.T) {
	ui := &testutil.MockUI{
		SelectReturn:     0,
		InputTextReturns: []string{"5", "memory leak in cache eviction"},
	}
	provider := &testutil.MockProvider{
		Caps:              core.Capabilities{HasTransitions: true},
		TransitionOptions: []string{"Done"},
		Registry:          map[string]*core.WorkItem{"ENG-1": {ID: "ENG-1", Type: "Bug"}},
	}
	ws := testutil.NewTestSession(ui)
	ws.Provider = provider
	ws.Workspace.TransitionHooks = map[string][]core.TransitionHook{
		"Done": {
			{Field: "actual_story_points", Prompt: "Actual SP", Type: "text", Required: true},
			{Field: "root_cause_description", Prompt: "Root Cause", Type: "text", Required: true, When: "issuetype == Bug"},
		},
	}

	if err := commands.Transition(context.Background(), ws, "ENG-1", ""); err != nil {
		t.Fatal(err)
	}
	fields := provider.UpdateCalls[0].Changes.Fields
	if fields["root_cause_description"] != "memory leak in cache eviction" {
		t.Errorf("root_cause_description = %v; want full text", fields["root_cause_description"])
	}
}

func TestTransition_NoHooksFastPath(t *testing.T) {
	// Issue type lookup must NOT happen when the chosen status has no hooks.
	// We omit Registry so any Get call would return an error.
	ui := &testutil.MockUI{SelectReturn: 0}
	provider := &testutil.MockProvider{
		Caps:              core.Capabilities{HasTransitions: true},
		TransitionOptions: []string{"In Progress"},
	}
	ws := testutil.NewTestSession(ui)
	ws.Provider = provider

	if err := commands.Transition(context.Background(), ws, "ENG-1", ""); err != nil {
		t.Fatal(err)
	}
	if provider.UpdateCalls[0].Changes.Fields != nil {
		t.Errorf("Fields should be nil with no hooks; got %v", provider.UpdateCalls[0].Changes.Fields)
	}
}

func TestTransition_SprintHook(t *testing.T) {
	// The sprint hook type rides the SprintLister capability; verify it
	// surfaces a numeric sprint ID into Changes.Fields.
	ui := &testutil.MockUI{
		SelectReturns: []int{0, 1}, // status=first, sprint=second option
	}
	mp := &testutil.MockProvider{
		Caps:              core.Capabilities{HasTransitions: true},
		TransitionOptions: []string{"Development in Progress"},
		Registry:          map[string]*core.WorkItem{"ENG-1": {ID: "ENG-1", Type: "Task"}},
	}
	provider := &mockSprintProvider{
		MockProvider: mp,
		sprints: []core.Sprint{
			{ID: 11, Name: "Sprint 11", State: "active"},
			{ID: 12, Name: "Sprint 12", State: "future"},
		},
	}
	ws := testutil.NewTestSession(ui)
	ws.Provider = provider
	ws.Workspace.TransitionHooks = map[string][]core.TransitionHook{
		"Development in Progress": {{Field: "sprint", Prompt: "Sprint", Type: "sprint", Required: true}},
	}

	if err := commands.Transition(context.Background(), ws, "ENG-1", ""); err != nil {
		t.Fatal(err)
	}
	got := mp.UpdateCalls[0].Changes.Fields["sprint"]
	if got != "12" {
		t.Errorf("sprint field = %v; want \"12\"", got)
	}
}
