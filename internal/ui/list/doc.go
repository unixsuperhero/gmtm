// Package list is the generic inventory list used by every mtm page.
//
// Items implement Item (stable ID, Fields map, Label, SearchText). A Model
// owns its own query, filters, sort, grouping configs, scope, selection and
// scroll, so each page/subsection keeps its state independently.
//
//	m := list.New([]list.FieldDef{{Name: "kind", Filterable: true, Groupable: true, Sortable: true}})
//	m.SetSize(80, 20)
//	m = m.SetItems(items)                       // keeps selection by ID
//	m = m.SetQuery("api")                       // fuzzy (sahilm/fuzzy)
//	m = m.SetFilter("kind", "agent")            // per-field value sets
//	m = m.SetSort(list.SortSpec{Field: "lastActivity", Desc: true})
//	m = m.SetGroupConfigs([]list.GroupConfig{{Name: "none"}, {Name: "by project", Fields: []string{"project", "session"}}})
//	m, cmd = m.Update(keyMsg)
//
// Pipeline (Visible): field filters → fuzzy search → sort (or fuzzy score when
// searching without a sort) → grouping into composite group keys
// ("website / main") → group scope. Matched/total counts are shown.
//
// Filterable/groupable fields come from FieldDefs plus any field observed on
// items that has no def (extensible sets). FilterValues unions def values and
// observed values. Filtering never removes items from the model.
//
// Keys (normal focus; see KeyMap): j/k/arrows move, d/u half page,
// ctrl+f/ctrl+b/pgdn/pgup page, g/G ends, {/} previous/next group, "/" search
// (motion letters become text while searching; Enter/Esc leave search keeping
// the query, ctrl+u clears it), z cycles grouping configs, Z emits
// CustomizeGroupsMsg (open GroupConfigModal), C clears query, filters, sort
// and scope.
//
// View renders a one-line status (query + counts, filters, sort, group, scope,
// clear hint), compact rows with text markers (">" selected, "^" pinned,
// "*" starred), an optional detail line under the selected row only
// (Model.Detail), and a position/overflow footer. Rows are truncated in
// terminal cells; scrolling always keeps the selection visible.
//
// GroupConfigModal picks grouping fields, their order and the included groups,
// emitting GroupConfigChosenMsg (apply with Model.ApplyGroupConfig) or
// GroupConfigCancelledMsg.
package list
