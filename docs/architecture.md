# mtm architecture (modular, thin orchestration)

Spec: `~/hub/prompts/clean-mtm.md`. UI identity: **cmtm**. Binary: `mtm`. Server: `clean-mtm`.

## Packages (each independently buildable and testable)

| package | owns | must not |
|---|---|---|
| `internal/domain` | pure types: Target identity, Window/Session/Host snapshots, Project, ContextItem, Note, ConnState, enums | import anything but stdlib |
| `internal/tuios` | socket client for a *named* server: path resolution, JSON-line RPC, subscribe stream, stream-pane frames, start/kill server via `tuios --server`, env scrubbing | know about UI or SQL |
| `internal/inventory` | one shared snapshot built from daemon reads + events; project ownership derived from session name; reserved sessions `no-project`, `mtm-only`; stale/disconnected retention | draw anything |
| `internal/store` | SQLite (modernc) with versioned migrations: projects, context items, notes, pins, stars, settings, field values; transactional | know about TUIOS |
| `internal/actions` | action registry: ids, verbs, default bindings, remaps, conflict detection; palette/help/CLI all read it | execute UI code directly |
| `internal/preview` | read-only capture → rendered cells via `x/vt`; sanitises OSC/clipboard/title; cached/stale labelling; generation guard | send input or leases |
| `internal/live` | live stream session: stream-pane with lease, leader key state machine, detach | touch inventory |
| `internal/ui/*` | Bubble Tea widgets: frame (cmtm border + mode + project), list (search/filter/sort/group/scope), forms, palette, help, modal stack, jump/context modal, note modal, json explorer | hold domain rules |
| `internal/app` | the thin orchestrator: top-level `tea.Model` that routes messages to modules, composes pages, holds mode/focus/modal stack. Pages register themselves. | contain SQL, protocol details, widget drawing |
| `internal/cli` | arg parsing, human help, external `mtm-<sub>` dispatch, `mtm json` entry | import `internal/app` internals beyond its Run entry |
| `cmd/mtm` | `main()` | logic |

## Shared seams
- `domain.Target{Server, Host, Session, WindowID}` is the only way to address a runtime object.
- `tuios.Client` interface (internal/tuios/client.go). One instance, created once in `internal/app` from the selected server name. No package constructs its own.
- `store.Store` interface (internal/store/store.go).
- `actions.Registry` (internal/actions/registry.go).
- UI modules expose `tea.Model`-style components plus typed messages; the orchestrator only forwards.

## Modes
normal | insert | live | jump | context | palette | help | note. Key routing: F4 (New note) is reserved before any mode routing. Live leader default Ctrl+a.

## Build/verify
`scripts/remote.sh <ws> [secs] -- go build ./... && go test ./...` runs on vps2 in an isolated workspace. Acceptance tests use throwaway servers named `cmt-<8hex>` (14-char limit; see docs/tuios-protocol-notes.md).
