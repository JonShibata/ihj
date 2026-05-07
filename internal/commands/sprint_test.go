package commands_test

import (
	"context"
	"testing"

	"github.com/mikecsmith/ihj/internal/commands"
	"github.com/mikecsmith/ihj/internal/core"
	"github.com/mikecsmith/ihj/internal/testutil"
)

// mockSprintProvider extends MockProvider with the SprintLister capability.
type mockSprintProvider struct {
	*testutil.MockProvider
	sprints   []core.Sprint
	listErr   error
	listCalls int
}

func (m *mockSprintProvider) ListSprints(_ context.Context, _ []string) ([]core.Sprint, error) {
	m.listCalls++
	return m.sprints, m.listErr
}

func TestSprint_AssignsByID(t *testing.T) {
	ui := &testutil.MockUI{SelectReturn: 1} // pick "Sprint 12" (index 1)
	provider := &mockSprintProvider{
		MockProvider: &testutil.MockProvider{},
		sprints: []core.Sprint{
			{ID: 11, Name: "Sprint 11", State: "active"},
			{ID: 12, Name: "Sprint 12", State: "future"},
		},
	}
	ws := testutil.NewTestSession(ui)
	ws.Provider = provider

	if err := commands.Sprint(context.Background(), ws, "ENG-7"); err != nil {
		t.Fatal(err)
	}
	if provider.listCalls != 1 {
		t.Errorf("ListSprints called %d times; want 1", provider.listCalls)
	}
	if len(provider.UpdateCalls) != 1 {
		t.Fatalf("UpdateCalls = %d; want 1", len(provider.UpdateCalls))
	}
	got := provider.UpdateCalls[0].Changes.Fields["sprint"]
	if got != "12" {
		t.Errorf("sprint field = %v; want \"12\"", got)
	}
}

func TestSprint_BacklogOption(t *testing.T) {
	// Picking the trailing "Backlog" option should write "none".
	ui := &testutil.MockUI{SelectReturn: 1}
	provider := &mockSprintProvider{
		MockProvider: &testutil.MockProvider{},
		sprints:      []core.Sprint{{ID: 5, Name: "Sprint 5", State: "active"}},
	}
	ws := testutil.NewTestSession(ui)
	ws.Provider = provider

	if err := commands.Sprint(context.Background(), ws, "ENG-1"); err != nil {
		t.Fatal(err)
	}
	got := provider.UpdateCalls[0].Changes.Fields["sprint"]
	if got != "none" {
		t.Errorf("sprint field = %v; want \"none\"", got)
	}
}

func TestSprint_Cancel(t *testing.T) {
	ui := &testutil.MockUI{SelectReturn: -1}
	provider := &mockSprintProvider{
		MockProvider: &testutil.MockProvider{},
		sprints:      []core.Sprint{{ID: 1, Name: "Sprint 1", State: "active"}},
	}
	ws := testutil.NewTestSession(ui)
	ws.Provider = provider

	err := commands.Sprint(context.Background(), ws, "ENG-2")
	if !commands.IsCancelled(err) {
		t.Errorf("expected CancelledError, got %v", err)
	}
	if len(provider.UpdateCalls) != 0 {
		t.Errorf("Update should not be called on cancel; got %d calls", len(provider.UpdateCalls))
	}
}

func TestSprint_NoCapability(t *testing.T) {
	// Plain MockProvider doesn't implement SprintLister — should notify and return nil.
	ui := &testutil.MockUI{}
	ws := testutil.NewTestSession(ui)
	ws.Provider = &testutil.MockProvider{}

	if err := commands.Sprint(context.Background(), ws, "ENG-3"); err != nil {
		t.Fatalf("expected nil error for missing capability; got %v", err)
	}
}
