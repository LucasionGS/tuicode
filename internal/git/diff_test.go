package git

import "testing"

func TestLineChanges(t *testing.T) {
	a := []string{"a", "b", "c", "d"}
	b := []string{"a", "B", "c", "x", "d", "e"}
	ch := LineChanges(a, b)
	if ch[1] != ChangeModified || ch[3] != ChangeAdded || ch[5] != ChangeAdded || len(ch) != 3 {
		t.Fatalf("%v", ch)
	}
	ch = LineChanges([]string{"a", "b", "c"}, []string{"a", "c"})
	if ch[1] != ChangeDeleted || len(ch) != 1 {
		t.Fatalf("%v", ch)
	}
}
