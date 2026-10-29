package envmgr

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TrustFile is where `venvctl allow` records project roots that may auto-activate.
// Paths are stored normalized (TrustKey) so the shell hooks can test membership with a plain
// substring search for "<key>" without parsing JSON.
func TrustFile() string { return filepath.Join(Dir(), "trust.json") }

// TrustKey normalizes a folder for trust.json: absolute, cleaned, lower case, forward slashes.
func TrustKey(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	return strings.ToLower(filepath.ToSlash(filepath.Clean(abs)))
}

// Trusted lists the trusted roots (as TrustKey values).
func Trusted() []string {
	var t struct {
		Trusted []string `json:"trusted"`
	}
	if b, err := os.ReadFile(TrustFile()); err == nil {
		json.Unmarshal(b, &t)
	}
	return t.Trusted
}

// IsTrusted reports whether dir is a trusted root.
func IsTrusted(dir string) bool {
	k := TrustKey(dir)
	for _, t := range Trusted() {
		if t == k {
			return true
		}
	}
	return false
}

// SetTrust adds or removes dir. changed is false when it was already in that state.
func SetTrust(dir string, trust bool) (changed bool, err error) {
	k := TrustKey(dir)
	var keep []string
	found := false
	for _, t := range Trusted() {
		if t == k {
			found = true
			if !trust {
				continue
			}
		}
		keep = append(keep, t)
	}
	if found == trust {
		return false, nil
	}
	if trust {
		keep = append(keep, k)
	}
	sort.Strings(keep)
	if keep == nil {
		keep = []string{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep & < > literal so the hooks' substring search matches
	enc.SetIndent("", "  ")
	if err := enc.Encode(struct {
		Trusted []string `json:"trusted"`
	}{keep}); err != nil {
		return false, err
	}
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(TrustFile(), buf.Bytes(), 0o644)
}

// MaxRootDepth is how many folders the auto-activate hooks walk up from the current one.
const MaxRootDepth = 12

// FindRoot is the auto-activate root for dir. Walking up at most MaxRootDepth folders, and
// stopping after a folder that holds .git, it is the closest folder with a venv (.venv redirect
// file, .venv\pyvenv.cfg or venv\pyvenv.cfg); failing that, the closest one with pyproject.toml;
// "" if neither. The PowerShell, bash and Clink hooks implement the same rule.
func FindRoot(dir string) string {
	proj := ""
	d := filepath.Clean(dir)
	for i := 0; i < MaxRootDepth; i++ {
		if HasVenv(d) {
			return d
		}
		if proj == "" && isFile(filepath.Join(d, "pyproject.toml")) {
			proj = d
		}
		if FileExists(filepath.Join(d, ".git")) {
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return proj
}

// HasVenv reports whether dir holds a venv the hooks would activate.
func HasVenv(dir string) bool {
	return isFile(filepath.Join(dir, ".venv")) ||
		FileExists(filepath.Join(dir, ".venv", "pyvenv.cfg")) ||
		FileExists(filepath.Join(dir, "venv", "pyvenv.cfg"))
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
