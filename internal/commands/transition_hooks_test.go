package commands_test

import (
	"context"
	"reflect"
	"testing"

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

	if err := commands.Transition(context.Background(), ws, "ENG-1"); err != nil {
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

	if err := commands.Transition(context.Background(), ws, "ENG-1"); err != nil {
		t.Fatal(err)
	}
	got := provider.UpdateCalls[0].Changes.Fields["dev_fix_release"]
	want := []string{"26.4", "master"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dev_fix_release = %v; want %v", got, want)
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

	err := commands.Transition(context.Background(), ws, "ENG-1")
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

	if err := commands.Transition(context.Background(), ws, "ENG-1"); err != nil {
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

	if err := commands.Transition(context.Background(), ws, "ENG-1"); err != nil {
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

	if err := commands.Transition(context.Background(), ws, "ENG-1"); err != nil {
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

	if err := commands.Transition(context.Background(), ws, "ENG-1"); err != nil {
		t.Fatal(err)
	}
	got := mp.UpdateCalls[0].Changes.Fields["sprint"]
	if got != "12" {
		t.Errorf("sprint field = %v; want \"12\"", got)
	}
}
