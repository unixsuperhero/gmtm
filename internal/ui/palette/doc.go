// Package palette is the command palette over *actions.Registry.
//
//	p := palette.New(reg, palette.Options{TargetID: win.Key(), TargetLabel: "api (agent)", Page: "panes"})
//	p, cmd = p.Update(msg)
//	// cmd yields ExecuteMsg{ActionID, TargetID} or CloseMsg; the orchestrator
//	// runs the action and closes the palette.
//
// Structure: nested single-column levels with a breadcrumb ("Palette › New").
// The root lists, in registry order, the selected target (object-first route
// to its NeedsTarget actions) and one entry per Verb (verb-first route to its
// objects; a verb with a single object-less action executes directly). Typing
// at the root also matches every action by its full label ("New → Terminal"),
// so both routes reach the same action id. Unbound actions are included and
// hinted "unbound"; key hints are right-aligned in terminal cells.
//
// Search and Escape: the palette opens searching. Typing filters the current
// level (fuzzy) and shows matched/total. Escape with typed text leaves search
// and preserves it; the next Escape closes. Escape with an empty query closes
// immediately. Back (h/left/backspace outside search, or backspace on an empty
// query) returns to the parent level restoring its query, search state and
// highlight. Outside search j/k/g/G/d/u/ctrl+f/ctrl+b navigate, "/" or "i"
// re-enters search, enter/l/right select. In search, up/down/ctrl+n/ctrl+p
// move the highlight and enter selects.
package palette
