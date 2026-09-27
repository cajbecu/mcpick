package cli

import (
	"sort"
	"strings"
)

// A mistyped name — a command, a server for --select — is answered with
// the closest names mcpick knows, so the user reads the fix instead of
// listing the candidates by hand.

// closest returns up to n of the candidates nearest to name, nearest
// first: those within a small edit distance, or that name is a prefix of
// (or the other way round). Case does not count. Nothing is suggested when
// nothing is close.
func closest(name string, candidates []string, n int) []string {
	type match struct {
		name string
		d    int
	}
	lower := strings.ToLower(name)
	var out []match
	seen := map[string]bool{}
	for _, c := range candidates {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		lc := strings.ToLower(c)
		d := editDistance(lower, lc)
		near := d <= 2 || d*3 <= len(lower)
		if !near && lower != "" && (strings.HasPrefix(lc, lower) || strings.HasPrefix(lower, lc)) {
			d, near = 3, true
		}
		if near {
			out = append(out, match{c, d})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].d < out[j].d })
	if len(out) > n {
		out = out[:n]
	}
	names := make([]string, len(out))
	for i, m := range out {
		names[i] = m.name
	}
	return names
}

// editDistance is the Levenshtein distance between a and b, in runes.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// didYouMean is the parenthesis after an error naming what was not found:
// "(did you mean github?)", "(did you mean github, gitlab?)", or "" when
// nothing is close.
func didYouMean(name string, candidates []string, n int) string {
	near := closest(name, candidates, n)
	if len(near) == 0 {
		return ""
	}
	return " (did you mean " + strings.Join(near, ", ") + "?)"
}
