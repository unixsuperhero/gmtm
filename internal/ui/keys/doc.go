// Package keys holds mtm's mode/focus key-routing primitives.
//
// Mode enumerates the interaction modes (normal, insert, live, jump, context,
// palette, help, note); Mode.String matches the actions.Mode* constants.
//
// Router classifies one tea.KeyMsg in a mode:
//
//	r := keys.NewRouter()                 // F4 → "note.new" reserved globally
//	r.AddSequences("n t", "N a")          // multi-key bindings
//	res := r.Route(keys.Normal, msg)
//	switch res.Kind {
//	case keys.Global:   // res.Action, handled before any mode routing
//	case keys.Navigate: // res.Nav (Down, HalfUp, Top, NextSection, …)
//	case keys.Pending:  // first key of a sequence; schedule r.TimeoutCmd()
//	case keys.Sequence: // res.Key == "n t"
//	case keys.Cancelled:// pending sequence dropped (Escape, timeout)
//	case keys.Pass:     // res.Key: hand to the focused widget / registry
//	}
//
// Rules:
//   - Global keys win in every mode, including insert and live.
//   - Navigation and sequences apply only in normal, jump, context and help.
//     Insert, note, palette and live never treat motion letters as navigation;
//     every non-global key passes through as text/input.
//   - A sequence prefix takes precedence over navigation for the same key.
//   - A pending sequence expires after Timeout; a non-matching key cancels it
//     and is then routed fresh.
//
// NavFor maps a single key string to a Nav for widgets that route their own
// keys (list, help).
package keys
