# TUIOS protocol notes (verified 2026-10-22 against `tuios version dev`)

Verified by probing a throwaway named server `cmtm-probe` locally.

## Named servers

- Start: `tuios --server <name> start-server` (restores saved sessions, returns without attaching, exit 0).
- Stop: `tuios --server <name> kill-server` (SIGTERM to the daemon; only that server).
- Name rules: 1..14 chars of `[A-Za-z0-9_-]`. `clean-mtm` is 9 chars; `clean-mtm-test-<id>` MUST stay ≤14 → use e.g. `cmt-<8hex>` for test servers (the spec's literal prefix does not fit; document this).
- Socket path: `<socketbase>/servers/<name>/tuios.sock` where `<socketbase>` is `$XDG_RUNTIME_DIR/tuios` if XDG_RUNTIME_DIR is set, else `/run/user/<uid>/tuios` if that dir exists (Linux), else `/tmp/tuios-<uid>` (macOS). Verified macOS: `/tmp/tuios-501/servers/cmtm-probe/tuios.sock`.
- Env to scrub before exec'ing `tuios` or any child: `TUIOS_SOCKET`, `TUIOS_SESSION`, `TUIOS_PANE_ID`, `TUIOS_WINDOW_ID`, `TUIOS_PANE_TOKEN`, `TUIOS_PANE_GRANTS`. Set `TUIOS_SERVER=<name>` explicitly. `TUIOS_SOCKET` names the pane's daemon; it must never be honored by mtm.
- State dir of a named server: `<XDG_STATE_HOME>/tuios-servers/<name>/tuios/...` (macOS `~/.local/state/tuios-servers/<name>`).

## Wire protocol (unix socket, newline-delimited JSON)

Request: `{"id":1,"verb":"list-windows","params":{"session":"work"}}`
Reply:   `{"id":1,"result":{...}}` or `{"id":2,"error":{"code":"session_not_found","message":"...","hint":{...}}}`
One JSON line per reply. Multiple requests may be pipelined on one connection. `subscribe` and `stream-pane` hold the connection; use a dedicated connection for each.

Verified replies:
- hello → `{"daemon_version","instance","protocol":1,"min_protocol":1,"os","pid","sessions","type":"hello"}`
- list-sessions → `{"sessions":[...],"type":"session_list"}`
- new-session {name,window:false} → `{"session","session_id","width","height","windows":0,"type":"session_created"}`
- new-window {session,name,focus:false,cwd?,command?[],host?} → `{"window_id","pty_id","name","workspace","focused","host","unplaced","type":"window_created"}`
- list-windows {session} → `{"windows":[{"window_id","pty_id","custom_name","display_name","title","cwd","agent_state","focused","width","height","pid","tty","host"?,"workspace","minimized",...}],"focused_window_id","shell","total","type":"window_list"}`

Key verbs (see `tuios list-verbs <verb>` for full params): list-sessions, session-info, new-session, rename-session, kill-session, list-windows, get-window, new-window, close-window, move-window{session,window,to_session|workspace}, focus-window, resize{session,window,width,height}, send-keys{keys,literal}, send-text{text,paste,submit}, capture-pane{source:visible|recent,styled,lines}, start-agent{session,agent,name,cwd,prompt,env,ready_timeout}, list-agents{session|all_sessions,all}, get-agent-state, agent-activity, agent-transcript (needs human_nonce from attach-presence), list-hosts, list-host-sessions{host}, subscribe{types[],after_seq,boot_id,hosts}, stream-pane{session,window,lease_cols,lease_rows,from_seq,boot_id}, wait-for, list-queued, queue-prompt, peek-prompt.

`start-agent.env` is an override map on top of the daemon's environment, not a
replacement environment; TUIOS accepts at most 64 entries. mtm forwards only
`PATH` (so executable lookup matches the launcher) and `TUIOS_SERVER` (the selected
server). Other variables, including credentials, are inherited from the daemon,
not copied from the launching mtm process.

## stream-pane frames
After the JSON reply line the connection carries binary frames: 1 type byte + u32 BE length + payload.
From daemon: `S` snapshot (u64 seq, u16 cols, u16 rows, bytes), `O` output (u64 seq, bytes), `R` resize (u64 seq, u16 cols, u16 rows), `E` error (JSON), `X` exit (reason text).
From client: `I` input (≤64KiB), `L` lease (u16 cols, u16 rows; 0 0 releases; resend every 10s or it lapses after 30s).
The stream never resizes the pane; only a lease holds it at most at that size. Preview must NOT send `L`.

## Events (subscribe)
Types: window-created, window-closed, window-exit, window-retitled, window-focused, window-moved, agent-state, agent-message, attention, session-created, session-closed, prompt, command-started, command-finished, gap, host-changed, ... Each event has `seq`, `boot_id` (on subscribed), `type`, `session`, `window`, `time`.

## Error codes
Stable `code` strings; see `tuios list-verbs` tail. Not timeouts; retrying unchanged fails the same way (except busy / too_many_connections).
