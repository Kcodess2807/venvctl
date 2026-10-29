package envmgr

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// python -m venv: set "VIRTUAL_ENV=C:\x"     uv: for %%i in ("C:\x") do @set "VIRTUAL_ENV=%%~fi"
// A relocatable uv venv writes ("%~dp0..") instead of a path.
var reVenvDir = regexp.MustCompile(`(?mi)^[@\s]*(?:set\s+"?VIRTUAL_ENV=([^"%\r\n]+)|for\s+%%i\s+in\s+\("([^"]+)"\))`)

// RepairOptions control Repair.
type RepairOptions struct {
	Apply    bool // write changes; false = report only
	Relative bool // distlib launchers get #!"<launcher_dir>\python.exe" instead of the new absolute path
}

// FileFix is one file Repair rewrote (or would rewrite).
type FileFix struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	From   string `json:"from"`   // interpreter path before (launchers), or the old venv path (text)
	To     string `json:"to"`     // what it becomes
	Signed bool   `json:"signed"` // it had an Authenticode signature, which the rewrite invalidates
}

// Skip is a file Repair would not touch although it mentions the old location.
type Skip struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

// RepairResult describes what Repair found and did.
type RepairResult struct {
	Old         string    // previous location from activate.bat ("" for relocatable venvs)
	Relocatable bool      // pyvenv.cfg relocatable = true, or activate.bat uses %~dp0
	Fixed       []FileFix // files rewritten (or that would be, without Apply)
	Skipped     []Skip
}

// Repair fixes a venv after its folder moved, without reinstalling anything:
//   - text scripts (activate, activate.bat, ...): old path rewritten at path boundaries
//   - uv >= 0.9.9 launchers: UV_PYTHON_PATH resource set to the relative python[w].exe
//   - old uv launchers: the trailer path rewritten and its length fixed up
//   - pip/distlib launchers: the shebang rewritten (absolute, or <launcher_dir> with Relative)
//   - any other PE or binary file: never modified; reported if it mentions the old path
//
// A relocatable venv has nothing to rewrite, except launchers that pip installed with an
// absolute interpreter that no longer exists; those are made relative.
func Repair(dir string, o RepairOptions) (*RepairResult, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	res := &RepairResult{}
	scripts := filepath.Join(abs, "Scripts")
	bat, err := os.ReadFile(filepath.Join(scripts, "activate.bat"))
	if err != nil {
		return nil, errors.New(`cannot determine previous location: Scripts\activate.bat missing`)
	}
	m := reVenvDir.FindSubmatch(bat)
	if m == nil {
		return nil, errors.New("cannot determine previous location from activate.bat")
	}
	old := strings.TrimSpace(string(m[1]) + string(m[2]))
	res.Relocatable = strings.Contains(old, "%~dp0") || relocatableCfg(abs)
	if res.Relocatable {
		old = ""
	}
	res.Old = old
	moved := old != "" && !SamePath(old, abs)
	if !moved && !res.Relocatable {
		return res, nil // still where activate.bat says it is
	}

	// launcherTarget decides the new interpreter for a launcher currently pointing at cur.
	launcherTarget := func(kind, cur string) (string, bool) {
		if !filepath.IsAbs(cur) || strings.HasPrefix(cur, launcherDirToken) {
			return "", false // already relative
		}
		stale := (moved && underDir(cur, old)) || (res.Relocatable && !exists(cur))
		if !stale {
			return "", false
		}
		switch {
		case kind == KindUvRcdata, res.Relocatable && kind == KindUvTrailer:
			return relativeInterp(cur), true
		case kind == KindDistlib && (o.Relative || res.Relocatable):
			return launcherDirToken + `\` + relativeInterp(cur), true
		case moved:
			return string(replaceBounded([]byte(cur), []byte(old), []byte(abs))), true
		}
		return "", false
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
		kind := Classify(p, b)
		mentionsOld := moved && (bytes.Contains(b, []byte(old)) || bytes.Contains(b, []byte(filepath.ToSlash(old))))
		var nb []byte
		fix := FileFix{Path: p, Kind: kind}
		switch kind {
		case KindText:
			if !moved {
				continue
			}
			nb = replaceBounded(b, []byte(old), []byte(abs))
			nb = replaceBounded(nb, []byte(filepath.ToSlash(old)), []byte(filepath.ToSlash(abs)))
			fix.From, fix.To = old, abs
		case KindUvRcdata:
			cur, _ := readUvResource(p)
			to, ok := launcherTarget(kind, cur)
			if !ok {
				continue
			}
			fix.From, fix.To, fix.Signed = cur, to, isSigned(b)
			if o.Apply {
				if err := writeUvResource(p, to); err != nil {
					return res, fmt.Errorf("%s: %w", p, err)
				}
			}
			res.Fixed = append(res.Fixed, fix)
			continue
		case KindUvTrailer:
			s, end, _ := uvTrailer(b)
			cur := string(b[s:end])
			to, ok := launcherTarget(kind, cur)
			if !ok {
				continue
			}
			nb = append(append([]byte{}, b[:s]...), to...)
			nb = append(binaryLE32(nb, uint32(len(to))), b[len(b)-4:]...)
			fix.From, fix.To = cur, to
		case KindDistlib:
			s, end, _ := shebangSpan(b)
			cur, rest := splitShebang(string(b[s:end]))
			to, ok := launcherTarget(kind, cur)
			if !ok {
				continue
			}
			line := "#!" + to
			if strings.ContainsAny(to, " <>") {
				line = `#!"` + to + `"`
			}
			nb = append(append(append([]byte{}, b[:s]...), line+rest...), b[end:]...)
			fix.From, fix.To = cur, to
		default: // KindOtherPE, KindBinary: no known structure, never edited
			if mentionsOld {
				res.Skipped = append(res.Skipped, Skip{p, kind, "unknown launcher format; not modified (reinstall the package that owns it)"})
			}
			continue
		}
		if bytes.Equal(nb, b) {
			continue
		}
		if o.Apply {
			if err := retry(func() error { return os.WriteFile(p, nb, 0o644) }); err != nil {
				return res, fmt.Errorf("%s: %w", p, err)
			}
		}
		res.Fixed = append(res.Fixed, fix)
	}
	return res, nil
}

func binaryLE32(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// relocatableCfg reports `relocatable = true` in pyvenv.cfg (written by `uv venv --relocatable`).
func relocatableCfg(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "pyvenv.cfg"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == "relocatable" && strings.EqualFold(strings.TrimSpace(v), "true") {
			return true
		}
	}
	return false
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
