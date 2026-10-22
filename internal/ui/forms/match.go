package forms

import (
	"sort"
	"strings"

	"github.com/sahilm/fuzzy"
)

// Option is one selectable value. Parent links a child option to a parent
// field's option value (Linked fields).
type Option struct {
	Value  string
	Label  string
	Parent string
	Hint   string
}

// Display is the text shown for the option.
func (o Option) Display() string {
	if o.Label != "" {
		return o.Label
	}
	return o.Value
}

func (o Option) is(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	return t != "" && (strings.ToLower(o.Value) == t || strings.ToLower(o.Display()) == t)
}

// ExactMatch returns the option whose value or label equals text (case-insensitive).
func ExactMatch(text string, opts []Option) (Option, bool) {
	for _, o := range opts {
		if o.is(text) {
			return o, true
		}
	}
	return Option{}, false
}

// Rank orders options matching query: exact matches, then prefix matches,
// then weaker fuzzy matches by score. An empty query returns all options.
func Rank(query string, opts []Option) []Option {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return append([]Option(nil), opts...)
	}
	var out []Option
	used := make([]bool, len(opts))
	for i, o := range opts {
		if o.is(q) {
			out = append(out, o)
			used[i] = true
		}
	}
	for i, o := range opts {
		if !used[i] && (strings.HasPrefix(strings.ToLower(o.Display()), q) || strings.HasPrefix(strings.ToLower(o.Value), q)) {
			out = append(out, o)
			used[i] = true
		}
	}
	var rest []string
	var idx []int
	for i, o := range opts {
		if !used[i] {
			rest = append(rest, strings.ToLower(o.Display()))
			idx = append(idx, i)
		}
	}
	ms := fuzzy.Find(q, rest)
	sort.SliceStable(ms, func(a, b int) bool { return ms[a].Score > ms[b].Score })
	for _, m := range ms {
		out = append(out, opts[idx[m.Index]])
	}
	return out
}
