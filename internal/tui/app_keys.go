package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/mikecsmith/ihj/internal/core"
)

// ── Top-level key handler ───────────────────────────────────────

func (m AppModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Capture-and-clear: pendingHint only survives across exactly one
	// subsequent keypress. tryChildNavigation reads it from the local copy
	// below and re-sets m.pendingHint when accumulating further.
	pending := m.pendingHint
	m.pendingHint = ""

	if m.vimMode {
		return m.handleKeyVim(msg, pending)
	}

	keys := m.keys

	// ── Global keys ──

	if key.Matches(msg, keys.Quit) {
		return m, m.quitCmd()
	}

	if key.Matches(msg, keys.Cancel) {
		return m.handleEscape()
	}

	if msg.Code == tea.KeyBackspace && m.view >= ViewDetail {
		return m.handleBackspace()
	}

	if key.Matches(msg, keys.Help) {
		m.showHelp = !m.showHelp
		return m, nil
	}

	// ── Pane focus ──

	if key.Matches(msg, keys.Focus) {
		m.enterFullscreen()
		return m, nil
	}

	if key.Matches(msg, keys.Tab) && m.view != ViewFullscreen {
		if m.view == ViewList {
			m.focusDetail()
		} else {
			m.focusList()
		}
		return m, nil
	}

	// ── Actions ──

	if model, cmd, handled := m.executeAction(m.resolveAction(msg)); handled {
		return model, cmd
	}

	// ── Navigation and child hint keys ──

	if handled, cmd := m.handleNavigation(msg, pending); handled {
		return m, cmd
	}

	// ── Search input fallthrough ──

	return m.forwardToSearch(msg)
}

// ── Escape / backspace ──────────────────────────────────────────

// handleEscape implements the cascading Esc behavior:
// exit detail view → clear search → quit.
func (m AppModel) handleEscape() (tea.Model, tea.Cmd) {
	if m.exitDetailView() {
		return m, nil
	}
	if m.list.search.Value() != "" {
		m.list.search.SetValue("")
		m.list.applyFilter()
		return m, nil
	}
	return m, m.quitCmd()
}

// handleBackspace navigates back through child history, or exits the detail view.
func (m AppModel) handleBackspace() (tea.Model, tea.Cmd) {
	if m.detail.CanGoBack() {
		m.detail.GoBack()
		m.recalcLayout()
		if issue := m.detail.Issue(); issue != nil {
			m.ui.Emit(EventBack, "id", issue.ID, "breadcrumb", m.detail.Breadcrumb())
		}
	} else {
		m.exitDetailView()
	}
	return m, nil
}

// ── Navigation ──────────────────────────────────────────────────

// handleNavigation processes cursor movement and child hint keys.
// pending is the previously-accumulated hint prefix (empty when none).
// Returns (handled, cmd) — cmd is non-nil when a related-issue hint
// kicks off a lazy fetch or when changing the list selection triggers
// a siblings fetch for the new issue's parent.
func (m *AppModel) handleNavigation(msg tea.KeyPressMsg, pending string) (bool, tea.Cmd) {
	if m.view >= ViewDetail {
		return m.handleDetailNavigation(msg, pending)
	}
	return m.handleListNavigation(msg)
}

func (m *AppModel) handleDetailNavigation(msg tea.KeyPressMsg, pending string) (bool, tea.Cmd) {
	keys := m.keys

	switch {
	case key.Matches(msg, keys.Up), key.Matches(msg, keys.DetailUp):
		m.detail.ScrollUp(scrollLines)
		return true, nil
	case key.Matches(msg, keys.Down), key.Matches(msg, keys.DetailDown):
		m.detail.ScrollDown(scrollLines)
		return true, nil
	case key.Matches(msg, keys.PageUp):
		m.detail.ScrollUp(m.detailContentH)
		return true, nil
	case key.Matches(msg, keys.PageDn):
		m.detail.ScrollDown(m.detailContentH)
		return true, nil
	case key.Matches(msg, keys.Home):
		m.detail.ScrollToTop()
		return true, nil
	case key.Matches(msg, keys.End):
		m.detail.ScrollToBottom()
		return true, nil
	}

	// Hint keys navigate to child issues.
	return m.tryChildNavigation(msg, pending)
}

func (m *AppModel) tryChildNavigation(msg tea.KeyPressMsg, pending string) (bool, tea.Cmd) {
	hl := m.detail.HintLabelLength()
	if hl == 0 {
		return false, nil
	}

	pressed := msg.String()
	if len([]rune(pressed)) != 1 {
		return false, nil
	}

	candidate := pending + pressed

	// Need more chars before this is a complete hint. Consume only when the
	// candidate is actually a prefix of some hint, otherwise let the press
	// fall through to search input or whatever else.
	if len(candidate) < hl {
		if !m.detail.IsHintPrefix(candidate) {
			return false, nil
		}
		m.pendingHint = candidate
		return true, nil
	}

	// Candidate is now full-length. Resolve against children → attachments → lazy related.
	if target := m.detail.NavTargetForKey(candidate); target != nil {
		m.detail.NavigateTo(target)
		m.recalcLayout()
		if issue := m.detail.Issue(); issue != nil {
			m.ui.Emit(EventNavigated, "id", issue.ID, "breadcrumb", m.detail.Breadcrumb())
		}
		return true, m.maybeFetchSiblings()
	}

	if att := m.detail.NavAttachmentForKey(candidate); att != nil {
		return true, m.viewAttachment(*att)
	}

	id := m.detail.NavLinkIDForKey(candidate)
	if id == "" {
		return false, nil
	}
	m.setNotify("Loading " + id + "…")
	return true, m.fetchRelated(id)
}

// viewAttachment downloads the attachment via the provider, then shells
// out to the configured attachment_view_command (default kitten icat
// --hold) which holds until the user presses any key. Tempfile is
// removed on completion.
func (m AppModel) viewAttachment(a core.Attachment) tea.Cmd {
	dl, ok := m.wsSess.Provider.(core.AttachmentDownloader)
	if !ok {
		m.setNotify("Provider does not support attachment downloads")
		return nil
	}
	tmpl := m.ws.AttachmentViewCommand
	if tmpl == "" {
		tmpl = "kitten icat --hold {path}"
	}
	url := a.ContentURL
	suggested := a.Filename
	ctx := m.ctx
	m.setNotify("Loading " + a.Filename + "…")
	return func() tea.Msg {
		path, err := dl.DownloadAttachment(ctx, url, suggested)
		if err != nil {
			return notifyMsg{title: "Attachment failed", message: err.Error()}
		}
		return attachmentReadyMsg{path: path, viewCommand: tmpl, filename: suggested}
	}
}

// fetchRelated returns a tea.Cmd that fetches the named issue via the
// active provider and ships the result back as a relatedFetchedMsg.
func (m AppModel) fetchRelated(id string) tea.Cmd {
	provider := m.wsSess.Provider
	ctx := m.ctx
	return func() tea.Msg {
		item, err := provider.Get(ctx, id)
		return relatedFetchedMsg{id: id, item: item, err: err}
	}
}

func (m *AppModel) handleListNavigation(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	keys := m.keys
	moved := func() tea.Cmd {
		m.syncDetail()
		return m.maybeFetchSiblings()
	}

	switch {
	case key.Matches(msg, keys.Up):
		if m.list.cursor > 0 {
			m.list.cursor--
			return true, moved()
		}
		return true, nil
	case key.Matches(msg, keys.Down):
		if m.list.cursor < len(m.list.filtered)-1 {
			m.list.cursor++
			return true, moved()
		}
		return true, nil
	case key.Matches(msg, keys.Home):
		m.list.cursor = 0
		return true, moved()
	case key.Matches(msg, keys.End):
		m.list.cursor = max(0, len(m.list.filtered)-1)
		return true, moved()
	case key.Matches(msg, keys.PageUp):
		m.list.cursor = max(0, m.list.cursor-m.list.visibleRows())
		return true, moved()
	case key.Matches(msg, keys.PageDn):
		m.list.cursor = min(len(m.list.filtered)-1, m.list.cursor+m.list.visibleRows())
		return true, moved()
	case key.Matches(msg, keys.DetailUp):
		m.detail.ScrollUp(scrollLines)
		return true, nil
	case key.Matches(msg, keys.DetailDown):
		m.detail.ScrollDown(scrollLines)
		return true, nil
	}
	return false, nil
}

// ── Search ──────────────────────────────────────────────────────

func (m AppModel) forwardToSearch(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	previousQuery := m.list.search.Value()
	var cmd tea.Cmd
	m.list.search, cmd = m.list.search.Update(msg)
	if m.list.search.Value() != previousQuery {
		m.list.applyFilter()
		m.syncDetail()
	}
	return m, cmd
}

// ── Action resolution ───────────────────────────────────────────

// resolveAction maps a key press to an Action using the default (alt-key) bindings.
func (m *AppModel) resolveAction(msg tea.KeyPressMsg) Action {
	keys := m.keys

	switch {
	case key.Matches(msg, keys.Refresh):
		return ActionRefresh
	case key.Matches(msg, keys.Filter):
		return ActionFilter
	case key.Matches(msg, keys.Assign):
		return ActionAssign
	case key.Matches(msg, keys.Transition):
		return ActionTransition
	case key.Matches(msg, keys.Open):
		return ActionOpen
	case key.Matches(msg, keys.Edit):
		return ActionEdit
	case key.Matches(msg, keys.Comment):
		return ActionComment
	case key.Matches(msg, keys.Branch):
		return ActionBranch
	case key.Matches(msg, keys.Extract):
		return ActionExtract
	case key.Matches(msg, keys.New):
		return ActionNew
	case key.Matches(msg, keys.Workspace):
		return ActionWorkspace
	case key.Matches(msg, keys.Sprint):
		return ActionSprint
	case key.Matches(msg, keys.View):
		return ActionView
	default:
		return ActionNone
	}
}

// ── Popup results ───────────────────────────────────────────────

func (m AppModel) handlePopupResult(result *PopupResult) (tea.Model, tea.Cmd) {
	// Bridge popups resolve a channel-based prompt for a background command.
	if model, cmd, handled := m.resolveBridgePopup(result); handled {
		return model, cmd
	}

	// TUI-only popups (filter/workspace switcher, etc.).
	if result.Canceled {
		m.setNotify("Cancelled")
		return m, nil
	}

	switch result.ID {
	case "filter":
		return m.handleFilterSelection(result.Value)
	case "workspace":
		return m.handleWorkspaceSelection(result.Value)
	case "adhocfilter":
		// Run the typed text through the same fetch path as named filters.
		if result.Text == "" {
			return m, nil
		}
		m.loading = "Searching " + result.Text + "..."
		return m, m.fetchData(result.Text, fetchOpts{})
	}

	return m, nil
}

func (m AppModel) handleFilterSelection(filterName string) (tea.Model, tea.Cmd) {
	if filterName == "" {
		return m, nil
	}
	if filterName == findFilterLabel {
		m.popup.ShowInput("adhocfilter", "Find issue (key or JQL)", "e.g. PROJ-123 or status = Done")
		m.ui.Emit(EventPopupInput, "title", "Find issue (key or JQL)")
		return m, nil
	}
	if filterName == m.filter {
		m.setNotify("Already on filter: " + filterName)
		return m, nil
	}
	m.loading = "Loading " + strings.ToUpper(filterName) + "..."
	return m, m.fetchData(filterName, fetchOpts{})
}

func (m AppModel) handleWorkspaceSelection(workspaceSlug string) (tea.Model, tea.Cmd) {
	if workspaceSlug == "" {
		return m, nil
	}
	if workspaceSlug == m.ws.Slug {
		m.setNotify("Already on this workspace")
		return m, nil
	}
	return m, m.switchWorkspace(workspaceSlug)
}

// ── Bridge popup resolution ─────────────────────────────────────

// resolveBridgePopup handles popup results that originate from the UI bridge
// (Select/Confirm/InputText). These resolve a channel-based prompt so a
// background command goroutine can continue. Returns handled=true if the
// result was a bridge popup.
func (m AppModel) resolveBridgePopup(result *PopupResult) (tea.Model, tea.Cmd, bool) {
	switch result.ID {
	case "bridge-select":
		selectedIndex := result.Index
		if result.Canceled {
			selectedIndex = -1
		}
		m.ui.resolveSelect(selectedIndex)
		return m, nil, true

	case "bridge-confirm":
		confirmed := !result.Canceled && result.Index == 0
		m.ui.resolveConfirm(confirmed)
		return m, nil, true

	case "bridge-input":
		m.ui.resolveInput(result.Text, result.Canceled)
		return m, nil, true

	case "bridge-multi":
		var idxs []int
		if !result.Canceled {
			idxs = result.Indices
		}
		m.ui.resolveSelectMulti(idxs)
		return m, nil, true
	}

	return m, nil, false
}
