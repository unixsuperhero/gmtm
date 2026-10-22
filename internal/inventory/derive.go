package inventory

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/tuios"
)

// ProjectForSession derives project ownership from a session name. The reserved
// sessions carry no project; every other session is owned by the project of that name.
func ProjectForSession(session string) string {
	switch session {
	case domain.SessionNoProject, domain.SessionMTMOnly, "":
		return ""
	}
	return session
}

// PlacementSession is the session a window of project belongs in ("" → no-project).
func PlacementSession(project string) string {
	if project == "" {
		return domain.SessionNoProject
	}
	return project
}

// MapState maps a daemon state string; unfamiliar values become StateUnknown
// (the caller keeps the raw value). Empty is unknown, not none.
func MapState(s string) domain.AgentState {
	switch domain.AgentState(s) {
	case domain.StateNone, domain.StateWorking, domain.StateNeedsInput, domain.StateIdle, domain.StateDone, domain.StateErrored, domain.StateUnknown:
		return domain.AgentState(s)
	}
	return domain.StateUnknown
}

var vimPrograms = map[string]bool{"vim": true, "nvim": true}

func program(cmdline string) string {
	f := strings.Fields(cmdline)
	if len(f) == 0 {
		return ""
	}
	return filepath.Base(f[0])
}

// DeriveKind: vim/nvim → vim; a harness or agent state other than none → agent; else terminal.
func DeriveKind(harness, rawState string, commands ...string) domain.Kind {
	for _, c := range commands {
		if vimPrograms[program(c)] {
			return domain.KindVim
		}
	}
	if harness != "" || (rawState != "" && rawState != string(domain.StateNone)) {
		return domain.KindAgent
	}
	return domain.KindTerminal
}

// DeriveFlags composes agent flags; none hides another.
func DeriveFlags(state domain.AgentState, a *tuios.AgentInfo) domain.Flags {
	f := domain.Flags{
		Working:    state == domain.StateWorking,
		NeedsInput: state == domain.StateNeedsInput,
		Completed:  state == domain.StateDone,
	}
	if a != nil {
		f.Blocked = a.BlockedBy != ""
		f.NeedsInput = f.NeedsInput || a.NeedsYou
		f.Unread = a.Unread
		f.Queued = a.Queued
		f.Subagents = a.Subagents
		f.Completed = f.Completed || a.FinishedUnread
	}
	return f
}

// unixTime interprets a daemon timestamp of unknown unit (s, ms, µs or ns).
func unixTime(v int64) time.Time {
	switch {
	case v <= 0:
		return time.Time{}
	case v > 1e17:
		return time.Unix(0, v)
	case v > 1e14:
		return time.UnixMicro(v)
	case v > 1e11:
		return time.UnixMilli(v)
	}
	return time.Unix(v, 0)
}

// buildWindow turns daemon rows into one inventory row.
func buildWindow(server, session string, w tuios.WindowInfo, a *tuios.AgentInfo) domain.Window {
	host := w.Host
	if host == "" {
		host = domain.HostLocal
	}
	rawState := w.AgentState
	harness := ""
	cmds := []string{w.RunningCmdline}
	if a != nil {
		if a.State != "" {
			rawState = a.State
		}
		harness = a.HarnessID
		cmds = append(cmds, a.Foreground)
	}
	if c, ok := w.Raw["command"].(string); ok {
		cmds = append(cmds, c)
	}
	state := MapState(rawState)
	name := w.CustomName
	if name == "" {
		name = w.DisplayName
	}
	raw := make(map[string]any, len(w.Raw)+1)
	for k, v := range w.Raw {
		raw[k] = v
	}
	var last time.Time
	if a != nil {
		raw["agent"] = a.Raw
		last = unixTime(a.AgentStateAt)
	}
	return domain.Window{
		Target:       domain.Target{Server: server, Host: host, Session: session, WindowID: w.WindowID},
		Name:         name,
		Title:        w.Title,
		Kind:         DeriveKind(harness, rawState, cmds...),
		Harness:      harness,
		State:        state,
		RawState:     rawState,
		Flags:        DeriveFlags(state, a),
		CWD:          w.CWD,
		PID:          w.PID,
		Workspace:    w.Workspace,
		LastActivity: last,
		Project:      ProjectForSession(session),
		Raw:          raw,
	}
}
