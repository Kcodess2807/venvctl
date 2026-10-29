// Package shell generates hooks for PowerShell, CMD and Git Bash. A compiled binary cannot
// change its parent shell's environment, so each shell gets a thin wrapper that evaluates
// the script `pygo __shell <shell> on|off|cd ...` prints.
package shell

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Handled reports whether a subcommand must run through the shell wrapper.
func Handled(cmd string) bool {
	switch cmd {
	case "on", "activate", "off", "deactivate", "cd":
		return true
	}
	return false
}

func hookDir() string { return filepath.Join(os.Getenv("LOCALAPPDATA"), "pygo") }

// EnvFile is where `pygo __shell cmd` writes the batch script the CMD doskey macro then calls.
func EnvFile() string { return filepath.Join(hookDir(), "pygo_env.cmd") }

// Init returns the wrapper definition for sh, for the user to eval in their profile.
func Init(sh, exe string) (string, error) {
	switch sh {
	case "pwsh", "powershell":
		return fmt.Sprintf(`function global:pygo {
    if ($args.Count -gt 0 -and $args[0] -in 'on','activate','off','deactivate','cd') {
        # PS 5.1 decodes native output with the OEM codepage; force UTF-8 so non-ASCII paths survive.
        $enc = [Console]::OutputEncoding; [Console]::OutputEncoding = [Text.Encoding]::UTF8
        try { $s = & '%s' __shell pwsh @args | Out-String } finally { [Console]::OutputEncoding = $enc }
        if ($LASTEXITCODE -eq 0 -and $s) { Invoke-Expression $s }
    } else { & '%s' @args }
}
`, psq(exe), psq(exe)), nil
	case "bash":
		e := posix(exe)
		return fmt.Sprintf(`pygo() {
  case "$1" in
    on|activate|off|deactivate|cd) local s; s="$('%s' __shell bash "$@")" && eval "$s" ;;
    *) '%s' "$@" ;;
  esac
}
`, e, e), nil
	case "cmd":
		return fmt.Sprintf("@doskey pygo=\"%s\" __shell cmd $* $T call \"%s\"\r\n", exe, EnvFile()), nil
	}
	return "", fmt.Errorf("unknown shell %q (pwsh, cmd, bash)", sh)
}

// Activate returns the script that puts the venv at path on PATH and sets the prompt.
func Activate(sh, path, name string) string {
	switch sh {
	case "pwsh", "powershell":
		return fmt.Sprintf(`if (-not $env:_PYGO_OLD_PATH) { $env:_PYGO_OLD_PATH = $env:PATH }
$env:VIRTUAL_ENV = '%[1]s'
$env:VIRTUAL_ENV_PROMPT = '%[2]s'
$env:PATH = '%[1]s\Scripts;' + $env:_PYGO_OLD_PATH
Remove-Item Env:PYTHONHOME -ErrorAction SilentlyContinue
# oh-my-posh / starship own the prompt and render VIRTUAL_ENV themselves; only wrap a plain prompt.
if (-not ($env:POSH_THEME -or $env:POSH_SHELL -or $env:STARSHIP_SHELL)) {
    if (-not (Test-Path function:global:_pygo_old_prompt)) { Set-Item function:global:_pygo_old_prompt (Get-Item function:prompt).ScriptBlock }
    function global:prompt { "($env:VIRTUAL_ENV_PROMPT) " + (& $function:global:_pygo_old_prompt) }
}
Write-Host "activated %[2]s" -ForegroundColor Green
`, psq(path), psq(name))
	case "cmd":
		return fmt.Sprintf(`@echo off
if not defined _PYGO_OLD_PATH set "_PYGO_OLD_PATH=%%PATH%%"
set "VIRTUAL_ENV=%[1]s"
set "VIRTUAL_ENV_PROMPT=%[2]s"
set "PATH=%[1]s\Scripts;%%_PYGO_OLD_PATH%%"
set PYTHONHOME=
if not defined PROMPT set PROMPT=$P$G
if not defined _PYGO_OLD_PROMPT set "_PYGO_OLD_PROMPT=%%PROMPT%%"
set "PROMPT=(%[2]s) %%_PYGO_OLD_PROMPT%%"
echo activated %[2]s
`, path, name)
	default: // bash
		p := posix(path)
		return fmt.Sprintf(`export _PYGO_OLD_PATH="${_PYGO_OLD_PATH:-$PATH}"
export VIRTUAL_ENV='%[1]s'
export VIRTUAL_ENV_PROMPT='%[2]s'
export PATH='%[1]s/Scripts':"$_PYGO_OLD_PATH"
unset PYTHONHOME
if [ -z "${POSH_SHELL:-}${STARSHIP_SHELL:-}" ]; then
  if [ -z "${_PYGO_OLD_PS1+x}" ]; then export _PYGO_OLD_PS1="$PS1"; fi
  PS1="(%[2]s) $_PYGO_OLD_PS1"
fi
echo "activated %[2]s"
`, p, name)
	}
}

// Deactivate returns the script that undoes Activate.
func Deactivate(sh string) string {
	switch sh {
	case "pwsh", "powershell":
		return `if ($env:_PYGO_OLD_PATH) { $env:PATH = $env:_PYGO_OLD_PATH; Remove-Item Env:_PYGO_OLD_PATH }
Remove-Item Env:VIRTUAL_ENV, Env:VIRTUAL_ENV_PROMPT -ErrorAction SilentlyContinue
if (Test-Path function:global:_pygo_old_prompt) { Set-Item function:global:prompt (Get-Item function:global:_pygo_old_prompt).ScriptBlock; Remove-Item function:global:_pygo_old_prompt }
Write-Host "deactivated"
`
	case "cmd":
		return `@echo off
if defined _PYGO_OLD_PATH set "PATH=%_PYGO_OLD_PATH%"
set _PYGO_OLD_PATH=
set VIRTUAL_ENV=
set VIRTUAL_ENV_PROMPT=
if defined _PYGO_OLD_PROMPT set "PROMPT=%_PYGO_OLD_PROMPT%"
set _PYGO_OLD_PROMPT=
echo deactivated
`
	default:
		return `if [ -n "${_PYGO_OLD_PATH:-}" ]; then export PATH="$_PYGO_OLD_PATH"; unset _PYGO_OLD_PATH; fi
unset VIRTUAL_ENV VIRTUAL_ENV_PROMPT
if [ -n "${_PYGO_OLD_PS1+x}" ]; then PS1="$_PYGO_OLD_PS1"; unset _PYGO_OLD_PS1; fi
echo deactivated
`
	}
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

// Install writes the hook into every shell profile found. Returns the files/keys touched.
func Install(exe string) ([]string, error) {
	var done []string
	var errs []error

	for _, ps := range PowerShells() {
		profile := Profiles()[ps]
		line := fmt.Sprintf("& '%s' init pwsh | Out-String | Invoke-Expression", psq(exe))
		if err := appendOnce(profile, line, "\r\n"); err != nil {
			errs = append(errs, err)
			continue
		}
		done = append(done, profile)
	}

	// CMD: doskey macro defined by a hook file that HKCU Command Processor\AutoRun calls.
	hook := hookFile()
	script, _ := Init("cmd", exe)
	os.MkdirAll(hookDir(), 0o755)
	if err := os.WriteFile(hook, []byte(script), 0o644); err != nil {
		errs = append(errs, err)
	} else if err := setAutoRun(fmt.Sprintf(`if exist "%s" call "%s"`, hook, hook)); err != nil {
		errs = append(errs, fmt.Errorf("cmd AutoRun: %w", err))
	} else {
		done = append(done, `HKCU\Software\Microsoft\Command Processor\AutoRun -> `+hook)
	}

	// Git Bash
	if _, err := exec.LookPath("bash"); err == nil {
		rc := filepath.Join(os.Getenv("USERPROFILE"), ".bashrc")
		line := fmt.Sprintf(`eval "$('%s' init bash)"`, posix(exe))
		if err := appendOnce(rc, line, "\n"); err != nil {
			errs = append(errs, err)
		} else {
			done = append(done, rc)
		}
	}
	return done, errors.Join(errs...)
}

func appendOnce(file, line, nl string) error {
	b, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if strings.Contains(string(b), line) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		prefix = nl
	}
	_, err = f.WriteString(prefix + "# pyGo shell hook" + nl + line + nl)
	return err
}

func setAutoRun(call string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Command Processor`, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	cur, _, _ := k.GetStringValue("AutoRun")
	if strings.Contains(cur, call) {
		return nil
	}
	if cur != "" {
		call = cur + " & " + call
	}
	return k.SetStringValue("AutoRun", call)
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

func hookFile() string { return filepath.Join(hookDir(), "pygo_hook.cmd") }

// Hook is the install state of one shell's hook.
type Hook struct {
	Shell  string // powershell, pwsh, cmd, bash
	State  string // ok, missing, stale, removed
	Detail string // file or key, and for stale the exe the hook points at
}

var (
	rePwshHook = regexp.MustCompile(`(?i)'([^']+\.exe)'\s+init\s+pwsh`)
	reBashHook = regexp.MustCompile(`(?i)'([^']+)'\s+init\s+bash`)
	reCmdHook  = regexp.MustCompile(`(?i)doskey\s+pygo="([^"]+)"`)
)

// Status inspects every shell's hook and compares the exe it calls with exe (the running pygo).
// A hook calling anything else (an old wv.exe, a moved pygo.exe) is "stale".
func Status(exe string) []Hook {
	var out []Hook
	for _, ps := range PowerShells() {
		profile := Profiles()[ps]
		b, _ := os.ReadFile(profile)
		out = append(out, judge(ps, profile, rePwshHook.FindAllStringSubmatch(string(b), -1), exe))
	}

	ar := AutoRun()
	switch {
	case strings.Contains(strings.ToLower(ar), strings.ToLower(hookFile())):
		b, _ := os.ReadFile(hookFile())
		out = append(out, judge("cmd", hookFile(), reCmdHook.FindAllStringSubmatch(string(b), -1), exe))
	case fileExists(hookFile()):
		out = append(out, Hook{"cmd", "removed", "AutoRun no longer calls " + hookFile() + " (rewritten, probably by VS Code)"})
	case strings.Contains(strings.ToLower(ar), "hook.cmd"):
		out = append(out, Hook{"cmd", "stale", "AutoRun calls another hook: " + ar})
	default:
		out = append(out, Hook{"cmd", "missing", `HKCU\Software\Microsoft\Command Processor\AutoRun`})
	}

	if _, err := exec.LookPath("bash"); err == nil {
		rc := filepath.Join(os.Getenv("USERPROFILE"), ".bashrc")
		b, _ := os.ReadFile(rc)
		h := judge("bash", rc, reBashHook.FindAllStringSubmatch(string(b), -1), posixPath(exe))
		out = append(out, h)
	}
	return out
}

// judge picks ok if any hook line calls want, stale if lines exist but call something else.
func judge(sh, where string, matches [][]string, want string) Hook {
	if len(matches) == 0 {
		return Hook{sh, "missing", where}
	}
	for _, m := range matches {
		if strings.EqualFold(strings.ReplaceAll(m[1], "''", "'"), want) {
			return Hook{sh, "ok", where}
		}
	}
	return Hook{sh, "stale", fmt.Sprintf("%s points at %s", where, matches[0][1])}
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
