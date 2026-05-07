package commands

import (
	"context"
	"fmt"
	"strconv"

	"github.com/mikecsmith/ihj/internal/core"
)

// Sprint presents a picker of active and future sprints (plus a Backlog
// option) and assigns the issue to the user's choice. Provider must
// implement core.SprintLister; otherwise the action is a no-op notify.
func Sprint(ctx context.Context, ws *WorkspaceSession, issueKey string) error {
	lister, ok := ws.Provider.(core.SprintLister)
	if !ok {
		ws.Runtime.UI.Notify(issueKey, "Provider does not support sprints.")
		return nil
	}

	sprints, err := lister.ListSprints(ctx, []string{"active", "future"})
	if err != nil {
		ws.Runtime.UI.Notify("Error", fmt.Sprintf("Failed to list sprints: %v", err))
		return err
	}

	// Build labels and a parallel ID slice. The trailing "Backlog" option
	// maps to id=0 and is translated to the "none" sentinel below.
	labels := make([]string, 0, len(sprints)+1)
	ids := make([]int, 0, len(sprints)+1)
	for _, s := range sprints {
		prefix := ""
		if s.State == "active" {
			prefix = "● "
		} else if s.State == "future" {
			prefix = "○ "
		}
		labels = append(labels, prefix+s.Name)
		ids = append(ids, s.ID)
	}
	labels = append(labels, "— Backlog (no sprint) —")
	ids = append(ids, 0)

	choice, err := ws.Runtime.UI.Select(fmt.Sprintf("Assign %s to sprint", issueKey), labels)
	if err != nil {
		return err
	}
	if choice < 0 {
		return &CancelledError{Operation: "sprint"}
	}

	sprintField := strconv.Itoa(ids[choice])
	if ids[choice] == 0 {
		sprintField = "none"
	}
	if err := ws.Provider.Update(ctx, issueKey, &core.Changes{
		Fields: map[string]any{"sprint": sprintField},
	}); err != nil {
		ws.Runtime.UI.Notify("Error", fmt.Sprintf("Failed to update sprint: %v", err))
		return err
	}

	ws.Runtime.UI.Notify(issueKey, "Moved to "+labels[choice])
	return nil
}
