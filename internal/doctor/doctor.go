// Package doctor diagnoses the Windows Python setup: PATH order, Store stubs, ExecutionPolicy,
// long paths, VC++ runtime, uv, and whether the venvctl shell hooks are installed.
package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/sys/windows/registry"

	"venvctl/internal/discovery"
	"venvctl/internal/envmgr"
	"venvctl/internal/shell"
	"venvctl/internal/winapi"
)

type Check struct {
	Name   string
	OK     bool
	Warn   bool // not a failure, but worth knowing
	Info   bool // informational only: nothing to fix right now
	Detail string
	Fix    string
}

// Status is "ok", "info", "warn" or "fail". Warn wins over OK=false.
func (c Check) Status() string {
	switch {
	case c.Warn:
		return "warn"
	case !c.OK:
		return "fail"
	case c.Info:
		return "info"
	}
	return "ok"
}

// Run executes every check. exe is the running venvctl.exe, which hooks must point at.
func Run(exe string) []Check {
	checks := []Check{
		pythonOnPath(),
		interpreters(),
		execPolicy(),
		longPaths(),
		vcRuntime(),
		uv(),
	}
	checks = append(checks, hooks(exe)...)
	return append(checks, oldStateDirs(), cloudVenvs(), ideDiscovery())
}

// ideDiscovery warns about central envs an editor cannot see: no project points at them with a
// .venv redirect, and no VS Code-family settings.json lists the central folder. Read-only.
func ideDiscovery() Check {
	c := Check{Name: "IDE discovery of central envs", OK: true}
	var central, unlinked []string
	for _, e := range envmgr.LoadRegistry().Envs {
		if !e.Central {
			continue
		}
		central = append(central, e.Name)
		if d := envmgr.Inspect(e.Project); e.Project != "" && d.Kind == "file" && envmgr.SamePath(d.Target, e.Path) {
			continue
		}
		unlinked = append(unlinked, e.Name)
	}
	switch {
	case len(central) == 0:
		c.Detail = "no central envs"
		return c
	case len(unlinked) == 0:
		c.Detail = fmt.Sprintf("all %d central envs are linked from a project .venv", len(central))
		return c
	}
	if f := settingsCovering(envmgr.CentralDir()); f != "" {
		c.Detail = "central folder is listed in " + f
		return c
	}
	rel, err := filepath.Rel(os.Getenv("USERPROFILE"), envmgr.CentralDir())
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = envmgr.CentralDir()
	}
	c.Warn = true
	c.Detail = fmt.Sprintf("%d of %d central envs have no project .venv redirect: %s", len(unlinked), len(central), strings.Join(unlinked, ", "))
	c.Fix = "run `venvctl link <name>` in each project folder, or add to VS Code settings.json: " +
		`"python.venvFolders": ["` + strings.ReplaceAll(rel, `\`, `\\`) + `"]`
	return c
}

// vscodeSettings are the user settings files of VS Code and its common forks.
func vscodeSettings() []string {
	var out []string
	for _, app := range []string{"Code", "Code - Insiders", "Cursor", "VSCodium"} {
		out = append(out, filepath.Join(os.Getenv("APPDATA"), app, "User", "settings.json"))
	}
	return out
}

// settingsCovering returns the first settings.json whose python.venvFolders,
// python-envs.workspaceSearchPaths or python-envs.globalSearchPaths names dir.
func settingsCovering(dir string) string {
	for _, f := range vscodeSettings() {
		b, err := os.ReadFile(f)
		if err == nil && covers(b, dir) {
			return f
		}
	}
	return ""
}

// covers reports whether settings JSON (comments and trailing commas allowed) lists dir in a
// venv search setting. Relative entries are relative to the home folder; ~ and ${userHome}
// expand to it; an entry ending in ** covers everything below it.
func covers(settings []byte, dir string) bool {
	var m map[string]any
	if json.Unmarshal(stripJSONC(settings), &m) != nil {
		return false
	}
	home := os.Getenv("USERPROFILE")
	for _, key := range []string{"python.venvFolders", "python-envs.workspaceSearchPaths", "python-envs.globalSearchPaths"} {
		list, _ := m[key].([]any)
		for _, v := range list {
			s, _ := v.(string)
			s = strings.ReplaceAll(s, "${userHome}", home)
			if strings.HasPrefix(s, "~") {
				s = home + s[1:]
			}
			deep := false
			for _, g := range []string{"/**", `\**`, "/*", `\*`} {
				if t, ok := strings.CutSuffix(s, g); ok {
					s, deep = t, strings.HasSuffix(g, "**")
				}
			}
			if s == "" {
				continue
			}
			if !filepath.IsAbs(s) {
				s = filepath.Join(home, s)
			}
			if envmgr.SamePath(s, dir) || (deep && strings.HasPrefix(strings.ToLower(filepath.Clean(dir)), strings.ToLower(filepath.Clean(s))+`\`)) {
				return true
			}
		}
	}
	return false
}

var reTrailingComma = regexp.MustCompile(`,(\s*[}\]])`)

// stripJSONC removes // and /* */ comments outside strings, then trailing commas, so VS Code's
// settings.json parses as JSON.
func stripJSONC(b []byte) []byte {
	var out []byte
	inStr, esc := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case inStr:
			out = append(out, c)
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			for i += 2; i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/'); i++ {
			}
			i++
		default:
			out = append(out, c)
		}
	}
	return reTrailingComma.ReplaceAll(out, []byte("$1"))
}

// oldStateDirs warns while the pyGo or WinVenv state folders are still on disk.
func oldStateDirs() Check {
	c := Check{Name: "old state folders", OK: true, Detail: "none"}
	var found []string
	pygo, winvenv := envmgr.LegacyDirs()
	for _, d := range []string{pygo, winvenv} {
		if fileExists(d) {
			found = append(found, d)
		}
	}
	if len(found) > 0 {
		c.Warn = true
		c.Detail = strings.Join(found, ", ") + " still present"
		c.Fix = "delete it after confirming `venvctl list` shows your envs (and no shell hook still calls an exe inside it)"
	}
	return c
}

func windowsApps() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WindowsApps")
}

func pythonsOnPath() []string {
	var out []string
	for _, d := range strings.Split(os.Getenv("PATH"), ";") {
		d = strings.Trim(d, `" `)
		if d == "" {
			continue
		}
		if p := filepath.Join(d, "python.exe"); fileExists(p) {
			out = append(out, p)
		}
	}
	return out
}

// aliasOwners classifies App Execution Aliases by the package that owns them. Verified against:
// PyManager's own check (python/pymanager src/manage/firstrun.py, check_app_alias, which expects
// PythonSoftwareFoundation.PythonManager_* identities); a Store CPython install
// (PythonSoftwareFoundation.Python.3.12_qbz5n2kfra8p0); and this machine's App Installer stub
// (Microsoft.DesktopAppInstaller_8wekyb3d8bbwe, target AppInstallerPythonRedirector.exe).
var aliasOwners = []struct{ prefix, kind, label string }{
	{"Microsoft.DesktopAppInstaller_", "stub", "the Microsoft Store redirector (App Installer)"},
	{"PythonSoftwareFoundation.PythonManager_", "python", "the Python install manager (PyManager)"},
	{"PythonSoftwareFoundation.Python.", "python", "a Microsoft Store CPython"},
}

const stubTarget = `\appinstallerpythonredirector.exe`

// classifyAlias reads a WindowsApps alias: kind is "stub", "python", "unknown", or "" when the file
// is not an App Execution Alias.
func classifyAlias(p string) (kind, label string, l winapi.AppExecLink) {
	l, err := winapi.ReadAppExecLink(p)
	if err != nil {
		return "", "", l
	}
	kind, label = classifyLink(l)
	return kind, label, l
}

// classifyLink is classifyAlias's pure part, from the alias's package and target.
func classifyLink(l winapi.AppExecLink) (kind, label string) {
	if strings.HasSuffix(strings.ToLower(l.Target), stubTarget) {
		return "stub", aliasOwners[0].label
	}
	for _, o := range aliasOwners {
		if strings.HasPrefix(l.Package, o.prefix) {
			return o.kind, o.label
		}
	}
	return "unknown", "an unrecognised alias (package " + l.Package + ")"
}

// pyManagerInstalled: PyManager owns the pymanager.exe (and py/python when enabled) aliases.
// Detected from the aliases only; venvctl never runs py.exe to find out.
func pyManagerInstalled() bool {
	for _, n := range []string{"pymanager.exe", "py.exe", "python.exe"} {
		if _, _, l := classifyAlias(filepath.Join(windowsApps(), n)); strings.HasPrefix(l.Package, aliasOwners[1].prefix) {
			return true
		}
	}
	return false
}

// pythonOnPath reports what `python` runs, including what a WindowsApps alias really is:
// the Store redirector fails, a PyManager or Store CPython alias passes.
func pythonOnPath() Check {
	ps := pythonsOnPath()
	c := Check{Name: "python on PATH", OK: true}
	fix := `Settings > Apps > Advanced app settings > App execution aliases: turn off the "App Installer" python.exe and python3.exe, or install Python from python.org (the Python install manager), whose aliases run a real Python`
	if pyManagerInstalled() {
		fix = "run `py install --configure` (PyManager repairs its aliases)"
	}
	switch {
	case len(ps) == 0:
		c.Warn, c.Detail = true, "no python.exe on PATH"
		c.Fix = "install Python (python.org or `uv python install`); `venvctl create` also finds installs via the registry"
	case strings.EqualFold(filepath.Dir(ps[0]), windowsApps()):
		kind, label, l := classifyAlias(ps[0])
		switch kind {
		case "stub":
			c.OK = false
			c.Detail = "`python` is " + label + ", which opens the Microsoft Store instead of running Python"
			c.Fix = fix
		case "python":
			c.Detail = "`python` is the alias of " + label + ": " + l.Target
		case "unknown":
			c.Warn = true
			c.Detail = "`python` is " + label + ": " + l.Target
			c.Fix = "check Settings > Apps > Advanced app settings > App execution aliases"
		default:
			c.Detail = ps[0]
		}
	default:
		c.Detail = ps[0]
		if len(ps) > 1 {
			c.Detail += fmt.Sprintf(" (+%d more later in PATH)", len(ps)-1)
		}
		for _, n := range []string{"python.exe", "python3.exe"} {
			if k, _, _ := classifyAlias(filepath.Join(windowsApps(), n)); k == "stub" {
				c.Detail += "; Store redirector aliases in WindowsApps are shadowed (harmless)"
				break
			}
		}
	}
	return c
}

func interpreters() Check {
	its := discovery.Interpreters()
	c := Check{Name: "registered interpreters (PEP 514)", OK: true}
	if len(its) == 0 {
		c.Warn = true
		c.Detail = "none in the registry (uv-managed Pythons are still usable via `venvctl create --python X.Y`)"
		return c
	}
	var vs []string
	for _, it := range its {
		v := it.Version
		if v == "" {
			v = it.Company + "/" + it.Tag
		}
		if it.Arch != "" {
			v += " (" + it.Arch + ")"
		}
		vs = append(vs, v)
	}
	c.Detail = strings.Join(vs, ", ")
	return c
}

func execPolicy() Check {
	c := Check{Name: "PowerShell ExecutionPolicy", OK: true}
	var bad, seen []string
	gpo := false // MachinePolicy/UserPolicy decide: Group Policy, which the user cannot override
	for _, ps := range []string{"pwsh", "powershell"} {
		if _, err := exec.LookPath(ps); err != nil {
			continue
		}
		// -List is in precedence order: MachinePolicy, UserPolicy, Process, CurrentUser, LocalMachine.
		// The first non-Undefined scope decides; Undefined everywhere means Restricted on client Windows.
		out, err := exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command",
			`Get-ExecutionPolicy -List | ForEach-Object { "$($_.Scope)=$($_.ExecutionPolicy)" }`).Output()
		if err != nil {
			continue
		}
		scope, pol := "default", "Restricted"
		for _, line := range strings.Fields(string(out)) {
			s, p, _ := strings.Cut(line, "=")
			if p != "Undefined" && s != "Process" { // Process is just this probe's own shell
				scope, pol = s, p
				break
			}
		}
		seen = append(seen, fmt.Sprintf("%s=%s (%s)", ps, pol, scope))
		if pol == "Restricted" || pol == "AllSigned" {
			bad = append(bad, fmt.Sprintf("%s: %s set by %s", ps, pol, scope))
			gpo = gpo || strings.HasSuffix(scope, "Policy")
		}
	}
	c.Detail = strings.Join(seen, ", ")
	if len(bad) > 0 {
		c.OK = false
		c.Fix = strings.Join(bad, "; ") + ". $PROFILE will not load, so the venvctl hook is inactive."
		if gpo {
			c.Fix += " Group Policy wins over -ExecutionPolicy Bypass; ask IT, or start shells with: powershell -NoExit -Command \"venvctl init pwsh | Out-String | iex\""
		} else {
			c.Fix += " Run: Set-ExecutionPolicy -Scope CurrentUser RemoteSigned"
		}
	}
	return c
}

// cloudVenvs flags registered venvs inside synced folders: thousands of files upload, and locks follow.
func cloudVenvs() Check {
	c := Check{Name: "venvs in cloud-synced folders", OK: true}
	var hits []string
	for _, e := range envmgr.LoadRegistry().Envs {
		if envmgr.CloudRoot(e.Path) != "" {
			hits = append(hits, e.Path)
		}
	}
	if len(hits) == 0 {
		c.Detail = "none"
		return c
	}
	c.Warn = true
	c.Detail = strings.Join(hits, ", ")
	c.Fix = "a venv is ~5-25k files; move it out (`venvctl create <name>` makes a central env) and nuke the synced copy"
	return c
}

func longPaths() Check {
	c := Check{Name: "LongPathsEnabled"}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\FileSystem`, registry.QUERY_VALUE)
	if err == nil {
		defer k.Close()
		if v, _, err := k.GetIntegerValue("LongPathsEnabled"); err == nil && v == 1 {
			c.OK, c.Detail = true, "enabled"
			return c
		}
	}
	c.Fix = `admin: reg add HKLM\SYSTEM\CurrentControlSet\Control\FileSystem /v LongPathsEnabled /t REG_DWORD /d 1 /f`
	if pyManagerInstalled() {
		c.Fix = "run `py install --configure` (it offers to enable long paths)"
	}
	if envmgr.UvAtLeast(0, 12, 14) {
		// uv 0.12.14+ installs packages past MAX_PATH by itself; pip and other tools may still fail.
		c.OK, c.Info = true, true
		c.Detail = "disabled; uv handles long paths itself (0.12.14+), but pip and some tools can still fail on deep paths"
		return c
	}
	c.Warn, c.Detail = true, "disabled; deep site-packages trees can exceed 260 chars"
	return c
}

func vcRuntime() Check {
	c := Check{Name: "VC++ 2015-2022 runtime"}
	p := filepath.Join(os.Getenv("SystemRoot"), "System32", "vcruntime140.dll")
	if fileExists(p) {
		c.OK, c.Detail = true, p
		return c
	}
	c.Detail = "vcruntime140.dll missing; CPython and many wheels will not start"
	c.Fix = "install https://aka.ms/vs/17/release/vc_redist.x64.exe"
	return c
}

func uv() Check {
	c := Check{Name: "uv"}
	if p, err := exec.LookPath("uv"); err == nil {
		c.OK, c.Detail = true, p
		return c
	}
	c.Warn, c.Detail, c.Fix = true, "not installed; `venvctl create` falls back to python -m venv", "powershell -c \"irm https://astral.sh/uv/install.ps1 | iex\""
	return c
}

// hooks checks every shell's hook against exe, the running venvctl.exe.
// hooks checks every shell's hook against exe, the running venvctl.exe, and adds one
// "legacy hook found" warning per shell that still has WinVenv or pyGo hook lines.
func hooks(exe string) []Check {
	c := Check{Name: "venvctl shell hooks", OK: true}
	var have, problems []string
	var legacy []Check
	fix := ""
	for _, h := range shell.Status(exe) {
		if len(h.Legacy) > 0 {
			legacy = append(legacy, Check{
				Name: "legacy hook found (" + strings.Join(h.Legacy, "/") + ")", OK: true, Warn: true,
				Detail: h.Shell + ": " + h.Detail, Fix: "run `venvctl init` (it removes old hooks; `venvctl init --dry-run` previews)",
			})
		}
		switch h.State {
		case "ok":
			have = append(have, h.Shell)
		case "stale":
			problems = append(problems, fmt.Sprintf("%s: stale hook (%s)", h.Shell, h.Detail))
			fix = "run `venvctl init` to point the hooks at " + exe + ", then restart the shell"
		case "removed":
			// VS Code's shellStartup mode rewrites the same AutoRun value; say so on its own line.
			legacy = append(legacy, Check{
				Name: "venvctl CMD hook removed (AutoRun was rewritten, probably by VS Code)", OK: true, Warn: true,
				Detail: h.Detail, Fix: "run `venvctl init cmd` (it keeps VS Code's AutoRun entry and adds venvctl's back)",
			})
			problems = append(problems, "cmd: removed from AutoRun")
		default:
			problems = append(problems, h.Shell+": missing")
			if fix == "" {
				fix = "run `venvctl init` and restart the shell"
			}
		}
	}
	if len(have) == 0 {
		have = []string{"none"}
	}
	c.Detail = "installed: " + strings.Join(have, ", ")
	if len(problems) > 0 {
		c.Warn = true
		c.Detail += "; " + strings.Join(problems, "; ")
		c.Fix = fix
	}
	return append([]Check{c}, legacy...)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
