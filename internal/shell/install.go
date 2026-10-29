package shell

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows/registry"

	"venvctl/internal/envmgr"
)

// Hook generations. venvctl removes the hooks of the two earlier ones on every init.
const (
	genWinVenv = "WinVenv"
	genPyGo    = "pyGo"
	genOwn     = "venvctl"
	genStale   = "venvctl (stale)" // a venvctl hook line calling a different exe
)

const hookComment = "# venvctl shell hook"

// lineGen classifies one profile line as a hook line, or a hook comment, of some generation.
// gen is "" for every other line.
func lineGen(line string) (gen string, comment bool) {
	l := strings.ToLower(line)
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(l, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("# winvenv shell hook"):
		return genWinVenv, true
	case has("# pygo shell hook"):
		return genPyGo, true
	case has(hookComment):
		return genOwn, true
	case has("wv.exe", `winvenv\bin`, "winvenv/bin"):
		return genWinVenv, false
	case has("pygo.exe", `pygo\bin`, "pygo/bin"):
		return genPyGo, false
	case has("venvctl.exe' init pwsh", "venvctl.exe' init bash"):
		return genOwn, false
	}
	return "", false
}

// filterResult says what filterProfile did.
type filterResult struct {
	Text    string
	Removed map[string]int // hook lines removed per generation (comments are not counted)
	Added   bool           // own was appended
	Kept    bool           // own was already there and stays where it was
}

// filterProfile removes WinVenv and pyGo hook lines (and each hook comment plus the line after
// it), removes venvctl hook lines other than own, and appends own (with its comment) unless it
// is already present. own == "" removes every venvctl hook too. Other lines stay byte-for-byte;
// lines are joined with nl.
func filterProfile(text, own, nl string) filterResult {
	res := filterResult{Removed: map[string]int{}}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var keep []string
	after := "" // generation of the hook comment on the previous line
	for _, ln := range lines {
		gen, comment := lineGen(ln)
		switch {
		case own != "" && strings.TrimSpace(ln) == own:
			if res.Kept {
				res.Removed[genOwn]++ // duplicate
			} else {
				res.Kept = true
				keep = append(keep, hookComment, ln)
			}
			after = ""
		case comment:
			after = gen
		case after != "" && strings.TrimSpace(ln) != "" && (after != genOwn || gen != ""):
			// the line after a legacy hook comment goes; after our own comment only if it is a hook
			res.Removed[label(after, gen, own)]++
			after = ""
		case gen != "":
			res.Removed[label(gen, gen, own)]++
			after = ""
		default:
			keep = append(keep, ln)
			after = ""
		}
	}
	if own != "" && !res.Kept {
		if n := len(keep); n > 0 && keep[n-1] == "" {
			keep = keep[:n-1]
		}
		if n := len(keep); n == 1 && keep[0] == "" {
			keep = nil
		}
		keep = append(keep, hookComment, own, "")
		res.Added = true
	}
	res.Text = strings.Join(keep, nl)
	return res
}

// label names what a removed line was. A venvctl line is "stale" when installing (own != "").
func label(commentGen, lineGen, own string) string {
	g := commentGen
	if lineGen == genWinVenv || lineGen == genPyGo {
		g = lineGen
	}
	if g == genOwn && own != "" {
		return genStale
	}
	return g
}

// filterAutoRun splits the CMD AutoRun value on " & ", drops WinVenv/pyGo segments and stale
// venvctl segments, keeps every other segment (VS Code's included) in order, and keeps own in
// place or appends it. own == "" removes venvctl's segment too.
func filterAutoRun(value, own string) filterResult {
	res := filterResult{Removed: map[string]int{}}
	var keep []string
	for _, seg := range strings.Split(value, " & ") {
		t := strings.TrimSpace(seg)
		l := strings.ToLower(t)
		switch {
		case t == "":
		case own != "" && strings.EqualFold(t, own):
			if res.Kept {
				res.Removed[genOwn]++
			} else {
				res.Kept = true
				keep = append(keep, t)
			}
		case strings.Contains(l, "wv_hook.cmd") || strings.Contains(l, `winvenv\`):
			res.Removed[genWinVenv]++
		case strings.Contains(l, "pygo_hook.cmd") || strings.Contains(l, `pygo\`):
			res.Removed[genPyGo]++
		case strings.Contains(l, "venvctl_hook.cmd"):
			res.Removed[label(genOwn, genOwn, own)]++
		default:
			keep = append(keep, t)
		}
	}
	if own != "" && !res.Kept {
		keep = append(keep, own)
		res.Added = true
	}
	res.Text = strings.Join(keep, " & ")
	return res
}

// Change is what Install did (or, in a dry run, would do) to one shell.
type Change struct {
	Shell   string // "PowerShell 5.1", "PowerShell 7", "CMD", "Git Bash"
	Target  string // file or registry value
	Removed map[string]int
	Added   bool
	Kept    bool
	Changed bool
	Notes   []string // extra actions, e.g. the Clink script
}

func (c Change) String() string {
	var parts []string
	n, gens := 0, []string{}
	for _, g := range []string{genWinVenv, genPyGo} {
		if c.Removed[g] > 0 {
			n += c.Removed[g]
			gens = append(gens, g)
		}
	}
	if n > 0 {
		parts = append(parts, fmt.Sprintf("removed %s (%s)", count(n, "legacy line"), strings.Join(gens, ", ")))
	}
	if k := c.Removed[genStale]; k > 0 {
		parts = append(parts, "removed "+count(k, "stale venvctl line"))
	}
	if k := c.Removed[genOwn]; k > 0 {
		parts = append(parts, "removed "+count(k, "venvctl hook line"))
	}
	switch {
	case c.Added:
		parts = append(parts, "added venvctl hook")
	case c.Kept:
		parts = append(parts, "venvctl hook already present")
	}
	if len(parts) == 0 {
		parts = append(parts, "nothing to change")
	}
	parts = append(parts, c.Notes...)
	return c.Shell + ": " + strings.Join(parts, ", ")
}

func count(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// Options for Install.
type Options struct {
	Only   string // "", "pwsh"/"powershell", "cmd" or "bash"
	DryRun bool   // report only, write nothing
	Remove bool   // remove venvctl and legacy hooks, add nothing
}

var psNames = map[string]string{"powershell": "PowerShell 5.1", "pwsh": "PowerShell 7"}

// Install updates every selected shell: removes WinVenv/pyGo hooks, then adds the venvctl hook
// (unless Remove). It returns one Change per shell, even on errors.
func Install(exe string, o Options) ([]Change, error) {
	if o.Only == "powershell" {
		o.Only = "pwsh"
	}
	want := func(sh string) bool { return o.Only == "" || o.Only == sh }
	var changes []Change
	var errs []error

	if want("pwsh") {
		own := ""
		if !o.Remove {
			own = fmt.Sprintf("& '%s' init pwsh | Out-String | Invoke-Expression", psq(exe))
		}
		for _, ps := range PowerShells() {
			c, err := updateFile(psNames[ps], Profiles()[ps], own, o, false)
			changes = append(changes, c)
			errs = append(errs, err)
		}
	}
	if want("cmd") {
		c, err := updateCmd(exe, o)
		changes = append(changes, c)
		errs = append(errs, err)
	}
	if want("bash") {
		rc := filepath.Join(os.Getenv("USERPROFILE"), ".bashrc")
		if _, err := exec.LookPath("bash"); err == nil || fileExists(rc) {
			own := ""
			if !o.Remove {
				own = fmt.Sprintf(`eval "$('%s' init bash)"`, posix(exe))
			}
			c, err := updateFile("Git Bash", rc, own, o, true)
			changes = append(changes, c)
			errs = append(errs, err)
		}
	}
	return changes, errors.Join(errs...)
}

// updateFile filters one profile. bash forces LF and no BOM; PowerShell profiles keep their
// encoding (UTF-8, UTF-8 BOM, UTF-16) and line endings.
func updateFile(name, path, own string, o Options, bash bool) (Change, error) {
	c := Change{Shell: name, Target: path}
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if raw == nil && own == "" {
		return c, nil // nothing to remove from a file that does not exist
	}
	text, enc := decode(raw)
	nl := "\r\n"
	if bash || (strings.Contains(text, "\n") && !strings.Contains(text, "\r\n")) {
		nl = "\n"
	}
	if bash {
		enc = "utf8"
	}
	r := filterProfile(text, own, nl)
	c.Removed, c.Added, c.Kept = r.Removed, r.Added, r.Kept
	out := encode(r.Text, enc)
	c.Changed = !bytes.Equal(out, raw)
	if !c.Changed || o.DryRun {
		return c, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return c, err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

const autoRunKey = `Software\Microsoft\Command Processor`

// updateCmd filters HKCU\...\Command Processor\AutoRun and writes venvctl_hook.cmd.
func updateCmd(exe string, o Options) (Change, error) {
	c := Change{Shell: "CMD", Target: `HKCU\` + autoRunKey + `\AutoRun`}
	hook := hookFile()
	own := ""
	if !o.Remove {
		own = fmt.Sprintf(`if exist "%s" call "%s"`, hook, hook)
	}
	cur, typ := autoRunValue()
	r := filterAutoRun(cur, own)
	c.Removed, c.Added, c.Kept = r.Removed, r.Added, r.Kept
	c.Changed = r.Text != cur
	script, _ := Init("cmd", exe)
	if old, _ := os.ReadFile(hook); !o.Remove && string(old) != script {
		c.Changed = true
	}
	clinkNote(&c, exe, o) // honours DryRun itself
	if o.DryRun {
		return c, nil
	}
	if o.Remove {
		os.Remove(hook)
	} else {
		os.MkdirAll(envmgr.Dir(), 0o755)
		if err := os.WriteFile(hook, []byte(script), 0o644); err != nil {
			return c, err
		}
	}
	if r.Text == cur {
		return c, nil
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, autoRunKey, registry.SET_VALUE)
	if err != nil {
		return c, fmt.Errorf("AutoRun: %w", err)
	}
	defer k.Close()
	switch {
	case r.Text == "":
		err = k.DeleteValue("AutoRun")
	case typ == registry.EXPAND_SZ:
		err = k.SetExpandStringValue("AutoRun", r.Text)
	default:
		err = k.SetStringValue("AutoRun", r.Text)
	}
	if err != nil {
		return c, fmt.Errorf("AutoRun: %w", err)
	}
	return c, nil
}

const clinkMarker = "-- venvctl auto-activate for Clink"

// clinkNote keeps %LOCALAPPDATA%\clink\venvctl.lua in step: written when auto-activate is on and
// Clink is installed, deleted when auto-activate is off or on --remove (only if venvctl wrote it).
// Plain CMD has no per-prompt hook, so without Clink auto-activate is skipped with a note.
func clinkNote(c *Change, exe string, o Options) {
	p := ClinkScript()
	old, _ := os.ReadFile(p)
	ours := bytes.HasPrefix(old, []byte(clinkMarker))
	switch {
	case o.Remove || !AutoEnabled():
		if ours {
			c.Notes = append(c.Notes, "removed Clink auto-activate script")
			c.Changed = true
			if !o.DryRun {
				os.Remove(p)
			}
		}
	case !HasClink():
		c.Notes = append(c.Notes, "auto-activate in CMD needs Clink (not found), skipped")
	default:
		want := clinkLua(exe)
		if string(old) != want {
			c.Notes = append(c.Notes, "wrote Clink auto-activate script "+p)
			c.Changed = true
			if !o.DryRun {
				os.MkdirAll(filepath.Dir(p), 0o755)
				os.WriteFile(p, []byte(want), 0o644)
			}
		}
	}
}

func autoRunValue() (string, uint32) {
	k, err := registry.OpenKey(registry.CURRENT_USER, autoRunKey, registry.QUERY_VALUE)
	if err != nil {
		return "", registry.SZ
	}
	defer k.Close()
	v, typ, _ := k.GetStringValue("AutoRun")
	return v, typ
}

// decode reads UTF-16 (with BOM), UTF-8 with BOM, or plain bytes as text, and names the
// encoding so encode can write it back the same way.
func decode(b []byte) (string, string) {
	switch {
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}), bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		le := b[0] == 0xFF
		u := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			if le {
				u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
			} else {
				u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
			}
		}
		if le {
			return string(utf16.Decode(u)), "utf16le"
		}
		return string(utf16.Decode(u)), "utf16be"
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return string(b[3:]), "utf8bom"
	}
	return string(b), "utf8"
}

func encode(s, enc string) []byte {
	switch enc {
	case "utf16le", "utf16be":
		u := utf16.Encode([]rune(s))
		out := []byte{0xFF, 0xFE}
		if enc == "utf16be" {
			out = []byte{0xFE, 0xFF}
		}
		for _, x := range u {
			if enc == "utf16le" {
				out = append(out, byte(x), byte(x>>8))
			} else {
				out = append(out, byte(x>>8), byte(x))
			}
		}
		return out
	case "utf8bom":
		return append([]byte{0xEF, 0xBB, 0xBF}, s...)
	}
	return []byte(s)
}
