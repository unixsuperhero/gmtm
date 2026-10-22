package list

import (
	"sort"

	"github.com/sahilm/fuzzy"
)

// Row is one display row: a group header or an item.
type Row struct {
	Header  bool
	Group   string
	Count   int  // items in group (headers)
	Item    Item // nil for headers
	Index   int  // visible item index (items)
	Matches []int
}

// Result is the computed view of the model's items.
type Result struct {
	Items   []Item // visible items in display order
	Rows    []Row
	Groups  []string // visible group keys in order
	GroupOf []int    // visible item index → index into Groups (-1 when ungrouped)
	Matched int
	Total   int
	AllKeys []string // every group key before scoping (for the modal)
}

type itemSource []Item

func (s itemSource) String(i int) string { return s[i].SearchText() }
func (s itemSource) Len() int            { return len(s) }

// Compute runs filter → search → sort → group → scope over items.
func Compute(items []Item, query string, filters map[string]map[string]bool, srt SortSpec, grp GroupConfig) Result {
	res := Result{Total: len(items)}
	// filters
	filtered := make([]Item, 0, len(items))
	for _, it := range items {
		if passFilters(it, filters) {
			filtered = append(filtered, it)
		}
	}
	// search
	if query != "" {
		ms := fuzzy.FindFrom(query, itemSource(filtered))
		out := make([]Item, len(ms))
		for i, m := range ms {
			out[i] = filtered[m.Index]
		}
		filtered = out
	}
	// sort
	if srt.Field != "" {
		sort.SliceStable(filtered, func(i, j int) bool {
			c := compareValues(filtered[i].Fields()[srt.Field], filtered[j].Fields()[srt.Field])
			if srt.Desc {
				a, b := filtered[i].Fields()[srt.Field], filtered[j].Fields()[srt.Field]
				if a == "" || b == "" { // empties stay last
					return c < 0
				}
				return c > 0
			}
			return c < 0
		})
	}
	matchesFor := func(it Item) []int {
		if query == "" {
			return nil
		}
		if m := fuzzy.Find(query, []string{it.Label()}); len(m) > 0 {
			return m[0].MatchedIndexes
		}
		return nil
	}
	// group
	if len(grp.Fields) == 0 {
		res.Items = filtered
		res.GroupOf = make([]int, len(filtered))
		for i, it := range filtered {
			res.GroupOf[i] = -1
			res.Rows = append(res.Rows, Row{Item: it, Index: i, Matches: matchesFor(it)})
		}
		res.Matched = len(filtered)
		return res
	}
	buckets := map[string][]Item{}
	seen := map[string]bool{}
	for _, it := range filtered {
		k := GroupKey(it, grp.Fields)
		buckets[k] = append(buckets[k], it)
		seen[k] = true
	}
	res.AllKeys = sortedKeys(seen)
	scope := map[string]bool{}
	for _, s := range grp.Scope {
		scope[s] = true
	}
	for _, k := range res.AllKeys {
		if len(scope) > 0 && !scope[k] {
			continue
		}
		gi := len(res.Groups)
		res.Groups = append(res.Groups, k)
		res.Rows = append(res.Rows, Row{Header: true, Group: k, Count: len(buckets[k])})
		for _, it := range buckets[k] {
			idx := len(res.Items)
			res.Items = append(res.Items, it)
			res.GroupOf = append(res.GroupOf, gi)
			res.Rows = append(res.Rows, Row{Item: it, Index: idx, Group: k, Matches: matchesFor(it)})
		}
	}
	res.Matched = len(res.Items)
	return res
}

func passFilters(it Item, filters map[string]map[string]bool) bool {
	if len(filters) == 0 {
		return true
	}
	f := it.Fields()
	for field, vals := range filters {
		if len(vals) == 0 {
			continue
		}
		if !vals[f[field]] {
			return false
		}
	}
	return true
}
