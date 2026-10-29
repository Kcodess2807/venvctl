package envmgr

import (
	"os"
	"path/filepath"
)

// Dotvenv describes what `.venv` is in a project folder.
type Dotvenv struct {
	Path   string // <dir>\.venv
	Kind   string // "none", "dir" (a real local venv) or "file" (a PEP 832 redirect)
	Target string // for "file": the venv it points at
}

// Inspect reports what <dir>\.venv is.
func Inspect(dir string) Dotvenv {
	p := filepath.Join(dir, ".venv")
	d := Dotvenv{Path: p, Kind: "none"}
	fi, err := os.Stat(p)
	switch {
	case err != nil:
	case fi.IsDir():
		d.Kind = "dir"
	default:
		d.Kind = "file"
		d.Target, _ = readRedirect(p)
	}
	return d
}

// WriteRedirect writes <dir>\.venv as a PEP 832 redirect file to venv: UTF-8 without BOM, one
// line, absolute path, "\n" ending. The caller has checked that .venv is not a directory.
func WriteRedirect(dir, venv string) error {
	abs, err := filepath.Abs(venv)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ".venv"), []byte(abs+"\n"), 0o644)
}

// RemoveRedirect deletes <dir>\.venv if it is a redirect file; it never touches a directory.
func RemoveRedirect(dir string) error {
	if Inspect(dir).Kind != "file" {
		return os.ErrNotExist
	}
	return os.Remove(filepath.Join(dir, ".venv"))
}
