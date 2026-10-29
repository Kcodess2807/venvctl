// Package doctor diagnoses the Windows Python setup: PATH order, Store stubs, ExecutionPolicy,
// long paths, VC++ runtime, uv, and whether the pygo shell hooks are installed.
package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"

	"pygo/internal/discovery"
	"pygo/internal/envmgr"
	"pygo/internal/shell"
	"pygo/internal/winapi"
)

type Check struct {
	Name   string
	OK     bool
	Warn   bool // not a failure, but worth knowing
	Detail string
	Fix    string
}

// Status is "ok", "warn" or "fail". Warn wins over OK=false.
func (c Check) Status() string {
	switch {
	case c.Warn:
		return "warn"
	case !c.OK:
		return "fail"
	}
	return "ok"
}

// Run executes every check. exe is the running pygo.exe, which hooks must point at.
func Run(exe string) []Check {
	return []Check{
		pythonOnPath(),
		storeAlias(),
		interpreters(),
		execPolicy(),
		longPaths(),
		vcRuntime(),
		uv(),
		hooks(exe),
		cloudVenvs(),
	}
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

func pythonOnPath() Check {
	ps := pythonsOnPath()
	c := Check{Name: "python on PATH"}
	switch {
	case len(ps) == 0:
		c.Warn, c.Detail, c.Fix = true, "no python.exe on PATH", "install Python (python.org or `uv python install`) or use `pygo create` which finds installs via the registry"
	case strings.EqualFold(filepath.Dir(ps[0]), windowsApps()):
		c.Detail = "`python` resolves to the Microsoft Store stub: " + ps[0]
		c.Fix = "Settings > Apps > Advanced app settings > App execution aliases: turn off python.exe and python3.exe, or install Python and move it ahead of WindowsApps in PATH"
	default:
		c.OK = true
		c.Detail = ps[0]
		if len(ps) > 1 {
			c.Detail += fmt.Sprintf(" (+%d more later in PATH)", len(ps)-1)
		}
	}
	return c
}

func storeAlias() Check {
	c := Check{Name: "Store app execution alias", OK: true}
	var stubs []string
	for _, n := range []string{"python.exe", "python3.exe"} {
		if p := filepath.Join(windowsApps(), n); winapi.IsAppExecAlias(p) {
			stubs = append(stubs, n)
		}
	}
	if len(stubs) == 0 {
		c.Detail = "no python stubs in WindowsApps"
		return c
	}
	c.Warn = true
	c.Detail = strings.Join(stubs, ", ") + " stubs present in " + windowsApps()
	c.Fix = "harmless while a real Python precedes WindowsApps in PATH; disable them under App execution aliases to be safe"
	return c
}

func interpreters() Check {
	its := discovery.Interpreters()
	c := Check{Name: "registered interpreters (PEP 514)", OK: len(its) > 0}
	if len(its) == 0 {
		c.Warn, c.OK = true, true
		c.Detail = "none in the registry (uv-managed Pythons are still usable via `pygo create --python X.Y`)"
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
		}
	}
	c.Detail = strings.Join(seen, ", ")
	if len(bad) > 0 {
		c.OK = false
		c.Fix = strings.Join(bad, "; ") + ". $PROFILE will not load, so the pygo hook is inactive."
		if strings.Contains(c.Fix, "Policy") { // MachinePolicy/UserPolicy = Group Policy, user cannot override
			c.Fix += " Group Policy wins over -ExecutionPolicy Bypass; ask IT, or start shells with: powershell -NoExit -Command \"pygo init pwsh | Out-String | iex\""
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
	c.Fix = "a venv is ~5-25k files; move it out (`pygo create <name>` makes a central env) and nuke the synced copy"
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
	c.Warn, c.Detail = true, "disabled; deep site-packages trees can exceed 260 chars"
	c.Fix = `admin: reg add HKLM\SYSTEM\CurrentControlSet\Control\FileSystem /v LongPathsEnabled /t REG_DWORD /d 1 /f`
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
	c.Warn, c.Detail, c.Fix = true, "not installed; `pygo create` falls back to python -m venv", "powershell -c \"irm https://astral.sh/uv/install.ps1 | iex\""
	return c
}

// hooks checks every shell's hook against exe, the running pygo.exe.
func hooks(exe string) Check {
	c := Check{Name: "pygo shell hooks", OK: true}
	var have, problems []string
	fix := ""
	for _, h := range shell.Status(exe) {
		switch h.State {
		case "ok":
			have = append(have, h.Shell)
		case "stale":
			problems = append(problems, fmt.Sprintf("%s: stale hook (%s)", h.Shell, h.Detail))
			fix = "run `pygo init` to point the hooks at " + exe + ", then restart the shell"
		case "removed":
			problems = append(problems, "cmd: pyGo CMD hook removed ("+h.Detail+")")
			if fix == "" {
				fix = "run `pygo init cmd`"
			}
		default:
			problems = append(problems, h.Shell+": missing")
			if fix == "" {
				fix = "run `pygo init` and restart the shell"
			}
		}
	}
	c.Detail = "installed: " + orNone(have)
	if len(problems) > 0 {
		c.Warn = true
		c.Detail += "; " + strings.Join(problems, "; ")
		c.Fix = fix
	}
	return c
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
