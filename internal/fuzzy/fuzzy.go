package fuzzy

import (
	"unicode"
)

// Match scores how well query matches target as a fuzzy subsequence.
// It returns ok=false when not all query characters appear in order.
// The returned positions are rune indices of matched characters in target.
func Match(query, target string) (score int, positions []int, ok bool) {
	if query == "" {
		return 0, nil, true
	}
	q := []rune(query)
	t := []rune(target)
	if len(q) > len(t) {
		return 0, nil, false
	}
	ql := make([]rune, len(q))
	for i, r := range q {
		ql[i] = unicode.ToLower(r)
	}
	tl := make([]rune, len(t))
	for i, r := range t {
		tl[i] = unicode.ToLower(r)
	}
	// quick subsequence check
	j := 0
	for i := 0; i < len(tl) && j < len(ql); i++ {
		if tl[i] == ql[j] {
			j++
		}
	}
	if j < len(ql) {
		return 0, nil, false
	}

	// dynamic programming over (query index, target index) for best score
	n, m := len(ql), len(tl)
	const neg = -1 << 30
	// best[i][k]: best score matching q[:i+1] with q[i] at t[k]
	best := make([][]int, n)
	from := make([][]int, n)
	for i := range best {
		best[i] = make([]int, m)
		from[i] = make([]int, m)
		for k := range best[i] {
			best[i][k] = neg
			from[i][k] = -1
		}
	}
	bonus := func(k int) int {
		if k == 0 {
			return 8
		}
		prev := t[k-1]
		cur := t[k]
		switch {
		case prev == '/' || prev == '\\':
			return 7
		case prev == '_' || prev == '-' || prev == '.' || prev == ' ' || prev == ':':
			return 6
		case unicode.IsLower(prev) && unicode.IsUpper(cur):
			return 6
		case !unicode.IsLetter(prev) && unicode.IsLetter(cur):
			return 4
		}
		return 0
	}
	for k := 0; k < m; k++ {
		if tl[k] == ql[0] {
			s := 1 + bonus(k)
			if t[k] == q[0] {
				s++
			}
			if k > 0 {
				s -= min(k, 5) / 2
			}
			best[0][k] = s
		}
	}
	for i := 1; i < n; i++ {
		bestPrev := neg
		bestPrevK := -1
		for k := 1; k < m; k++ {
			if best[i-1][k-1] > bestPrev {
				bestPrev = best[i-1][k-1]
				bestPrevK = k - 1
			}
			if tl[k] != ql[i] {
				continue
			}
			s := neg
			fk := -1
			// consecutive match
			if best[i-1][k-1] > neg {
				s = best[i-1][k-1] + 1 + 5 + bonus(k)
				fk = k - 1
			}
			if bestPrev > neg {
				gap := k - bestPrevK - 1
				alt := bestPrev + 1 + bonus(k) - min(gap, 3)
				if alt > s {
					s = alt
					fk = bestPrevK
				}
			}
			if s > neg {
				if t[k] == q[i] {
					s++
				}
				best[i][k] = s
				from[i][k] = fk
			}
		}
	}
	end := -1
	score = neg
	for k := 0; k < m; k++ {
		if best[n-1][k] > score {
			score = best[n-1][k]
			end = k
		}
	}
	if end < 0 {
		return 0, nil, false
	}
	positions = make([]int, n)
	k := end
	for i := n - 1; i >= 0; i-- {
		positions[i] = k
		k = from[i][k]
	}
	// prefer shorter targets
	score -= len(t) / 16
	return score, positions, true
}

// MatchPath scores a query against a file path, preferring matches in the file name.
func MatchPath(query, path string) (int, []int, bool) {
	// split off the base name
	rs := []rune(path)
	base := 0
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i] == '/' || rs[i] == '\\' {
			base = i + 1
			break
		}
	}
	name := string(rs[base:])
	if s, pos, ok := Match(query, name); ok {
		for i := range pos {
			pos[i] += base
		}
		return s + 20, pos, true
	}
	return Match(query, path)
}
