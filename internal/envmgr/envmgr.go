// Package envmgr owns the venv registry, config, creation, deletion and repair.
package envmgr

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"pygo/internal/discovery"
)

// Entry is one known virtual environment.
type Entry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Python   string    `json:"python"` // base interpreter dir from pyvenv.cfg
	Version  string    `json:"version"`
	Central  bool      `json:"central"`
	LastUsed time.Time `json:"last_used"`
}

// Registry is %LOCALAPPDATA%\pygo\registry.json.
type Registry struct {
	Envs []Entry `json:"envs"`
}

// Config is %LOCALAPPDATA%\pygo\config.json.
type Config struct {
	ScanRoots     []string `json:"scan_roots"`
	DefaultPython string   `json:"default_python"`
}

func Dir() string        { return filepath.Join(os.Getenv("LOCALAPPDATA"), "pygo") }
func CentralDir() string { return filepath.Join(Dir(), "envs") }

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
			if e.LastUsed.IsZero() {
				e.LastUsed = r.Envs[i].LastUsed
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
	return Entry{Name: name, Path: abs, Python: home, Version: ver, Central: central}, true
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
					return e, nil
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
		return e, nil
	}
	if e, ok := FromDir(filepath.Join(CentralDir(), arg)); ok {
		return e, nil
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

// Create makes a venv with `uv venv` when available, else `python -m venv`.
// local=true creates .\.venv; otherwise %LOCALAPPDATA%\pygo\envs\<name>.
func Create(name, pyver string, local bool) (Entry, error) {
	var dest string
	if local {
		dest, _ = filepath.Abs(".venv")
	} else {
		if name == "" {
			return Entry{}, errors.New("a name is required for a central environment (or pass --local)")
		}
		dest = filepath.Join(CentralDir(), name)
	}
	if _, err := os.Stat(filepath.Join(dest, "pyvenv.cfg")); err == nil {
		return Entry{}, fmt.Errorf("%s already exists", dest)
	}
	var cmd *exec.Cmd
	if uv, err := exec.LookPath("uv"); err == nil {
		args := []string{"venv", dest}
		if pyver != "" {
			args = append(args, "--python", pyver)
		}
		cmd = exec.Command(uv, args...)
	} else {
		exe, err := discovery.FindInterpreter(pyver)
		if err != nil {
			return Entry{}, err
		}
		cmd = exec.Command(exe, "-m", "venv", dest)
	}
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return Entry{}, fmt.Errorf("venv creation failed: %w", err)
	}
	e, ok := FromDir(dest)
	if !ok {
		return Entry{}, fmt.Errorf("created %s but found no pyvenv.cfg", dest)
	}
	if name != "" {
		e.Name = name
	}
	return e, nil
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
	trash := fmt.Sprintf("%s.%d.pygo-trash", dir, time.Now().UnixNano())
	var err error
	for _, wait := range []time.Duration{0, 50, 100, 200, 400, 800} {
		time.Sleep(wait * time.Millisecond)
		if err = os.Rename(dir, trash); err == nil {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("still locked, nothing deleted: %w", err)
	}
	filepath.WalkDir(trash, func(p string, _ fs.DirEntry, err error) error {
		if err == nil {
			os.Chmod(p, 0o777) // clear read-only
		}
		return nil
	})
	for _, wait := range []time.Duration{0, 50, 100, 200, 400, 800} {
		time.Sleep(wait * time.Millisecond)
		if err = os.RemoveAll(trash); err == nil {
			return nil
		}
	}
	return fmt.Errorf("venv removed from %s but leftovers remain in %s: %w", dir, trash, err)
}

// python -m venv: set "VIRTUAL_ENV=C:\x"     uv: for %%i in ("C:\x") do @set "VIRTUAL_ENV=%%~fi"
var reVenvDir = regexp.MustCompile(`(?mi)^[@\s]*(?:set\s+"?VIRTUAL_ENV=([^"%\r\n]+)|for\s+%%i\s+in\s+\("([^"]+)"\))`)

// Repair rewrites the venv's previous absolute path (embedded in activate scripts and
// Scripts\*.exe launchers after a move/rename) to its current location.
// With apply=false nothing is written; the returned files are what would change.
func Repair(dir string, apply bool) (old string, files []string, err error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", nil, err
	}
	scripts := filepath.Join(abs, "Scripts")
	bat, err := os.ReadFile(filepath.Join(scripts, "activate.bat"))
	if err != nil {
		return "", nil, errors.New("cannot determine previous location: Scripts\\activate.bat missing")
	}
	m := reVenvDir.FindSubmatch(bat)
	if m == nil {
		return "", nil, errors.New("cannot determine previous location from activate.bat")
	}
	old = strings.TrimSpace(string(m[1]) + string(m[2]))
	if SamePath(old, abs) {
		return old, nil, nil
	}
	ents, _ := os.ReadDir(scripts)
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(scripts, e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		nb := RewritePath(b, old, abs)
		if bytes.Equal(nb, b) {
			continue
		}
		if apply {
			if err := os.WriteFile(p, nb, 0o644); err != nil {
				return old, files, fmt.Errorf("%s: %w", p, err)
			}
		}
		files = append(files, p)
	}
	return old, files, nil
}

// RewritePath replaces old with new (backslash and forward-slash forms) inside a script or launcher.
// uv trampolines end with <python path utf8><u32 LE len><magic>; their length field is fixed up.
// Magic is "UVSC" (script entry point) or "UVPY" (python); "UVUV" in old uv releases.
func RewritePath(b []byte, old, new string) []byte {
	n := len(b)
	if n > 8 && isUvMagic(b[n-4:]) {
		l := int(binary.LittleEndian.Uint32(b[n-8 : n-4]))
		if start := n - 8 - l; start >= 0 {
			path := replaceBounded(b[start:n-8], []byte(old), []byte(new))
			out := append([]byte{}, b[:start]...)
			out = append(out, path...)
			out = binary.LittleEndian.AppendUint32(out, uint32(len(path)))
			return append(out, b[n-4:]...)
		}
	}
	nb := replaceBounded(b, []byte(old), []byte(new))
	return replaceBounded(nb, []byte(filepath.ToSlash(old)), []byte(filepath.ToSlash(new)))
}

// pathBoundary are the bytes that may follow a path for it to count as a whole-path match.
const pathBoundary = `\/"');` + " \t\r\n"

// replaceBounded replaces old with new only where old ends at a path boundary: followed by
// a pathBoundary byte or the end of data. So C:\x\proj never rewrites C:\x\proj2.
func replaceBounded(b, old, new []byte) []byte {
	if len(old) == 0 || !bytes.Contains(b, old) {
		return b
	}
	var out []byte
	i := 0
	for {
		j := bytes.Index(b[i:], old)
		if j < 0 {
			break
		}
		end := i + j + len(old)
		if end == len(b) || strings.IndexByte(pathBoundary, b[end]) >= 0 {
			out = append(append(out, b[i:i+j]...), new...)
		} else {
			out = append(out, b[i:end]...)
		}
		i = end
	}
	return append(out, b[i:]...)
}

func isUvMagic(m []byte) bool {
	s := string(m)
	return s == "UVSC" || s == "UVPY" || s == "UVUV"
}
