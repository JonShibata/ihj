package commands

import (
	"context"
	"fmt"

	"github.com/mikecsmith/ihj/internal/core"
)

// Transition prompts for a new status and applies the change to the issue.
// currentStatus is an optional hint — when non-empty, the provider can skip
// the round-trip it would otherwise make to learn the issue's current state.
// Pass "" when calling without prior knowledge of the issue.
func Transition(ctx context.Context, ws *WorkspaceSession, issueKey, currentStatus string) error {
	caps := ws.Provider.Capabilities()
	if !caps.HasTransitions && caps.StatusSource != core.StatusSourceEntity {
		return fmt.Errorf("provider %q does not support status transitions", ws.Workspace.Provider)
	}

	current, options, err := ws.Provider.TransitionsFor(ctx, issueKey, currentStatus)
	if err != nil {
		return err
	}
	if len(options) == 0 {
		ws.Runtime.UI.Notify(issueKey, fmt.Sprintf("No transitions available (currently %s)", current))
		return nil
	}

	verb := "Transition"
	if caps.StatusSource == core.StatusSourceEntity && !caps.HasTransitions {
		verb = "Change state"
	}
	prompt := fmt.Sprintf("%s: %s (currently %s)", verb, issueKey, current)

	choice, err := ws.Runtime.UI.Select(prompt, options)
	if err != nil {
		return err
	}
	if choice < 0 {
		return &CancelledError{Operation: "transition"}
	}

	newStatus := options[choice]

	var issueType string
	if len(ws.Workspace.TransitionHooks[newStatus]) > 0 {
		// Predicate evaluation (e.g. `issuetype == Bug`) needs the issue's
		// type. Skip the round-trip when no hooks are configured for this
		// status to keep the no-hook fast path identical to the original.
		item, err := ws.Provider.Get(ctx, issueKey)
		if err != nil {
			return fmt.Errorf("fetching %s: %w", issueKey, err)
		}
		if item != nil {
			issueType = item.Type
		}
	}

	hookFields, err := runTransitionHooks(ctx, ws, issueKey, newStatus, issueType)
	if err != nil {
		return err
	}

	if err := ws.Provider.Update(ctx, issueKey, &core.Changes{Status: &newStatus, Fields: hookFields}); err != nil {
		ws.Runtime.UI.Notify("Error", fmt.Sprintf("Failed to move %s", issueKey))
		return err
	}

	ws.Runtime.UI.Notify(issueKey, fmt.Sprintf("Moved to %s", newStatus))
	return nil
}
