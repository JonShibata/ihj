# TUI Guide

The `ihj` TUI is a split-pane terminal interface: a detail pane (top) showing the selected issue, and a list pane (bottom) with fuzzy search.

## Detail Pane

The detail pane displays the selected issue's metadata in sections:

- **Header** — issue key, type, status, priority, and summary.
- **Ownership** — assignee, reporter (paired with temporal fields).
- **Temporal** — created and updated dates.
- **Iteration** — sprint name (scrum boards only, shown when populated).
- **Categorisation** — labels, components (shown when populated).
- **Parent** — parent issue link (shown when set).
- **FIELDS** — scalar custom and dynamic fields discovered from the provider. Pinned fields (configured via `fields` on the issue type) always appear, with an em dash if empty; unpinned scalar fields are hidden to avoid noise from fields Jira reports broadly across types.
- **Description** — rendered Markdown from the issue body.
- **Rich-text fields** — long-form custom fields (e.g. Success Criteria, Steps to Reproduce) render as their own full-width blocks below the description whenever they contain content — pinned or not. Empty ones stay hidden.
- **Child issues** — direct children with hint keys for navigation.
- **Related issues** — non-hierarchical links (parent, blocks, blocked by, siblings sharing the same parent, relates to). Every link gets a hint key. Targets in the current filter view navigate instantly; targets outside the view trigger a `Provider.Get` and the detail pane jumps to them when they arrive (a transient "Loading X…" notice covers the latency).
- **Comments** — the most recent comments (the last three by default; configurable via `comment_limit`, where `0` shows all). See [config.md](config.md).

## Layout

- **Enter** expands the detail pane to fill the entire terminal (focus mode).
- **Tab** toggles keyboard focus between panes without changing the layout.
- **Esc** exits focus mode, then clears search, then quits (in that priority order).

When the detail pane is focused (via Tab or Enter), `Up`/`Down` scroll the detail content, hint keys (`0`-`9`, then `a`-`z`) navigate child and related issues (children first, then related), and `Backspace` pops back one level. All action keys work regardless of focus state.

## Key Bindings (Default Mode)

### Navigation

| Key                     | Action                                  |
| ----------------------- | --------------------------------------- |
| `Up` / `Down`           | Move cursor (or scroll detail when focused) |
| `Home` / `End`          | Jump to first / last issue              |
| `PgUp` / `PgDown`       | Page up / down                          |
| `Shift+Up` / `Ctrl+U`   | Scroll detail up (from list focus)      |
| `Shift+Down` / `Ctrl+D` | Scroll detail down (from list focus)    |
| `Enter`                 | Focus mode (full-screen detail)         |
| `Tab`                   | Toggle focus between list / detail pane |
| `0`-`9`, `a`-`z`        | Navigate to child issue by hint key     |
| `Backspace`             | Go back (pop child history)             |
| `Esc`                   | Exit focus / clear search / quit        |
| `Ctrl+C`                | Quit                                    |

### Actions

| Key      | Action                             |
| -------- | ---------------------------------- |
| `Alt+E`  | Edit selected issue (opens editor) |
| `Ctrl+N` | Create new issue                   |
| `Alt+T`  | Transition (change status)         |
| `Alt+A`  | Assign to yourself                 |
| `Alt+C`  | Add comment                        |
| `Alt+O`  | Open in browser                    |
| `Alt+N`  | Copy git branch name to clipboard  |
| `Alt+X`  | Extract issue context for LLM      |
| `Alt+F`  | Switch filter / find by key or JQL |
| `Alt+W`  | Switch workspace                   |
| `Alt+S`  | Assign to sprint                   |
| `Alt+V`  | View issue via external command (see [view_command](#external-viewer)) |
| `Alt+R`  | Refresh data                       |
| `Alt+/`  | Show help overlay                  |

### Search

Type any character to start fuzzy filtering. Matches across issue key, summary, assignee, status, and type. Press `Esc` to clear the filter.

This is a client-side filter over the issues already loaded. To pull up a ticket that isn't in the current view — a closed one, or one from a previous sprint — open the filter popup (`Alt+F` / `f`) and choose **Find by key or JQL…**. Type an issue key (`PROJ-123`) or any JQL clause (`status = Done`); it's run server-side against the board.

## Vim Mode

Enable with `vim_mode: true` in your config. Replaces modifier-key bindings with a modal interface.

### Normal Mode

Single-character keys for actions and navigation:

| Key   | Action                             |
| ----- | ---------------------------------- |
| `j`/`k` | Move cursor down / up            |
| `g`/`G` | Jump to first / last issue       |
| `e`   | Edit selected issue                |
| `n`   | Create new issue                   |
| `t`   | Transition (change status)         |
| `a`   | Assign to yourself                 |
| `c`   | Add comment                        |
| `o`   | Open in browser                    |
| `b`   | Copy git branch name               |
| `x`   | Extract issue context for LLM      |
| `f`   | Switch filter / find by key or JQL |
| `w`   | Switch workspace                   |
| `s`   | Assign to sprint                   |
| `v`   | View issue via external command    |
| `r`   | Refresh data                       |
| `/`   | Enter search mode                  |
| `:`   | Enter command mode                 |
| `Enter` | Focus mode (full-screen detail)  |
| `Tab` | Toggle focus between panes         |
| `Backspace` | Go back (pop child history)  |
| `Esc` | Exit focus / clear search          |
| `?`   | Show help overlay                  |

### Search Mode

Press `/` to enter. Type to fuzzy filter. `Enter` or `Esc` returns to normal mode. The filter is preserved.

### Command Mode

Press `:` to enter. Supported commands: `:q`, `:quit`, `:h`, `:help`.

## Layout Configuration

The detail pane height and help bar visibility are configurable:

```yaml
layout:
  detail_height: 55    # Percentage of available height (20-80, default 55)
  show_help_bar: true  # Show key binding bar (default true)
```

When `show_help_bar` is `false`, the space is reclaimed for content. In vim mode, a minimal mode indicator (NORMAL / `/` / `:`) is always shown regardless of this setting. The `?` help overlay remains accessible either way.

## Custom Shortcuts

Default-mode action keys can be remapped. Ignored when `vim_mode` is enabled.

```yaml
shortcuts:
  extract: "ctrl+x"
  branch: "ctrl+b"
```

Available actions: `refresh`, `filter`, `workspace`, `sprint`, `view`, `edit`, `new`, `transition`, `assign`, `comment`, `open`, `branch`, `extract`.

## External viewer

Bubble Tea renders to ANSI; it can't paint inline images. When a workspace
defines `view_command:`, pressing `Alt+V` (or `v` in vim mode) suspends
the TUI and runs the configured viewer with stdin/stdout/stderr attached
to your terminal. Useful for tools like `mdcat` that emit kitty graphics
protocol escapes for ticket attachments.

```yaml
workspaces:
  mine:
    view_command: "/home/me/bin/jira_issue_view.sh {key}"
```

The template is whitespace-split; `{key}` substitutes with the selected
issue's ID. When the viewer exits, the TUI redraws automatically.

Shortcuts must include a modifier prefix (`alt+`, `ctrl+`, `super+`, `hyper+`). Collisions with reserved bindings are rejected at config load.
