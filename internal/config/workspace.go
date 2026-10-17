package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// WorkspaceFolder is one root folder of a multi-root workspace.
type WorkspaceFolder struct {
	Name string
	Path string // absolute
}

// Workspace is a VS Code `.code-workspace` file.
type Workspace struct {
	File     string
	Folders  []WorkspaceFolder
	Settings map[string]any
}

// IsWorkspaceFile reports whether path looks like a `.code-workspace` file.
func IsWorkspaceFile(path string) bool {
	return strings.HasSuffix(path, ".code-workspace")
}

// LoadWorkspace reads a `.code-workspace` file. Relative folder paths are resolved against the file's folder.
func LoadWorkspace(path string) (*Workspace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Folders []struct {
			Name string `json:"name"`
			Path string `json:"path"`
			URI  string `json:"uri"`
		} `json:"folders"`
		Settings map[string]any `json:"settings"`
	}
	if err := UnmarshalJSONC(data, &raw); err != nil {
		return nil, errors.New("Failed to parse " + filepath.Base(path) + ": " + err.Error())
	}
	ws := &Workspace{File: path, Settings: raw.Settings}
	dir := filepath.Dir(path)
	for _, f := range raw.Folders {
		p := f.Path
		if p == "" && strings.HasPrefix(f.URI, "file://") {
			p = strings.TrimPrefix(f.URI, "file://")
		}
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "~/") {
			home, _ := os.UserHomeDir()
			p = filepath.Join(home, p[2:])
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		p = filepath.Clean(p)
		name := f.Name
		if name == "" {
			name = filepath.Base(p)
		}
		ws.Folders = append(ws.Folders, WorkspaceFolder{Name: name, Path: p})
	}
	if len(ws.Folders) == 0 {
		return nil, errors.New("The workspace file " + filepath.Base(path) + " has no folders.")
	}
	return ws, nil
}

// SaveWorkspace writes the folders to the workspace file, keeping its other keys. Paths inside the
// file's folder are stored relative to it.
func SaveWorkspace(w *Workspace, write func(path string, data []byte, perm os.FileMode) error) error {
	doc := map[string]any{}
	if data, err := os.ReadFile(w.File); err == nil {
		_ = UnmarshalJSONC(data, &doc)
	}
	dir := filepath.Dir(w.File)
	var folders []map[string]any
	for _, f := range w.Folders {
		p := f.Path
		if rel, err := filepath.Rel(dir, p); err == nil && !strings.HasPrefix(rel, "..") {
			p = filepath.ToSlash(rel)
		}
		entry := map[string]any{"path": p}
		if f.Name != filepath.Base(f.Path) {
			entry["name"] = f.Name
		}
		folders = append(folders, entry)
	}
	doc["folders"] = folders
	if _, ok := doc["settings"]; !ok {
		doc["settings"] = map[string]any{}
	}
	data, err := json.MarshalIndent(doc, "", "\t")
	if err != nil {
		return err
	}
	return write(w.File, append(data, '\n'), 0o644)
}

// Name returns the workspace name shown in the title, like "project (Workspace)".
func (w *Workspace) Name() string {
	if w.File == "" {
		return "Untitled"
	}
	return strings.TrimSuffix(filepath.Base(w.File), ".code-workspace")
}

// PrimaryRoot returns the folder used for git, tasks and terminals: the workspace file's folder
// when every root lives inside it, otherwise the first root.
func (w *Workspace) PrimaryRoot() string {
	dir := filepath.Dir(w.File)
	for _, f := range w.Folders {
		if f.Path != dir && !strings.HasPrefix(f.Path, dir+string(filepath.Separator)) {
			return w.Folders[0].Path
		}
	}
	return dir
}
