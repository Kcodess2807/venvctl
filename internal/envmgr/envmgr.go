// Package envmgr owns the venv registry, config, creation, deletion and repair.
package envmgr

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"venvctl/internal/discovery"
)

// Entry is one known virtual environment.
type Entry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Python   string    `json:"python"` // base interpreter dir from pyvenv.cfg
	Version  string    `json:"version"`
	Central  bool      `json:"central"`
	LastUsed time.Time `json:"last_used"`
	Size     int64     `json:"size,omitempty"`    // bytes, cached by the last `list`/`prune`
	Project  string    `json:"project,omitempty"` // folder holding the .venv redirect file, if any
}

// Registry is %LOCALAPPDATA%\venvctl\registry.json.
type Registry struct {
	Envs []Entry `json:"envs"`
}

// Config is %LOCALAPPDATA%\venvctl\config.json.
type Config struct {
	ScanRoots     []string `json:"scan_roots"`
	DefaultPython string   `json:"default_python"`
	AutoActivate  bool     `json:"auto_activate"`
	MigratedFrom  string   `json:"migrated_from,omitempty"`
	MigratedAt    string   `json:"migrated_at,omitempty"`
}

// Dir is the state folder: VENVCTL_HOME if set, else %LOCALAPPDATA%\venvctl.
func Dir() string {
	if h := os.Getenv("VENVCTL_HOME"); h != "" {
		return h
	}
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "venvctl")
}

func CentralDir() string { return filepath.Join(Dir(), "envs") }

// SetConfig sets one key in config.json, keeping every other key (including unknown ones).
func SetConfig(key string, value any) error {
	p := filepath.Join(Dir(), "config.json")
	m := map[string]any{}
	if b, err := os.ReadFile(p); err == nil {
		if err := json.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("%s is not valid JSON: %w", p, err)
		}
	}
	m[key] = value
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(p, b, 0o644)
}

func LoadConfig() Config {
	var c Config
	if b, err := os.ReadFile(filepath.Join(Dir(), "config.json")); err == nil {
		json.Unmarshal(b, &c)
	}
	return c
}

func LoadRegistry() *Registry {
	r := &Registry{}
	if b, err := os.ReadFile(filepath.Join(Dir(), "registry.json")); err == nil {
		json.Unmarshal(b, r)
	}
	return r
}

func (r *Registry) Save() error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	sort.Slice(r.Envs, func(i, j int) bool { return strings.ToLower(r.Envs[i].Name) < strings.ToLower(r.Envs[j].Name) })
	b, _ := json.MarshalIndent(r, "", "  ")
	return os.WriteFile(filepath.Join(Dir(), "registry.json"), b, 0o644)
}

// Upsert replaces the entry with the same path (case-insensitive) or appends.
func (r *Registry) Upsert(e Entry) {
	for i := range r.Envs {
		if SamePath(r.Envs[i].Path, e.Path) {
			old := r.Envs[i]
			if e.LastUsed.IsZero() {
				e.LastUsed = old.LastUsed
			}
			if e.Size == 0 {
				e.Size = old.Size
			}
			if e.Project == "" {
				e.Project = old.Project
			} else if old.Name != "" {
				e.Name = old.Name // found through a redirect: keep the env's own name, not the project's
			}
			r.Envs[i] = e
			return
		}
	}
	r.Envs = append(r.Envs, e)
}

func (r *Registry) Remove(path string) {
	out := r.Envs[:0]
	for _, e := range r.Envs {
		if !SamePath(e.Path, path) {
			out = append(out, e)
		}
	}
	r.Envs = out
}

func SamePath(a, b string) bool { return strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) }

// FromDir builds an Entry from a venv directory. Local ".venv"/"venv" dirs take the project's name.
func FromDir(dir string) (Entry, bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Entry{}, false
	}
	project := ""
	if target, ok := readRedirect(abs); ok {
		project, abs = filepath.Dir(abs), target
	}
	home, ver, ok := discovery.ParsePyvenvCfg(abs)
	if !ok {
		return Entry{}, false
	}
	name := filepath.Base(abs)
	if n := strings.ToLower(name); n == ".venv" || n == "venv" {
		name = filepath.Base(filepath.Dir(abs))
	}
	if project != "" {
		name = filepath.Base(project)
	}
	central := strings.HasPrefix(strings.ToLower(abs), strings.ToLower(CentralDir())+`\`)
	return Entry{Name: name, Path: abs, Python: home, Version: ver, Central: central, Project: project}, true
}

// readRedirect reads a PEP 832 `.venv` redirect file: one UTF-8 line holding a venv path,
// relative paths resolved against the file's directory. The content is path data only.
func readRedirect(p string) (string, bool) {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() || fi.Size() > 32<<10 {
		return "", false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	line, _, _ := strings.Cut(strings.TrimPrefix(string(b), "\xef\xbb\xbf"), "\n")
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false
	}
	if !filepath.IsAbs(line) {
		line = filepath.Join(filepath.Dir(p), line)
	}
	return filepath.Clean(line), true
}

// ErrNoLocalVenv: Resolve("") found no .venv or venv walking up from the current folder.
var ErrNoLocalVenv = errors.New("no .venv or venv in this folder or any parent")

// Resolve picks a venv: "" walks up from cwd for .venv/venv; otherwise a directory, a central
// env name, or a registered name.
func Resolve(r *Registry, arg string) (Entry, error) {
	if arg == "" {
		dir, _ := os.Getwd()
		for {
			for _, n := range []string{".venv", "venv"} {
				if e, ok := FromDir(filepath.Join(dir, n)); ok {
					return r.Merge(e), nil
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		return Entry{}, ErrNoLocalVenv
	}
	if e, ok := FromDir(arg); ok {
		return r.Merge(e), nil
	}
	if e, ok := FromDir(filepath.Join(CentralDir(), arg)); ok {
		return r.Merge(e), nil
	}
	var hits []Entry
	for _, e := range r.Envs {
		if strings.EqualFold(e.Name, arg) {
			hits = append(hits, e)
		}
	}
	switch len(hits) {
	case 0:
		return Entry{}, &NotFoundError{Arg: arg}
	case 1:
		return hits[0], nil
	}
	return Entry{}, &AmbiguousError{Name: arg, Hits: hits}
}

// Merge fills in what only the registry knows (linked project, last use, cached size, and the
// env's own name when found through a redirect) for an entry built from disk.
func (r *Registry) Merge(e Entry) Entry {
	if r == nil {
		return e
	}
	for _, old := range r.Envs {
		if !SamePath(old.Path, e.Path) {
			continue
		}
		if e.Project == "" {
			e.Project = old.Project
		} else if old.Name != "" {
			e.Name = old.Name
		}
		e.LastUsed, e.Size = old.LastUsed, old.Size
		break
	}
	return e
}

// NotFoundError: the argument is neither a venv folder nor a known name.
type NotFoundError struct{ Arg string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%q is neither a venv folder nor a registered environment", e.Arg)
}

// AmbiguousError: several registered venvs share the name; the caller picks one.
type AmbiguousError struct {
	Name string
	Hits []Entry
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%q matches %d environments", e.Name, len(e.Hits))
}

// CreateCommand returns the command that creates a venv at dest: `uv venv` when uv is on PATH,
// else `<python> -m venv`. py is "", a version ("3.12", "3.12.3") or a path to python.exe.
func CreateCommand(dest, py string) ([]string, error) {
	if uv, err := exec.LookPath("uv"); err == nil {
		args := []string{uv, "venv", dest}
		if py != "" {
			args = append(args, "--python", py)
		}
		return args, nil
	}
	exe := py
	if !IsInterpreterPath(py) {
		var err error
		if exe, err = discovery.FindInterpreter(py); err != nil {
			return nil, err
		}
	}
	return []string{exe, "-m", "venv", dest}, nil
}

// IsInterpreterPath reports whether a --python value is a path rather than a version.
func IsInterpreterPath(py string) bool {
	return strings.ContainsAny(py, `\/`) || strings.HasSuffix(strings.ToLower(py), ".exe")
}

// Create runs args (from CreateCommand), sending its output to out, and returns the new venv.
func Create(dest string, args []string, out io.Writer) (Entry, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return Entry{}, fmt.Errorf("%s failed: %w", filepath.Base(args[0]), err)
	}
	e, ok := FromDir(dest)
	if !ok {
		return Entry{}, fmt.Errorf("%s ran but left no pyvenv.cfg in %s", filepath.Base(args[0]), dest)
	}
	return e, nil
}

// CloudRoot returns the synced folder (OneDrive, Dropbox, Google Drive, iCloudDrive) that
// contains path, or "" when path is not inside one.
func CloudRoot(path string) string {
	var roots []string
	for _, v := range []string{"OneDrive", "OneDriveCommercial", "OneDriveConsumer"} {
		if p := os.Getenv(v); p != "" {
			roots = append(roots, p)
		}
	}
	for _, d := range []string{"Dropbox", "Google Drive", "iCloudDrive"} {
		roots = append(roots, filepath.Join(os.Getenv("USERPROFILE"), d))
	}
	lp := strings.ToLower(filepath.Clean(path)) + `\`
	for _, r := range roots {
		if strings.HasPrefix(lp, strings.ToLower(filepath.Clean(r))+`\`) {
			return r
		}
	}
	return ""
}

// Activity is the best guess of when a venv was last used: venvctl's own last_used stamp, or the
// newest modification time of the venv folder, pyvenv.cfg or site-packages (changes on install).
func Activity(e Entry) time.Time {
	t := e.LastUsed
	for _, p := range []string{e.Path, filepath.Join(e.Path, "pyvenv.cfg"), filepath.Join(e.Path, "Lib", "site-packages")} {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(t) {
			t = fi.ModTime()
		}
	}
	return t
}

// Size sums file sizes under dir.
func Size(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, e := d.Info(); e == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// Delete renames dir to a sibling trash name first, so a locked venv is left whole instead of
// half-deleted, then removes the trash. Retries with backoff because handles (killed processes,
// antivirus scans) release a little late.
func Delete(dir string) error {
	trash := fmt.Sprintf("%s.%d.venvctl-trash", dir, time.Now().UnixNano())
	if err := retry(func() error { return os.Rename(dir, trash) }); err != nil {
		return &LockedError{Dir: dir, Err: err}
	}
	filepath.WalkDir(trash, func(p string, _ fs.DirEntry, err error) error {
		if err == nil {
			os.Chmod(p, 0o777) // clear read-only
		}
		return nil
	})
	if err := retry(func() error { return os.RemoveAll(trash) }); err != nil {
		return &LeftoverError{Dir: dir, Trash: trash, Err: err}
	}
	return nil
}

// retry runs f with backoff 0/50/100/200/400/800 ms until it succeeds.
func retry(f func() error) (err error) {
	for _, ms := range []time.Duration{0, 50, 100, 200, 400, 800} {
		time.Sleep(ms * time.Millisecond)
		if err = f(); err == nil {
			return nil
		}
	}
	return err
}

// LockedError: the venv could not be renamed away, so nothing was deleted.
type LockedError struct {
	Dir string
	Err error
}

func (e *LockedError) Error() string { return fmt.Sprintf("%s is still locked: %v", e.Dir, e.Err) }

// LeftoverError: the venv is gone from its folder, but some files remain in Trash.
type LeftoverError struct {
	Dir, Trash string
	Err        error
}

func (e *LeftoverError) Error() string {
	return fmt.Sprintf("venv removed from %s, but leftovers remain in %s: %v", e.Dir, e.Trash, e.Err)
}
