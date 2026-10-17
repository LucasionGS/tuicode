package theme

import (
	"testing"

	"tuicode/internal/syntax"
)

func TestLoadBuiltins(t *testing.T) {
	for _, inf := range Available() {
		th, err := LoadInfo(inf)
		if err != nil {
			t.Errorf("%s: %v", inf.Label, err)
			continue
		}
		if inf.Label == "Dark Modern" {
			for k := syntax.Kind(0); k < syntax.KCount; k++ {
				t.Logf("%v %+v", syntax.Scopes[k][0], th.Token(k).FG)
			}
			t.Logf("bg %+v sb %+v", th.RGBA("editor.background"), th.RGBA("statusBar.background"))
		}
	}
	t.Logf("%d themes", len(Available()))
}
