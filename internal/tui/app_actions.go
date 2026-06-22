package tui

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/mikecsmith/ihj/internal/commands"
	"github.com/mikecsmith/ihj/internal/core"
)

// ── Quit ────────────────────────────────────────────────────────

// quitCmd signals the UI bridge to unblock any pending interactive prompts
// and then returns the Bubble Tea quit command. All quit paths route through
// here so background goroutines waiting on Select/Confirm/InputText don't
// leak when the app exits with a pending prompt.
func (m AppModel) quitCmd() tea.Cmd {
	m.ui.Shutdown()
	return tea.Quit
}

// ── Issue targeting ─────────────────────────────────────────────

// targetIssue returns the issue that actions should operate on.
// When the detail pane has navigated into a child, it returns
// that child — otherwise the list's selected parent.
func (m AppModel) targetIssue() *core.WorkItem {
	issue := m.detail.Issue()
	if issue == nil {
		issue = m.list.SelectedIssue()
	}
	return issue
}

// issueCommand runs commandFn against the currently targeted issue.
// Returns handled=false if no issue is selected. This collapses the
// repeated nil-guard + ID-capture + runCommand pattern used by most
// action branches.
func (m AppModel) issueCommand(commandFn func(issueID string) error) (tea.Model, tea.Cmd, bool) {
	issue := m.targetIssue()
	if issue == nil {
		return m, nil, false
	}
	issueID := issue.ID
	return m, m.runCommand(func() error { return commandFn(issueID) }), true
}

// ── Command lifecycle ───────────────────────────────────────────

// runCommand launches commandFn in a goroutine via tea.Cmd. The result is
// sent back as commandCompleteMsg, which triggers a data reload.
func (m *AppModel) runCommand(commandFn func() error) tea.Cmd {
	m.commandRunning = true
	return func() tea.Msg {
		err := commandFn()
		return commandCompleteMsg{err: err}
	}
}

// ── Action dispatch ─────────────────────────────────────────────

// executeAction performs an action. Returns handled=false only for ActionNone.
func (m AppModel) executeAction(action Action) (tea.Model, tea.Cmd, bool) {
	if action == ActionNone {
		return m, nil, false
	}

	// Suppress actions while a command is running.
	if m.commandRunning {
		return m, nil, false
	}

	switch action {
	case ActionComment:
		return m.issueCommand(func(issueID string) error {
			return commands.Comment(m.ctx, m.wsSess, issueID)
		})

	case ActionExtract:
		return m.issueCommand(func(issueID string) error {
			return commands.Extract(m.ctx, m.wsSess, issueID, commands.ExtractOptions{
				Copy:   true,
				Filter: m.filter,
			})
		})

	case ActionTransition:
		// Pass the registry-known status so the provider can skip the
		// per-press GET /issue round-trip.
		issue := m.targetIssue()
		if issue == nil {
			return m, nil, false
		}
		issueID, currentStatus := issue.ID, issue.Status
		return m, m.runCommand(func() error {
			return commands.Transition(m.ctx, m.wsSess, issueID, currentStatus)
		}), true

	case ActionAssign:
		return m.issueCommand(func(issueID string) error {
			return commands.Assign(m.ctx, m.wsSess, issueID)
		})

	case ActionEdit:
		return m.issueCommand(func(issueID string) error {
			return commands.Edit(m.ctx, m.wsSess, issueID, nil)
		})

	case ActionBranch:
		return m.issueCommand(func(issueID string) error {
			return commands.Branch(m.ctx, m.wsSess, issueID)
		})

	case ActionOpen:
		return m.executeOpen()

	case ActionFilter:
		return m.executeFilterSwitch()

	case ActionRefresh:
		m.loading = "Refreshing..."
		return m, m.fetchData(m.filter, fetchOpts{}), true

	case ActionNew:
		return m, m.runCommand(func() error {
			return commands.Create(m.ctx, m.wsSess, nil)
		}), true

	case ActionWorkspace:
		return m.executeWorkspaceSwitch()

	case ActionSprint:
		return m.issueCommand(func(issueID string) error {
			return commands.Sprint(m.ctx, m.wsSess, issueID)
		})

	case ActionView:
		return m.executeView()
	}

	return m, nil, false
}

// ── Action implementations ──────────────────────────────────────

func (m AppModel) executeOpen() (tea.Model, tea.Cmd, bool) {
	issue := m.targetIssue()
	if issue == nil {
		return m, nil, false
	}
	browseURL := m.ws.BrowseURL(issue.ID)
	if browseURL == "" {
		m.setNotify("No browse URL configured")
		return m, nil, true
	}
	issueKey := issue.ID
	cmd := func() tea.Msg {
		if err := commands.OpenInBrowser(browseURL); err != nil {
			return notifyMsg{title: "Open failed", message: err.Error()}
		}
		return notifyMsg{title: "Opened", message: issueKey}
	}
	return m, cmd, true
}

// executeView suspends the TUI and runs the workspace's configured viewer
// (e.g. mdcat with kitty graphics) so rich content like inline images can
// be displayed without a Bubble Tea render. Falls back to a notify when
// no view_command is configured.
func (m AppModel) executeView() (tea.Model, tea.Cmd, bool) {
	issue := m.targetIssue()
	if issue == nil {
		return m, nil, false
	}
	if m.ws.ViewCommand == "" {
		m.setNotify("No view_command configured")
		return m, nil, true
	}
	process, err := buildViewProcess(m.ws.ViewCommand, issue.ID)
	if err != nil {
		m.setNotify("View failed: " + err.Error())
		return m, nil, true
	}
	issueKey := issue.ID
	return m, tea.ExecProcess(process, func(err error) tea.Msg {
		if err != nil {
			return notifyMsg{title: "View failed", message: err.Error()}
		}
		return notifyMsg{title: "Viewed", message: issueKey}
	}), true
}

// buildViewProcess parses a view_command template (whitespace-separated
// tokens) into an *exec.Cmd. Substitutes value into both {key} and
// {path} placeholders — issue-view templates use {key}, attachment
// viewers use {path}, but only one appears per template, so a single
// substitution value is unambiguous. Inherits stdio so the viewer
// paints directly to the user's terminal.
func buildViewProcess(template, value string) (*exec.Cmd, error) {
	tokens := strings.Fields(template)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("empty view_command")
	}
	for i, tok := range tokens {
		tok = strings.ReplaceAll(tok, "{key}", value)
		tok = strings.ReplaceAll(tok, "{path}", value)
		tokens[i] = tok
	}
	cmd := exec.Command(tokens[0], tokens[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd, nil
}

// findFilterLabel is the synthetic first entry in the filter popup. Selecting
// it opens a free-text input so the user can pull up any ticket by key or an
// ad-hoc JQL clause (see handleFilterSelection and buildJQL).
const findFilterLabel = "Find by key or JQL…"

func (m AppModel) executeFilterSwitch() (tea.Model, tea.Cmd, bool) {
	var otherFilters []string
	for filterName := range m.ws.Filters {
		if filterName != m.filter {
			otherFilters = append(otherFilters, filterName)
		}
	}
	sort.Strings(otherFilters)

	// Find is always available, so there's no "only one filter" case — it sits
	// at cursor 0 so `f` then Enter jumps straight to it.
	options := append([]string{findFilterLabel}, otherFilters...)
	m.popup.ShowSelect("filter", "Switch Filter", options)
	m.ui.Emit(EventPopupSelect, "title", "Switch Filter")
	return m, nil, true
}

func (m AppModel) executeWorkspaceSwitch() (tea.Model, tea.Cmd, bool) {
	workspaces := m.runtime.Workspaces
	var otherSlugs []string
	for wsSlug := range workspaces {
		if wsSlug != m.ws.Slug {
			otherSlugs = append(otherSlugs, wsSlug)
		}
	}
	if len(otherSlugs) == 0 {
		m.setNotify("Only one workspace configured")
		return m, nil, true
	}
	sort.Strings(otherSlugs)

	displayNames := make([]string, len(otherSlugs))
	for idx, wsSlug := range otherSlugs {
		displayNames[idx] = workspaceLabel(workspaces[wsSlug])
	}

	m.popup.ShowSelectWithActive("workspace", "Switch Workspace", displayNames, otherSlugs, noActiveItem)
	m.ui.Emit(EventPopupSelect, "title", "Switch Workspace")
	return m, nil, true
}

// workspaceLabel returns a human-readable label for a workspace,
// including the server alias when configured.
func workspaceLabel(workspace *core.Workspace) string {
	label := workspace.Name
	if label == "" {
		label = workspace.Slug
	}
	if workspace.ServerAlias != "" {
		label += " (" + workspace.ServerAlias + ")"
	}
	return label
}
