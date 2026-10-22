// Package frame draws the persistent cmtm application frame.
//
// Every view of mtm is wrapped in a rounded border whose top edge carries the
// identity "cmtm", the current mode (text, upper-cased) and the active project,
// and whose bottom edge carries the selected server name and its connection
// state. These indicators are never covered by content: Render crops content to
// the inner area reported by Inner, measuring in terminal cells (wide runes,
// combining marks and ANSI escapes are handled by x/ansi).
//
//	f := frame.Frame{Width: 80, Height: 24, Mode: "normal", Project: "", Server: "clean-mtm", Conn: "connected"}
//	w, h := f.Inner()          // 78, 22 — size pages and live panes with this
//	out := f.Render(pageView)  // full 80x24 screen
//
// Project text: "" renders as "unknown" (undeterminable); callers pass
// domain.SessionNoProject ("no-project") when there is explicitly no project.
// The indicator is never blank.
//
// When the terminal cannot show the border plus identity, mode and project,
// Render returns an explicit too-small message instead of an unframed view
// (see TooSmall).
//
// Overlay and OverlayAt place a modal string over a base string inside a given
// inner area, so modals stay inside the frame; render the result through
// Frame.Render.
//
// Helpers Width, Truncate, Pad and FitLines are cell-measured string utilities
// shared by the other ui packages.
package frame
