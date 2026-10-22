# mtm (UI identity: cmtm)

Project-based TUIOS workspace manager built from the clean-room spec in `~/hub/prompts/clean-mtm.md`.
Go + Bubble Tea. Always talks to the dedicated TUIOS server **clean-mtm**; never the default daemon.

## Run

```sh
go build -o bin/mtm ./cmd/mtm
bin/mtm                 # TUI (default)   flags: --server NAME --db PATH --project NAME
bin/mtm server          # selected server, socket path, answering?
bin/mtm json file.json  # built-in JSON explorer (no file + TTY → fuzzy path picker)
bin/mtm actions         # every action and its effective keys
bin/mtm help
```

Unknown first words run `mtm-<word>` from PATH (unique prefix allowed: `mtm dep` → `mtm-deploy`).

## Keys (normal mode)

| keys | action |
|---|---|
| `n t` / `n a` / `n v` | quick new terminal / agent / vim |
| `N t` / `N a` / `N v` | same via editable form |
| `i` | live mode on selected pane (leader `ctrl+a`: `d` detach, `n`/`p` next/prev, `?` help, `ctrl+a` literal) |
| `x` | close (confirm) · `p` pin · `s` star · `a` save context |
| `/` search · `z` cycle grouping · `Z` customize grouping · `C` clear |
| `:` / `ctrl+p` | palette · `?` help · `J` jump · `C`ontext · `M` meta-mode |
| `F4` | New note (global, every mode) · `ctrl+s` saves · `ctrl+o` in path fields opens the fd picker |
| `1`–`6` `[` `]` | pages: Panes, Sessions, Hosts, Projects, Context, Settings |

## Layout

See `docs/architecture.md` (module boundaries, thin orchestrator in `internal/app`) and
`docs/tuios-protocol-notes.md` (verified daemon protocol). Build/test on vps2:
`scripts/remote.sh <ws> [secs] -- 'go build ./... && go test ./...'`.
Acceptance: `scripts/acceptance.sh` (throwaway servers `cmt-<8hex>`), report in `docs/acceptance-report.md`.
