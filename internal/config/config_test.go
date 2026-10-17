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

func TestLanguageOverridePrecedence(t *testing.T) {
	s := &Settings{layers: []map[string]any{
		{"editor.tabSize": 4.0, "[go]": map[string]any{"editor.tabSize": 8.0}},
		{"editor.tabSize": 2.0},
	}}
	if got := s.Int("editor.tabSize", "go", 0); got != 8 {
		t.Fatalf("language default should win over a user value: %d", got)
	}
	if got := s.Int("editor.tabSize", "python", 0); got != 2 {
		t.Fatalf("user value should apply to other languages: %d", got)
	}
}
