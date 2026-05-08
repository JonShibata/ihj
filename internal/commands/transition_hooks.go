package commands

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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

	case "version":
		names, err := pickVersions(ctx, ws, h, false)
		if err != nil || len(names) == 0 {
			return nil, err
		}
		// Shape depends on whether the underlying field is a true version
		// picker (wants {name: X}) or a labels/string-array field (wants
		// the raw string). Only wrap for true version fields.
		if isVersionField(ws.Workspace, h.Field) {
			return []map[string]any{{"name": names[0]}}, nil
		}
		return names[0], nil

	case "versions":
		names, err := pickVersions(ctx, ws, h, true)
		if err != nil || len(names) == 0 {
			return nil, err
		}
		if isVersionField(ws.Workspace, h.Field) {
			out := make([]map[string]any, len(names))
			for i, n := range names {
				out[i] = map[string]any{"name": n}
			}
			return out, nil
		}
		return names, nil

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

// pickVersions presents a picker for a version-typed transition-hook field.
// Source order, in priority:
//  1. FieldDef.Enum  — createmeta allowedValues (select / multiselect / version pickers).
//  2. LabelSuggester — labels-typed custom fields (Jira's free-form labels store no
//     allowedValues; their picker UI is fed by the labels-suggest endpoint).
//  3. VersionLister  — project-wide release versions (the standard fixVersions field).
//
// The first source that returns a non-empty list wins; later sources are
// silently skipped. Released versions are flagged with "(released)" but
// the underlying name is what's sent back to the provider.
func pickVersions(ctx context.Context, ws *WorkspaceSession, h core.TransitionHook, multi bool) ([]string, error) {
	// Resolve any year-window rules (Years > 0) into concrete Match/Seed
	// anchored to today's date — so YY rolls over automatically and we
	// don't have to ship hand-written regexes that decay over time.
	if len(h.Priority) > 0 {
		now := time.Now()
		resolved := make([]core.PriorityRule, len(h.Priority))
		for i, r := range h.Priority {
			resolved[i] = r.Resolve(now)
		}
		h.Priority = resolved
	}

	labels, names := versionsForField(ws.Workspace, h.Field)
	released := map[string]bool{}

	if len(names) == 0 {
		if cfID := customFieldID(ws.Workspace, h.Field); cfID > 0 {
			if sugg, ok := ws.Provider.(core.LabelSuggester); ok {
				suggestions, err := suggestLabelsForRules(ctx, sugg, cfID, h.Priority)
				if err != nil {
					return nil, err
				}
				if len(suggestions) > 0 {
					labels = make([]string, len(suggestions))
					names = make([]string, len(suggestions))
					copy(labels, suggestions)
					copy(names, suggestions)
				}
			}
		}
	}

	if len(names) == 0 {
		lister, ok := ws.Provider.(core.VersionLister)
		if !ok {
			return nil, fmt.Errorf("transition hook %q: field has no allowed values and provider does not support versions", h.Field)
		}
		versions, err := lister.ListVersions(ctx)
		if err != nil {
			return nil, err
		}
		if len(versions) == 0 {
			return nil, fmt.Errorf("transition hook %q: no versions available", h.Field)
		}
		labels = make([]string, len(versions))
		names = make([]string, len(versions))
		for i, v := range versions {
			labels[i] = v.Name
			names[i] = v.Name
			if v.Released {
				released[v.Name] = true
			}
		}
	}

	for i := range labels {
		if released[names[i]] {
			labels[i] += " (released)"
		}
	}

	if len(h.Priority) > 0 {
		labels, names = applyPriority(labels, names, h.Priority)
	}

	if multi {
		idxs, err := ws.Runtime.UI.SelectMulti(h.Prompt, labels)
		if err != nil || len(idxs) == 0 {
			return nil, err
		}
		out := make([]string, len(idxs))
		for i, idx := range idxs {
			out[i] = names[idx]
		}
		return out, nil
	}
	idx, err := ws.Runtime.UI.Select(h.Prompt, labels)
	if err != nil || idx < 0 {
		return nil, err
	}
	return []string{names[idx]}, nil
}

// suggestLabelsForRules fetches just the labels needed to satisfy the
// configured priority rules. When all rules contribute a fetch prefix
// (explicit Seed or auto-derived literal head from Match), we issue
// targeted suggest queries — Jira's autocomplete is prefix-based, so
// asking for "26" returns 26.X without dragging in the rest of the
// historical label namespace. The returned set is then filtered to
// entries that match at least one rule. If no rule yields a prefix,
// we fall back to the provider's broad alphabet fanout — losing the
// optimization but keeping the picker functional.
func suggestLabelsForRules(ctx context.Context, sugg core.LabelSuggester, cfID int, rules []core.PriorityRule) ([]string, error) {
	if len(rules) == 0 {
		return sugg.SuggestLabels(ctx, cfID, "")
	}

	prefixes := make(map[string]bool)
	allHaveSeed := true
	for _, r := range rules {
		seeds := r.Seed
		if len(seeds) == 0 {
			if p := literalRegexPrefix(r.Match); p != "" {
				seeds = []string{p}
			}
		}
		if len(seeds) == 0 {
			allHaveSeed = false
			continue
		}
		for _, s := range seeds {
			prefixes[s] = true
		}
	}

	if !allHaveSeed || len(prefixes) == 0 {
		return sugg.SuggestLabels(ctx, cfID, "")
	}

	type res struct {
		labels []string
		err    error
	}
	results := make(chan res, len(prefixes))
	for p := range prefixes {
		go func(p string) {
			labels, err := sugg.SuggestLabels(ctx, cfID, p)
			results <- res{labels: labels, err: err}
		}(p)
	}

	seen := map[string]bool{}
	var firstErr error
	for range prefixes {
		r := <-results
		if r.err != nil && firstErr == nil {
			firstErr = r.err
			continue
		}
		for _, l := range r.labels {
			seen[l] = true
		}
	}
	if len(seen) == 0 && firstErr != nil {
		return nil, firstErr
	}

	// Filter: keep only entries that match at least one rule.
	compiled := make([]*regexp.Regexp, 0, len(rules))
	for _, r := range rules {
		if c, err := regexp.Compile(r.Match); err == nil {
			compiled = append(compiled, c)
		}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		matched := false
		for _, c := range compiled {
			if c.MatchString(l) {
				matched = true
				break
			}
		}
		if matched {
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out, nil
}

// literalRegexPrefix returns the longest literal prefix anchored at the
// start of pat — i.e. everything between a leading "^" and the first
// regex metacharacter. Used to derive a fetch prefix from a priority
// rule without forcing the user to spell it out twice. Returns ""
// when pat lacks a leading "^" or the first character is a metachar.
func literalRegexPrefix(pat string) string {
	s := strings.TrimPrefix(pat, "^")
	if s == pat {
		return "" // no leading anchor
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\', '.', '[', '(', '|', '?', '*', '+', '{', '}', '$', '^':
			return b.String()
		}
		b.WriteByte(c)
	}
	return b.String()
}

// applyPriority reorders parallel (labels, names) so entries matching
// rules[0].Match come first, then rules[1], …, with unmatched entries
// last. Within a rank, the rule's Sort field decides the order. Rules
// whose regex fails to compile are skipped silently — a malformed
// pattern shouldn't block a transition.
func applyPriority(labels, names []string, rules []core.PriorityRule) ([]string, []string) {
	compiled := make([]*regexp.Regexp, 0, len(rules))
	keptRules := make([]core.PriorityRule, 0, len(rules))
	for _, r := range rules {
		c, err := regexp.Compile(r.Match)
		if err != nil {
			continue
		}
		compiled = append(compiled, c)
		keptRules = append(keptRules, r)
	}
	if len(compiled) == 0 {
		return labels, names
	}
	rankOf := func(name string) int {
		for i, r := range compiled {
			if r.MatchString(name) {
				return i
			}
		}
		return len(compiled)
	}

	type item struct {
		label string
		name  string
		rank  int
		idx   int
	}
	items := make([]item, len(names))
	for i := range names {
		items[i] = item{label: labels[i], name: names[i], rank: rankOf(names[i]), idx: i}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].rank != items[j].rank {
			return items[i].rank < items[j].rank
		}
		// Tie-breaker is per-rank Sort. Unmatched (rank == len(rules))
		// uses default original order.
		var sortMode string
		if items[i].rank < len(keptRules) {
			sortMode = keptRules[items[i].rank].Sort
		}
		return lessBySortMode(items[i].name, items[j].name, items[i].idx, items[j].idx, sortMode)
	})
	outL := make([]string, len(items))
	outN := make([]string, len(items))
	for i, it := range items {
		outL[i] = it.label
		outN[i] = it.name
	}
	return outL, outN
}

// lessBySortMode returns whether a < b under the named sort mode.
// fallbackI/J are the original indices used when mode is empty (preserve
// the input order).
func lessBySortMode(a, b string, fallbackI, fallbackJ int, mode string) bool {
	switch mode {
	case "asc":
		return a < b
	case "desc":
		return a > b
	case "version-asc":
		return core.CompareIDsNatural(a, b)
	case "version-desc":
		return core.CompareIDsNatural(b, a)
	default:
		return fallbackI < fallbackJ
	}
}

// isVersionField reports whether the named hook field is backed by a
// Jira "Version" or "MultiVersion" custom field, in which case writes
// must wrap each value as {"name": "X"}. Labels-typed fields and free
// strings stay raw.
func isVersionField(ws *core.Workspace, fieldKey string) bool {
	for _, def := range ws.AllFieldDefs() {
		if def.Key != fieldKey {
			continue
		}
		// Heuristic: a true version picker arrives as FieldEnum (createmeta
		// gives it allowedValues). Labels arrive as FieldStringArray with
		// no enum. The FieldEnum branch wins because both single-version
		// and multi-version pickers map to it.
		return def.Type == core.FieldEnum && len(def.Enum) > 0
	}
	return false
}

// customFieldID returns the numeric ID parsed from a FieldDef's FieldID
// (e.g. "customfield_12901" → 12901). Returns 0 when the field isn't a
// custom-numeric backend ID (system fields, missing FieldDef, etc.).
func customFieldID(ws *core.Workspace, fieldKey string) int {
	for _, def := range ws.AllFieldDefs() {
		if def.Key != fieldKey {
			continue
		}
		const prefix = "customfield_"
		if !strings.HasPrefix(def.FieldID, prefix) {
			return 0
		}
		n, err := strconv.Atoi(def.FieldID[len(prefix):])
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// versionsForField returns the (labels, names) for the field's
// createmeta-discovered allowed values. Both slices are empty when the
// field is unknown or has no enum — caller falls back to project versions.
func versionsForField(ws *core.Workspace, fieldKey string) (labels, names []string) {
	for _, def := range ws.AllFieldDefs() {
		if def.Key != fieldKey {
			continue
		}
		if len(def.Enum) == 0 {
			return nil, nil
		}
		labels = make([]string, len(def.Enum))
		names = make([]string, len(def.Enum))
		copy(labels, def.Enum)
		copy(names, def.Enum)
		return labels, names
	}
	return nil, nil
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
