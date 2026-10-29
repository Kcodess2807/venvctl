// Package discovery finds virtual environments on disk and Python interpreters via PEP 514.
package discovery

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// ParsePyvenvCfg reads dir\pyvenv.cfg. ok is false when the file is absent.
func ParsePyvenvCfg(dir string) (home, version string, ok bool) {
	b, err := os.ReadFile(filepath.Join(dir, "pyvenv.cfg"))
	if err != nil {
		return "", "", false
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "home":
			home = v
		case "version":
			version = v
		case "version_info": // uv / virtualenv
			if version == "" {
				version = v
			}
		}
	}
	return home, version, true
}

// TrashSuffixes end folders that a delete renamed away. The pyGo and WinVenv generations used
// their own names; all three are trash.
var TrashSuffixes = []string{".venvctl-trash", ".pygo-trash", ".wv-trash"}

// IsTrash reports whether a folder name ends in one of TrashSuffixes.
func IsTrash(name string) bool {
	n := strings.ToLower(name)
	for _, s := range TrashSuffixes {
		if strings.HasSuffix(n, s) {
			return true
		}
	}
	return false
}

var skipDirs = map[string]bool{
	"windows": true, "program files": true, "program files (x86)": true, "programdata": true,
	"$recycle.bin": true, "system volume information": true, "appdata": true,
	"node_modules": true, ".git": true, "__pycache__": true, ".cache": true, "site-packages": true,
}

// Progress counts scan work. Read it from another goroutine while Scan runs.
type Progress struct{ Dirs, Found atomic.Int64 }

// Scan walks roots concurrently and returns every directory holding a pyvenv.cfg, plus every
// `.venv` file (PEP 832 redirect), sorted. It does not descend into a venv once found, nor into
// skipDirs, trash folders or symlinks. p may be nil.
func Scan(roots []string, p *Progress) []string {
	if p == nil {
		p = &Progress{}
	}
	var (
		mu  sync.Mutex
		out []string
		wg  sync.WaitGroup
		sem = make(chan struct{}, 64)
	)
	// ponytail: goroutine per directory, gated on ReadDir only; fine up to a few hundred k dirs.
	var walk func(dir string)
	walk = func(dir string) {
		defer wg.Done()
		sem <- struct{}{}
		ents, err := os.ReadDir(dir)
		<-sem
		p.Dirs.Add(1)
		if err != nil {
			return
		}
		for _, e := range ents {
			if !e.IsDir() && e.Name() == "pyvenv.cfg" {
				p.Found.Add(1)
				mu.Lock()
				out = append(out, dir)
				mu.Unlock()
				return
			}
		}
		for _, e := range ents {
			if e.Name() == ".venv" && !e.IsDir() { // a PEP 832 redirect file; the caller follows it
				mu.Lock()
				out = append(out, filepath.Join(dir, ".venv"))
				mu.Unlock()
				continue
			}
			if !e.IsDir() || e.Type()&os.ModeSymlink != 0 || skipDirs[strings.ToLower(e.Name())] || IsTrash(e.Name()) {
				continue
			}
			wg.Add(1)
			go walk(filepath.Join(dir, e.Name()))
		}
	}
	for _, r := range roots {
		wg.Add(1)
		go walk(r)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// FixedDrives returns roots like `C:\` for every fixed disk.
func FixedDrives() []string {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return []string{os.Getenv("SystemDrive") + `\`}
	}
	var out []string
	for i := 0; i < 26; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		p, _ := windows.UTF16PtrFromString(root)
		if windows.GetDriveType(p) == windows.DRIVE_FIXED {
			out = append(out, root)
		}
	}
	return out
}

// Interpreter is a Python install registered per PEP 514.
type Interpreter struct {
	Company string
	Tag     string
	Version string
	Arch    string
	Exe     string
}

// Interpreters lists installs from HKCU and HKLM (64- and 32-bit views), newest first.
func Interpreters() []Interpreter {
	roots := []struct {
		key  registry.Key
		path string
	}{
		{registry.CURRENT_USER, `Software\Python`},
		{registry.LOCAL_MACHINE, `Software\Python`},
		{registry.LOCAL_MACHINE, `Software\WOW6432Node\Python`},
	}
	seen := map[string]bool{}
	var out []Interpreter
	for _, r := range roots {
		for _, company := range subKeys(r.key, r.path) {
			for _, tag := range subKeys(r.key, r.path+`\`+company) {
				base := r.path + `\` + company + `\` + tag
				exe := regStr(r.key, base+`\InstallPath`, "ExecutablePath")
				if exe == "" {
					if dir := regStr(r.key, base+`\InstallPath`, ""); dir != "" {
						exe = filepath.Join(dir, "python.exe")
					}
				}
				if exe == "" || seen[strings.ToLower(exe)] {
					continue
				}
				if _, err := os.Stat(exe); err != nil {
					continue
				}
				seen[strings.ToLower(exe)] = true
				ver := regStr(r.key, base, "Version")
				if ver == "" {
					ver = regStr(r.key, base, "SysVersion")
				}
				arch := regStr(r.key, base, "SysArchitecture")
				if arch == "" && strings.Contains(r.path, "WOW6432Node") {
					arch = "32bit"
				}
				out = append(out, Interpreter{Company: company, Tag: tag, Version: ver, Arch: arch, Exe: exe})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Version != out[j].Version {
			return CompareVersions(out[i].Version, out[j].Version) > 0
		}
		return out[i].Arch == "64bit" && out[j].Arch != "64bit"
	})
	return out
}

// FindInterpreter returns the newest interpreter whose version starts with want ("" matches any).
// Falls back to `python` on PATH when the registry is empty.
func FindInterpreter(want string) (string, error) {
	for _, it := range Interpreters() {
		if want == "" || strings.HasPrefix(it.Version, want) {
			return it.Exe, nil
		}
	}
	if want == "" {
		if p, err := exec.LookPath("python"); err == nil {
			return p, nil
		}
	}
	return "", &NotFoundError{Want: want}
}

type NotFoundError struct{ Want string }

func (e *NotFoundError) Error() string {
	if e.Want == "" {
		return "no Python interpreter found (install Python or uv)"
	}
	return "no Python " + e.Want + " found in the registry (PEP 514); install it or use uv"
}

func subKeys(root registry.Key, path string) []string {
	k, err := registry.OpenKey(root, path, registry.READ)
	if err != nil {
		return nil
	}
	defer k.Close()
	names, _ := k.ReadSubKeyNames(-1)
	return names
}

func regStr(root registry.Key, path, name string) string {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue(name)
	return v
}

// CompareVersions compares dotted versions numerically, so "3.10" > "3.9". Each part counts only
// its leading digits ("0rc3" is 0); a shorter version that is a prefix sorts first.
func CompareVersions(a, b string) int { return slices.Compare(versionParts(a), versionParts(b)) }

func versionParts(v string) []int {
	var out []int
	for _, p := range strings.Split(v, ".") {
		if i := strings.IndexFunc(p, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
			p = p[:i]
		}
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}
