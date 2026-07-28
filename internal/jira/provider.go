// Package jira implements the Atlassian Jira provider.
//
// It acts as an adapter between the Jira REST API and the universal
// domain model defined in the core package. Its primary responsibilities
// are translating Jira-specific concepts (ADF descriptions, JQL, custom
// fields, sprint management, and workflow transitions) into backend-agnostic
// core.WorkItem structures, and managing per-workspace caching.
//
// API types are derived from the Atlassian OpenAPI spec at:
//
//	https://developer.atlassian.com/cloud/jira/platform/swagger-v3.v3.json
package jira

import (
	"context"
	"fmt"
	"maps"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/mikecsmith/ihj/internal/core"
	"github.com/mikecsmith/ihj/internal/document"
)

// Provider implements core.Provider for Jira backends.
// It wraps the low-level API client and translates between
// Jira-specific types and the universal core.WorkItem model.
type Provider struct {
	client   API
	ws       *core.Workspace
	cfg      *Config
	cacheDir string

	// cachedUser avoids repeated FetchMyself calls within a session.
	cachedUser *user

	// wellKnown is the single source of truth for fields the provider recognises.
	wellKnown wellKnownFields

	nameToID map[string]string // "fieldKey:valueName" → "valueID" for payload construction
}

// Compile-time check that *Provider implements core.Provider and the
// optional history capability the TUI's history overlay relies on.
var (
	_ core.Provider       = (*Provider)(nil)
	_ core.HistoryFetcher = (*Provider)(nil)
)

// NewProvider creates a Jira provider for the given workspace.
// The workspace's ProviderConfig must already be a *jira.Config
// (hydrated by the composition root).
// Eagerly loads createmeta (from disk cache or API) so field metadata
// is available immediately. Returns an error if createmeta cannot be loaded.
// cacheDir may be empty to disable disk caching.
func NewProvider(client API, ws *core.Workspace, cacheDir string) (*Provider, error) {
	cfg, _ := ws.ProviderConfig.(*Config)
	p := &Provider{
		client:   client,
		ws:       ws,
		cfg:      cfg,
		cacheDir: cacheDir,
	}
	p.wellKnown = p.buildWellKnownFields()

	if err := p.loadFieldMeta(); err != nil {
		return nil, fmt.Errorf("loading field metadata: %w", err)
	}

	// If the disk cache is approaching expiry, refresh in the background
	// so the next session has a warm cache.
	p.backgroundRefreshIfNeeded()

	return p, nil
}

// Search returns work items matching the named filter.
// By default, a fresh disk cache is returned without hitting the API.
// Pass noCache=true to force a fresh fetch.
func (p *Provider) Search(ctx context.Context, filter string, noCache bool) ([]*core.WorkItem, error) {
	// Try cache first unless caller explicitly wants fresh data.
	if !noCache && p.cacheDir != "" {
		if cached, err := loadCache(p.cacheDir, p.ws.Slug, filter, p.ws.CacheTTL); err == nil {
			return issuesToWorkItems(cached.Issues, p.wellKnown, p.customFieldMap(), p.ws.CommentLimit), nil
		}
	}

	jql, err := buildJQL(p.ws, p.cfg, filter)
	if err != nil {
		return nil, err
	}

	issues, err := fetchAllIssues(ctx, p.client, jql, p.cfg.FormattedFields, p.customFieldIDs())
	if err != nil {
		return nil, err
	}

	// Save to cache for future calls.
	if p.cacheDir != "" {
		_ = saveCache(p.cacheDir, p.ws.Slug, filter, issues)
	}

	return issuesToWorkItems(issues, p.wellKnown, p.customFieldMap(), p.ws.CommentLimit), nil
}

// Get returns a single work item by its Jira issue key.
func (p *Provider) Get(ctx context.Context, id string) (*core.WorkItem, error) {
	iss, err := p.client.FetchIssue(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("fetching issue %s: %w", id, err)
	}
	return issueToWorkItem(iss, p.wellKnown, p.customFieldMap(), p.ws.CommentLimit), nil
}

// FetchHistory implements core.HistoryFetcher. It returns the issue's change
// history newest-first, with each Jira changelog entry mapped to a
// core.HistoryEntry. Entries carrying no field changes are dropped.
func (p *Provider) FetchHistory(ctx context.Context, id string) ([]core.HistoryEntry, error) {
	raw, err := p.client.FetchIssueChangelog(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("fetching history for %s: %w", id, err)
	}
	entries := make([]core.HistoryEntry, 0, len(raw))
	// Jira returns oldest-first; iterate in reverse for newest-first display.
	for i := len(raw) - 1; i >= 0; i-- {
		e := raw[i]
		changes := make([]core.HistoryChange, 0, len(e.Items))
		for _, it := range e.Items {
			changes = append(changes, core.HistoryChange{
				Field: it.Field,
				From:  it.FromString,
				To:    it.ToString,
			})
		}
		if len(changes) == 0 {
			continue
		}
		entries = append(entries, core.HistoryEntry{
			Author:  e.Author.DisplayNameOrDefault("Unknown"),
			Created: formatDateTime(e.Created),
			Changes: changes,
		})
	}
	return entries, nil
}

// Create persists a new work item and returns its assigned key.
func (p *Provider) Create(ctx context.Context, item *core.WorkItem) (string, error) {
	fields := map[string]any{
		"summary": item.Summary,
		"project": map[string]any{"key": p.cfg.ProjectKey},
	}

	if tc := p.ws.TypeByName(item.Type); tc != nil {
		fields["issuetype"] = map[string]any{"id": fmt.Sprintf("%d", tc.ID)}
	}

	if item.ParentID != "" {
		fields["parent"] = map[string]any{"key": strings.ToUpper(item.ParentID)}
	}

	if item.Description != nil {
		fields["description"] = renderADFValue(item.Description)
	}

	tx, err := p.wellKnown.TranslateFields(p, ctx, item.Fields)
	if err != nil {
		return "", err
	}

	maps.Copy(fields, tx.fields)

	created, err := p.client.CreateIssue(ctx, map[string]any{"fields": fields})
	if err != nil {
		return "", fmt.Errorf("creating issue: %w", err)
	}

	return created.Key, nil
}

// Update applies changes to an existing work item.
func (p *Provider) Update(ctx context.Context, id string, changes *core.Changes) error {
	if l := dbg(); l != nil {
		status := ""
		if changes.Status != nil {
			status = *changes.Status
		}
		l.Printf("Update id=%s status=%q fields=%+v", id, status, changes.Fields)
	}
	fields := make(map[string]any)

	if changes.Summary != nil {
		fields["summary"] = *changes.Summary
	}

	if changes.Type != nil {
		if tc := p.ws.TypeByName(*changes.Type); tc != nil {
			fields["issuetype"] = map[string]any{"id": fmt.Sprintf("%d", tc.ID)}
		}
	}

	if changes.ParentID != nil {
		if *changes.ParentID == "" {
			fields["parent"] = nil // clear parent
		} else {
			fields["parent"] = map[string]any{"key": strings.ToUpper(*changes.ParentID)}
		}
	}

	if changes.Description != nil {
		fields["description"] = renderADFValue(changes.Description)
	}

	tx, err := p.wellKnown.TranslateFields(p, ctx, changes.Fields)
	if err != nil {
		return err
	}
	for k, v := range tx.fields {
		fields[k] = v
	}
	if l := dbg(); l != nil {
		l.Printf("Update id=%s translated fields=%+v sprintTarget=%q sprintByID=%d assignUser=%v",
			id, fields, tx.sprintTarget, tx.sprintByID, tx.assignUser)
	}

	if len(fields) > 0 {
		if err := p.client.UpdateIssue(ctx, id, map[string]any{"fields": fields}); err != nil {
			return fmt.Errorf("updating issue %s: %w", id, err)
		}
	}

	if tx.assignUser != nil {
		if err := p.client.AssignIssue(ctx, id, *tx.assignUser); err != nil {
			return fmt.Errorf("assigning %s: %w", id, err)
		}
	}

	if changes.Status != nil {
		if err := performTransition(ctx, p.client, id, *changes.Status); err != nil {
			return fmt.Errorf("transitioning %s to '%s': %w", id, *changes.Status, err)
		}
	}

	if tx.sprintTarget != "" {
		if err := sprintAssign(ctx, p.client, p.cfg.BoardID, id, tx.sprintTarget); err != nil {
			return fmt.Errorf("assigning %s to %s sprint: %w", id, tx.sprintTarget, err)
		}
	}

	if tx.sprintByID > 0 {
		if err := p.client.AddToSprint(ctx, tx.sprintByID, []string{id}); err != nil {
			return fmt.Errorf("adding %s to sprint %d: %w", id, tx.sprintByID, err)
		}
	}

	return nil
}

// DownloadAttachment implements core.AttachmentDownloader. Fetches the
// authenticated content URL into a tempfile preserving the suggested
// filename's extension so external viewers can dispatch by suffix.
// Caller owns the returned path and must remove it when done.
func (p *Provider) DownloadAttachment(ctx context.Context, url, suggestedName string) (string, error) {
	if url == "" {
		return "", fmt.Errorf("empty attachment URL")
	}
	pattern := "ihj-attachment-*"
	if ext := pathExt(suggestedName); ext != "" {
		pattern = "ihj-attachment-*" + ext
	}
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", fmt.Errorf("creating tempfile: %w", err)
	}
	if err := p.client.DownloadTo(ctx, url, f); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// pathExt returns the lowercase extension of a filename including the
// leading dot, or empty string if there is none.
func pathExt(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		switch name[i] {
		case '.':
			return strings.ToLower(name[i:])
		case '/', '\\':
			return ""
		}
	}
	return ""
}

// Children implements core.ChildrenLister. Runs JQL `parent = <key>`
// against the active project and returns the matching items. The result
// is uncached — siblings are usually a small set and the call only fires
// when the user lands on an issue whose parent isn't in the current view.
func (p *Provider) Children(ctx context.Context, parentKey string) ([]*core.WorkItem, error) {
	if parentKey == "" {
		return nil, nil
	}
	jql := fmt.Sprintf(`parent = "%s"`, parentKey)
	issues, err := fetchAllIssues(ctx, p.client, jql, p.cfg.FormattedFields, p.customFieldIDs())
	if err != nil {
		return nil, err
	}
	return issuesToWorkItems(issues, p.wellKnown, p.customFieldMap(), p.ws.CommentLimit), nil
}

// ListSprints implements core.SprintLister. Returns sprints for the
// workspace's board filtered by state. An empty states slice returns all.
func (p *Provider) ListSprints(ctx context.Context, states []string) ([]core.Sprint, error) {
	if p.cfg == nil || p.cfg.BoardID == 0 {
		return nil, fmt.Errorf("workspace has no board_id configured")
	}
	sprints, err := p.client.FetchSprints(ctx, p.cfg.BoardID, states)
	if err != nil {
		return nil, err
	}
	out := make([]core.Sprint, len(sprints))
	for i, s := range sprints {
		out[i] = core.Sprint{ID: s.ID, Name: s.Name, State: s.State}
	}
	return out, nil
}

// ListVersions implements core.VersionLister. Returns the project's
// release versions sorted unreleased-first, then released, alphabetical
// within each group. Archived versions are filtered out — they're
// historical noise that Jira refuses to assign anyway.
func (p *Provider) ListVersions(ctx context.Context) ([]core.Version, error) {
	if p.cfg == nil || p.cfg.ProjectKey == "" {
		return nil, fmt.Errorf("workspace has no project_key configured")
	}
	versions, err := p.client.FetchVersions(ctx, p.cfg.ProjectKey)
	if err != nil {
		return nil, err
	}
	out := make([]core.Version, 0, len(versions))
	for _, v := range versions {
		if v.Archived {
			continue
		}
		out = append(out, core.Version{Name: v.Name, Released: v.Released})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Released != out[j].Released {
			return !out[i].Released // unreleased first
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// SuggestLabels implements core.LabelSuggester. When prefix is empty,
// fans out across the seed alphabet ("" + 0-9 + a-z) so the picker has
// the full historical label set instead of Jira's per-query alphabetic
// head (Jira caps each suggest call at ~15 entries — without this fan
// out the picker would only ever see the first 15 names). Branches that
// hit the cap recursively drill one more character so dense prefixes
// like "2" surface "26.x" entries that would otherwise be hidden behind
// the alphabetically-earlier "2.0" / "2.1" cluster.
//
// When prefix is set, queries that one prefix only — used by the
// filter-as-you-type path. Results are deduped and sorted alphabetically.
func (p *Provider) SuggestLabels(ctx context.Context, customFieldID int, prefix string) ([]string, error) {
	if prefix != "" {
		return p.client.FetchLabelSuggestions(ctx, customFieldID, prefix)
	}

	const (
		jiraSuggestCap = 15 // Empirical: each call returns at most this many.
		maxDrillDepth  = 3  // Bound the recursion: prefix+3 chars covers nearly all real label sets.
		maxConcurrent  = 16 // Soft per-host cap so we don't hammer Jira.
	)
	alphabet := "0123456789abcdefghijklmnopqrstuvwxyz"

	var (
		mu       sync.Mutex
		seen     = map[string]bool{}
		firstErr error
	)
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	var drill func(prefix string, depth int)
	drill = func(prefix string, depth int) {
		defer wg.Done()
		sem <- struct{}{}
		labels, err := p.client.FetchLabelSuggestions(ctx, customFieldID, prefix)
		<-sem

		mu.Lock()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
			return
		}
		for _, l := range labels {
			seen[l] = true
		}
		mu.Unlock()

		if len(labels) >= jiraSuggestCap && depth < maxDrillDepth {
			for i := 0; i < len(alphabet); i++ {
				wg.Add(1)
				go drill(prefix+string(alphabet[i]), depth+1)
			}
		}
	}

	wg.Add(1)
	go drill("", 0)
	for i := 0; i < len(alphabet); i++ {
		wg.Add(1)
		go drill(string(alphabet[i]), 0)
	}
	wg.Wait()

	if len(seen) == 0 && firstErr != nil {
		return nil, firstErr
	}

	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	sort.Strings(out)
	return out, nil
}

// Comment adds a comment to a Jira issue.
func (p *Provider) Comment(ctx context.Context, id string, body string) error {
	ast, err := document.ParseMarkdownString(body)
	if err != nil {
		return fmt.Errorf("parsing comment: %w", err)
	}
	adfBody := renderADFValue(ast)
	return p.client.AddComment(ctx, id, adfBody)
}

// Assign assigns the issue to the current authenticated user.
func (p *Provider) Assign(ctx context.Context, id string) error {
	u, err := p.resolveUser(ctx)
	if err != nil {
		return fmt.Errorf("fetching current user: %w", err)
	}
	return p.client.AssignIssue(ctx, id, u.AccountID)
}

// CurrentUser returns the authenticated Jira user.
func (p *Provider) CurrentUser(ctx context.Context) (*core.User, error) {
	u, err := p.resolveUser(ctx)
	if err != nil {
		return nil, err
	}
	return &core.User{
		ID:          u.AccountID,
		DisplayName: u.DisplayName,
		Email:       u.Email,
	}, nil
}

// resolveUser returns the cached user or fetches and caches it.
func (p *Provider) resolveUser(ctx context.Context) (*user, error) {
	if p.cachedUser != nil {
		return p.cachedUser, nil
	}
	u, err := p.client.FetchMyself(ctx)
	if err != nil {
		return nil, err
	}
	p.cachedUser = u
	return p.cachedUser, nil
}

// Capabilities returns the feature set supported by the Jira provider.
func (p *Provider) Capabilities() core.Capabilities {
	return core.Capabilities{
		HasHierarchy:   true,
		HasTransitions: true,
		HasTypes:       true,
		StatusSource:   core.StatusSourceWorkflow,
	}
}

// TransitionsFor returns the selectable workflow transition names for the
// issue along with its current status name. Jira filters transitions by
// workflow on the server, so we simply surface what the API returns.
func (p *Provider) TransitionsFor(ctx context.Context, id, currentStatus string) (string, []string, error) {
	// When the caller already knows the current status (TUI passes it
	// straight from its loaded registry), skip the issue Get entirely
	// and only fetch the transitions list. Otherwise fire both requests
	// in parallel — they don't depend on each other.
	if currentStatus != "" {
		transitions, err := p.client.FetchTransitions(ctx, id)
		if err != nil {
			return "", nil, fmt.Errorf("fetching transitions for %s: %w", id, err)
		}
		return currentStatus, filterTransitions(transitions, currentStatus), nil
	}

	type itemResult struct {
		item *core.WorkItem
		err  error
	}
	type txResult struct {
		transitions []transition
		err         error
	}
	itemCh := make(chan itemResult, 1)
	txCh := make(chan txResult, 1)

	go func() {
		item, err := p.Get(ctx, id)
		itemCh <- itemResult{item: item, err: err}
	}()
	go func() {
		t, err := p.client.FetchTransitions(ctx, id)
		txCh <- txResult{transitions: t, err: err}
	}()

	itemR := <-itemCh
	txR := <-txCh

	if itemR.err != nil {
		return "", nil, itemR.err
	}
	if txR.err != nil {
		return "", nil, fmt.Errorf("fetching transitions for %s: %w", id, txR.err)
	}

	return itemR.item.Status, filterTransitions(txR.transitions, itemR.item.Status), nil
}

// filterTransitions returns the user-facing transition target names
// (verbatim from Jira) with self-transitions (no-ops) removed. Names
// are NOT case-normalised — the workspace's transition_hooks map is
// keyed on the verbatim status name, and any rewriting here breaks
// the lookup (e.g. "Development in Progress" → "Development In
// Progress" misses the hook list and the transition POSTs without
// the required field, which Jira rejects).
func filterTransitions(transitions []transition, currentStatus string) []string {
	opts := make([]string, 0, len(transitions))
	for _, t := range transitions {
		name := t.To.Name
		if name == "" {
			name = t.Name
		}
		if strings.EqualFold(name, currentStatus) {
			continue
		}
		opts = append(opts, name)
	}
	return opts
}

// ContentRenderer returns the Jira ADF content renderer.
func (p *Provider) ContentRenderer() core.ContentRenderer {
	return &adfRenderer{}
}
