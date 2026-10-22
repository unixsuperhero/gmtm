# mtm (UI identity: cmtm)

Project-based TUIOS workspace manager built from the clean-room spec in `~/hub/prompts/clean-mtm.md`.
Go + Bubble Tea. Uses the dedicated TUIOS server **clean-mtm** by default; `-d` selects the normal TUIOS daemon.

## Run

```sh
go build -o bin/mtm ./cmd/mtm
bin/mtm                 # TUI (default)   flags: --server NAME --db PATH --project NAME
bin/mtm -d              # use the default TUIOS daemon (also --default)
bin/mtm server          # selected server, socket path, answering?
bin/mtm json file.json  # built-in JSON explorer (no file + TTY → fuzzy path picker)
bin/mtm actions         # every action and its effective keys
bin/mtm help
```

Unknown first words run `mtm-<word>` from PATH (unique prefix allowed: `mtm dep` → `mtm-deploy`).

Agent creation defaults to `omp` when installed, then falls back to `claude`,
`codex`, or `gemini`. The editable agent form can select a different harness.

`-d` / `--default` cannot be combined with `--server NAME`. Selection is explicit:
inherited `TUIOS_SOCKET` and `TUIOS_SERVER` never redirect mtm. The default daemon
uses identity `@default` in status and capture records, distinct from a named
server called `default`. Use `bin/mtm -d server` to inspect its connection or
`bin/mtm -d setup` to install capture hooks for it.

## Keys (normal mode)

| keys | action |
|---|---|
| `n t` / `n a` / `n v` | quick new terminal / agent / vim |
| `N t` / `N a` / `N v` | same via editable form |
| `i` | live mode on selected pane (leader `ctrl+a`: `d` detach, `n`/`p` next/prev, `?` help, `ctrl+a` literal) |
| `P` | set active project; type an existing or new name, then `Ctrl+S` to submit (new projects are created only on submit; cancel creates nothing) |
| `A` | assign selected pane to an existing or new project; `Ctrl+S` saves metadata only (session and process unchanged) |
| `x` | close (confirm) · `p` pin · `s` star · `a` save context |
| `/` search · `z` cycle grouping · `Z` customize grouping · `C` clear |
| `:` / `ctrl+p` | palette · `?` help · `J` jump · `C`ontext · `M` meta-mode |
| `F4` | New note (global, every mode) · `ctrl+s` saves · `ctrl+o` in path fields opens the fd picker |
| `1`–`7` `[` `]` | pages: Panes, Sessions, Hosts, Projects, Context, Settings, Notes |

Panes are scoped to the active (pinned) project after choosing one with `P`,
activating it from Projects, or starting with `--project`. Switching projects
updates the pane list immediately; choosing `no-project` shows only unowned panes.
Before a project is chosen, Panes and Notes show all projects. Other pages remain unscoped.
Quick-create always uses the pinned project, including when its pane list is empty
or another project's row is selected. Create forms default to the pinned project;
their project field remains explicitly editable.

To assign a terminal, Vim, or agent pane, select it on Panes and press `A`.
Choose an existing project or type a new name, then `Ctrl+S`; choose `no-project`
to clear ownership. New projects and assignments are saved together, only on
submit. Assignment persists in the local database and overrides session-derived
ownership without moving the pane, restarting its process, or changing TUIOS.
The active project stays unchanged, so a reassigned pane disappears from its old
project filter; use `P` to view its destination project. Project counts and newly
saved context use the assignment; existing saved context keeps its own association.

The **Screen** detail tab refreshes the selected pane's current screen automatically,
250 ms after each capture completes. It does not require keyboard input and does
not show scrollback. Refresh pauses while hidden, behind a modal, or in interactive
live mode. Screen viewing never changes pane focus, sends input, or resizes it.

Opening **Context** (`C`) extracts HTTP(S) URLs and file/directory references from
the selected pane's captured commands, output, prompts, responses, and recent
screen content. `.md` and `.markdown` references appear as documents. Opening the
Context page (`5`) extracts from panes belonging to the pinned project.

References are saved with their source pane and project, tagged `extracted`, and
deduplicated on subsequent opens. Existing titles, notes, and edited project
associations are preserved. Relative paths use the pane's reported working
directory; quoted paths with spaces and Markdown links are supported.

Local files/documents have read-only previews and directories can be browsed.
URLs are not fetched or opened automatically. Remote paths are retained as
references but never read from the local filesystem. Extraction runs on Context
entry, not continuously, and reports unavailable history/capture without hiding
previously saved references.

### Saved notes

Press `7` (or choose **Open Saved notes** in the palette) to browse saved notes.
Use `P` to select a project; Notes shows only that project's notes, or unassigned
notes when `no-project` is selected. Before choosing a project, it shows all notes.
Projects found only in saved notes remain available even with no running panes.

`Enter` opens the selected note read-only; `Esc` returns to the list. `/` searches
titles and note text. `e` changes the note's project association, including
`no-project`, using the existing project form. `Ctrl+R` reloads saved notes.
`F4` creates a note associated with the active project; `Ctrl+S` saves it.
Notes remain in the local database across app restarts and also appear in Context.


### Single-line editing

Forms show unique lowercase field codes: one letter where available, longer only
after shorter codes are exhausted. Multi-field forms use the displayed `:code`
and Enter to select a field. Single-field forms open directly in edit mode.
The active-project chooser (`P`) starts blank: type a project name immediately,
or select a suggestion. Enter applies the field and Ctrl+S submits the form.

Single-line form, picker, JSON search, and note-project inputs support:

| keys | editing action |
|---|---|
| `Ctrl+A` / `Ctrl+E` | start / end |
| `Ctrl+B` / `Ctrl+F` | backward / forward character |
| `Alt+B` / `Alt+F` | backward / forward word |
| `Ctrl+H` / `Ctrl+D` | delete previous / next character |
| `Ctrl+U` / `Ctrl+K` | kill to start / end |
| `Ctrl+W` or `Alt+Backspace` / `Alt+D` | kill previous / next word |
| `Ctrl+Y` | yank the last killed text |
| `Ctrl+T` | transpose adjacent characters |

In path inputs, words are directory components: `/tmp/my folder/leaf/` becomes
`/tmp/my folder/` after Ctrl+W, then `/tmp/`. Editing acts at the cursor and
preserves the suffix. Picker Ctrl+U/K/D edit in insert mode; normal-mode navigation
and Ctrl+N/P option selection remain available.

## Layout

See `docs/architecture.md` (module boundaries, thin orchestrator in `internal/app`) and
`docs/tuios-protocol-notes.md` (verified daemon protocol). Build/test on vps2:
`scripts/remote.sh <ws> [secs] -- 'go build ./... && go test ./...'`.
Acceptance: `scripts/acceptance.sh` (throwaway servers `cmt-<8hex>`), report in `docs/acceptance-report.md`.
