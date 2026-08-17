package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// These tests pin the invariant behind the bug close-out keystroke-leak fix:
// while a background command is running, the main UI must not process key
// presses even in the windows where no bridge popup is on screen yet. A
// multi-step workflow (pick status → Resolution → Root Cause) drives popups
// from a goroutine; between them there's a gap where the previous popup has
// closed but the next one hasn't arrived, and keys typed then used to leak
// into search / navigation / actions, dropping the workflow.

// TestCommandRunning_KeyDoesNotLeakToSearch verifies a printable key is
// swallowed while a command is running, but reaches search otherwise (the
// control case documents the exact leak this guards against).
func TestCommandRunning_KeyDoesNotLeakToSearch(t *testing.T) {
	// Control: with no command running, a plain key falls through to search.
	m := newTestModel(t)
	r, _ := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = r.(AppModel)
	if m.list.search.Value() != "x" {
		t.Fatalf("control: search = %q, want %q (key should reach search when idle)",
			m.list.search.Value(), "x")
	}

	// Guarded: while a command is running, the same key is swallowed.
	m = newTestModel(t)
	m.commandRunning = true
	r, _ = m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = r.(AppModel)
	if m.list.search.Value() != "" {
		t.Errorf("search = %q, want empty — key leaked to main UI during a running command",
			m.list.search.Value())
	}
}

// TestCommandRunning_EscDoesNotDropWorkflow verifies Esc typed in a between-
// popups gap is swallowed rather than triggering a main-UI Esc side effect
// (here: clearing the search filter), which in the field manifested as the
// close-out workflow being dropped.
func TestCommandRunning_EscDoesNotDropWorkflow(t *testing.T) {
	m := newVimTestModel(t)
	m.list.search.SetValue("keep-me")
	m.commandRunning = true

	r, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = r.(AppModel)
	if m.list.search.Value() != "keep-me" {
		t.Errorf("search = %q, want %q — Esc leaked to main UI during a running command",
			m.list.search.Value(), "keep-me")
	}

	// A leaked '/' would flip vim into search capture mode.
	r, _ = m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = r.(AppModel)
	if m.capture != CaptureNone {
		t.Errorf("capture = %d, want CaptureNone — '/' leaked into search mode during a running command", m.capture)
	}
}

// TestCommandRunning_CtrlCStillQuits verifies the escape hatch: a stuck
// command can still be aborted with Ctrl+C.
func TestCommandRunning_CtrlCStillQuits(t *testing.T) {
	m := newTestModel(t)
	m.commandRunning = true
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Error("Ctrl+C should still produce a quit command while a command is running")
	}
}

// TestCommandRunning_PopupStillReceivesInput guards against over-reaching: the
// bridge popups that the workflow does put on screen must keep receiving keys.
func TestCommandRunning_PopupStillReceivesInput(t *testing.T) {
	m := newTestModel(t)
	m.commandRunning = true
	m.popup.ShowInput("bridge-input", "Root Cause", "")

	r, _ := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = r.(AppModel)
	if got := m.popup.input.Value(); got != "x" {
		t.Errorf("popup input = %q, want %q — active bridge popup should still receive keys", got, "x")
	}
}
