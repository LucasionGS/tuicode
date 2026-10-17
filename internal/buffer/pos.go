package buffer

// Pos is a position in a buffer. Col is measured in runes.
type Pos struct {
	Line int
	Col  int
}

// Cmp compares two positions and returns -1, 0 or 1.
func (p Pos) Cmp(q Pos) int {
	if p.Line < q.Line {
		return -1
	}
	if p.Line > q.Line {
		return 1
	}
	if p.Col < q.Col {
		return -1
	}
	if p.Col > q.Col {
		return 1
	}
	return 0
}

// Less reports whether p comes before q.
func (p Pos) Less(q Pos) bool { return p.Cmp(q) < 0 }

// Range is a span between two positions. Start is not necessarily before End;
// use Normalized when ordering matters.
type Range struct {
	Start Pos
	End   Pos
}

// Normalized returns the range with Start <= End.
func (r Range) Normalized() Range {
	if r.End.Less(r.Start) {
		return Range{r.End, r.Start}
	}
	return r
}

// Empty reports whether the range covers no text.
func (r Range) Empty() bool { return r.Start == r.End }

// Contains reports whether p lies inside the (normalized) range, inclusive of both ends.
func (r Range) Contains(p Pos) bool {
	n := r.Normalized()
	return !p.Less(n.Start) && !n.End.Less(p)
}

// Selection is a cursor with an anchor. When Anchor == Active it is a plain caret.
type Selection struct {
	Anchor Pos
	Active Pos
}

// Range returns the normalized selected range.
func (s Selection) Range() Range { return Range{s.Anchor, s.Active}.Normalized() }

// Empty reports whether nothing is selected.
func (s Selection) Empty() bool { return s.Anchor == s.Active }

// Reversed reports whether the active end is before the anchor.
func (s Selection) Reversed() bool { return s.Active.Less(s.Anchor) }

// Caret creates an empty selection at p.
func Caret(p Pos) Selection { return Selection{p, p} }

// TransformPos maps a position through an edit that replaced old with a range
// ending at newEnd. stickRight decides where positions inside the replaced range go.
func TransformPos(p Pos, old Range, newEnd Pos, stickRight bool) Pos {
	if p.Less(old.Start) {
		return p
	}
	if p.Less(old.End) || (p == old.End && p == old.Start && !stickRight) {
		if p == old.Start && !stickRight {
			return p
		}
		return newEnd
	}
	// p >= old.End
	if p.Line == old.End.Line {
		return Pos{newEnd.Line, newEnd.Col + (p.Col - old.End.Col)}
	}
	return Pos{p.Line + (newEnd.Line - old.End.Line), p.Col}
}
