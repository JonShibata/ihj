package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mikecsmith/ihj/internal/core"
)

// runTransitionHooks evaluates the workspace's hook list for the chosen
// status and collects field values from the user. Returns a Fields map
// (possibly nil) ready to merge into core.Changes alongside the status,
// or a CancelledError if the user aborts a required prompt.
//
// Hooks are filtered by the optional `when` predicate (currently only
// supports `field == value`, e.g. `issuetype == Bug`). issueFields is
// the current state used for predicate evaluation.
func runTransitionHooks(
	ctx context.Context,
	ws *WorkspaceSession,
	issueKey string,
	status string,
	issueType string,
) (map[string]any, error) {
	hooks := ws.Workspace.TransitionHooks[status]
	if len(hooks) == 0 {
		return nil, nil
	}

	// "field == value" predicate — currently only `issuetype` is supported,
	// since that's the lone case in the workflows we model. Add new keys here
	// (e.g. priority, project) when needed.
	predicateContext := map[string]string{
		"issuetype": issueType,
	}

	fields := map[string]any{}
	for _, h := range hooks {
		if !evalWhen(h.When, predicateContext) {
			continue
		}

		val, err := promptForHook(ctx, ws, issueKey, h)
		if err != nil {
			return nil, err
		}
		if val == nil {
			if h.Required {
				return nil, &CancelledError{Operation: "transition (" + h.Field + " required)"}
			}
			continue
		}
		fields[h.Field] = val
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return fields, nil
}

// promptForHook routes one hook to the right UI primitive. Returns nil
// (no value, no error) when the user cancels or leaves a non-required
// prompt blank.
func promptForHook(ctx context.Context, ws *WorkspaceSession, issueKey string, h core.TransitionHook) (any, error) {
	switch h.Type {
	case "text":
		s, err := ws.Runtime.UI.InputText(h.Prompt, "")
		if err != nil || s == "" {
			return nil, err
		}
		return s, nil

	case "csv":
		s, err := ws.Runtime.UI.InputText(h.Prompt+" (comma-separated)", "")
		if err != nil || s == "" {
			return nil, err
		}
		parts := strings.Split(s, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				out = append(out, t)
			}
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil

	case "select":
		if len(h.Values) == 0 {
			return nil, fmt.Errorf("transition hook %q: type=select but no values configured", h.Field)
		}
		idx, err := ws.Runtime.UI.Select(h.Prompt, h.Values)
		if err != nil || idx < 0 {
			return nil, err
		}
		return h.Values[idx], nil

	case "sprint":
		lister, ok := ws.Provider.(core.SprintLister)
		if !ok {
			return nil, fmt.Errorf("transition hook %q: provider does not support sprints", h.Field)
		}
		sprints, err := lister.ListSprints(ctx, []string{"active", "future"})
		if err != nil {
			return nil, err
		}
		labels := make([]string, 0, len(sprints))
		ids := make([]int, 0, len(sprints))
		for _, s := range sprints {
			prefix := ""
			switch s.State {
			case "active":
				prefix = "● "
			case "future":
				prefix = "○ "
			}
			labels = append(labels, prefix+s.Name)
			ids = append(ids, s.ID)
		}
		idx, err := ws.Runtime.UI.Select(h.Prompt, labels)
		if err != nil || idx < 0 {
			return nil, err
		}
		// Sprint values are written via Changes.Fields["sprint"] = "<id>".
		// The transition hook's Field stays the alias the user named
		// (typically "sprint"), and the provider's translation layer
		// converts the numeric string to an AddToSprint call.
		return strconv.Itoa(ids[idx]), nil

	default:
		return nil, fmt.Errorf("transition hook %q: unknown type %q", h.Field, h.Type)
	}
}

// evalWhen returns true when expr is empty or holds (best-effort).
// Format: `<key> == <value>` — keys come from the predicate context the
// caller supplies (currently only `issuetype`). Returns true on parse
// failure so a malformed expression doesn't silently skip every hook.
func evalWhen(expr string, ctx map[string]string) bool {
	if expr == "" {
		return true
	}
	parts := strings.SplitN(expr, "==", 2)
	if len(parts) != 2 {
		return true
	}
	key := strings.TrimSpace(parts[0])
	want := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
	got, ok := ctx[key]
	if !ok {
		return true
	}
	return got == want
}
