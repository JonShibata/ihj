package tui

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/mikecsmith/ihj/internal/core"
	"github.com/mikecsmith/ihj/internal/terminal"
)

// HistoryModel is the scrollable content of the issue-history overlay. It
// holds the change entries for one issue and renders them newest-first, one
// author/timestamp header per entry followed by its individual field changes.
type HistoryModel struct {
	viewport viewport.Model
	styles   *terminal.Styles
	issueID  string
	entries  []core.HistoryEntry
	width    int
	height   int
}

// NewHistoryModel creates the history overlay content model.
func NewHistoryModel(styles *terminal.Styles) HistoryModel {
	return HistoryModel{
		viewport: viewport.New(),
		styles:   styles,
	}
}

// SetEntries loads a fresh set of history entries for an issue and resets the
// scroll position to the top.
func (m *HistoryModel) SetEntries(issueID string, entries []core.HistoryEntry) {
	m.issueID = issueID
	m.entries = entries
	m.rebuildContent()
	m.viewport.GotoTop()
}

// IssueID returns the ID of the issue whose history is loaded.
func (m *HistoryModel) IssueID() string { return m.issueID }

// SetSize updates the viewport dimensions and re-renders.
func (m *HistoryModel) SetSize(w, h int) {
	if m.width == w && m.height == h {
		return
	}
	m.width = w
	m.height = h
	m.viewport.SetWidth(w)
	m.viewport.SetHeight(h)
	m.rebuildContent()
}

// Scroll helpers mirror DetailModel so the overlay reuses the same keys.
func (m *HistoryModel) ScrollUp(lines int)   { m.viewport.ScrollUp(lines) }
func (m *HistoryModel) ScrollDown(lines int) { m.viewport.ScrollDown(lines) }
func (m *HistoryModel) ScrollToTop()         { m.viewport.GotoTop() }
func (m *HistoryModel) ScrollToBottom()      { m.viewport.GotoBottom() }

// View returns the rendered, scrollable history content.
func (m HistoryModel) View() string { return m.viewport.View() }

// rebuildContent renders the entries into the viewport, wrapping every line
// to the viewport width so long field values (descriptions, summaries) wrap
// instead of being truncated.
func (m *HistoryModel) rebuildContent() {
	styles := m.styles

	width := m.width
	if width <= 0 {
		width = 80 // not sized yet — fall back so content still wraps sanely
	}

	if len(m.entries) == 0 {
		placeholder := lipgloss.NewStyle().Faint(true).Italic(true).Width(width).Render("No history.")
		m.viewport.SetContent(placeholder)
		return
	}

	fieldStyle := lipgloss.NewStyle().Bold(true)
	arrow := lipgloss.NewStyle().Faint(true).Render(" " + core.GlyphArrow + " ")
	empty := lipgloss.NewStyle().Faint(true).Italic(true).Render("∅")

	var buf strings.Builder
	for i, e := range m.entries {
		if i > 0 {
			buf.WriteString("\n")
		}
		header := styles.CommentAuthor.Render(e.Author) + "  " +
			styles.CommentDate.Render(core.GlyphDot+" "+e.Created)
		buf.WriteString(lipgloss.NewStyle().Width(width).Render(header) + "\n")
		for _, c := range e.Changes {
			from, to := c.From, c.To
			if from == "" {
				from = empty
			}
			if to == "" {
				to = empty
			}
			prefix := "  " + fieldStyle.Render(c.Field) + ": "
			buf.WriteString(hangingWrap(prefix, from+arrow+to, width) + "\n")
		}
	}

	m.viewport.SetContent(strings.TrimRight(buf.String(), "\n"))
}

// hangingWrap renders "prefix + body" wrapped to width, indenting wrapped
// continuation lines so they align under the body (a hanging indent). ANSI
// styling in prefix and body is preserved. When the terminal is too narrow
// for a hanging indent, the whole thing wraps flush left instead.
func hangingWrap(prefix, body string, width int) string {
	pw := lipgloss.Width(prefix)
	avail := width - pw
	if avail < 8 || pw >= width {
		return lipgloss.NewStyle().Width(max(width, 1)).Render(prefix + body)
	}
	wrapped := lipgloss.NewStyle().Width(avail).Render(body)
	lines := strings.Split(wrapped, "\n")
	indent := strings.Repeat(" ", pw)
	var b strings.Builder
	for i, ln := range lines {
		if i > 0 {
			b.WriteByte('\n')
			b.WriteString(indent)
		} else {
			b.WriteString(prefix)
		}
		b.WriteString(ln)
	}
	return b.String()
}
