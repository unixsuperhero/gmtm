// Package help renders the generated help view.
//
// Help is built only from registry.Effective() — never a hard-coded list —
// plus a Context (current mode, page and live leader):
//
//	h := help.New(reg, help.Context{Mode: "normal", Page: "panes", Leader: "ctrl+a"})
//	h = h.SetSize(w, ht)
//	h, cmd = h.Update(msg) // CloseMsg on esc/q/? outside search
//
// Entries returns the pure, ordered entry list: section "Global" (actions in
// actions.ModeGlobal, e.g. F4 New note), "<Mode> mode" for the current mode
// (filtered to the current page), "Live mode (leader …)" when a leader is set
// and the current mode is not live, then "Other: <mode>" sections for the rest.
// Unbound actions are listed as "unbound"; remapped keys are marked "*" and
// the description notes "(remapped)".
//
// "/" searches (fuzzy over label, id, keys and description) and shows
// matched/total; motion letters are text while searching; Enter/Esc leave
// search keeping the query. Outside search j/k/d/u/ctrl+f/ctrl+b/g/G and
// arrows scroll. Descriptions wrap to the width in terminal cells.
package help
