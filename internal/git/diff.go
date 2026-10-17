package git

// LineChange kinds for gutter decorations.
const (
	ChangeAdded = iota + 1
	ChangeModified
	ChangeDeleted // deletion happened just above this line
)

// Op is one step of an edit script.
type Op struct {
	Kind  byte // '=', '-', '+'
	ALine int  // index in a (for '=' and '-')
	BLine int  // index in b (for '=' and '+')
}

// DiffLines computes a line-level edit script from a to b using the Myers algorithm.
func DiffLines(a, b []string) []Op {
	// trim common prefix and suffix to keep the core small
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	core := myers(a[pre:len(a)-suf], b[pre:len(b)-suf])
	ops := make([]Op, 0, pre+len(core)+suf)
	for i := 0; i < pre; i++ {
		ops = append(ops, Op{'=', i, i})
	}
	for _, o := range core {
		o.ALine += pre
		o.BLine += pre
		ops = append(ops, o)
	}
	for i := 0; i < suf; i++ {
		ops = append(ops, Op{'=', len(a) - suf + i, len(b) - suf + i})
	}
	return ops
}

func myers(a, b []string) []Op {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil
	}
	if n == 0 {
		ops := make([]Op, m)
		for i := range ops {
			ops[i] = Op{'+', 0, i}
		}
		return ops
	}
	if m == 0 {
		ops := make([]Op, n)
		for i := range ops {
			ops[i] = Op{'-', i, 0}
		}
		return ops
	}
	if n*m > 25_000_000 {
		// too large for a full diff: treat as replace
		ops := []Op{}
		for i := 0; i < n; i++ {
			ops = append(ops, Op{'-', i, 0})
		}
		for i := 0; i < m; i++ {
			ops = append(ops, Op{'+', 0, i})
		}
		return ops
	}
	max := n + m
	off := max
	v := make([]int, 2*max+2)
	var trace [][]int
	for d := 0; d <= max; d++ {
		vc := make([]int, len(v))
		copy(vc, v)
		trace = append(trace, vc)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(trace, a, b, off)
			}
		}
	}
	return nil
}

func backtrack(trace [][]int, a, b []string, off int) []Op {
	x, y := len(a), len(b)
	var ops []Op
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var pk int
		if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := v[off+pk]
		py := px - pk
		for x > px && y > py {
			x--
			y--
			ops = append(ops, Op{'=', x, y})
		}
		if d > 0 {
			if x == px {
				y--
				ops = append(ops, Op{'+', x, y})
			} else {
				x--
				ops = append(ops, Op{'-', x, y})
			}
		}
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}

// Hunk is a contiguous change between two texts.
type Hunk struct {
	AStart, ALen int // removed lines in a
	BStart, BLen int // added lines in b
}

// Hunks groups an edit script into hunks.
func Hunks(ops []Op) []Hunk {
	var res []Hunk
	var cur *Hunk
	aPos, bPos := 0, 0
	for _, o := range ops {
		switch o.Kind {
		case '=':
			if cur != nil {
				res = append(res, *cur)
				cur = nil
			}
			aPos, bPos = o.ALine+1, o.BLine+1
		case '-':
			if cur == nil {
				cur = &Hunk{AStart: aPos, BStart: bPos}
			}
			cur.ALen++
			aPos = o.ALine + 1
		case '+':
			if cur == nil {
				cur = &Hunk{AStart: aPos, BStart: bPos}
			}
			cur.BLen++
			bPos = o.BLine + 1
		}
	}
	if cur != nil {
		res = append(res, *cur)
	}
	return res
}

// LineChanges maps lines in b to gutter change kinds relative to a.
func LineChanges(a, b []string) map[int]int {
	res := map[int]int{}
	for _, h := range Hunks(DiffLines(a, b)) {
		switch {
		case h.ALen == 0:
			for i := 0; i < h.BLen; i++ {
				res[h.BStart+i] = ChangeAdded
			}
		case h.BLen == 0:
			line := h.BStart
			if line >= len(b) {
				line = len(b) - 1
			}
			if line < 0 {
				line = 0
			}
			if _, ok := res[line]; !ok {
				res[line] = ChangeDeleted
			}
		default:
			for i := 0; i < h.BLen; i++ {
				res[h.BStart+i] = ChangeModified
			}
		}
	}
	return res
}
