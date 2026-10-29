package shell

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"venvctl/internal/envmgr"
)

// Auto-activate on cd. The shell does the cheap part itself: it notices the folder changed,
// walks up for the project root (envmgr.FindRoot's rule) and checks trust.json with a plain
// substring search. It only runs venvctl.exe (`__shell <sh> auto <root>`) when the root changed
// and is trusted, or when leaving a venv venvctl auto-activated (_VENVCTL_AUTO=1). A venv that
// the user or an editor activated (VIRTUAL_ENV set without _VENVCTL_AUTO) is never touched.

// autoPwsh is appended to `venvctl init pwsh` when auto-activate is on. %[1]s is the exe and
// %[2]s trust.json, both escaped for single-quoted strings. Before the spawn it uses only .NET
// calls, no child processes.
const autoPwsh = `
# venvctl auto-activate ("venvctl config auto-activate off" disables it)
$global:__venvctl_exe = '%[1]s'
$global:__venvctl_trust = '%[2]s'
$global:__venvctl_last = $null; $global:__venvctl_root = $null; $global:__venvctl_hinted = @{}
$global:__venvctl_trust_text = ''; $global:__venvctl_trust_time = [datetime]::MinValue
function global:__venvctl_auto {
    if ($PWD.Provider.Name -ne 'FileSystem') { return }
    $here = $PWD.ProviderPath
    if ($here -eq $global:__venvctl_last) { return }
    $global:__venvctl_last = $here
    $root = $null; $proj = $null; $d = $here
    for ($i = 0; $i -lt %[3]d -and $d; $i++) {
        if ([IO.File]::Exists("$d\.venv") -or [IO.File]::Exists("$d\.venv\pyvenv.cfg") -or [IO.File]::Exists("$d\venv\pyvenv.cfg")) { $root = $d; break }
        if (-not $proj -and [IO.File]::Exists("$d\pyproject.toml")) { $proj = $d }
        if ([IO.Directory]::Exists("$d\.git") -or [IO.File]::Exists("$d\.git")) { break }
        $d = [IO.Path]::GetDirectoryName($d)
    }
    if (-not $root) { $root = $proj }
    if ($root -eq $global:__venvctl_root) { return }
    $global:__venvctl_root = $root
    $trusted = $false
    if ($root) {
        $t = [IO.File]::GetLastWriteTimeUtc($global:__venvctl_trust)
        if ($t -ne $global:__venvctl_trust_time) {
            $global:__venvctl_trust_time = $t
            $global:__venvctl_trust_text = ''
            if ([IO.File]::Exists($global:__venvctl_trust)) { $global:__venvctl_trust_text = [IO.File]::ReadAllText($global:__venvctl_trust).ToLowerInvariant() }
        }
        $trusted = $global:__venvctl_trust_text.Contains('"' + $root.Replace('\', '/').ToLowerInvariant() + '"')
        if (-not $trusted -and -not $global:__venvctl_hinted[$root] -and ([IO.File]::Exists("$root\.venv") -or [IO.File]::Exists("$root\.venv\pyvenv.cfg") -or [IO.File]::Exists("$root\venv\pyvenv.cfg"))) {
            $global:__venvctl_hinted[$root] = $true
            Write-Host "venvctl: $root has a venv; run 'venvctl allow' to auto-activate" -ForegroundColor DarkGray
        }
    }
    if ($trusted -or $env:_VENVCTL_AUTO) {
        $enc = [Console]::OutputEncoding; [Console]::OutputEncoding = [Text.Encoding]::UTF8
        try { $s = & $global:__venvctl_exe __shell pwsh auto "$root" | Out-String } finally { [Console]::OutputEncoding = $enc }
        if ($s) { Invoke-Expression $s }
    }
}
if ($env:VENVCTL_AUTO_HOOK -eq 'cd') {
    # Opt-in fallback: wrap whatever cd is (zoxide's __zoxide_z if present), then run the hook.
    $global:__venvctl_cd_target = (Get-Alias cd -ErrorAction SilentlyContinue).ReferencedCommand
    if (-not $global:__venvctl_cd_target) { $global:__venvctl_cd_target = Get-Command Set-Location }
    function global:__venvctl_cd { & $global:__venvctl_cd_target @args; __venvctl_auto }
    Set-Alias -Name cd -Value __venvctl_cd -Option AllScope -Scope Global -Force
} elseif ($env:POSH_THEME -or $env:POSH_SHELL) {
    # oh-my-posh owns the prompt; it calls Set-PoshContext before every render.
    if (Test-Path function:global:Set-PoshContext) { $global:__venvctl_prev_hook = ${function:global:Set-PoshContext} }
    function global:Set-PoshContext { __venvctl_auto; if ($global:__venvctl_prev_hook) { & $global:__venvctl_prev_hook } }
} elseif ($env:STARSHIP_SHELL) {
    # starship owns the prompt; it calls Invoke-Starship-PreCommand before every render.
    if (Test-Path function:global:Invoke-Starship-PreCommand) { $global:__venvctl_prev_hook = ${function:global:Invoke-Starship-PreCommand} }
    function global:Invoke-Starship-PreCommand { __venvctl_auto; if ($global:__venvctl_prev_hook) { & $global:__venvctl_prev_hook } }
} else {
    $global:__venvctl_inner_prompt = ${function:prompt}
    function global:prompt { __venvctl_auto; & $global:__venvctl_inner_prompt }
}
`

// autoBash is appended to `venvctl init bash` when auto-activate is on (PROMPT_COMMAND).
// %[1]s is the exe (posix, single-quote escaped), %[2]s trust.json (posix), %[3]d the depth.
const autoBash = `
# venvctl auto-activate ("venvctl config auto-activate off" disables it)
__venvctl_auto() {
  [ "$PWD" = "${__venvctl_last-}" ] && return
  __venvctl_last=$PWD
  local d=$PWD root= proj= i=0 key= win= text= trusted=
  while [ $i -lt %[3]d ] && [ -n "$d" ]; do
    if [ -f "$d/.venv" ] || [ -f "$d/.venv/pyvenv.cfg" ] || [ -f "$d/venv/pyvenv.cfg" ]; then root=$d; break; fi
    [ -z "$proj" ] && [ -f "$d/pyproject.toml" ] && proj=$d
    [ -e "$d/.git" ] && break
    [ "$d" = "/" ] && break
    d=${d%%/*}; [ -z "$d" ] && d=/
    i=$((i+1))
  done
  [ -z "$root" ] && root=$proj
  [ "$root" = "${__venvctl_root-}" ] && return
  __venvctl_root=$root
  if [ -n "$root" ]; then
    # MSYS paths (/c/x, /tmp, /home) -> the Windows path trust.json uses. Runs only when the
    # root changed, so the unchanged-folder fast path stays fork-free.
    win=$(builtin cd -- "$root" 2>/dev/null && builtin pwd -W 2>/dev/null) || win=$root
    key=${win,,}
    [ -f '%[2]s' ] && IFS= read -r -d '' text < '%[2]s'
    case "${text,,}" in *"\"$key\""*) trusted=1 ;; esac
    if [ -z "$trusted" ] && [[ " ${__venvctl_hinted-} " != *" $root "* ]] && { [ -f "$root/.venv" ] || [ -f "$root/.venv/pyvenv.cfg" ] || [ -f "$root/venv/pyvenv.cfg" ]; }; then
      __venvctl_hinted="${__venvctl_hinted-} $root"
      echo "venvctl: $root has a venv; run 'venvctl allow' to auto-activate" >&2
    fi
  fi
  if [ -n "$trusted" ] || [ -n "${_VENVCTL_AUTO-}" ]; then
    local s; s="$('%[1]s' __shell bash auto "${win-}")" && eval "$s"
  fi
}
case ";${PROMPT_COMMAND-};" in *";__venvctl_auto;"*) ;; *) PROMPT_COMMAND="__venvctl_auto${PROMPT_COMMAND:+;$PROMPT_COMMAND}" ;; esac
`

// autoClink is written to %LOCALAPPDATA%\clink\venvctl.lua when Clink is installed. Clink runs
// inside cmd.exe, so os.setenv changes the CMD session directly. %[1]s exe, %[2]s trust.json.
const autoClink = `-- venvctl auto-activate for Clink (written by "venvctl init cmd"; "venvctl init --remove" deletes it)
local exe, trust, depth = [[%[1]s]], [[%[2]s]], %[3]d
local last, last_root, hinted = nil, false, {}
local function isfile(p) return os.isfile(p) end
local function has_venv(d) return isfile(d.."\\.venv") or isfile(d.."\\.venv\\pyvenv.cfg") or isfile(d.."\\venv\\pyvenv.cfg") end
local function find_root(dir)
  local d, proj = dir, nil
  for _ = 1, depth do
    if has_venv(d) then return d end
    if not proj and isfile(d.."\\pyproject.toml") then proj = d end
    if os.isdir(d.."\\.git") or isfile(d.."\\.git") then break end
    local parent = d:match("^(.*)\\[^\\]+$")
    if not parent or parent == d then break end
    if parent:match("^%%a:$") then parent = parent.."\\" end
    d = parent
  end
  return proj
end
clink.onbeginedit(function()
  local cwd = os.getcwd()
  if cwd == last then return end
  last = cwd
  local root = find_root(cwd)
  if root == last_root then return end
  last_root = root
  local trusted = false
  if root then
    local f = io.open(trust, "r")
    local text = f and f:read("*a"):lower() or ""
    if f then f:close() end
    trusted = text:find('"'..root:gsub("\\", "/"):lower()..'"', 1, true) ~= nil
    if not trusted and not hinted[root:lower()] and has_venv(root) then
      hinted[root:lower()] = true
      print("venvctl: "..root.." has a venv; run 'venvctl allow' to auto-activate")
    end
  end
  if trusted or os.getenv("_VENVCTL_AUTO") then
    local p = io.popen('""'..exe..'" __shell clink auto "'..(root or "")..'""')
    if not p then return end
    for line in p:lines() do
      local name, value = line:match("^set ([^=]+)=(.*)$")
      if name then
        os.setenv(name, value)
      elseif line:match("^unset ") then
        local n = line:sub(7)
        if os.unsetenv then os.unsetenv(n) else os.setenv(n, nil) end
      elseif line:match("^echo ") then
        print(line:sub(6))
      end
    end
    p:close()
  end
end)
`

// AutoEnabled reports whether config.json turns auto-activate on.
func AutoEnabled() bool { return envmgr.LoadConfig().AutoActivate }

// AutoInit returns the auto-activate block for `venvctl init <sh>`, or "" for shells without one.
func AutoInit(sh, exe string) string {
	switch sh {
	case "pwsh", "powershell":
		return fmt.Sprintf(autoPwsh, psq(exe), psq(envmgr.TrustFile()), envmgr.MaxRootDepth)
	case "bash":
		return fmt.Sprintf(autoBash, posix(exe), posix(envmgr.TrustFile()), envmgr.MaxRootDepth)
	}
	return ""
}

// ClinkScript is the path of the Clink auto-activate script.
func ClinkScript() string { return filepath.Join(os.Getenv("LOCALAPPDATA"), "clink", "venvctl.lua") }

// HasClink reports whether Clink is on PATH.
func HasClink() bool {
	_, err := exec.LookPath("clink")
	return err == nil
}

// clinkLua returns the Clink script for exe.
func clinkLua(exe string) string {
	return fmt.Sprintf(autoClink, exe, envmgr.TrustFile(), envmgr.MaxRootDepth)
}

// SetAuto returns the line that marks (on) or unmarks (off) a venv as auto-activated.
func SetAuto(sh string, on bool) string {
	switch sh {
	case "pwsh", "powershell":
		if on {
			return "$env:_VENVCTL_AUTO = '1'\n"
		}
		return "Remove-Item Env:_VENVCTL_AUTO -ErrorAction SilentlyContinue\n"
	case "cmd":
		if on {
			return "set _VENVCTL_AUTO=1\r\n"
		}
		return "set _VENVCTL_AUTO=\r\n"
	case "clink":
		if on {
			return "set _VENVCTL_AUTO=1\n"
		}
		return "unset _VENVCTL_AUTO\n"
	}
	if on {
		return "export _VENVCTL_AUTO=1\n"
	}
	return "unset _VENVCTL_AUTO\n"
}

// clinkActivate computes, from the current CMD environment, the variable changes that activate
// path: lines "set NAME=VALUE", "unset NAME" and "echo TEXT" that the Clink script applies.
func clinkActivate(path, name string) string {
	old := os.Getenv("_VENVCTL_OLD_PATH")
	if old == "" {
		old = os.Getenv("PATH")
	}
	prompt := os.Getenv("_VENVCTL_OLD_PROMPT")
	if prompt == "" {
		if prompt = os.Getenv("PROMPT"); prompt == "" {
			prompt = "$P$G"
		}
	}
	return strings.Join([]string{
		"set _VENVCTL_OLD_PATH=" + old,
		"set VIRTUAL_ENV=" + path,
		"set VIRTUAL_ENV_PROMPT=" + name,
		"set PATH=" + filepath.Join(path, "Scripts") + ";" + old,
		"unset PYTHONHOME",
		"set _VENVCTL_OLD_PROMPT=" + prompt,
		"set PROMPT=(" + name + ") " + prompt,
		"echo activated " + name,
	}, "\n") + "\n"
}

// clinkDeactivate undoes clinkActivate.
func clinkDeactivate() string {
	var lines []string
	if old := os.Getenv("_VENVCTL_OLD_PATH"); old != "" {
		lines = append(lines, "set PATH="+old)
	}
	if p := os.Getenv("_VENVCTL_OLD_PROMPT"); p != "" {
		lines = append(lines, "set PROMPT="+p)
	}
	lines = append(lines, "unset _VENVCTL_OLD_PATH", "unset _VENVCTL_OLD_PROMPT", "unset VIRTUAL_ENV",
		"unset VIRTUAL_ENV_PROMPT", "echo deactivated")
	return strings.Join(lines, "\n") + "\n"
}
