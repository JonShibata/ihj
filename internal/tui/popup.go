package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mikecsmith/ihj/internal/core"
	"github.com/mikecsmith/ihj/internal/terminal"
)

// ── Layout constants ────────────────────────────────────────────

const (
	popupMaxWidth       = 80 // Maximum popup panel width.
	popupMinWidth       = 30 // Minimum popup panel width.
	popupHorizontalPad  = 8  // Breathing room subtracted from terminal width.
	popupBorderPadding  = 6  // Border + padding consumed by the box frame.
	popupMinInnerWidth  = 20 // Floor for the text-input inner width.
	popupInputHeight    = 15 // Visible rows in the text-input area.
	popupInputCharLimit = 4000

	selectViewportMargin  = 10 // Rows reserved for title, hints, and box chrome.
	selectMinVisibleItems = 5  // Minimum items shown even in a tiny terminal.

	// noActiveItem indicates that no option should be marked as active.
	noActiveItem = -1
)

// ── Types ───────────────────────────────────────────────────────

// PopupMode indicates what kind of popup is active.
type PopupMode int

const (
	PopupNone        PopupMode = iota
	PopupSelect                // Choose from a list of options.
	PopupInput                 // Free-text input (comments, extract prompts).
	PopupMultiSelect           // Choose any number of options from a filterable list.
)

// PopupResult is sent when the user confirms or cancels a popup.
type PopupResult struct {
	ID       string   // Identifies which action triggered the popup.
	Index    int      // Selected index (PopupSelect), -1 if cancelled.
	Value    string   // The exact string selected from the options list.
	Indices  []int    // Selected indices (PopupMultiSelect).
	Values   []string // Selected option strings (PopupMultiSelect).
	Text     string   // Input text (PopupInput), empty if cancelled.
	Canceled bool
}

// PopupModel is a centered floating overlay panel, styled like LazyGit.
type PopupModel struct {
	mode   PopupMode
	id     string   // Action identifier (e.g. "transition", "comment").
	title  string   // Rendered in the top border.
	labels []string // Display text for PopupSelect.
	values []string // Underlying values returned in PopupResult.Value (nil = use labels).
	cursor int      // Currently highlighted option (PopupSelect).

	// activeIndex marks which option is the "current" item (e.g. the
	// active filter or workspace). Set to noActiveItem when none is active.
	// The renderer prefixes it with a bullet and dims it.
	activeIndex int

	input textarea.Model // For PopupInput.

	// Multi-select state (PopupMultiSelect only).
	multiSelected map[int]bool   // keyed on original index in p.labels
	visibleOrder  []int          // original-index list after filter
	filter        textinput.Model

	// pendingHint accumulates the first character of a multi-char hint
	// label while waiting for the second keypress. Reset whenever the
	// popup processes any non-hint key.
	pendingHint string

	width, height int // Available terminal dimensions.
	styles        *terminal.Styles
	keys          terminal.KeyMap
}

// ── Lifecycle ───────────────────────────────────────────────────

// NewPopupModel creates an inactive popup.
func NewPopupModel(styles *terminal.Styles, keys terminal.KeyMap) PopupModel {
	textArea := textarea.New()
	textArea.ShowLineNumbers = false
	textArea.CharLimit = popupInputCharLimit
	textArea.SetStyles(popupTextareaStyles(styles.Theme()))

	filter := textinput.New()
	filter.Prompt = "/ "
	filter.Placeholder = "type to filter"
	filter.CharLimit = 120

	return PopupModel{
		mode:   PopupNone,
		styles: styles,
		keys:   keys,
		input:  textArea,
		filter: filter,
	}
}

// popupTextareaStyles overrides the bubbles/textarea defaults so the
// input is readable on both light and dark terminal backgrounds. The
// stock DefaultDarkStyles paints the cursor line with background color 0
// (black) and leaves the text style unset so it inherits the terminal's
// default foreground — on a light terminal that's near-black on black
// (i.e. invisible). We drop the cursor-line background entirely (the
// popup's own background shows through) and route the remaining colours
// through the theme's mid-tone palette so neither extreme breaks.
func popupTextareaStyles(theme *terminal.Theme) textarea.Styles {
	noBg := lipgloss.NewStyle()
	muted := lipgloss.NewStyle().Foreground(theme.Muted)
	prompt := lipgloss.NewStyle().Foreground(theme.Accent)

	state := func() textarea.StyleState {
		return textarea.StyleState{
			Base:             lipgloss.NewStyle(),
			Text:             lipgloss.NewStyle(),
			LineNumber:       muted,
			CursorLineNumber: muted,
			CursorLine:       noBg,
			EndOfBuffer:      muted,
			Placeholder:      muted,
			Prompt:           prompt,
		}
	}
	return textarea.Styles{
		Focused: state(),
		Blurred: state(),
		Cursor: textarea.CursorStyle{
			Color: theme.Accent,
			Shape: tea.CursorBlock,
			Blink: true,
		},
	}
}

// Active returns true if a popup is currently displayed.
func (p *PopupModel) Active() bool { return p.mode != PopupNone }

// ShowSelect opens a selection popup. PopupResult.Value returns the
// selected option string unchanged.
func (p *PopupModel) ShowSelect(id, title string, options []string) {
	p.mode = PopupSelect
	p.id = id
	p.title = title
	p.labels = options
	p.values = nil
	p.activeIndex = noActiveItem
	p.cursor = 0
	p.pendingHint = ""
}

// ShowSelectWithActive opens a selection popup where one option is marked
// as the current/active item (e.g. the current filter or workspace).
// The popup renders the active item with a bullet prefix and dims it.
// activeIndex is the index into options of the current item, or noActiveItem for none.
// When labels differ from the underlying values (e.g. display names vs slugs),
// pass both; otherwise pass nil for values and the labels are returned as values.
func (p *PopupModel) ShowSelectWithActive(id, title string, labels []string, values []string, activeIndex int) {
	p.mode = PopupSelect
	p.id = id
	p.title = title
	p.labels = labels
	p.values = values
	p.activeIndex = activeIndex
	p.cursor = 0
	p.pendingHint = ""
}

// ShowMultiSelect opens a multi-selection popup with a substring filter.
// Cursor and visible window operate over the filter-narrowed list; the
// returned PopupResult.Indices reference the original options slice.
func (p *PopupModel) ShowMultiSelect(id, title string, options []string) {
	p.mode = PopupMultiSelect
	p.id = id
	p.title = title
	p.labels = options
	p.values = nil
	p.activeIndex = noActiveItem
	p.cursor = 0
	p.pendingHint = ""
	p.multiSelected = make(map[int]bool)
	p.filter.Reset()
	p.filter.Focus()
	p.rebuildVisible()
}

// rebuildVisible recomputes visibleOrder from the current filter query.
// Substring match is case-insensitive. Cursor is clamped into the new range.
func (p *PopupModel) rebuildVisible() {
	q := strings.ToLower(strings.TrimSpace(p.filter.Value()))
	p.visibleOrder = p.visibleOrder[:0]
	if q == "" {
		for i := range p.labels {
			p.visibleOrder = append(p.visibleOrder, i)
		}
	} else {
		for i, l := range p.labels {
			if strings.Contains(strings.ToLower(l), q) {
				p.visibleOrder = append(p.visibleOrder, i)
			}
		}
	}
	if p.cursor >= len(p.visibleOrder) {
		p.cursor = len(p.visibleOrder) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
}

// ShowInput opens a text input popup.
func (p *PopupModel) ShowInput(id, title, placeholder string) {
	p.mode = PopupInput
	p.id = id
	p.title = title
	p.input.Reset()
	p.input.Placeholder = placeholder
	p.input.Focus()
}

// SetSize tells the popup how large the terminal is so it can center itself.
func (p *PopupModel) SetSize(width, height int) {
	p.width = width
	p.height = height
}

// Close dismisses the popup without producing a result.
func (p *PopupModel) Close() {
	p.mode = PopupNone
	p.input.Blur()
	p.filter.Blur()
	p.multiSelected = nil
	p.visibleOrder = nil
}

// ── Update handlers ─────────────────────────────────────────────

// Update handles key events when the popup is active.
func (p *PopupModel) Update(msg tea.Msg) (tea.Cmd, *PopupResult) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch p.mode {
		case PopupSelect:
			return p.updateSelect(msg)
		case PopupInput:
			return p.updateInput(msg)
		case PopupMultiSelect:
			return p.updateMultiSelect(msg)
		}
	}
	return nil, nil
}

func (p *PopupModel) updateMultiSelect(msg tea.KeyPressMsg) (tea.Cmd, *PopupResult) {
	keys := p.keys

	switch {
	case key.Matches(msg, keys.Cancel), key.Matches(msg, keys.Quit):
		result := &PopupResult{ID: p.id, Index: -1, Canceled: true}
		p.Close()
		return nil, result
	case key.Matches(msg, keys.Submit), key.Matches(msg, keys.Focus):
		// Commit selected indices in original order.
		var idxs []int
		var vals []string
		for i, l := range p.labels {
			if p.multiSelected[i] {
				idxs = append(idxs, i)
				vals = append(vals, l)
			}
		}
		result := &PopupResult{ID: p.id, Indices: idxs, Values: vals}
		p.Close()
		return nil, result
	case key.Matches(msg, keys.Up):
		if p.cursor > 0 {
			p.cursor--
		}
		return nil, nil
	case key.Matches(msg, keys.Down):
		if p.cursor < len(p.visibleOrder)-1 {
			p.cursor++
		}
		return nil, nil
	case key.Matches(msg, keys.Home):
		p.cursor = 0
		return nil, nil
	case key.Matches(msg, keys.End):
		p.cursor = len(p.visibleOrder) - 1
		if p.cursor < 0 {
			p.cursor = 0
		}
		return nil, nil
	}

	// Space toggles the highlighted entry. tea reports a Space keypress
	// with Code=KeySpace and String()=="space"; the textinput would
	// otherwise consume it as a literal " " character in the filter.
	if msg.Code == tea.KeySpace {
		if p.cursor >= 0 && p.cursor < len(p.visibleOrder) {
			origIdx := p.visibleOrder[p.cursor]
			if p.multiSelected[origIdx] {
				delete(p.multiSelected, origIdx)
			} else {
				p.multiSelected[origIdx] = true
			}
		}
		return nil, nil
	}

	// Everything else feeds the filter input.
	prev := p.filter.Value()
	var cmd tea.Cmd
	p.filter, cmd = p.filter.Update(msg)
	if p.filter.Value() != prev {
		p.rebuildVisible()
	}
	return cmd, nil
}

func (p *PopupModel) updateSelect(msg tea.KeyPressMsg) (tea.Cmd, *PopupResult) {
	keys := p.keys

	switch {
	case key.Matches(msg, keys.Up):
		p.pendingHint = ""
		if p.cursor > 0 {
			p.cursor--
		}
	case key.Matches(msg, keys.Down):
		p.pendingHint = ""
		if p.cursor < len(p.labels)-1 {
			p.cursor++
		}
	case key.Matches(msg, keys.Home):
		p.pendingHint = ""
		p.cursor = 0
	case key.Matches(msg, keys.End):
		p.pendingHint = ""
		p.cursor = len(p.labels) - 1
	case key.Matches(msg, keys.Submit), key.Matches(msg, keys.Focus):
		result := &PopupResult{ID: p.id, Index: p.cursor, Value: p.selectedValue(p.cursor)}
		p.Close()
		return nil, result
	case key.Matches(msg, keys.Cancel), key.Matches(msg, keys.Quit):
		result := &PopupResult{ID: p.id, Index: -1, Canceled: true}
		p.Close()
		return nil, result
	default:
		return p.tryHintKeySelect(msg)
	}
	return nil, nil
}

// tryHintKeySelect resolves a hint label against the popup's option list.
// Hints are 1-char until the option count overflows the alphabet, then 2-char
// (see KeyMap.Hints); the second char is consumed via pendingHint.
func (p *PopupModel) tryHintKeySelect(msg tea.KeyPressMsg) (tea.Cmd, *PopupResult) {
	pending := p.pendingHint
	p.pendingHint = ""

	pressed := msg.String()
	if len([]rune(pressed)) != 1 {
		return nil, nil
	}

	hints := p.keys.Hints(len(p.labels))
	if len(hints) == 0 {
		return nil, nil
	}
	hl := len(hints[0])
	candidate := pending + pressed

	if len(candidate) < hl {
		// Not yet a complete hint — only consume if the prefix is plausible.
		for _, h := range hints {
			if len(h) > len(candidate) && strings.HasPrefix(h, candidate) {
				p.pendingHint = candidate
				return nil, nil
			}
		}
		return nil, nil
	}

	for idx, h := range hints {
		if h == candidate {
			result := &PopupResult{ID: p.id, Index: idx, Value: p.selectedValue(idx)}
			p.Close()
			return nil, result
		}
	}
	return nil, nil
}

// selectedValue returns the underlying value for the given index.
// When values are provided, returns values[idx]; otherwise returns labels[idx].
func (p *PopupModel) selectedValue(idx int) string {
	if p.values != nil && idx < len(p.values) {
		return p.values[idx]
	}
	return p.labels[idx]
}

func (p *PopupModel) updateInput(msg tea.KeyPressMsg) (tea.Cmd, *PopupResult) {
	keys := p.keys

	// Plain Enter submits (chat-style); Shift+Enter inserts a newline.
	// Alt+Enter / Ctrl+S still work via the keys.Submit binding for muscle
	// memory.
	if msg.Code == tea.KeyEnter && msg.Mod == 0 {
		text := strings.TrimSpace(p.input.Value())
		result := &PopupResult{ID: p.id, Text: text, Canceled: text == ""}
		p.Close()
		return nil, result
	}

	switch {
	case key.Matches(msg, keys.Cancel), key.Matches(msg, keys.Quit):
		result := &PopupResult{ID: p.id, Canceled: true}
		p.Close()
		return nil, result
	case key.Matches(msg, keys.Submit):
		text := strings.TrimSpace(p.input.Value())
		result := &PopupResult{ID: p.id, Text: text, Canceled: text == ""}
		p.Close()
		return nil, result
	default:
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(msg)
		return cmd, nil
	}
}

// ── Rendering ───────────────────────────────────────────────────

// View renders the popup as a centered overlay. The caller composites this
// on top of the main TUI content.
func (p *PopupModel) View() string {
	if p.mode == PopupNone {
		return ""
	}

	theme := p.styles.Theme()
	popupWidth := p.width - popupHorizontalPad
	if popupWidth > popupMaxWidth {
		popupWidth = popupMaxWidth
	}
	if popupWidth < popupMinWidth {
		popupWidth = popupMinWidth
	}

	var body string
	switch p.mode {
	case PopupSelect:
		body = p.renderSelect()
	case PopupInput:
		body = p.renderInput(popupWidth)
	case PopupMultiSelect:
		body = p.renderMultiSelect(popupWidth)
	}

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Accent).
		Padding(1, 2).
		Width(popupWidth)

	return boxStyle.Render(body)
}

func (p *PopupModel) renderSelect() string {
	theme := p.styles.Theme()
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.Accent)
	selectedStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.Info)
	normalStyle := lipgloss.NewStyle().Foreground(theme.Text)
	dimStyle := lipgloss.NewStyle().Foreground(theme.Muted)
	hintStyle := lipgloss.NewStyle().Foreground(theme.Muted).Italic(true)

	var buf strings.Builder
	buf.WriteString(titleStyle.Render(p.title) + "\n\n")

	// Calculate a safe sliding window so the popup never exceeds terminal height.
	maxVisible := max(p.height-selectViewportMargin, selectMinVisibleItems)
	start, end := CalculateWindow(p.cursor, len(p.labels), maxVisible)

	if start > 0 {
		buf.WriteString(dimStyle.Render("  "+core.GlyphArrowUp+"  ...") + "\n")
	}

	activeStyle := lipgloss.NewStyle().Foreground(theme.Muted)

	hints := p.keys.Hints(len(p.labels))
	hintW := 0
	if len(hints) > 0 {
		hintW = len(hints[0])
	}
	pad := strings.Repeat(" ", hintW)
	for idx := start; idx < end; idx++ {
		option := p.labels[idx]
		isActive := idx == p.activeIndex

		prefix := "  "
		style := normalStyle
		if idx == p.cursor {
			prefix = core.GlyphTriangle + " "
			style = selectedStyle
		}

		// Mark the currently active item with a bullet and dim styling.
		if isActive {
			option = core.GlyphCircle + " " + option
			if idx != p.cursor {
				style = activeStyle
			}
		}

		shortcut := dimStyle.Render(pad)
		if idx < len(hints) {
			shortcut = dimStyle.Render(hints[idx])
		}
		buf.WriteString(prefix + shortcut + " " + style.Render(option) + "\n")
	}

	if end < len(p.labels) {
		buf.WriteString(dimStyle.Render("  "+core.GlyphArrowDown+"  ...") + "\n")
	}

	buf.WriteString("\n" + hintStyle.Render(
		core.GlyphArrowUp+core.GlyphArrowDown+" Navigate "+
			core.GlyphDot+" Enter Confirm "+
			core.GlyphDot+" Esc Cancel"))
	return buf.String()
}

func (p *PopupModel) renderMultiSelect(popupWidth int) string {
	theme := p.styles.Theme()
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.Accent)
	selectedStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.Info)
	normalStyle := lipgloss.NewStyle().Foreground(theme.Text)
	dimStyle := lipgloss.NewStyle().Foreground(theme.Muted)
	hintStyle := lipgloss.NewStyle().Foreground(theme.Muted).Italic(true)

	innerWidth := max(popupWidth-popupBorderPadding, popupMinInnerWidth)
	p.filter.SetWidth(innerWidth)

	var buf strings.Builder
	checkedCount := len(p.multiSelected) // sparse-true map: counts only true keys
	header := p.title
	if checkedCount > 0 {
		header = fmt.Sprintf("%s  (%d selected)", p.title, checkedCount)
	}
	buf.WriteString(titleStyle.Render(header) + "\n")
	buf.WriteString(p.filter.View() + "\n\n")

	if len(p.visibleOrder) == 0 {
		buf.WriteString(dimStyle.Render("  no matches") + "\n")
	} else {
		maxVisible := max(p.height-selectViewportMargin-2, selectMinVisibleItems)
		start, end := CalculateWindow(p.cursor, len(p.visibleOrder), maxVisible)

		if start > 0 {
			buf.WriteString(dimStyle.Render("  "+core.GlyphArrowUp+"  ...") + "\n")
		}
		for i := start; i < end; i++ {
			origIdx := p.visibleOrder[i]
			option := p.labels[origIdx]
			prefix := "  "
			style := normalStyle
			if i == p.cursor {
				prefix = core.GlyphTriangle + " "
				style = selectedStyle
			}
			check := "[ ]"
			if p.multiSelected[origIdx] {
				check = "[x]"
			}
			buf.WriteString(prefix + dimStyle.Render(check) + " " + style.Render(option) + "\n")
		}
		if end < len(p.visibleOrder) {
			buf.WriteString(dimStyle.Render("  "+core.GlyphArrowDown+"  ...") + "\n")
		}
	}

	buf.WriteString("\n" + hintStyle.Render(
		core.GlyphArrowUp+core.GlyphArrowDown+" Navigate "+
			core.GlyphDot+" Space Toggle "+
			core.GlyphDot+" Enter Confirm "+
			core.GlyphDot+" Esc Cancel"))
	return buf.String()
}

func (p *PopupModel) renderInput(popupWidth int) string {
	theme := p.styles.Theme()
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.Accent)
	hintStyle := lipgloss.NewStyle().Foreground(theme.Muted).Italic(true)

	innerWidth := max(popupWidth-popupBorderPadding, popupMinInnerWidth)
	p.input.SetWidth(innerWidth)
	p.input.SetHeight(popupInputHeight)

	var buf strings.Builder
	buf.WriteString(titleStyle.Render(p.title) + "\n\n")
	buf.WriteString(p.input.View() + "\n\n")

	keys := p.keys
	hint := fmt.Sprintf("Enter Submit "+core.GlyphDot+" Shift+Enter Newline "+core.GlyphDot+" %s %s",
		keys.Cancel.Help().Key, keys.Cancel.Help().Desc,
	)
	_ = keys.Submit // muscle-memory binding still works
	buf.WriteString(hintStyle.Render(hint))
	return buf.String()
}
