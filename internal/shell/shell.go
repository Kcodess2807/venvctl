// Package shell generates hooks for PowerShell, CMD and Git Bash. A compiled binary cannot
// change its parent shell's environment, so each shell gets a thin wrapper that evaluates
// the script `venvctl __shell <shell> on|off|cd ...` prints.
package shell

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"venvctl/internal/envmgr"
)

// Handled reports whether a subcommand must run through the shell wrapper.
func Handled(cmd string) bool {
	switch cmd {
	case "on", "activate", "off", "deactivate", "cd", "auto":
		return true
	}
	return false
}

// EnvFile is where `venvctl __shell cmd` writes the batch script the CMD doskey macro then calls.
func EnvFile() string { return filepath.Join(envmgr.Dir(), "venvctl_env.cmd") }

// Init returns the wrapper definition for sh, for the user to eval in their profile.
func Init(sh, exe string) (string, error) {
	switch sh {
	case "pwsh", "powershell":
		return fmt.Sprintf(`function global:venvctl {
    if ($args.Count -gt 0 -and $args[0] -in 'on','activate','off','deactivate','cd') {
        # PS 5.1 decodes native output with the OEM codepage; force UTF-8 so non-ASCII paths survive.
        $enc = [Console]::OutputEncoding; [Console]::OutputEncoding = [Text.Encoding]::UTF8
        try { $s = & '%s' __shell pwsh @args | Out-String } finally { [Console]::OutputEncoding = $enc }
        if ($LASTEXITCODE -eq 0 -and $s) { Invoke-Expression $s }
    } else { & '%s' @args }
}
`, psq(exe), psq(exe)) + autoIf(sh, exe), nil
	case "bash":
		e := posix(exe)
		return fmt.Sprintf(`venvctl() {
  case "$1" in
    on|activate|off|deactivate|cd) local s; s="$('%s' __shell bash "$@")" && eval "$s" ;;
    *) '%s' "$@" ;;
  esac
}
`, e, e) + autoIf(sh, exe), nil
	case "cmd":
		return fmt.Sprintf("@doskey venvctl=\"%s\" __shell cmd $* $T call \"%s\"\r\n", exe, EnvFile()), nil
	}
	return "", fmt.Errorf("unknown shell %q (pwsh, cmd, bash)", sh)
}

// autoIf is the auto-activate block when `venvctl config auto-activate on` is set.
// VENVCTL_AUTO_ACTIVATE=1 forces it (testdata\autocd.ps1 and bench-hook use that).
func autoIf(sh, exe string) string {
	if AutoEnabled() || os.Getenv("VENVCTL_AUTO_ACTIVATE") == "1" {
		return AutoInit(sh, exe)
	}
	return ""
}

// Activate returns the script that puts the venv at path on PATH and sets the prompt.
func Activate(sh, path, name string) string { return legacyVars(sh) + activate(sh, path, name) }

func activate(sh, path, name string) string {
	switch sh {
	case "pwsh", "powershell":
		return savePath(sh) + fmt.Sprintf(`$env:VIRTUAL_ENV = '%[1]s'
$env:VIRTUAL_ENV_PROMPT = '%[2]s'
$env:PATH = '%[1]s\Scripts;' + $env:_VENVCTL_OLD_PATH
Remove-Item Env:PYTHONHOME -ErrorAction SilentlyContinue
# oh-my-posh / starship own the prompt and render VIRTUAL_ENV themselves; only wrap a plain prompt.
if (-not ($env:POSH_THEME -or $env:POSH_SHELL -or $env:STARSHIP_SHELL)) {
    if (-not (Test-Path function:global:_venvctl_old_prompt)) { ${function:global:_venvctl_old_prompt} = ${function:prompt} }
    function global:prompt { "($env:VIRTUAL_ENV_PROMPT) " + (& $function:global:_venvctl_old_prompt) }
}
Write-Host "activated %[2]s" -ForegroundColor Green
`, psq(path), psq(name))
	case "cmd":
		return "@echo off\r\n" + savePath(sh) + fmt.Sprintf(`set "VIRTUAL_ENV=%[1]s"
set "VIRTUAL_ENV_PROMPT=%[2]s"
set "PATH=%[1]s\Scripts;%%_VENVCTL_OLD_PATH%%"
set PYTHONHOME=
if not defined PROMPT set PROMPT=$P$G
if not defined _VENVCTL_OLD_PROMPT set "_VENVCTL_OLD_PROMPT=%%PROMPT%%"
set "PROMPT=(%[2]s) %%_VENVCTL_OLD_PROMPT%%"
echo activated %[2]s
`, path, name)
	default: // bash
		p := posix(path)
		return savePath(sh) + fmt.Sprintf(`export VIRTUAL_ENV='%[1]s'
export VIRTUAL_ENV_PROMPT='%[2]s'
export PATH='%[1]s/Scripts':"$_VENVCTL_OLD_PATH"
unset PYTHONHOME
if [ -z "${POSH_SHELL:-}${STARSHIP_SHELL:-}" ]; then
  if [ -z "${_VENVCTL_OLD_PS1+x}" ]; then export _VENVCTL_OLD_PS1="$PS1"; fi
  PS1="(%[2]s) $_VENVCTL_OLD_PS1"
fi
echo "activated %[2]s"
`, p, name)
	}
}

// savePath stores the PATH to restore on `off`, once. If VIRTUAL_ENV is already set at that point,
// someone else activated it (VS Code, an activate script): its Scripts folder is left out of the
// saved PATH, so `off` does not bring that venv back onto PATH without its VIRTUAL_ENV.
func savePath(sh string) string {
	switch sh {
	case "pwsh", "powershell":
		return `if (-not $env:_VENVCTL_OLD_PATH) {
    $p = $env:PATH
    if ($env:VIRTUAL_ENV) {
        $s = $env:VIRTUAL_ENV.TrimEnd('\') + '\Scripts'
        $p = (($p -split ';') | Where-Object { $_ -and $_.TrimEnd('\') -ne $s }) -join ';'
    }
    $env:_VENVCTL_OLD_PATH = $p
}
`
	case "cmd":
		return "if not defined _VENVCTL_OLD_PATH set \"_VENVCTL_NEW=1\"\r\n" +
			"if defined _VENVCTL_NEW set \"_VENVCTL_OLD_PATH=;%PATH%;\"\r\n" +
			"if defined _VENVCTL_NEW if defined VIRTUAL_ENV call set \"_VENVCTL_OLD_PATH=%%_VENVCTL_OLD_PATH:;%VIRTUAL_ENV%\\Scripts;=;%%\"\r\n" +
			"if defined _VENVCTL_NEW call set \"_VENVCTL_OLD_PATH=%%_VENVCTL_OLD_PATH:~1,-1%%\"\r\n" +
			"set _VENVCTL_NEW=\r\n"
	}
	return `if [ -z "${_VENVCTL_OLD_PATH:-}" ]; then
  _VENVCTL_OLD_PATH=":$PATH:"
  if [ -n "${VIRTUAL_ENV:-}" ]; then _s=":${VIRTUAL_ENV%/}/Scripts:"; _VENVCTL_OLD_PATH="${_VENVCTL_OLD_PATH//"$_s"/:}"; unset _s; fi
  _VENVCTL_OLD_PATH="${_VENVCTL_OLD_PATH#:}"; _VENVCTL_OLD_PATH="${_VENVCTL_OLD_PATH%:}"
  export _VENVCTL_OLD_PATH
fi
`
}

// legacyVars adopts the save variables of a shell activated before the rename from pyGo
// (_PYGO_OLD_PATH, _PYGO_OLD_PROMPT, _PYGO_OLD_PS1, PowerShell's _pygo_old_prompt) when
// venvctl's own are unset, then clears the old ones. So `off` restores the original PATH and
// prompt, and `on` does not save a PATH that still holds the old venv.
// PowerShell note: Get-Item and Remove-Item ignore a "function:global:" scope prefix, so prompt
// functions are read and written as ${function:global:name} and removed by unqualified path.
func legacyVars(sh string) string {
	switch sh {
	case "pwsh", "powershell":
		return `if (-not $env:_VENVCTL_OLD_PATH -and $env:_PYGO_OLD_PATH) { $env:_VENVCTL_OLD_PATH = $env:_PYGO_OLD_PATH }
Remove-Item Env:_PYGO_OLD_PATH -ErrorAction SilentlyContinue
if (-not (Test-Path function:global:_venvctl_old_prompt) -and (Test-Path function:global:_pygo_old_prompt)) { ${function:global:_venvctl_old_prompt} = ${function:global:_pygo_old_prompt} }
if (Test-Path function:global:_pygo_old_prompt) { Remove-Item function:_pygo_old_prompt }
`
	case "cmd":
		return "@echo off\r\n" +
			"if not defined _VENVCTL_OLD_PATH if defined _PYGO_OLD_PATH set \"_VENVCTL_OLD_PATH=%_PYGO_OLD_PATH%\"\r\n" +
			"if not defined _VENVCTL_OLD_PROMPT if defined _PYGO_OLD_PROMPT set \"_VENVCTL_OLD_PROMPT=%_PYGO_OLD_PROMPT%\"\r\n" +
			"set _PYGO_OLD_PATH=\r\nset _PYGO_OLD_PROMPT=\r\n"
	default:
		return `if [ -z "${_VENVCTL_OLD_PATH:-}" ] && [ -n "${_PYGO_OLD_PATH:-}" ]; then export _VENVCTL_OLD_PATH="$_PYGO_OLD_PATH"; fi
if [ -z "${_VENVCTL_OLD_PS1+x}" ] && [ -n "${_PYGO_OLD_PS1+x}" ]; then export _VENVCTL_OLD_PS1="$_PYGO_OLD_PS1"; fi
unset _PYGO_OLD_PATH _PYGO_OLD_PS1
`
	}
}

// Deactivate returns the script that undoes Activate.
func Deactivate(sh string) string { return legacyVars(sh) + deactivate(sh) }

func deactivate(sh string) string {
	switch sh {
	case "pwsh", "powershell":
		return `if ($env:_VENVCTL_OLD_PATH) { $env:PATH = $env:_VENVCTL_OLD_PATH; Remove-Item Env:_VENVCTL_OLD_PATH }
Remove-Item Env:VIRTUAL_ENV, Env:VIRTUAL_ENV_PROMPT -ErrorAction SilentlyContinue
if (Test-Path function:global:_venvctl_old_prompt) { ${function:global:prompt} = ${function:global:_venvctl_old_prompt}; Remove-Item function:_venvctl_old_prompt }
Remove-Item Env:_VENVCTL_AUTO -ErrorAction SilentlyContinue
Write-Host "deactivated"
`
	case "cmd":
		return `@echo off
if defined _VENVCTL_OLD_PATH set "PATH=%_VENVCTL_OLD_PATH%"
set _VENVCTL_OLD_PATH=
set VIRTUAL_ENV=
set VIRTUAL_ENV_PROMPT=
if defined _VENVCTL_OLD_PROMPT set "PROMPT=%_VENVCTL_OLD_PROMPT%"
set _VENVCTL_OLD_PROMPT=
set _VENVCTL_AUTO=
echo deactivated
`
	default:
		return `if [ -n "${_VENVCTL_OLD_PATH:-}" ]; then export PATH="$_VENVCTL_OLD_PATH"; unset _VENVCTL_OLD_PATH; fi
unset VIRTUAL_ENV VIRTUAL_ENV_PROMPT _VENVCTL_AUTO
if [ -n "${_VENVCTL_OLD_PS1+x}" ]; then PS1="$_VENVCTL_OLD_PS1"; unset _VENVCTL_OLD_PS1; fi
echo deactivated
`
	}
}

// ActivateFor is Activate plus the clink format, which is computed here from the environment.
func ActivateFor(sh, path, name string) string {
	if sh == "clink" {
		return clinkActivate(path, name)
	}
	return Activate(sh, path, name)
}

// DeactivateFor is Deactivate plus the clink format.
func DeactivateFor(sh string) string {
	if sh == "clink" {
		return clinkDeactivate() + SetAuto(sh, false)
	}
	return Deactivate(sh)
}

// Cd returns the script that changes the shell's directory.
func Cd(sh, path string) string {
	switch sh {
	case "pwsh", "powershell":
		return fmt.Sprintf("Set-Location -LiteralPath '%s'\n", psq(path))
	case "cmd":
		return fmt.Sprintf("@cd /d \"%s\"\r\n", path)
	default:
		return fmt.Sprintf("cd '%s'\n", posix(path))
	}
}

// Profiles maps each installed PowerShell ("powershell" = 5.1, "pwsh" = 7) to its
// CurrentUserCurrentHost profile path, the file $PROFILE names in a console. It reads the
// Documents known folder instead of spawning PowerShell, which also follows OneDrive redirection.
func Profiles() map[string]string {
	docs, err := windows.KnownFolderPath(windows.FOLDERID_Documents, 0)
	if err != nil {
		docs = filepath.Join(os.Getenv("USERPROFILE"), "Documents")
	}
	m := map[string]string{}
	if _, err := exec.LookPath("powershell"); err == nil {
		m["powershell"] = filepath.Join(docs, "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1")
	}
	if _, err := exec.LookPath("pwsh"); err == nil {
		m["pwsh"] = filepath.Join(docs, "PowerShell", "Microsoft.PowerShell_profile.ps1")
	}
	return m
}

// PowerShells returns the installed PowerShell names from Profiles, sorted.
func PowerShells() []string {
	var names []string
	for k := range Profiles() {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func hookFile() string { return filepath.Join(envmgr.Dir(), "venvctl_hook.cmd") }

// Hook is the install state of one shell's hook.
type Hook struct {
	Shell  string   // powershell, pwsh, cmd, bash
	State  string   // ok, missing, stale, removed
	Detail string   // file or key, and for stale the exe the hook points at
	Legacy []string // WinVenv / pyGo hooks still present (`venvctl init` removes them)
}

// Only lines invoking a venvctl.exe count as venvctl hooks; wv.exe and pygo.exe lines are legacy.
var (
	rePwshHook = regexp.MustCompile(`(?i)'([^']*venvctl\.exe)'\s+init\s+pwsh`)
	reBashHook = regexp.MustCompile(`(?i)'([^']*venvctl\.exe)'\s+init\s+bash`)
	reCmdHook  = regexp.MustCompile(`(?i)doskey\s+venvctl="([^"]+)"`)
)

// Status inspects every shell's hook and compares the exe it calls with exe (the running venvctl).
// A venvctl hook calling another venvctl.exe (moved, or a dev build) is "stale".
func Status(exe string) []Hook {
	var out []Hook
	for _, ps := range PowerShells() {
		profile := Profiles()[ps]
		b, _ := os.ReadFile(profile)
		text, _ := decode(b)
		h := judge(ps, profile, rePwshHook.FindAllStringSubmatch(text, -1), exe)
		h.Legacy = legacyIn(filterProfile(text, "", "\n").Removed)
		out = append(out, h)
	}

	ar := AutoRun()
	var h Hook
	switch {
	case strings.Contains(strings.ToLower(ar), strings.ToLower(hookFile())):
		b, _ := os.ReadFile(hookFile())
		h = judge("cmd", hookFile(), reCmdHook.FindAllStringSubmatch(string(b), -1), exe)
	case fileExists(hookFile()):
		h = Hook{Shell: "cmd", State: "removed", Detail: "AutoRun no longer calls " + hookFile() + " (rewritten, probably by VS Code)"}
	default:
		h = Hook{Shell: "cmd", State: "missing", Detail: `HKCU\Software\Microsoft\Command Processor\AutoRun`}
	}
	h.Legacy = legacyIn(filterAutoRun(ar, "").Removed)
	out = append(out, h)

	rc := filepath.Join(os.Getenv("USERPROFILE"), ".bashrc")
	if _, err := exec.LookPath("bash"); err == nil || fileExists(rc) {
		b, _ := os.ReadFile(rc)
		h := judge("bash", rc, reBashHook.FindAllStringSubmatch(string(b), -1), posixPath(exe))
		h.Legacy = legacyIn(filterProfile(string(b), "", "\n").Removed)
		out = append(out, h)
	}
	return out
}

// legacyIn lists the earlier generations present in a filter's removal counts.
func legacyIn(removed map[string]int) []string {
	var gens []string
	for _, g := range []string{genWinVenv, genPyGo} {
		if removed[g] > 0 {
			gens = append(gens, g)
		}
	}
	return gens
}

// judge picks ok if any hook line calls want, stale if lines exist but call something else.
func judge(sh, where string, matches [][]string, want string) Hook {
	if len(matches) == 0 {
		return Hook{Shell: sh, State: "missing", Detail: where}
	}
	for _, m := range matches {
		if strings.EqualFold(strings.ReplaceAll(m[1], "''", "'"), want) {
			return Hook{Shell: sh, State: "ok", Detail: where}
		}
	}
	return Hook{Shell: sh, State: "stale", Detail: fmt.Sprintf("%s points at %s", where, matches[0][1])}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// AutoRun returns the current HKCU Command Processor AutoRun value.
func AutoRun() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Command Processor`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue("AutoRun")
	return v
}

// psq escapes a string for a single-quoted PowerShell literal.
func psq(s string) string { return strings.ReplaceAll(s, "'", "''") }

// posix converts C:\a\b to /c/a/b for Git Bash.
func posix(p string) string { return strings.ReplaceAll(posixPath(p), "'", `'\''`) }

// posixPath converts C:\a\b to /c/a/b without shell quoting.
func posixPath(p string) string {
	p = filepath.ToSlash(p)
	if len(p) > 1 && p[1] == ':' {
		p = "/" + strings.ToLower(p[:1]) + p[2:]
	}
	return p
}
