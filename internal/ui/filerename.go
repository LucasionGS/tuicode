package ui

import (
	"encoding/json"
	"path/filepath"
	"time"

	"tuicode/internal/lsp"
)

// movePath renames a file or folder on disk. Language servers that support `workspace/willRenameFiles`
// are asked for import updates first, which are applied after confirmation like VS Code does.
func (a *App) movePath(from, to string, done func(err error)) {
	finish := func() {
		_ = fsMkdirAll(filepath.Dir(to), 0o755)
		err := fsRename(from, to)
		if err == nil {
			a.onFileRenamed(from, to)
			a.notifyFilesRenamed(from, to)
		}
		if done != nil {
			done(err)
		}
	}
	mode := a.settings.String("typescript.updateImportsOnFileMove.enabled", "")
	var clients []*lsp.Client
	if a.lsp != nil && mode != "never" {
		for _, c := range a.lsp.Clients() {
			if c.Cap("workspace.fileOperations.willRename") != nil {
				clients = append(clients, c)
			}
		}
	}
	if len(clients) == 0 {
		finish()
		return
	}
	params := map[string]any{"files": []map[string]any{{"oldUri": lsp.PathToURI(from), "newUri": lsp.PathToURI(to)}}}
	var edits []lsp.WorkspaceEdit
	pending := len(clients)
	settled := false
	settle := func() {
		if settled {
			return
		}
		settled = true
		count := 0
		for _, we := range edits {
			e, ops := we.AllEdits()
			count += len(e) + len(ops)
		}
		if count == 0 {
			finish()
			return
		}
		apply := func() {
			for _, we := range edits {
				a.applyWorkspaceEdit(we)
			}
			finish()
		}
		if mode == "always" {
			apply()
			return
		}
		a.showDialog("Update imports for '"+filepath.Base(to)+"'?", "Automatically update imports for moved file.",
			[]string{"Yes", "No", "Always", "Never"}, func(choice int) {
				switch choice {
				case 0:
					apply()
				case 1:
					finish()
				case 2:
					_ = setUserSetting(a, "typescript.updateImportsOnFileMove.enabled", "always")
					apply()
				case 3:
					_ = setUserSetting(a, "typescript.updateImportsOnFileMove.enabled", "never")
					finish()
				default:
					if done != nil {
						done(nil)
					}
				}
			})
	}
	for _, c := range clients {
		c.Call("workspace/willRenameFiles", params, func(raw json.RawMessage, err error) {
			if err == nil {
				var we lsp.WorkspaceEdit
				if json.Unmarshal(raw, &we) == nil {
					edits = append(edits, we)
				}
			}
			pending--
			if pending == 0 {
				settle()
			}
		})
	}
	// a slow server doesn't block the rename
	time.AfterFunc(3*time.Second, func() { a.Post(settle) })
}

// notifyFilesRenamed tells language servers about a finished rename.
func (a *App) notifyFilesRenamed(from, to string) {
	if a.lsp == nil {
		return
	}
	params := map[string]any{"files": []map[string]any{{"oldUri": lsp.PathToURI(from), "newUri": lsp.PathToURI(to)}}}
	for _, c := range a.lsp.Clients() {
		if c.Cap("workspace.fileOperations.didRename") != nil {
			c.Notify("workspace/didRenameFiles", params)
		}
	}
}
