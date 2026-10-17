package config

import "testing"

func TestWhen(t *testing.T) {
	ctx := func(k string) any {
		switch k {
		case "editorTextFocus":
			return true
		case "editorLangId":
			return "go"
		case "resourceFilename":
			return "main.go"
		}
		return nil
	}
	cases := map[string]bool{
		"editorTextFocus":                                            true,
		"!editorTextFocus":                                           false,
		"editorTextFocus && !editorReadonly":                         true,
		"editorLangId == go && editorTextFocus":                      true,
		"editorLangId == 'typescript' || terminalFocus":              false,
		"resourceFilename =~ /\\.go$/":                               true,
		"(terminalFocus || editorTextFocus) && editorLangId != rust": true,
	}
	for src, want := range cases {
		w, _ := ParseWhen(src)
		if got := w.Eval(ctx); got != want {
			t.Errorf("%s: got %v", src, got)
		}
	}
	if NormalizeKey("Shift+Ctrl+P") != "ctrl+shift+p" || NormalizeKey("ctrl+[Semicolon]") != "ctrl+;" || NormalizeKey("ctrl++") != "ctrl++" {
		t.Errorf("normalize %s %s %s", NormalizeKey("Shift+Ctrl+P"), NormalizeKey("ctrl+[Semicolon]"), NormalizeKey("ctrl++"))
	}
	if string(StripJSONC([]byte(`{"a": 1, // c
	"b": "//x", /* y */ }`))) == "" {
		t.Error("strip")
	}
}
