package list

import (
	"sort"
	"strconv"
	"strings"
)

// Item is one row of an inventory list.
type Item interface {
	ID() string
	Fields() map[string]string
	Label() string
	SearchText() string
}

// FieldDef declares a field's capabilities and known values.
type FieldDef struct {
	Name       string
	Label      string
	Filterable bool
	Sortable   bool
	Groupable  bool
	Values     []string // known values; observed values are added
}

// SortSpec is the active sort. Field "" means natural order.
type SortSpec struct {
	Field string
	Desc  bool
}

// GroupConfig is one grouping configuration cycled with z.
type GroupConfig struct {
	Name   string
	Fields []string // ordered grouping fields; empty = no grouping
	Scope  []string // included group keys; empty = all groups
}

// NoGroupKey labels items whose grouping field is empty.
const NoGroupKey = "(none)"

// GroupSep joins multi-field group keys.
const GroupSep = " / "

// GroupKey returns the composite group key of it for fields.
func GroupKey(it Item, fields []string) string {
	f := it.Fields()
	parts := make([]string, len(fields))
	for i, name := range fields {
		v := f[name]
		if v == "" {
			v = NoGroupKey
		}
		parts[i] = v
	}
	return strings.Join(parts, GroupSep)
}

func compareValues(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}
	fa, ea := strconv.ParseFloat(a, 64)
	fb, eb := strconv.ParseFloat(b, 64)
	if ea == nil && eb == nil {
		switch {
		case fa < fb:
			return -1
		case fa > fb:
			return 1
		}
		return 0
	}
	la, lb := strings.ToLower(a), strings.ToLower(b)
	switch {
	case la < lb:
		return -1
	case la > lb:
		return 1
	case a < b:
		return -1
	}
	return 1
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return compareGroups(out[i], out[j]) })
	return out
}

func compareGroups(a, b string) bool {
	an, bn := strings.Contains(a, NoGroupKey), strings.Contains(b, NoGroupKey)
	if an != bn {
		return bn
	}
	return compareValues(a, b) < 0
}
