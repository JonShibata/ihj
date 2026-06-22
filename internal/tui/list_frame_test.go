package tui_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mikecsmith/ihj/internal/core"
	"github.com/mikecsmith/ihj/internal/testutil"
	"github.com/mikecsmith/ihj/internal/tui"
)

// TestFullFrameNoWrap guards against the regression where the list pane was
// sized to innerW but the outer frame's content area was 2 cells narrower, so
// long summaries wrapped onto a second line. A wrapped row makes the frame
// taller than the terminal, so the frame must stay exactly termH lines and no
// line may exceed termW.
func TestFullFrameNoWrap(t *testing.T) {
	const termW, termH = 116, 40

	items := make([]*core.WorkItem, 8)
	for i := range items {
		items[i] = &core.WorkItem{
			ID:      fmt.Sprintf("PROJ-%04d", i),
			Type:    "Bug",
			Status:  "To Do",
			Summary: "a deliberately long summary line that must be truncated so it fits within the list column width",
		}
	}

	ui := tui.NewBubbleTeaUI()
	ui.EditorCmd = "vim"
	h := testutil.NewTestHarness(t, ui)
	m := tui.NewAppModel(context.Background(), h.Runtime, h.Session, h.Factory, h.WS, "default", items, time.Time{}, ui, false, nil, 0, true)

	res, _ := m.Update(tea.WindowSizeMsg{Width: termW, Height: termH})
	m = res.(tui.AppModel)

	lines := strings.Split(m.View().Content, "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) != termH {
		t.Errorf("frame is %d lines, want %d (rows wrapping?)", len(lines), termH)
	}
	for i, ln := range lines {
		if w := ansi.StringWidth(ln); w > termW {
			t.Errorf("line %d width=%d exceeds termW=%d: %q", i, w, termW, ansi.Strip(ln))
		}
	}
}
