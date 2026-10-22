package actions

import (
	"fmt"
	"strings"
)

// Page ids used by page-scoped actions.
const (
	PagePanes    = "panes"
	PageSessions = "sessions"
	PageHosts    = "hosts"
	PageProjects = "projects"
	PageContext  = "context"
	PageSettings = "settings"
)

// Defs is every mtm action. Keys are tea.KeyMsg strings; multi-key
// sequences are space separated ("n t"). Live leader actions are listed for
// help/discovery; the live session interprets them after the leader.
func Defs() []Action {
	n := []string{ModeNormal}
	g := []string{ModeGlobal}
	l := []string{ModeLive}
	return []Action{
		{ID: "new.terminal", Verb: "New", Object: "Terminal", Description: "Quick-create a terminal (project from the focused row; else no-project)", Modes: n, Keys: []string{"n t"}},
		{ID: "new.agent", Verb: "New", Object: "Agent", Description: "Quick-create an agent with the first usable harness", Modes: n, Keys: []string{"n a"}},
		{ID: "new.vim", Verb: "New", Object: "Vim", Description: "Quick-create a Vim/Neovim window", Modes: n, Keys: []string{"n v"}},
		{ID: "form.terminal", Verb: "New", Object: "Terminal (form)", Description: "Create a terminal with an editable form", Modes: n, Keys: []string{"N t"}},
		{ID: "form.agent", Verb: "New", Object: "Agent (form)", Description: "Create an agent: harness, model, effort, prompt, send or queue", Modes: n, Keys: []string{"N a"}},
		{ID: "form.vim", Verb: "New", Object: "Vim (form)", Description: "Create a Vim window with an editable form", Modes: n, Keys: []string{"N v"}},
		{ID: "open.live", Verb: "Open", Object: "Live", Description: "Enter live mode on the selected window (input goes to it)", Modes: n, Keys: []string{"i"}, Page: PagePanes, NeedsTarget: true},
		{ID: "close.window", Verb: "Close", Object: "Window", Description: "Close the selected window (asks for confirmation)", Modes: n, Keys: []string{"x"}, Page: PagePanes, NeedsTarget: true},
		{ID: "pin", Verb: "Pin", Description: "Toggle pin on the selected item (presentation only)", Modes: n, Keys: []string{"p"}, NeedsTarget: true},
		{ID: "star", Verb: "Star", Description: "Toggle star on the selected item", Modes: n, Keys: []string{"s"}, NeedsTarget: true},
		{ID: "detail.next", Verb: "Next", Object: "Detail tab", Description: "Next detail tab (Screen/Activity/Transcript/Turns)", Modes: n, Keys: []string{"tab"}, Page: PagePanes},
		{ID: "detail.prev", Verb: "Previous", Object: "Detail tab", Description: "Previous detail tab", Modes: n, Keys: []string{"shift+tab"}, Page: PagePanes},
		{ID: "prompt.retry", Verb: "Retry", Object: "Prompt", Description: "Re-send the last failed prompt to its existing agent (no new launch)", Modes: n, Keys: []string{"R"}},
		{ID: "context.save", Verb: "Save", Object: "Context", Description: "Save a context item from the selected window (project prefilled)", Modes: n, Keys: []string{"a"}, Page: PagePanes, NeedsTarget: true},
		{ID: "group.cycle", Verb: "Cycle", Object: "Grouping", Description: "Cycle grouping configurations", Modes: n, Keys: []string{"z"}},
		{ID: "group.customize", Verb: "Customize", Object: "Grouping", Description: "Choose grouping fields, order and scope", Modes: n, Keys: []string{"Z"}},
		{ID: "search", Verb: "Search", Description: "Search the current list", Modes: n, Keys: []string{"/"}},
		{ID: "clear.filters", Verb: "Clear", Object: "Filters", Description: "Clear query, filters, sort and scope", Modes: n, Keys: []string{"ctrl+l"}},
		{ID: "palette", Verb: "Open", Object: "Palette", Description: "Command palette (verb-first and object-first)", Modes: n, Keys: []string{":", "ctrl+p"}},
		{ID: "help", Verb: "Open", Object: "Help", Description: "Help generated from effective bindings", Modes: n, Keys: []string{"?"}},
		{ID: "jump", Verb: "Open", Object: "Jump", Description: "Jump mode: outline of the selected agent/terminal with preview", Modes: n, Keys: []string{"J"}},
		{ID: "context", Verb: "Open", Object: "Context", Description: "Context mode: files, docs and saved project context", Modes: n, Keys: []string{"C"}},
		{ID: "metamode", Verb: "Open", Object: "Modes", Description: "Meta-mode selector listing Jump, Context and Project context", Modes: n, Keys: []string{"M"}},
		{ID: "note.new", Verb: "New", Object: "Note", Description: "New project note from anywhere (reserved before mode routing)", Modes: g, Keys: []string{"f4"}},
		{ID: "project.choose", Verb: "Choose", Object: "Active project", Description: "Set the active project context (or no-project)", Modes: n, Keys: []string{"P"}},
		{ID: "page.panes", Verb: "Go", Object: "Panes", Description: "Panes page", Modes: n, Keys: []string{"1"}},
		{ID: "page.sessions", Verb: "Go", Object: "Sessions", Description: "Sessions page", Modes: n, Keys: []string{"2"}},
		{ID: "page.hosts", Verb: "Go", Object: "Hosts", Description: "Hosts page", Modes: n, Keys: []string{"3"}},
		{ID: "page.projects", Verb: "Go", Object: "Projects", Description: "Projects page", Modes: n, Keys: []string{"4"}},
		{ID: "page.context", Verb: "Go", Object: "Context page", Description: "Saved context page", Modes: n, Keys: []string{"5"}},
		{ID: "page.settings", Verb: "Go", Object: "Settings", Description: "Settings page", Modes: n, Keys: []string{"6"}},
		{ID: "page.next", Verb: "Go", Object: "Next page", Description: "Next page", Modes: n, Keys: []string{"]"}},
		{ID: "page.prev", Verb: "Go", Object: "Previous page", Description: "Previous page", Modes: n, Keys: []string{"["}},
		{ID: "project.activate", Verb: "Activate", Object: "Project", Description: "Make the selected project the active project context", Modes: n, Keys: []string{"enter"}, Page: PageProjects, NeedsTarget: true},
		{ID: "context.open", Verb: "Open", Object: "Context item", Description: "Open the selected saved item read-only in the main area", Modes: n, Keys: []string{"enter"}, Page: PageContext, NeedsTarget: true},
		{ID: "context.reassociate", Verb: "Edit", Object: "Context project", Description: "Change the selected item's project association", Modes: n, Keys: []string{"e"}, Page: PageContext, NeedsTarget: true},
		{ID: "context.clear", Verb: "Clear", Object: "Context project", Description: "Remove the selected item's project association (no-project)", Modes: n, Keys: []string{"D"}, Page: PageContext, NeedsTarget: true},
		{ID: "setting.edit", Verb: "Edit", Object: "Setting", Description: "Edit the selected setting", Modes: n, Keys: []string{"enter"}, Page: PageSettings, NeedsTarget: true},
		{ID: "setting.reset", Verb: "Reset", Object: "Setting", Description: "Reset the selected setting to its default", Modes: n, Keys: []string{"r"}, Page: PageSettings, NeedsTarget: true},
		{ID: "capture.setup", Verb: "Set up", Object: "Capture", Description: "Install terminal command/output and agent prompt/response capture", Modes: n, Page: PageSettings},
		{ID: "capture.status", Verb: "Inspect", Object: "Capture setup", Description: "Inspect capture integration status and file paths", Modes: n, Page: PageSettings},
		{ID: "server.start", Verb: "Start", Object: "Server", Description: "Start the selected isolated TUIOS server, then resynchronize", Modes: n, Keys: []string{"S"}},
		{ID: "refresh", Verb: "Refresh", Description: "Re-read the inventory from the selected server", Modes: n, Keys: []string{"ctrl+r"}},
		{ID: "quit", Verb: "Quit", Description: "Quit mtm (managed windows and the server keep running)", Modes: n, Keys: []string{"q", "ctrl+c"}},
		{ID: "live.detach", Verb: "Detach", Object: "Live", Description: "Leave live mode (leader d)", Modes: l, Keys: []string{"ctrl+a d"}},
		{ID: "live.next", Verb: "Next", Object: "Live pane", Description: "Switch live to the next pane (leader n)", Modes: l, Keys: []string{"ctrl+a n"}},
		{ID: "live.prev", Verb: "Previous", Object: "Live pane", Description: "Switch live to the previous pane (leader p)", Modes: l, Keys: []string{"ctrl+a p"}},
		{ID: "live.zoom", Verb: "Zoom", Object: "Live pane", Description: "TUIOS zoom/fullscreen (leader z; reported unsupported when the API lacks it)", Modes: l, Keys: []string{"ctrl+a z"}},
		{ID: "live.help", Verb: "Help", Object: "Live", Description: "Leader help (leader ?)", Modes: l, Keys: []string{"ctrl+a ?"}},
		{ID: "live.literal", Verb: "Send", Object: "Leader literally", Description: "Doubled leader sends the leader key to the target", Modes: l, Keys: []string{"ctrl+a ctrl+a"}},
	}
}

// Register adds every action in Defs, rejecting key conflicts.
func Register(r *Registry) error {
	for _, a := range Defs() {
		if err := r.CheckConflicts(a); err != nil {
			return err
		}
		if err := r.Register(a); err != nil {
			return err
		}
	}
	return nil
}

// NewDefault returns a registry with Defs registered (panics on a conflict:
// the defaults are a program invariant).
func NewDefault() *Registry {
	r := New()
	if err := Register(r); err != nil {
		panic(err)
	}
	return r
}

// CheckConflicts reports a binding of a that collides with an existing action
// in a shared mode and overlapping page: equal keys, or one sequence being a
// prefix of the other (which would make the shorter unreachable).
func (r *Registry) CheckConflicts(a Action) error {
	modes := a.Modes
	if len(modes) == 0 {
		modes = []string{ModeNormal}
	}
	for _, o := range r.All() {
		if o.ID == a.ID {
			continue
		}
		if !sharesMode(modes, o.Modes) && !contains(modes, ModeGlobal) && !contains(o.Modes, ModeGlobal) {
			continue
		}
		if a.Page != "" && o.Page != "" && a.Page != o.Page {
			continue
		}
		for _, k := range a.Keys {
			for _, ok := range r.Keys(o.ID) {
				if k == ok || strings.HasPrefix(k, ok+" ") || strings.HasPrefix(ok, k+" ") {
					return fmt.Errorf("binding %q of %s conflicts with %q of %s", k, a.ID, ok, o.ID)
				}
			}
		}
	}
	return nil
}
