package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mikecsmith/ihj/internal/commands"
	"github.com/mikecsmith/ihj/internal/core"
)

type tickMsg time.Time

// tickCmd fires once per second to update the cache age display.
func (m AppModel) tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// userFetchedMsg carries the cached user from the initial FetchMyself call.
type userFetchedMsg struct {
	displayName string
	err         error
}

// dataReloadedMsg carries fresh issue data after a filter switch or refresh.
type dataReloadedMsg struct {
	filter    string
	items     []*core.WorkItem
	fetchedAt time.Time
	err       error
	silent    bool // If true, skip the "Loaded N issues" notification.
	startup   bool // If true, errors are fatal (e.g. auth failure on initial refresh).
}

// ── Bridge message types ──
// Sent by BubbleTeaUI methods via program.Send, handled by AppModel.Update.

type bridgeSelectMsg struct {
	title   string
	options []string
}

type bridgeConfirmMsg struct {
	prompt string
}

type bridgeInputMsg struct {
	prompt  string
	initial string
}

type bridgeEditDocMsg struct {
	initial string
	prefix  string
}

type bridgeEditorDoneMsg struct {
	content string
	err     error
}

// commandCompleteMsg is sent when a runCommand goroutine finishes.
type commandCompleteMsg struct {
	err error
}

// relatedFetchedMsg carries the result of a lazy Provider.Get fired when
// the user hits a hint key for a related issue that isn't in the current
// filter view. Item is non-nil on success.
type relatedFetchedMsg struct {
	id   string
	item *core.WorkItem
	err  error
}

// siblingsFetchedMsg carries the result of a "load siblings" fetch fired
// when the user opens an issue whose parent isn't in the current filter
// view. forIssue is the issue whose detail pane should be updated;
// items are the parent's children (current issue filtered out).
type siblingsFetchedMsg struct {
	forIssue string
	items    []*core.WorkItem
	err      error
}

// attachmentReadyMsg fires once an attachment download completes; the
// detail pane handler then suspends the TUI and runs the viewer.
type attachmentReadyMsg struct {
	path        string // local tempfile path (caller removes when done)
	viewCommand string // template with {path}
	filename    string // for the post-view notify
}

// workspaceSwitchedMsg carries the result of a workspace switch request.
type workspaceSwitchedMsg struct {
	slug      string
	wsSess    *commands.WorkspaceSession
	items     []*core.WorkItem
	fetchedAt time.Time
	err       error
}
